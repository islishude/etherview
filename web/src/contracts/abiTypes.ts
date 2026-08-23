import type { AbiFunction, Hex } from "viem";

export const ABI_LIMITS = Object.freeze({
  jsonBytes: 1024 * 1024,
  items: 2048,
  parameters: 4096,
  depth: 16,
  fixedArrayLength: 256,
  dynamicArrayLength: 256,
  inputNodes: 4096,
  outputNodes: 4096,
  stringBytes: 64 * 1024,
  bytesLength: 128 * 1024,
  nameBytes: 256,
  typeBytes: 256,
});

export type AbiFormErrorCode =
  | "INVALID_ABI_JSON"
  | "INVALID_ABI"
  | "ABI_LIMIT_EXCEEDED"
  | "INVALID_ABI_VALUE"
  | "ABI_VALUE_LIMIT_EXCEEDED";

export class AbiFormError extends Error {
  readonly code: AbiFormErrorCode;
  readonly path: string;

  constructor(code: AbiFormErrorCode, path: string) {
    super(`${code} at ${path}`);
    this.name = "AbiFormError";
    this.code = code;
    this.path = path;
  }
}

export type AbiInputNode =
  | Readonly<{
      kind: "scalar";
      type: string;
      value: string;
    }>
  | Readonly<{
      kind: "tuple";
      type: string;
      fields: readonly AbiInputNode[];
    }>
  | Readonly<{
      kind: "array";
      type: string;
      fixedLength: number | null;
      items: readonly AbiInputNode[];
    }>;

export interface AbiFunctionEntry {
  readonly fn: AbiFunction;
  readonly signature: string;
  readonly abi: readonly [AbiFunction];
  readonly payable: boolean;
}

export type FormattedAbiValue =
  | Readonly<{
      kind: "scalar";
      type: string;
      text: string;
      internalType?: string;
    }>
  | Readonly<{
      kind: "tuple";
      type: string;
      fields: readonly FormattedAbiField[];
      internalType?: string;
    }>
  | Readonly<{
      kind: "array";
      type: string;
      items: readonly FormattedAbiValue[];
      internalType?: string;
    }>;

export interface FormattedAbiField {
  readonly index: number;
  readonly name: string;
  readonly type: string;
  readonly internalType?: string;
  readonly value: FormattedAbiValue;
}

export interface FormattedAbiOutput extends FormattedAbiField {
  readonly display: string;
}

export interface DecodedAbiRevert {
  readonly errorName: string;
  readonly signature: string;
  readonly args: readonly FormattedAbiOutput[];
  readonly display: string;
}

export type CalldataDecodeStatus =
  | "decoded"
  | "unknown_selector"
  | "malformed_calldata"
  | "abi_unavailable"
  | "ambiguous_abi_match";

export type CalldataDecodeResult =
  | Readonly<{
      status: "decoded";
      selector: Hex;
      signature: string;
      args: readonly FormattedAbiOutput[];
    }>
  | Readonly<{
      status: "unknown_selector";
      selector: Hex;
    }>
  | Readonly<{
      status: "malformed_calldata";
      selector?: Hex;
    }>
  | Readonly<{
      status: "abi_unavailable";
    }>
  | Readonly<{
      status: "ambiguous_abi_match";
      selector: Hex;
      signatures: readonly string[];
    }>;

export type ArrayDimension = Readonly<{
  length: number | null;
  suffix: string;
}>;

export type ParsedAbiType = Readonly<{
  base: string;
  dimensions: readonly ArrayDimension[];
}>;

export const identifierPattern = /^[A-Za-z_$][A-Za-z0-9_$]*$/u;
export const decimalUnsignedPattern = /^(?:0|[1-9][0-9]*)$/u;
export const decimalSignedPattern = /^-?(?:0|[1-9][0-9]*)$/u;
export const hexPattern = /^0x(?:[0-9a-fA-F]{2})*$/u;
export const textEncoder = new TextEncoder();

export class AbiBudget {
  bytes = 0;
  parameters = 0;

  addBytes(value: string, path: string): void {
    this.bytes += textEncoder.encode(value).byteLength;
    if (this.bytes > ABI_LIMITS.jsonBytes) {
      throw new AbiFormError("ABI_LIMIT_EXCEEDED", path);
    }
  }

  addParameter(path: string): void {
    this.parameters += 1;
    if (this.parameters > ABI_LIMITS.parameters) {
      throw new AbiFormError("ABI_LIMIT_EXCEEDED", path);
    }
  }
}

export class ValueBudget {
  nodes = 0;

  constructor(private readonly maximum: number) {}

  add(path: string): void {
    this.nodes += 1;
    if (this.nodes > this.maximum) {
      throw new AbiFormError("ABI_VALUE_LIMIT_EXCEEDED", path);
    }
  }
}
