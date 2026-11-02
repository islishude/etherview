import type { TopupProgress } from "./progress";
import { checkTopupBalance, TopupBalanceError } from "./balance";
import { x402Client } from "@x402/core/client";
import type { Network, PaymentRequirements } from "@x402/core/types";
import { ExactEvmScheme } from "@x402/evm/exact/client";
import { wrapFetchWithPayment } from "@x402/fetch";
import { decodeFunctionResult, encodeFunctionData, getAddress, type Address, type Hex } from "viem";

import type {
  BillingPaymentConfig,
  BillingTopupIntent,
  BillingTopupReceipt,
  BillingTransferMethod,
} from "@/api/billing";
import type { ActiveWallet } from "@/wallet/WalletProvider";
import type { BillingSigningBinding, BillingTypedData } from "@/wallet/billing";

const PERMIT2 = getAddress("0x000000000022D473030F116dDEE9F6B43aC78BA3");
const allowanceABI = [
  {
    type: "function",
    name: "allowance",
    stateMutability: "view",
    inputs: [
      { name: "owner", type: "address" },
      { name: "spender", type: "address" },
    ],
    outputs: [{ name: "", type: "uint256" }],
  },
] as const;
const approveABI = [
  {
    type: "function",
    name: "approve",
    stateMutability: "nonpayable",
    inputs: [
      { name: "spender", type: "address" },
      { name: "amount", type: "uint256" },
    ],
    outputs: [{ name: "", type: "bool" }],
  },
] as const;

export interface TopupWallet {
  active: ActiveWallet;
  readContract(call: { to: Address; data: Hex }, chainID?: string): Promise<Hex>;
  sendTransaction(call: { to: Address; data: Hex }, chainID?: string): Promise<Hex>;
  signBillingTypedData(
    typedData: BillingTypedData,
    binding: BillingSigningBinding,
    expected: ActiveWallet,
  ): Promise<Hex>;
  waitForBillingTransaction(hash: Hex, expected: ActiveWallet): Promise<void>;
}

export class TopupPendingError extends Error {
  readonly intentID: string;

  constructor(intentID: string) {
    super("Top-up settlement is pending");
    this.name = "TopupPendingError";
    this.intentID = intentID;
  }
}

export class TopupAuthorizationError extends Error {
  constructor() {
    super("Top-up authorization could not be completed");
    this.name = "TopupAuthorizationError";
  }
}

export async function payBillingTopup(options: {
  config: BillingPaymentConfig;
  intent: BillingTopupIntent;
  method: BillingTransferMethod;
  csrfToken: string;
  wallet: TopupWallet;
  onProgress?: (progress: TopupProgress) => void;
}): Promise<BillingTopupReceipt> {
  const { intent, method, csrfToken, wallet } = options;
  const snapshot = intent.payment_option;
  const fields = [
    "id",
    "network",
    "asset_decimals",
    "asset_eip712_name",
    "asset_eip712_version",
    "credit_rate_numerator",
    "credit_rate_denominator",
  ] as const;
  if (
    fields.some((key) => snapshot[key] !== options.config[key]) ||
    snapshot.asset.toLowerCase() !== options.config.asset.toLowerCase() ||
    snapshot.recipient.toLowerCase() !== options.config.recipient.toLowerCase() ||
    !options.config.asset_transfer_methods.includes(method)
  )
    throw new TopupAuthorizationError();
  const config = { ...options.config, ...intent.payment_option };
  const network = required(config.network, "billing network") as Network;
  const asset = getAddress(required(config.asset, "billing asset"));
  const recipient = getAddress(required(config.recipient, "billing recipient"));
  if (
    intent.state !== "open" ||
    intent.network !== network ||
    getAddress(intent.asset) !== asset ||
    getAddress(intent.recipient) !== recipient ||
    getAddress(intent.payer) !== wallet.active.account ||
    !config.asset_transfer_methods.includes(method)
  ) {
    throw new TypeError("Top-up intent does not match the current billing configuration");
  }
  const binding: BillingSigningBinding = {
    method,
    chainID: network.slice("eip155:".length),
    account: wallet.active.account,
    asset,
    recipient,
    amountAtomic: intent.amount_atomic,
    assetName: config.asset_eip712_name,
    assetVersion: config.asset_eip712_version,
  };
  const checkBalance = () =>
    checkTopupBalance(
      wallet.readContract,
      binding.account,
      binding.asset,
      binding.chainID,
      binding.amountAtomic,
    );
  if (method === "permit2") {
    await checkBalance();
    await ensureExactPermit2Allowance(wallet, binding, options.onProgress);
  }
  let balanceError: TopupBalanceError | undefined;
  const signer = {
    address: wallet.active.account,
    signTypedData: async (typedData: BillingTypedData) => {
      try {
        await checkBalance();
      } catch (cause) {
        if (cause instanceof TopupBalanceError) balanceError = cause;
        throw cause;
      }
      options.onProgress?.({ stage: "signing" });
      return wallet.signBillingTypedData(typedData, binding, wallet.active);
    },
  };
  const selector = (_version: number, requirements: PaymentRequirements[]) => {
    const selected = requirements.find(
      (requirement) =>
        requirement.extra?.assetTransferMethod === method &&
        requirement.network === network &&
        getAddress(requirement.asset) === asset &&
        getAddress(requirement.payTo) === recipient &&
        requirement.amount === intent.amount_atomic,
    );
    if (!selected) throw new TypeError("Selected x402 transfer method is unavailable");
    return selected;
  };
  const client = new x402Client(selector)
    .register(network, new ExactEvmScheme(signer))
    .setSpendControls({
      allowedAssets: [{ network, asset, maxAmountPerPayment: intent.amount_atomic }],
    });
  let paymentSubmitted = false;
  const fetchWithPayment = wrapFetchWithPayment((input, init) => {
    const request = new Request(input, init);
    // Mark before transport: a lost response does not prove settlement failed.
    if (request.headers.has("PAYMENT-SIGNATURE")) {
      paymentSubmitted = true;
      options.onProgress?.({ stage: "submitting" });
    }
    return globalThis.fetch(request);
  }, client);
  const response = await fetchWithPayment(
    `/api/v1/billing/topup-intents/${encodeURIComponent(intent.id)}/pay`,
    {
      method: "POST",
      credentials: "same-origin",
      cache: "no-store",
      headers: {
        Accept: "application/json",
        "X-CSRF-Token": csrfToken,
      },
    },
  ).catch(() => {
    if (!paymentSubmitted && balanceError) throw balanceError;
    if (!paymentSubmitted) throw new TopupAuthorizationError();
    throw new TopupPendingError(intent.id);
  });
  if (paymentSubmitted && response.status === 402 && response.headers.has("PAYMENT-RESPONSE")) {
    throw new TopupPendingError(intent.id);
  }
  if (!response.ok) {
    if (paymentSubmitted && response.status >= 500) throw new TopupPendingError(intent.id);
    throw new Error(`Top-up failed (${response.status})`);
  }
  const invalidResponse = () =>
    paymentSubmitted ? new TopupPendingError(intent.id) : new TopupAuthorizationError();
  const payload: unknown = await response.json().catch(() => {
    throw invalidResponse();
  });
  if (!isReceiptEnvelope(payload)) throw invalidResponse();
  return payload.data;
}

export async function ensureExactPermit2Allowance(
  wallet: TopupWallet,
  binding: BillingSigningBinding,
  onProgress?: (progress: TopupProgress) => void,
): Promise<void> {
  const allowanceData = encodeFunctionData({
    abi: allowanceABI,
    functionName: "allowance",
    args: [binding.account, PERMIT2],
  });
  const encoded = await wallet.readContract(
    { to: binding.asset, data: allowanceData },
    binding.chainID,
  );
  const allowance = decodeFunctionResult({
    abi: allowanceABI,
    functionName: "allowance",
    data: encoded,
  });
  const amount = BigInt(binding.amountAtomic);
  if (allowance === amount) return;
  onProgress?.({ stage: "approval" });
  const transaction = await wallet.sendTransaction(
    {
      to: binding.asset,
      data: encodeFunctionData({
        abi: approveABI,
        functionName: "approve",
        args: [PERMIT2, amount],
      }),
    },
    binding.chainID,
  );
  onProgress?.({ stage: "approvalConfirming", approvalHash: transaction });
  await wallet.waitForBillingTransaction(transaction, wallet.active);
}

function required<T>(value: T | null | undefined, name: string): T {
  if (value === null || value === undefined || value === "") {
    throw new TypeError(`${name} is unavailable`);
  }
  return value;
}

function isReceiptEnvelope(value: unknown): value is { data: BillingTopupReceipt } {
  if (typeof value !== "object" || value === null || !("data" in value)) return false;
  const data = value.data;
  return typeof data === "object" && data !== null && "intent" in data && "account" in data;
}
