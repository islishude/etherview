import {
  decodeErrorResult,
  getAddress,
  isAddress,
  type Abi,
  type AbiFunction,
  type AbiParameter,
  type Hex,
} from "viem";

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
    }>
  | Readonly<{
      kind: "tuple";
      type: string;
      fields: readonly FormattedAbiField[];
    }>
  | Readonly<{
      kind: "array";
      type: string;
      items: readonly FormattedAbiValue[];
    }>;

export interface FormattedAbiField {
  readonly index: number;
  readonly name: string;
  readonly type: string;
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

type ArrayDimension = Readonly<{
  length: number | null;
  suffix: string;
}>;

type ParsedAbiType = Readonly<{
  base: string;
  dimensions: readonly ArrayDimension[];
}>;

const identifierPattern = /^[A-Za-z_$][A-Za-z0-9_$]*$/u;
const decimalUnsignedPattern = /^(?:0|[1-9][0-9]*)$/u;
const decimalSignedPattern = /^-?(?:0|[1-9][0-9]*)$/u;
const hexPattern = /^0x(?:[0-9a-fA-F]{2})*$/u;
const textEncoder = new TextEncoder();

class AbiBudget {
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

class ValueBudget {
  nodes = 0;

  constructor(private readonly maximum: number) {}

  add(path: string): void {
    this.nodes += 1;
    if (this.nodes > this.maximum) {
      throw new AbiFormError("ABI_VALUE_LIMIT_EXCEEDED", path);
    }
  }
}

/**
 * Parses one verified artifact ABI into a detached, deeply frozen value.
 * Accessors, custom prototypes, sparse arrays, unknown fields, unsupported
 * Solidity types, and values outside the fixed browser work limits fail closed.
 */
export function parseVerifiedABI(value: unknown): Abi {
  try {
    const decoded = parseAbiJSON(value);
    const budget = new AbiBudget();
    const items = snapshotArray(decoded, "$", ABI_LIMITS.items, "INVALID_ABI", budget);
    const parsed = items.map((item, index) => parseAbiItem(item, `$[${index}]`, budget));
    assertUniqueFunctionSignatures(parsed as Abi);
    return Object.freeze(parsed) as Abi;
  } catch (error) {
    if (error instanceof AbiFormError) throw error;
    throw new AbiFormError("INVALID_ABI", "$");
  }
}

export function canonicalFunctionSignature(fn: AbiFunction): string {
  if (fn.type !== "function" || !validIdentifier(fn.name)) {
    throw new AbiFormError("INVALID_ABI", "function");
  }
  return namedSignature(fn.name, fn.inputs);
}

export function singleFunctionAbi(fn: AbiFunction): readonly [AbiFunction] {
  canonicalFunctionSignature(fn);
  return Object.freeze([fn] as const);
}

export function partitionAbiFunctions(abi: Abi): Readonly<{
  read: readonly AbiFunctionEntry[];
  write: readonly AbiFunctionEntry[];
}> {
  const read: AbiFunctionEntry[] = [];
  const write: AbiFunctionEntry[] = [];
  const signatures = new Set<string>();
  for (const item of abi) {
    if (item.type !== "function") continue;
    const signature = canonicalFunctionSignature(item);
    if (signatures.has(signature)) {
      throw new AbiFormError("INVALID_ABI", signature);
    }
    signatures.add(signature);
    const entry = Object.freeze({
      fn: item,
      signature,
      abi: singleFunctionAbi(item),
      payable: item.stateMutability === "payable",
    });
    if (item.stateMutability === "view" || item.stateMutability === "pure") {
      read.push(entry);
    } else {
      write.push(entry);
    }
  }
  return Object.freeze({ read: Object.freeze(read), write: Object.freeze(write) });
}

export function createAbiInputTree(
  inputs: readonly AbiParameter[],
): readonly AbiInputNode[] {
  const budget = new ValueBudget(ABI_LIMITS.inputNodes);
  const nodes = inputs.map((parameter, index) =>
    createInputNode(parameter, `$[${index}]`, 0, budget),
  );
  return Object.freeze(nodes);
}

/** Creates the positional element shape for either a fixed or dynamic array. */
export function createAbiArrayItem(parameter: AbiParameter): AbiInputNode {
  const outer = outerArray(parameter.type);
  if (!outer) throw new AbiFormError("INVALID_ABI", parameter.type);
  return createInputNode(
    parameterWithType(parameter, outer.elementType),
    "$[item]",
    0,
    new ValueBudget(ABI_LIMITS.inputNodes),
  );
}

/**
 * Checks the complete rendered input tree against the per-function browser
 * work budget. Dynamic array items are created independently, so callers must
 * re-run this check on the complete candidate tree before committing an add.
 */
export function assertAbiInputTreeWithinLimits(
  tree: readonly AbiInputNode[],
): void {
  if (!Array.isArray(tree)) {
    throw new AbiFormError("INVALID_ABI_VALUE", "$");
  }
  const budget = new ValueBudget(ABI_LIMITS.inputNodes);
  tree.forEach((node, index) => assertInputNodeWithinLimits(node, `$[${index}]`, 0, budget));
}

export function parseAbiArguments(
  inputs: readonly AbiParameter[],
  tree: readonly AbiInputNode[],
): readonly unknown[] {
  if (!Array.isArray(tree) || tree.length !== inputs.length) {
    throw new AbiFormError("INVALID_ABI_VALUE", "$");
  }
  const budget = new ValueBudget(ABI_LIMITS.inputNodes);
  const values = inputs.map((parameter, index) =>
    parseInputNode(parameter, tree[index], `$[${index}]`, 0, budget),
  );
  return Object.freeze(values);
}

export function formatAbiResult(
  fn: AbiFunction,
  result: unknown,
): readonly FormattedAbiOutput[] {
  const outputs = fn.outputs;
  if (outputs.length === 0) return Object.freeze([]);
  const rawValues = outputs.length === 1
    ? [result]
    : snapshotArray(result, "$result", outputs.length, "INVALID_ABI_VALUE");
  if (rawValues.length !== outputs.length) {
    throw new AbiFormError("INVALID_ABI_VALUE", "$result");
  }
  return formatParameterValues(outputs, rawValues, "$result");
}

/**
 * Decodes only bounded revert hex. Unknown selectors and malformed payloads are
 * intentionally indistinguishable and return undefined; provider errors never
 * cross this helper.
 */
export function decodeRevert(abi: Abi, data: unknown): DecodedAbiRevert | undefined {
  if (typeof data !== "string" || !validHex(data, 4, ABI_LIMITS.bytesLength)) {
    return undefined;
  }
  try {
    const decoded = decodeErrorResult({ abi, data: data.toLowerCase() as Hex });
    if (decoded.abiItem.type !== "error") return undefined;
    const inputs = decoded.abiItem.inputs;
    const rawArgs = inputs.length === 0
      ? []
      : snapshotArray(decoded.args, "$revert", inputs.length, "INVALID_ABI_VALUE");
    if (rawArgs.length !== inputs.length) return undefined;
    const args = formatParameterValues(inputs, rawArgs, "$revert");
    const signature = namedSignature(decoded.errorName, inputs);
    return Object.freeze({
      errorName: decoded.errorName,
      signature,
      args,
      display: `${signature}${args.length === 0 ? "" : `: ${args.map((arg) => arg.display).join(", ")}`}`,
    });
  } catch {
    return undefined;
  }
}

function parseAbiJSON(value: unknown): unknown {
  if (typeof value !== "string") return value;
  if (textEncoder.encode(value).byteLength > ABI_LIMITS.jsonBytes) {
    throw new AbiFormError("ABI_LIMIT_EXCEEDED", "$");
  }
  try {
    return JSON.parse(value) as unknown;
  } catch {
    throw new AbiFormError("INVALID_ABI_JSON", "$");
  }
}

function parseAbiItem(value: unknown, path: string, budget: AbiBudget): Abi[number] {
  const record = snapshotRecord(value, path, budget);
  const type = requiredString(record, "type", path, budget);
  switch (type) {
    case "function": {
      assertAllowedKeys(
        record,
        new Set(["type", "name", "inputs", "outputs", "stateMutability"]),
        path,
      );
      const name = requiredIdentifier(record, "name", path, budget);
      const inputs = parseParameters(record.get("inputs"), `${path}.inputs`, budget, 0, false);
      const outputs = parseParameters(record.get("outputs"), `${path}.outputs`, budget, 0, false);
      const stateMutability = requiredEnum(
        record,
        "stateMutability",
        ["pure", "view", "nonpayable", "payable"] as const,
        path,
        budget,
      );
      return Object.freeze({ type, name, inputs, outputs, stateMutability });
    }
    case "constructor": {
      assertAllowedKeys(record, new Set(["type", "inputs", "stateMutability"]), path);
      const inputs = parseParameters(record.get("inputs"), `${path}.inputs`, budget, 0, false);
      const stateMutability = requiredEnum(
        record,
        "stateMutability",
        ["nonpayable", "payable"] as const,
        path,
        budget,
      );
      return Object.freeze({ type, inputs, stateMutability });
    }
    case "fallback": {
      assertAllowedKeys(record, new Set(["type", "stateMutability"]), path);
      const stateMutability = requiredEnum(
        record,
        "stateMutability",
        ["nonpayable", "payable"] as const,
        path,
        budget,
      );
      return Object.freeze({ type, stateMutability });
    }
    case "receive": {
      assertAllowedKeys(record, new Set(["type", "stateMutability"]), path);
      const stateMutability = requiredEnum(
        record,
        "stateMutability",
        ["payable"] as const,
        path,
        budget,
      );
      return Object.freeze({ type, stateMutability });
    }
    case "event": {
      assertAllowedKeys(record, new Set(["type", "name", "inputs", "anonymous"]), path);
      const name = requiredIdentifier(record, "name", path, budget);
      const inputs = parseParameters(record.get("inputs"), `${path}.inputs`, budget, 0, true);
      const anonymous = optionalBoolean(record, "anonymous", path) ?? false;
      return Object.freeze({ type, name, inputs, anonymous });
    }
    case "error": {
      assertAllowedKeys(record, new Set(["type", "name", "inputs"]), path);
      const name = requiredIdentifier(record, "name", path, budget);
      const inputs = parseParameters(record.get("inputs"), `${path}.inputs`, budget, 0, false);
      return Object.freeze({ type, name, inputs });
    }
    default:
      throw new AbiFormError("INVALID_ABI", `${path}.type`);
  }
}

function parseParameters(
  value: unknown,
  path: string,
  budget: AbiBudget,
  depth: number,
  event: boolean,
): readonly AbiParameter[] {
  const values = snapshotArray(value, path, ABI_LIMITS.parameters, "INVALID_ABI", budget);
  return Object.freeze(
    values.map((parameter, index) =>
      parseParameter(parameter, `${path}[${index}]`, budget, depth, event),
    ),
  );
}

function parseParameter(
  value: unknown,
  path: string,
  budget: AbiBudget,
  depth: number,
  event: boolean,
): AbiParameter {
  budget.addParameter(path);
  if (depth > ABI_LIMITS.depth) {
    throw new AbiFormError("ABI_LIMIT_EXCEEDED", path);
  }
  const record = snapshotRecord(value, path, budget);
  const allowed = new Set(["type", "name", "internalType", "components"]);
  if (event) allowed.add("indexed");
  assertAllowedKeys(record, allowed, path);
  const type = requiredString(record, "type", path, budget);
  const parsedType = parseType(type, `${path}.type`);
  if (depth + parsedType.dimensions.length > ABI_LIMITS.depth) {
    throw new AbiFormError("ABI_LIMIT_EXCEEDED", `${path}.type`);
  }
  const name = optionalName(record, "name", path, budget);
  const internalType = optionalBoundedString(record, "internalType", path, budget);
  const indexed = event ? optionalBoolean(record, "indexed", path) : undefined;
  let components: readonly AbiParameter[] | undefined;
  if (parsedType.base === "tuple") {
    components = parseParameters(
      record.get("components"),
      `${path}.components`,
      budget,
      depth + parsedType.dimensions.length + 1,
      false,
    );
  } else if (record.has("components")) {
    throw new AbiFormError("INVALID_ABI", `${path}.components`);
  }

  const parameter: Record<string, unknown> = { type };
  if (name !== undefined) parameter.name = name;
  if (internalType !== undefined) parameter.internalType = internalType;
  if (components !== undefined) parameter.components = components;
  if (indexed !== undefined) parameter.indexed = indexed;
  return Object.freeze(parameter) as AbiParameter;
}

function parseType(type: string, path: string): ParsedAbiType {
  if (textEncoder.encode(type).byteLength > ABI_LIMITS.typeBytes || type.trim() !== type) {
    throw new AbiFormError("INVALID_ABI", path);
  }
  let remaining = type;
  const dimensions: ArrayDimension[] = [];
  while (remaining.endsWith("]")) {
    const match = /^(.*)\[([0-9]*)\]$/u.exec(remaining);
    if (!match || match[1] === undefined || match[2] === undefined) {
      throw new AbiFormError("INVALID_ABI", path);
    }
    const rawLength = match[2];
    let length: number | null = null;
    if (rawLength !== "") {
      if (!/^[1-9][0-9]*$/u.test(rawLength)) {
        throw new AbiFormError("INVALID_ABI", path);
      }
      length = Number(rawLength);
      if (!Number.isSafeInteger(length) || length > ABI_LIMITS.fixedArrayLength) {
        throw new AbiFormError("ABI_LIMIT_EXCEEDED", path);
      }
    }
    dimensions.push(Object.freeze({ length, suffix: `[${rawLength}]` }));
    remaining = match[1];
    if (dimensions.length > ABI_LIMITS.depth) {
      throw new AbiFormError("ABI_LIMIT_EXCEEDED", path);
    }
  }
  if (remaining.includes("[") || remaining.includes("]") || !validBaseType(remaining)) {
    throw new AbiFormError("INVALID_ABI", path);
  }
  return Object.freeze({ base: remaining, dimensions: Object.freeze(dimensions) });
}

function validBaseType(type: string): boolean {
  if (["address", "bool", "string", "bytes", "function", "tuple"].includes(type)) {
    return true;
  }
  const bytes = /^bytes([0-9]+)$/u.exec(type);
  if (bytes?.[1] !== undefined) {
    const size = Number(bytes[1]);
    return Number.isInteger(size) && size >= 1 && size <= 32 && String(size) === bytes[1];
  }
  const integer = /^(u?int)([0-9]*)$/u.exec(type);
  if (!integer) return false;
  const rawBits = integer[2] ?? "";
  if (rawBits === "") return true;
  const bits = Number(rawBits);
  return bits >= 8 && bits <= 256 && bits % 8 === 0 && String(bits) === rawBits;
}

function namedSignature(name: string, inputs: readonly AbiParameter[]): string {
  if (!validIdentifier(name)) throw new AbiFormError("INVALID_ABI", name);
  return `${name}(${inputs.map((parameter) => canonicalParameterType(parameter)).join(",")})`;
}

function canonicalParameterType(parameter: AbiParameter): string {
  const parsed = parseType(parameter.type, parameter.type);
  const suffix = parsed.dimensions
    .slice()
    .reverse()
    .map((dimension) => dimension.suffix)
    .join("");
  if (parsed.base === "tuple") {
    const components = "components" in parameter ? parameter.components : undefined;
    if (!components) throw new AbiFormError("INVALID_ABI", parameter.type);
    return `(${components.map((component) => canonicalParameterType(component)).join(",")})${suffix}`;
  }
  const base = parsed.base === "uint" ? "uint256" : parsed.base === "int" ? "int256" : parsed.base;
  return `${base}${suffix}`;
}

function assertUniqueFunctionSignatures(abi: Abi): void {
  const signatures = new Set<string>();
  for (const item of abi) {
    if (item.type !== "function") continue;
    const signature = canonicalFunctionSignature(item);
    if (signatures.has(signature)) throw new AbiFormError("INVALID_ABI", signature);
    signatures.add(signature);
  }
}

function createInputNode(
  parameter: AbiParameter,
  path: string,
  depth: number,
  budget: ValueBudget,
): AbiInputNode {
  budget.add(path);
  if (depth > ABI_LIMITS.depth) {
    throw new AbiFormError("ABI_VALUE_LIMIT_EXCEEDED", path);
  }
  const array = outerArray(parameter.type);
  if (array) {
    const element = parameterWithType(parameter, array.elementType);
    const items: AbiInputNode[] = [];
    if (array.length !== null) {
      for (let index = 0; index < array.length; index += 1) {
        items.push(createInputNode(element, `${path}[${index}]`, depth + 1, budget));
      }
    }
    return Object.freeze({
      kind: "array",
      type: parameter.type,
      fixedLength: array.length,
      items: Object.freeze(items),
    });
  }
  const parsed = parseType(parameter.type, path);
  if (parsed.base === "tuple") {
    const components = "components" in parameter ? parameter.components : undefined;
    if (!components) throw new AbiFormError("INVALID_ABI", path);
    const fields = components.map((component, index) =>
      createInputNode(component, `${path}.${index}`, depth + 1, budget),
    );
    return Object.freeze({ kind: "tuple", type: parameter.type, fields: Object.freeze(fields) });
  }
  return Object.freeze({ kind: "scalar", type: parameter.type, value: "" });
}

function assertInputNodeWithinLimits(
  node: AbiInputNode,
  path: string,
  depth: number,
  budget: ValueBudget,
): void {
  budget.add(path);
  if (depth > ABI_LIMITS.depth || typeof node !== "object" || node === null) {
    throw new AbiFormError("ABI_VALUE_LIMIT_EXCEEDED", path);
  }
  switch (node.kind) {
    case "scalar":
      return;
    case "tuple":
      if (!Array.isArray(node.fields)) {
        throw new AbiFormError("INVALID_ABI_VALUE", `${path}.fields`);
      }
      node.fields.forEach((field, index) =>
        assertInputNodeWithinLimits(field, `${path}.${index}`, depth + 1, budget),
      );
      return;
    case "array":
      if (!Array.isArray(node.items)) {
        throw new AbiFormError("INVALID_ABI_VALUE", `${path}.items`);
      }
      node.items.forEach((item, index) =>
        assertInputNodeWithinLimits(item, `${path}[${index}]`, depth + 1, budget),
      );
  }
}

function parseInputNode(
  parameter: AbiParameter,
  node: AbiInputNode | undefined,
  path: string,
  depth: number,
  budget: ValueBudget,
): unknown {
  budget.add(path);
  if (depth > ABI_LIMITS.depth || node === undefined) {
    throw new AbiFormError("INVALID_ABI_VALUE", path);
  }
  const record = snapshotRecord(node, path);
  const kind = requiredLiteral(record, "kind", ["scalar", "tuple", "array"] as const, path);
  const type = requiredLiteralString(record, "type", path);
  if (type !== parameter.type) throw new AbiFormError("INVALID_ABI_VALUE", `${path}.type`);
  const array = outerArray(parameter.type);
  if (array) {
    assertAllowedKeys(record, new Set(["kind", "type", "fixedLength", "items"]), path);
    if (kind !== "array") throw new AbiFormError("INVALID_ABI_VALUE", `${path}.kind`);
    const fixedLength = record.get("fixedLength");
    if (fixedLength !== array.length) {
      throw new AbiFormError("INVALID_ABI_VALUE", `${path}.fixedLength`);
    }
    const maximum = array.length ?? ABI_LIMITS.dynamicArrayLength;
    const items = snapshotArray(record.get("items"), `${path}.items`, maximum, "INVALID_ABI_VALUE");
    if (array.length !== null && items.length !== array.length) {
      throw new AbiFormError("INVALID_ABI_VALUE", `${path}.items`);
    }
    const element = parameterWithType(parameter, array.elementType);
    return Object.freeze(
      items.map((item, index) =>
        parseInputNode(element, item as AbiInputNode, `${path}[${index}]`, depth + 1, budget),
      ),
    );
  }
  const parsed = parseType(parameter.type, path);
  if (parsed.base === "tuple") {
    assertAllowedKeys(record, new Set(["kind", "type", "fields"]), path);
    if (kind !== "tuple") throw new AbiFormError("INVALID_ABI_VALUE", `${path}.kind`);
    const components = "components" in parameter ? parameter.components : undefined;
    if (!components) throw new AbiFormError("INVALID_ABI", path);
    const fields = snapshotArray(
      record.get("fields"),
      `${path}.fields`,
      components.length,
      "INVALID_ABI_VALUE",
    );
    if (fields.length !== components.length) {
      throw new AbiFormError("INVALID_ABI_VALUE", `${path}.fields`);
    }
    return Object.freeze(
      components.map((component, index) =>
        parseInputNode(
          component,
          fields[index] as AbiInputNode,
          `${path}.${index}`,
          depth + 1,
          budget,
        ),
      ),
    );
  }
  assertAllowedKeys(record, new Set(["kind", "type", "value"]), path);
  if (kind !== "scalar" || typeof record.get("value") !== "string") {
    throw new AbiFormError("INVALID_ABI_VALUE", path);
  }
  return parseScalar(parsed.base, record.get("value") as string, path);
}

function parseScalar(type: string, value: string, path: string): unknown {
  switch (type) {
    case "address":
      if (!isAddress(value, { strict: true })) {
        throw new AbiFormError("INVALID_ABI_VALUE", path);
      }
      return getAddress(value);
    case "bool":
      if (value === "true") return true;
      if (value === "false") return false;
      throw new AbiFormError("INVALID_ABI_VALUE", path);
    case "string":
      if (textEncoder.encode(value).byteLength > ABI_LIMITS.stringBytes) {
        throw new AbiFormError("ABI_VALUE_LIMIT_EXCEEDED", path);
      }
      return value;
    case "bytes":
      if (!validHex(value, 0, ABI_LIMITS.bytesLength)) {
        throw new AbiFormError("INVALID_ABI_VALUE", path);
      }
      return value.toLowerCase() as Hex;
    case "function":
      if (!validHex(value, 24, 24)) throw new AbiFormError("INVALID_ABI_VALUE", path);
      return value.toLowerCase() as Hex;
    default:
      break;
  }
  const bytes = /^bytes([0-9]+)$/u.exec(type);
  if (bytes?.[1] !== undefined) {
    const length = Number(bytes[1]);
    if (!validHex(value, length, length)) throw new AbiFormError("INVALID_ABI_VALUE", path);
    return value.toLowerCase() as Hex;
  }
  const integer = /^(u?int)([0-9]*)$/u.exec(type);
  if (!integer) throw new AbiFormError("INVALID_ABI", path);
  const signed = integer[1] === "int";
  const bits = integer[2] === "" ? 256 : Number(integer[2]);
  const pattern = signed ? decimalSignedPattern : decimalUnsignedPattern;
  if (!pattern.test(value) || value === "-0" || value.length > 80) {
    throw new AbiFormError("INVALID_ABI_VALUE", path);
  }
  const parsed = BigInt(value);
  const minimum = signed ? -(1n << BigInt(bits - 1)) : 0n;
  const maximum = signed ? (1n << BigInt(bits - 1)) - 1n : (1n << BigInt(bits)) - 1n;
  if (parsed < minimum || parsed > maximum) {
    throw new AbiFormError("INVALID_ABI_VALUE", path);
  }
  return parsed;
}

function formatParameterValues(
  parameters: readonly AbiParameter[],
  values: readonly unknown[],
  path: string,
): readonly FormattedAbiOutput[] {
  const budget = new ValueBudget(ABI_LIMITS.outputNodes);
  const outputs = parameters.map((parameter, index) => {
    const value = formatValue(parameter, values[index], `${path}[${index}]`, 0, budget);
    return Object.freeze({
      index,
      name: parameter.name ?? "",
      type: parameter.type,
      value,
      display: formattedValueText(value),
    });
  });
  return Object.freeze(outputs);
}

function formatValue(
  parameter: AbiParameter,
  value: unknown,
  path: string,
  depth: number,
  budget: ValueBudget,
): FormattedAbiValue {
  budget.add(path);
  if (depth > ABI_LIMITS.depth) {
    throw new AbiFormError("ABI_VALUE_LIMIT_EXCEEDED", path);
  }
  const array = outerArray(parameter.type);
  if (array) {
    const maximum = array.length ?? ABI_LIMITS.dynamicArrayLength;
    const values = snapshotArray(value, path, maximum, "INVALID_ABI_VALUE");
    if (array.length !== null && values.length !== array.length) {
      throw new AbiFormError("INVALID_ABI_VALUE", path);
    }
    const element = parameterWithType(parameter, array.elementType);
    const items = values.map((item, index) =>
      formatValue(element, item, `${path}[${index}]`, depth + 1, budget),
    );
    return Object.freeze({ kind: "array", type: parameter.type, items: Object.freeze(items) });
  }
  const parsed = parseType(parameter.type, path);
  if (parsed.base === "tuple") {
    const components = "components" in parameter ? parameter.components : undefined;
    if (!components) throw new AbiFormError("INVALID_ABI", path);
    const values = tupleValues(components, value, path);
    const fields = components.map((component, index) =>
      Object.freeze({
        index,
        name: component.name ?? "",
        type: component.type,
        value: formatValue(component, values[index], `${path}.${index}`, depth + 1, budget),
      }),
    );
    return Object.freeze({ kind: "tuple", type: parameter.type, fields: Object.freeze(fields) });
  }
  return Object.freeze({ kind: "scalar", type: parameter.type, text: formatScalar(parsed.base, value, path) });
}

function formatScalar(type: string, value: unknown, path: string): string {
  if (/^(?:u?int)(?:[0-9]*)$/u.test(type)) {
    if (typeof value !== "bigint") throw new AbiFormError("INVALID_ABI_VALUE", path);
    return value.toString(10);
  }
  if (type === "bool") {
    if (typeof value !== "boolean") throw new AbiFormError("INVALID_ABI_VALUE", path);
    return String(value);
  }
  if (type === "address") {
    if (typeof value !== "string" || !isAddress(value, { strict: false })) {
      throw new AbiFormError("INVALID_ABI_VALUE", path);
    }
    return getAddress(value);
  }
  if (type === "string") {
    if (typeof value !== "string") throw new AbiFormError("INVALID_ABI_VALUE", path);
    if (textEncoder.encode(value).byteLength > ABI_LIMITS.stringBytes) {
      throw new AbiFormError("ABI_VALUE_LIMIT_EXCEEDED", path);
    }
    return value;
  }
  if (type === "bytes") {
    if (typeof value !== "string" || !validHex(value, 0, ABI_LIMITS.bytesLength)) {
      throw new AbiFormError("INVALID_ABI_VALUE", path);
    }
    return value.toLowerCase();
  }
  if (type === "function") {
    if (typeof value !== "string" || !validHex(value, 24, 24)) {
      throw new AbiFormError("INVALID_ABI_VALUE", path);
    }
    return value.toLowerCase();
  }
  const bytes = /^bytes([0-9]+)$/u.exec(type);
  if (bytes?.[1] !== undefined) {
    const length = Number(bytes[1]);
    if (typeof value !== "string" || !validHex(value, length, length)) {
      throw new AbiFormError("INVALID_ABI_VALUE", path);
    }
    return value.toLowerCase();
  }
  throw new AbiFormError("INVALID_ABI", path);
}

function tupleValues(
  components: readonly AbiParameter[],
  value: unknown,
  path: string,
): readonly unknown[] {
  if (Array.isArray(value)) {
    const values = snapshotArray(value, path, components.length, "INVALID_ABI_VALUE");
    if (values.length !== components.length) throw new AbiFormError("INVALID_ABI_VALUE", path);
    return values;
  }
  const names = components.map((component) => component.name ?? "");
  if (names.some((name) => name === "") || new Set(names).size !== names.length) {
    throw new AbiFormError("INVALID_ABI_VALUE", path);
  }
  const record = snapshotRecord(value, path);
  assertAllowedKeys(record, new Set(names), path);
  return names.map((name) => {
    if (!record.has(name)) throw new AbiFormError("INVALID_ABI_VALUE", `${path}.${name}`);
    return record.get(name);
  });
}

function formattedValueText(value: FormattedAbiValue): string {
  switch (value.kind) {
    case "scalar":
      return value.text;
    case "array":
      return `[${value.items.map((item) => formattedValueText(item)).join(", ")}]`;
    case "tuple":
      return `(${value.fields.map((field) => `${field.name === "" ? field.index : field.name}: ${formattedValueText(field.value)}`).join(", ")})`;
  }
}

function outerArray(type: string): { elementType: string; length: number | null } | undefined {
  const match = /^(.*)\[([0-9]*)\]$/u.exec(type);
  if (!match || match[1] === undefined || match[2] === undefined) return undefined;
  return {
    elementType: match[1],
    length: match[2] === "" ? null : Number(match[2]),
  };
}

function parameterWithType(parameter: AbiParameter, type: string): AbiParameter {
  const next: Record<string, unknown> = { type };
  if (parameter.name !== undefined) next.name = parameter.name;
  if (parameter.internalType !== undefined) next.internalType = parameter.internalType;
  if ("components" in parameter && parameter.components !== undefined) {
    next.components = parameter.components;
  }
  return Object.freeze(next) as AbiParameter;
}

function validHex(value: string, minimumBytes: number, maximumBytes: number): boolean {
  if (!hexPattern.test(value)) return false;
  const bytes = (value.length - 2) / 2;
  return bytes >= minimumBytes && bytes <= maximumBytes;
}

function validIdentifier(value: string): boolean {
  return identifierPattern.test(value) && textEncoder.encode(value).byteLength <= ABI_LIMITS.nameBytes;
}

function snapshotArray(
  value: unknown,
  path: string,
  maximum: number,
  code: Extract<AbiFormErrorCode, "INVALID_ABI" | "INVALID_ABI_VALUE">,
  budget?: AbiBudget,
): readonly unknown[] {
  if (!Array.isArray(value)) throw new AbiFormError(code, path);
  let keys: readonly PropertyKey[];
  let length: number;
  try {
    keys = Reflect.ownKeys(value);
    length = value.length;
  } catch {
    throw new AbiFormError(code, path);
  }
  if (!Number.isSafeInteger(length) || length < 0) throw new AbiFormError(code, path);
  if (length > maximum) {
    throw new AbiFormError(
      code === "INVALID_ABI" ? "ABI_LIMIT_EXCEEDED" : "ABI_VALUE_LIMIT_EXCEEDED",
      path,
    );
  }
  const expectedKeys = new Set(["length", ...Array.from({ length }, (_, index) => String(index))]);
  if (keys.some((key) => typeof key !== "string" || !expectedKeys.has(key))) {
    throw new AbiFormError(code, path);
  }
  const result: unknown[] = [];
  for (let index = 0; index < length; index += 1) {
    let descriptor: PropertyDescriptor | undefined;
    try {
      descriptor = Object.getOwnPropertyDescriptor(value, String(index));
    } catch {
      throw new AbiFormError(code, `${path}[${index}]`);
    }
    if (!descriptor || !("value" in descriptor) || !descriptor.enumerable) {
      throw new AbiFormError(code, `${path}[${index}]`);
    }
    result.push(descriptor.value);
  }
  budget?.addBytes(String(length), path);
  return Object.freeze(result);
}

function snapshotRecord(
  value: unknown,
  path: string,
  budget?: AbiBudget,
): ReadonlyMap<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new AbiFormError("INVALID_ABI", path);
  }
  let prototype: object | null;
  let keys: readonly PropertyKey[];
  try {
    prototype = Object.getPrototypeOf(value) as object | null;
    keys = Reflect.ownKeys(value);
  } catch {
    throw new AbiFormError("INVALID_ABI", path);
  }
  if (prototype !== Object.prototype && prototype !== null) {
    throw new AbiFormError("INVALID_ABI", path);
  }
  const result = new Map<string, unknown>();
  for (const key of keys) {
    if (typeof key !== "string") throw new AbiFormError("INVALID_ABI", path);
    let descriptor: PropertyDescriptor | undefined;
    try {
      descriptor = Object.getOwnPropertyDescriptor(value, key);
    } catch {
      throw new AbiFormError("INVALID_ABI", `${path}.${key}`);
    }
    if (!descriptor || !("value" in descriptor) || !descriptor.enumerable) {
      throw new AbiFormError("INVALID_ABI", `${path}.${key}`);
    }
    budget?.addBytes(key, `${path}.${key}`);
    result.set(key, descriptor.value);
  }
  return result;
}

function assertAllowedKeys(
  record: ReadonlyMap<string, unknown>,
  allowed: ReadonlySet<string>,
  path: string,
): void {
  for (const key of record.keys()) {
    if (!allowed.has(key)) throw new AbiFormError("INVALID_ABI", `${path}.${key}`);
  }
}

function requiredString(
  record: ReadonlyMap<string, unknown>,
  key: string,
  path: string,
  budget: AbiBudget,
): string {
  const value = record.get(key);
  if (typeof value !== "string") throw new AbiFormError("INVALID_ABI", `${path}.${key}`);
  budget.addBytes(value, `${path}.${key}`);
  return value;
}

function requiredIdentifier(
  record: ReadonlyMap<string, unknown>,
  key: string,
  path: string,
  budget: AbiBudget,
): string {
  const value = requiredString(record, key, path, budget);
  if (!validIdentifier(value)) throw new AbiFormError("INVALID_ABI", `${path}.${key}`);
  return value;
}

function optionalName(
  record: ReadonlyMap<string, unknown>,
  key: string,
  path: string,
  budget: AbiBudget,
): string | undefined {
  if (!record.has(key)) return undefined;
  const value = requiredString(record, key, path, budget);
  if (value !== "" && !validIdentifier(value)) {
    throw new AbiFormError("INVALID_ABI", `${path}.${key}`);
  }
  return value;
}

function optionalBoundedString(
  record: ReadonlyMap<string, unknown>,
  key: string,
  path: string,
  budget: AbiBudget,
): string | undefined {
  if (!record.has(key)) return undefined;
  const value = requiredString(record, key, path, budget);
  if (textEncoder.encode(value).byteLength > ABI_LIMITS.typeBytes) {
    throw new AbiFormError("ABI_LIMIT_EXCEEDED", `${path}.${key}`);
  }
  return value;
}

function optionalBoolean(
  record: ReadonlyMap<string, unknown>,
  key: string,
  path: string,
): boolean | undefined {
  if (!record.has(key)) return undefined;
  const value = record.get(key);
  if (typeof value !== "boolean") throw new AbiFormError("INVALID_ABI", `${path}.${key}`);
  return value;
}

function requiredEnum<const Values extends readonly string[]>(
  record: ReadonlyMap<string, unknown>,
  key: string,
  values: Values,
  path: string,
  budget: AbiBudget,
): Values[number] {
  const value = requiredString(record, key, path, budget);
  if (!values.includes(value)) throw new AbiFormError("INVALID_ABI", `${path}.${key}`);
  return value;
}

function requiredLiteral<const Values extends readonly string[]>(
  record: ReadonlyMap<string, unknown>,
  key: string,
  values: Values,
  path: string,
): Values[number] {
  const value = record.get(key);
  if (typeof value !== "string" || !values.includes(value)) {
    throw new AbiFormError("INVALID_ABI_VALUE", `${path}.${key}`);
  }
  return value;
}

function requiredLiteralString(
  record: ReadonlyMap<string, unknown>,
  key: string,
  path: string,
): string {
  const value = record.get(key);
  if (typeof value !== "string") throw new AbiFormError("INVALID_ABI_VALUE", `${path}.${key}`);
  return value;
}
