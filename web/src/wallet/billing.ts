import { getAddress, isAddress, type Address, type Hex } from "viem";

const PERMIT2 = getAddress("0x000000000022D473030F116dDEE9F6B43aC78BA3");
const X402_EXACT_PERMIT2_PROXY = getAddress(
  "0x402085c248EeA27D92E8b30b2C58ed07f9E20001",
);

export type BillingTransferMethod = "eip3009" | "permit2";

export interface BillingSigningBinding {
  method: BillingTransferMethod;
  chainID: string;
  account: Address;
  asset: Address;
  recipient: Address;
  amountAtomic: string;
  assetName?: string;
  assetVersion?: string;
}

export interface BillingTypedData {
  domain: Record<string, unknown>;
  types: Record<string, unknown>;
  primaryType: string;
  message: Record<string, unknown>;
}

export function encodeBillingTypedData(
  input: BillingTypedData,
  binding: BillingSigningBinding,
): string {
  validateBillingTypedData(input, binding);
  return JSON.stringify(deepJSONValue(input));
}

export function validateBillingTypedData(
  input: BillingTypedData,
  binding: BillingSigningBinding,
): void {
  if (!canonicalPositiveUint256(binding.chainID) || !canonicalPositiveUint256(binding.amountAtomic)) {
    throw new TypeError("Invalid billing signing binding");
  }
  if (decimal(input.domain.chainId) !== binding.chainID) {
    throw new TypeError("Billing typed-data chain mismatch");
  }
  if (binding.method === "eip3009") {
    validateEIP3009(input, binding);
    return;
  }
  validatePermit2(input, binding);
}

function validateEIP3009(input: BillingTypedData, binding: BillingSigningBinding): void {
  const validAfter = decimal(input.message.validAfter);
  const validBefore = decimal(input.message.validBefore);
  if (
    !exactKeys(input.domain, ["chainId", "name", "verifyingContract", "version"]) ||
    !exactKeys(input.message, ["from", "nonce", "to", "validAfter", "validBefore", "value"]) ||
    !exactTypes(input.types, {
      TransferWithAuthorization: [
        ["from", "address"], ["to", "address"], ["value", "uint256"],
        ["validAfter", "uint256"], ["validBefore", "uint256"], ["nonce", "bytes32"],
      ],
    }) ||
    input.primaryType !== "TransferWithAuthorization" ||
    input.domain.name !== binding.assetName ||
    input.domain.version !== binding.assetVersion ||
    address(input.domain.verifyingContract) !== binding.asset ||
    address(input.message.from) !== binding.account ||
    address(input.message.to) !== binding.recipient ||
    decimal(input.message.value) !== binding.amountAtomic ||
    !canonicalUint256(validAfter) ||
    !canonicalPositiveUint256(validBefore) ||
    BigInt(validBefore) <= BigInt(validAfter) ||
    !bytes32(input.message.nonce)
  ) {
    throw new TypeError("Invalid EIP-3009 top-up typed data");
  }
}

function validatePermit2(input: BillingTypedData, binding: BillingSigningBinding): void {
  const permitted = object(input.message.permitted);
  const witness = object(input.message.witness);
  const nonce = decimal(input.message.nonce);
  const deadline = decimal(input.message.deadline);
  const validAfter = decimal(witness.validAfter);
  if (
    !exactKeys(input.domain, ["chainId", "name", "verifyingContract"]) ||
    !exactKeys(input.message, ["deadline", "nonce", "permitted", "spender", "witness"]) ||
    !exactKeys(permitted, ["amount", "token"]) ||
    !exactKeys(witness, ["to", "validAfter"]) ||
    !exactTypes(input.types, {
      PermitWitnessTransferFrom: [
        ["permitted", "TokenPermissions"], ["spender", "address"],
        ["nonce", "uint256"], ["deadline", "uint256"], ["witness", "Witness"],
      ],
      TokenPermissions: [["token", "address"], ["amount", "uint256"]],
      Witness: [["to", "address"], ["validAfter", "uint256"]],
    }) ||
    input.primaryType !== "PermitWitnessTransferFrom" ||
    input.domain.name !== "Permit2" ||
    input.domain.version !== undefined ||
    address(input.domain.verifyingContract) !== PERMIT2 ||
    address(permitted.token) !== binding.asset ||
    decimal(permitted.amount) !== binding.amountAtomic ||
    address(input.message.spender) !== X402_EXACT_PERMIT2_PROXY ||
    !canonicalUint256(nonce) ||
    !canonicalPositiveUint256(deadline) ||
    address(witness.to) !== binding.recipient ||
    !canonicalUint256(validAfter) ||
    BigInt(deadline) <= BigInt(validAfter)
  ) {
    throw new TypeError("Invalid Permit2 top-up typed data");
  }
}

function address(value: unknown): Address | undefined {
  if (typeof value !== "string" || !isAddress(value)) return undefined;
  return getAddress(value);
}

function decimal(value: unknown): string {
  if (typeof value === "bigint") return value.toString(10);
  if (typeof value === "number" && Number.isSafeInteger(value) && value >= 0) {
    return value.toString(10);
  }
  if (typeof value === "string") return value;
  return "";
}

function canonicalPositiveUint256(value: string): boolean {
  return canonicalUint256(value) && value !== "0";
}

function canonicalUint256(value: string): boolean {
  return /^(?:0|[1-9][0-9]*)$/u.test(value) && BigInt(value) < 1n << 256n;
}

function bytes32(value: unknown): value is Hex {
  if (typeof value === "string") return /^0x[0-9a-fA-F]{64}$/u.test(value);
  return value instanceof Uint8Array && value.length === 32;
}

function object(value: unknown): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return {};
  return value as Record<string, unknown>;
}

function exactKeys(value: Record<string, unknown>, expected: string[]): boolean {
  const actual = Object.keys(value).sort();
  return actual.length === expected.length && actual.every((key, index) => key === [...expected].sort()[index]);
}

function exactTypes(
  value: Record<string, unknown>,
  expected: Record<string, Array<[string, string]>>,
): boolean {
  if (!exactKeys(value, Object.keys(expected))) return false;
  for (const [typeName, fields] of Object.entries(expected)) {
    const actual = value[typeName];
    if (!Array.isArray(actual) || actual.length !== fields.length) return false;
    for (let index = 0; index < fields.length; index += 1) {
      const field = object(actual[index]);
      if (
        !exactKeys(field, ["name", "type"]) ||
        field.name !== fields[index]?.[0] ||
        field.type !== fields[index]?.[1]
      ) return false;
    }
  }
  return true;
}

function deepJSONValue(value: unknown): unknown {
  if (typeof value === "bigint") return value.toString(10);
  if (value instanceof Uint8Array) {
    return `0x${[...value].map(item => item.toString(16).padStart(2, "0")).join("")}`;
  }
  if (Array.isArray(value)) return value.map(deepJSONValue);
  if (typeof value === "object" && value !== null) {
    const result: Record<string, unknown> = {};
    for (const [key, item] of Object.entries(value)) result[key] = deepJSONValue(item);
    return result;
  }
  return value;
}
