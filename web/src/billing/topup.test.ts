import { testBillingConfig, testTopupIntent } from "@/billing/testFixtures";
import {
  decodeFunctionData,
  encodeFunctionResult,
  recoverTypedDataAddress,
  type Address,
  type Hex,
  type TypedDataDefinition,
} from "viem";
import { privateKeyToAccount } from "viem/accounts";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  payBillingTopup,
  TopupAuthorizationError,
  TopupPendingError,
  ensureExactPermit2Allowance,
  type TopupWallet,
} from "./topup";
import { encodeBillingTypedData, type BillingSigningBinding } from "@/wallet/billing";

const signer = privateKeyToAccount(`0x${"01".repeat(32)}`);
const account = signer.address;
const asset = "0x2222222222222222222222222222222222222222" as Address;
const recipient = "0x3333333333333333333333333333333333333333" as Address;
const permit2 = "0x000000000022D473030F116dDEE9F6B43aC78BA3";
const hash = `0x${"ab".repeat(32)}` as Hex;
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

describe("Permit2 top-up approval", () => {
  it("keeps an exact allowance and rewrites larger or smaller allowances to this top-up only", async () => {
    for (const allowance of [1000n, 999n, 1001n]) {
      const sendTransaction = vi.fn(
        async (_call: { to: Address; data: Hex }, _chainID?: string) => hash,
      );
      const waitForBillingTransaction = vi.fn(async () => undefined);
      const wallet = {
        active: { account, chainID: "31337", revision: 7, name: "Local" },
        readContract: vi.fn(async () =>
          encodeFunctionResult({
            abi: allowanceABI,
            functionName: "allowance",
            result: allowance,
          }),
        ),
        sendTransaction,
        signBillingTypedData: vi.fn(),
        waitForBillingTransaction,
      } as unknown as TopupWallet;
      const binding: BillingSigningBinding = {
        method: "permit2",
        chainID: "31337",
        account,
        asset,
        recipient,
        amountAtomic: "1000",
      };

      const onProgress = vi.fn();
      await ensureExactPermit2Allowance(wallet, binding, onProgress);
      expect(onProgress.mock.calls.map(([progress]) => progress.stage)).toEqual(
        allowance === 1000n ? [] : ["approval", "approvalConfirming"],
      );

      if (allowance === 1000n) {
        expect(sendTransaction).not.toHaveBeenCalled();
        expect(waitForBillingTransaction).not.toHaveBeenCalled();
        continue;
      }
      expect(sendTransaction).toHaveBeenCalledOnce();
      const call = sendTransaction.mock.calls[0]?.[0] as { to: Address; data: Hex };
      expect(call.to).toBe(asset);
      expect(decodeFunctionData({ abi: approveABI, data: call.data })).toEqual({
        functionName: "approve",
        args: [permit2, 1000n],
      });
      expect(waitForBillingTransaction).toHaveBeenCalledWith(hash, wallet.active);
    }
  });
});

describe("x402 SDK top-up flow", () => {
  const network = "eip155:48815";
  const config = testBillingConfig({
    api_billing_enabled: true,
    x402_topups_enabled: true,
    x402_version: 2,
    scheme: "exact",
    asset_transfer_methods: ["eip3009", "permit2"],
    operations: [],
    network,
    asset,
    recipient,
    asset_eip712_name: "Local USD",
    asset_eip712_version: "1",
  });
  const intent = testTopupIntent(
    {
      id: "topup-test",
      user_id: "user",
      network,
      asset,
      recipient,
      payer: account,
      amount_atomic: "1000",
      state: "open",
      created_at: "2026-10-30T00:00:00Z",
      updated_at: "2026-10-30T00:00:00Z",
      expires_at: "2099-01-01T00:00:00Z",
    },
    config,
  );

  beforeEach(() => {
    // Node's Request needs an absolute URL; browsers resolve same-origin paths.
    const NativeRequest = globalThis.Request;
    vi.stubGlobal(
      "Request",
      class extends NativeRequest {
        constructor(input: RequestInfo | URL, init?: RequestInit) {
          super(
            typeof input === "string" ? new URL(input, "https://app.etherview.localhost") : input,
            init,
          );
        }
      },
    );
  });
  afterEach(() => vi.unstubAllGlobals());

  function fixture(method: "eip3009" | "permit2", overrides = {}) {
    const requirement = {
      scheme: "exact",
      network,
      asset,
      payTo: recipient,
      amount: intent.amount_atomic,
      maxTimeoutSeconds: 60,
      extra: { name: "Local USD", version: "1", assetTransferMethod: method },
      ...overrides,
    };
    const fetch = vi.fn(async (request: Request) => {
      if (!request.headers.has("PAYMENT-SIGNATURE")) {
        return new Response(null, {
          status: 402,
          headers: {
            "PAYMENT-REQUIRED": btoa(
              JSON.stringify({
                x402Version: 2,
                resource: { url: request.url },
                accepts: [requirement],
              }),
            ),
          },
        });
      }
      return Response.json({ data: { intent: { ...intent, state: "credited" }, account: {} } });
    });
    vi.stubGlobal("fetch", fetch);
    const wallet = {
      active: { account, chainID: "48815", revision: 7, name: "Local", uuid: "test-wallet" },
      readContract: vi.fn(async () =>
        encodeFunctionResult({ abi: allowanceABI, functionName: "allowance", result: 1000n }),
      ),
      sendTransaction: vi.fn(),
      signBillingTypedData: vi.fn(async (data, binding) => {
        const wire = JSON.parse(encodeBillingTypedData(data, binding)) as TypedDataDefinition;
        // Raw wallet RPC must not get viem's automatic repair of missing domain types.
        return signer.signTypedData({ ...wire, types: { EIP712Domain: [], ...wire.types } });
      }),
      waitForBillingTransaction: vi.fn(),
    } satisfies TopupWallet;
    return { fetch, wallet, options: { config, intent, method, csrfToken: "csrf", wallet } };
  }

  it.each(["eip3009", "permit2"] as const)(
    "blocks %s authorization when balance is insufficient or unreadable",
    async (method) => {
      for (const invalid of ["zero", "error"] as const) {
        const { options, wallet, fetch } = fixture(method);
        if (invalid === "zero") wallet.readContract.mockResolvedValue(`0x${"0".repeat(64)}`);
        else wallet.readContract.mockRejectedValue(new Error("private provider error"));
        await expect(payBillingTopup(options)).rejects.toMatchObject({
          name: "TopupBalanceError",
          reason: invalid === "zero" ? "insufficient" : "unavailable",
        });
        expect(wallet.signBillingTypedData).not.toHaveBeenCalled();
        expect(wallet.sendTransaction).not.toHaveBeenCalled();
        expect(fetch.mock.calls.some(([request]) => request.headers.has("PAYMENT-SIGNATURE"))).toBe(
          false,
        );
      }
    },
  );

  it.each(["asset_decimals", "credit_rate_numerator", "network"] as const)(
    "rejects a changed %s quote before wallet authorization",
    async (field) => {
      const { options, wallet, fetch } = fixture("eip3009");
      const payment_option = {
        ...options.intent.payment_option,
        ...(field === "asset_decimals"
          ? { asset_decimals: 18 }
          : field === "network"
            ? { network: "eip155:31337" }
            : { credit_rate_numerator: "2" }),
      };
      await expect(
        payBillingTopup({ ...options, intent: { ...options.intent, payment_option } }),
      ).rejects.toBeInstanceOf(TopupAuthorizationError);
      expect(wallet.signBillingTypedData).not.toHaveBeenCalled();
      expect(wallet.sendTransaction).not.toHaveBeenCalled();
      expect(fetch).not.toHaveBeenCalled();
    },
  );

  it("rechecks balance after Permit2 approval before signing", async () => {
    const { options, wallet } = fixture("permit2");
    wallet.readContract
      .mockResolvedValueOnce(`0x${1000n.toString(16).padStart(64, "0")}`)
      .mockResolvedValueOnce(`0x${"0".repeat(64)}`)
      .mockResolvedValueOnce(`0x${"0".repeat(64)}`);
    await expect(payBillingTopup(options)).rejects.toMatchObject({
      name: "TopupBalanceError",
      reason: "insufficient",
    });
    expect(wallet.sendTransaction).toHaveBeenCalledOnce();
    expect(wallet.signBillingTypedData).not.toHaveBeenCalled();
  });

  it.each(["eip3009", "permit2"] as const)(
    "signs and submits custom Preview assets with %s using the real SDK",
    async (method) => {
      const { options, wallet, fetch } = fixture(method);
      expect((await payBillingTopup(options)).intent.state).toBe("credited");
      expect(wallet.signBillingTypedData).toHaveBeenCalledOnce();
      expect(fetch).toHaveBeenCalledTimes(2);
      const request = fetch.mock.calls[1]![0];
      expect(request.headers.get("X-CSRF-Token")).toBe("csrf");
      const payload = JSON.parse(atob(request.headers.get("PAYMENT-SIGNATURE")!));
      expect(payload.accepted).toMatchObject({ network, asset, amount: "1000" });
      const signedData = wallet.signBillingTypedData.mock.calls[0]![0] as TypedDataDefinition;
      expect(
        await recoverTypedDataAddress({ ...signedData, signature: payload.payload.signature }),
      ).toBe(account);
    },
  );

  it.each([
    { amount: "1001" },
    { amount: "999" },
    { network: "eip155:1" },
    { asset: recipient },
    { payTo: asset },
    { network: "eip155:48815", asset: "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913" },
  ])("rejects mismatched challenges before signing: %j", async (overrides) => {
    const { options, wallet, fetch } = fixture("eip3009", overrides);
    await expect(payBillingTopup(options)).rejects.toBeInstanceOf(TopupAuthorizationError);
    expect(wallet.signBillingTypedData).not.toHaveBeenCalled();
    expect(fetch).toHaveBeenCalledOnce();
  });

  it("classifies wallet rejection before submission without leaking provider errors", async () => {
    const { options, wallet } = fixture("eip3009");
    wallet.signBillingTypedData.mockRejectedValue(new Error("private provider detail"));
    await expect(payBillingTopup(options)).rejects.toThrow(
      "Top-up authorization could not be completed",
    );
  });

  it("reports signing and submission at the actual boundaries", async () => {
    const { options, fetch } = fixture("eip3009");
    const stages: string[] = [];
    const original = fetch.getMockImplementation()!;
    fetch.mockImplementation(async (request) => {
      if (request.headers.has("PAYMENT-SIGNATURE"))
        expect(stages).toEqual(["signing", "submitting"]);
      return original(request);
    });
    await payBillingTopup({ ...options, onProgress: ({ stage }) => stages.push(stage) });
    expect(stages).toEqual(["signing", "submitting"]);
  });

  it.each([false, true])(
    "classifies malformed receipts based on actual signed submission (%s)",
    async (submitted) => {
      const { options, fetch } = fixture("eip3009");
      const original = fetch.getMockImplementation()!;
      fetch.mockImplementation(async (request) => {
        if (!submitted || request.headers.has("PAYMENT-SIGNATURE"))
          return Response.json({ data: {} });
        return original(request);
      });
      await expect(payBillingTopup(options)).rejects.toBeInstanceOf(
        submitted ? TopupPendingError : TopupAuthorizationError,
      );
      expect(options.wallet.signBillingTypedData).toHaveBeenCalledTimes(submitted ? 1 : 0);
    },
  );

  it("preserves uncertain outcomes once a signed payment is submitted", async () => {
    const { options, fetch } = fixture("eip3009");
    const original = fetch.getMockImplementation()!;
    fetch.mockImplementation(async (request) => {
      if (request.headers.has("PAYMENT-SIGNATURE")) throw new Error("response lost");
      return original(request);
    });
    await expect(payBillingTopup(options)).rejects.toBeInstanceOf(TopupPendingError);
  });

  it("preserves settlement-pending responses", async () => {
    const { options, fetch } = fixture("eip3009");
    const original = fetch.getMockImplementation()!;
    fetch.mockImplementation(async (request) => {
      if (request.headers.has("PAYMENT-SIGNATURE"))
        return new Response(null, { status: 402, headers: { "PAYMENT-RESPONSE": "pending" } });
      return original(request);
    });
    await expect(payBillingTopup(options)).rejects.toBeInstanceOf(TopupPendingError);
  });
});
