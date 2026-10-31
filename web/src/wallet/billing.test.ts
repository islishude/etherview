import { hashTypedData, type TypedDataDefinition } from "viem";
import { describe, expect, it } from "vitest";

import {
  encodeBillingTypedData,
  validateBillingTypedData,
  type BillingSigningBinding,
  type BillingTypedData,
} from "./billing";

const account = "0x1111111111111111111111111111111111111111";
const asset = "0x2222222222222222222222222222222222222222";
const recipient = "0x3333333333333333333333333333333333333333";

describe("billing typed-data boundary", () => {
  it.each([
    {
      method: "eip3009" as const,
      create: eip3009TypedData,
      domainFields: ["name", "version", "chainId", "verifyingContract"],
      digest: "0xb2ffc8ce212f6b4c78dfe55f487413a8a74e8291a9a0b18ecefd6b3ae99d6c77",
      missingDomainDigest: "0x6aa0cd83bce5ec628bf95137f07b219e8a228db7319db2572ce8fa4de4680a0f",
    },
    {
      method: "permit2" as const,
      create: permit2TypedData,
      domainFields: ["name", "chainId", "verifyingContract"],
      digest: "0xcc98ae8403559bc9d1293262361cfdbc5d12900d97af498c74f652e6a1392d8b",
      missingDomainDigest: "0xe38004500c37cc51dbeb903f1caf0a76fb498c57e577557217b033f099780ede",
    },
  ])("encodes the complete $method signing domain for raw wallet RPC", (testCase) => {
    const input = testCase.create();
    const original = structuredClone(input);
    const binding: BillingSigningBinding = {
      method: testCase.method,
      chainID: "31337",
      account,
      asset,
      recipient,
      amountAtomic: "1000",
      assetName: "Local USD",
      assetVersion: "1",
    };
    const wire = JSON.parse(encodeBillingTypedData(input, binding)) as TypedDataDefinition;

    // Independent vectors from @metamask/eth-sig-util 8.2.0, TypedDataUtils.eip712Hash V4.
    // An explicit empty fallback models its missing-domain behavior and prevents
    // viem from silently deriving the omitted schema, which hid this regression.
    expect(hashTypedData({ ...wire, types: { EIP712Domain: [], ...wire.types } })).toBe(
      testCase.digest,
    );
    expect(hashTypedData({ ...wire, types: { ...wire.types, EIP712Domain: [] } })).toBe(
      testCase.missingDomainDigest,
    );
    expect(wire.types.EIP712Domain).toEqual(
      testCase.domainFields.map((name) => ({
        name,
        type: name === "chainId" ? "uint256" : name === "verifyingContract" ? "address" : "string",
      })),
    );
    expect(input).toEqual(original);
    expect(input.types).not.toHaveProperty("EIP712Domain");

    // Only this boundary may add the trusted domain schema after validating the SDK input.
    for (const fields of [[], [{ name: "chainId", type: "uint256" }]]) {
      const hostile = structuredClone(input);
      hostile.types.EIP712Domain = fields;
      expect(() => encodeBillingTypedData(hostile, binding)).toThrow(TypeError);
    }
  });

  it("preserves uint256 precision and byte nonces when encoding wallet RPC", () => {
    const input = eip3009TypedData();
    const amount = (1n << 256n) - 1n;
    input.message.value = amount;
    input.message.nonce = new Uint8Array(32).fill(17);
    const wire = JSON.parse(
      encodeBillingTypedData(input, {
        method: "eip3009",
        chainID: "31337",
        account,
        asset,
        recipient,
        amountAtomic: amount.toString(),
        assetName: "Local USD",
        assetVersion: "1",
      }),
    );
    expect(wire.message.value).toBe(amount.toString());
    expect(wire.message.nonce).toBe(`0x${"11".repeat(32)}`);
  });

  it("accepts only the exact EIP-3009 shape and binding", () => {
    const binding: BillingSigningBinding = {
      method: "eip3009",
      chainID: "31337",
      account,
      asset,
      recipient,
      amountAtomic: "1000",
      assetName: "Local USD",
      assetVersion: "1",
    };
    const typedData = eip3009TypedData();
    expect(() => validateBillingTypedData(typedData, binding)).not.toThrow();
    for (const mutate of [
      (value: BillingTypedData) => {
        value.message.value = 1001n;
      },
      (value: BillingTypedData) => {
        value.message.extra = "hostile";
      },
      (value: BillingTypedData) => {
        value.domain.chainId = 1;
      },
      (value: BillingTypedData) => {
        value.types.TransferWithAuthorization = [];
      },
      (value: BillingTypedData) => {
        value.message.validBefore = 0n;
      },
    ]) {
      const candidate = structuredClone(typedData);
      mutate(candidate);
      expect(() => validateBillingTypedData(candidate, binding)).toThrow(TypeError);
    }
  });

  it("accepts only the canonical Permit2 proxy and witness", () => {
    const binding: BillingSigningBinding = {
      method: "permit2",
      chainID: "31337",
      account,
      asset,
      recipient,
      amountAtomic: "1000",
    };
    const typedData = permit2TypedData();
    expect(() => validateBillingTypedData(typedData, binding)).not.toThrow();
    for (const mutate of [
      (value: BillingTypedData) => {
        value.message.spender = recipient;
      },
      (value: BillingTypedData) => {
        object(value.message.witness).to = account;
      },
      (value: BillingTypedData) => {
        object(value.message.permitted).amount = 999n;
      },
      (value: BillingTypedData) => {
        value.domain.version = "1";
      },
      (value: BillingTypedData) => {
        value.types.Witness = [{ name: "to", type: "address" }];
      },
      (value: BillingTypedData) => {
        value.message.deadline = 0n;
      },
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
      from: account,
      to: recipient,
      value: 1000n,
      validAfter: 0n,
      validBefore: 2_000_000_000n,
      nonce: `0x${"11".repeat(32)}`,
    },
  };
}

function permit2TypedData(): BillingTypedData {
  return {
    domain: {
      name: "Permit2",
      chainId: 31337,
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
      nonce: 7n,
      deadline: 2_000_000_000n,
      witness: { to: recipient, validAfter: 0n },
    },
  };
}

function object(value: unknown): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) throw new TypeError();
  return value as Record<string, unknown>;
}
