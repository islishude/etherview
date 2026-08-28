import { describe, expect, it } from "vitest";

import {
  validateBillingTypedData,
  type BillingSigningBinding,
  type BillingTypedData,
} from "./billing";

const account = "0x1111111111111111111111111111111111111111";
const asset = "0x2222222222222222222222222222222222222222";
const recipient = "0x3333333333333333333333333333333333333333";

describe("billing typed-data boundary", () => {
  it("accepts only the exact EIP-3009 shape and binding", () => {
    const binding: BillingSigningBinding = {
      method: "eip3009", chainID: "31337", account, asset, recipient,
      amountAtomic: "1000", assetName: "Local USD", assetVersion: "1",
    };
    const typedData = eip3009TypedData();
    expect(() => validateBillingTypedData(typedData, binding)).not.toThrow();
    for (const mutate of [
      (value: BillingTypedData) => { value.message.value = 1001n; },
      (value: BillingTypedData) => { value.message.extra = "hostile"; },
      (value: BillingTypedData) => { value.domain.chainId = 1; },
      (value: BillingTypedData) => { value.types.TransferWithAuthorization = []; },
      (value: BillingTypedData) => { value.message.validBefore = 0n; },
    ]) {
      const candidate = structuredClone(typedData);
      mutate(candidate);
      expect(() => validateBillingTypedData(candidate, binding)).toThrow(TypeError);
    }
  });

  it("accepts only the canonical Permit2 proxy and witness", () => {
    const binding: BillingSigningBinding = {
      method: "permit2", chainID: "31337", account, asset, recipient,
      amountAtomic: "1000",
    };
    const typedData = permit2TypedData();
    expect(() => validateBillingTypedData(typedData, binding)).not.toThrow();
    for (const mutate of [
      (value: BillingTypedData) => { value.message.spender = recipient; },
      (value: BillingTypedData) => { object(value.message.witness).to = account; },
      (value: BillingTypedData) => { object(value.message.permitted).amount = 999n; },
      (value: BillingTypedData) => { value.domain.version = "1"; },
      (value: BillingTypedData) => { value.types.Witness = [{ name: "to", type: "address" }]; },
      (value: BillingTypedData) => { value.message.deadline = 0n; },
    ]) {
      const candidate = structuredClone(typedData);
      mutate(candidate);
      expect(() => validateBillingTypedData(candidate, binding)).toThrow(TypeError);
    }
  });
});

function eip3009TypedData(): BillingTypedData {
  return {
    domain: { name: "Local USD", version: "1", chainId: 31337, verifyingContract: asset },
    types: {
      TransferWithAuthorization: [
        { name: "from", type: "address" },
        { name: "to", type: "address" },
        { name: "value", type: "uint256" },
        { name: "validAfter", type: "uint256" },
        { name: "validBefore", type: "uint256" },
        { name: "nonce", type: "bytes32" },
      ],
    },
    primaryType: "TransferWithAuthorization",
    message: {
      from: account, to: recipient, value: 1000n, validAfter: 0n,
      validBefore: 2_000_000_000n, nonce: `0x${"11".repeat(32)}`,
    },
  };
}

function permit2TypedData(): BillingTypedData {
  return {
    domain: {
      name: "Permit2", chainId: 31337,
      verifyingContract: "0x000000000022D473030F116dDEE9F6B43aC78BA3",
    },
    types: {
      PermitWitnessTransferFrom: [
        { name: "permitted", type: "TokenPermissions" },
        { name: "spender", type: "address" },
        { name: "nonce", type: "uint256" },
        { name: "deadline", type: "uint256" },
        { name: "witness", type: "Witness" },
      ],
      TokenPermissions: [
        { name: "token", type: "address" },
        { name: "amount", type: "uint256" },
      ],
      Witness: [
        { name: "to", type: "address" },
        { name: "validAfter", type: "uint256" },
      ],
    },
    primaryType: "PermitWitnessTransferFrom",
    message: {
      permitted: { token: asset, amount: 1000n },
      spender: "0x402085c248EeA27D92E8b30b2C58ed07f9E20001",
      nonce: 7n, deadline: 2_000_000_000n,
      witness: { to: recipient, validAfter: 0n },
    },
  };
}

function object(value: unknown): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) throw new TypeError();
  return value as Record<string, unknown>;
}
