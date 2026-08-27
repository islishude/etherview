import { decodeFunctionData, encodeFunctionResult, type Address, type Hex } from "viem";
import { describe, expect, it, vi } from "vitest";

import { ensureExactPermit2Allowance, type TopupWallet } from "./topup";
import type { BillingSigningBinding } from "@/wallet/billing";

const account = "0x1111111111111111111111111111111111111111" as Address;
const asset = "0x2222222222222222222222222222222222222222" as Address;
const recipient = "0x3333333333333333333333333333333333333333" as Address;
const permit2 = "0x000000000022D473030F116dDEE9F6B43aC78BA3";
const hash = `0x${"ab".repeat(32)}` as Hex;
const approveABI = [{
  type: "function", name: "approve", stateMutability: "nonpayable",
  inputs: [{ name: "spender", type: "address" }, { name: "amount", type: "uint256" }],
  outputs: [{ name: "", type: "bool" }],
}] as const;
const allowanceABI = [{
  type: "function", name: "allowance", stateMutability: "view",
  inputs: [{ name: "owner", type: "address" }, { name: "spender", type: "address" }],
  outputs: [{ name: "", type: "uint256" }],
}] as const;

describe("Permit2 top-up approval", () => {
  it("keeps an exact allowance and rewrites larger or smaller allowances to this top-up only", async () => {
    for (const allowance of [1000n, 999n, 1001n]) {
      const sendTransaction = vi.fn(async (
        _call: { to: Address; data: Hex },
        _chainID?: string,
      ) => hash);
      const waitForBillingTransaction = vi.fn(async () => undefined);
      const wallet = {
        active: { account, chainID: "31337", revision: 7, name: "Local" },
        readContract: vi.fn(async () => encodeFunctionResult({
          abi: allowanceABI, functionName: "allowance", result: allowance,
        })),
        sendTransaction,
        signBillingTypedData: vi.fn(),
        waitForBillingTransaction,
      } as unknown as TopupWallet;
      const binding: BillingSigningBinding = {
        method: "permit2", chainID: "31337", account, asset, recipient,
        amountAtomic: "1000",
      };

      await ensureExactPermit2Allowance(wallet, binding);

      if (allowance === 1000n) {
        expect(sendTransaction).not.toHaveBeenCalled();
        expect(waitForBillingTransaction).not.toHaveBeenCalled();
        continue;
      }
      expect(sendTransaction).toHaveBeenCalledOnce();
      const call = sendTransaction.mock.calls[0]?.[0] as { to: Address; data: Hex };
      expect(call.to).toBe(asset);
      expect(decodeFunctionData({ abi: approveABI, data: call.data })).toEqual({
        functionName: "approve", args: [permit2, 1000n],
      });
      expect(waitForBillingTransaction).toHaveBeenCalledWith(hash, wallet.active);
    }
  });
});
