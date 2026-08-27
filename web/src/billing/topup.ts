import { x402Client } from "@x402/core/client";
import type { Network, PaymentRequirements } from "@x402/core/types";
import { ExactEvmScheme } from "@x402/evm/exact/client";
import { wrapFetchWithPayment } from "@x402/fetch";
import {
  decodeFunctionResult,
  encodeFunctionData,
  getAddress,
  type Address,
  type Hex,
} from "viem";

import type {
  BillingConfig,
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

export async function payBillingTopup(options: {
  config: BillingConfig;
  intent: BillingTopupIntent;
  method: BillingTransferMethod;
  csrfToken: string;
  wallet: TopupWallet;
}): Promise<BillingTopupReceipt> {
  const { config, intent, method, csrfToken, wallet } = options;
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
  if (method === "permit2") {
    await ensureExactPermit2Allowance(wallet, binding);
  }
  const signer = {
    address: wallet.active.account,
    signTypedData: (typedData: BillingTypedData) =>
      wallet.signBillingTypedData(typedData, binding, wallet.active),
  };
  const selector = (_version: number, requirements: PaymentRequirements[]) => {
    const selected = requirements.find(
      requirement => requirement.extra?.assetTransferMethod === method,
    );
    if (!selected) throw new TypeError("Selected x402 transfer method is unavailable");
    return selected;
  };
  const client = new x402Client(selector).register(network, new ExactEvmScheme(signer));
  const fetchWithPayment = wrapFetchWithPayment(globalThis.fetch, client);
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
  );
  if (response.status === 402 && response.headers.has("PAYMENT-RESPONSE")) {
    throw new TopupPendingError(intent.id);
  }
  if (!response.ok) throw new Error(`Top-up failed (${response.status})`);
  const payload: unknown = await response.json();
  if (!isReceiptEnvelope(payload)) throw new TypeError("Top-up response is invalid");
  return payload.data;
}

export async function ensureExactPermit2Allowance(
  wallet: TopupWallet,
  binding: BillingSigningBinding,
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
  return (
    typeof data === "object" &&
    data !== null &&
    "intent" in data &&
    "account" in data
  );
}
