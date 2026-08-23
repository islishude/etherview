import {
  decodeAbiParameters,
  decodeErrorResult,
  decodeFunctionData,
  encodeAbiParameters,
  encodeFunctionData,
  getAddress,
  isAddress,
  toFunctionSelector,
  type Abi,
  type AbiFunction,
  type AbiParameter,
  type Hex,
} from "viem";
import {
  ABI_LIMITS,
  AbiBudget,
  AbiFormError,
  ValueBudget,
  decimalSignedPattern,
  decimalUnsignedPattern,
  hexPattern,
  identifierPattern,
  textEncoder,
  type AbiFormErrorCode,
  type AbiFunctionEntry,
  type AbiInputNode,
  type ArrayDimension,
  type CalldataDecodeResult,
  type DecodedAbiRevert,
  type FormattedAbiOutput,
  type FormattedAbiValue,
  type ParsedAbiType,
} from "./abiTypes";
export { ABI_LIMITS, AbiFormError } from "./abiTypes";
export type {
  AbiFormErrorCode, AbiInputNode, AbiFunctionEntry, CalldataDecodeResult,
  CalldataDecodeStatus, DecodedAbiRevert, FormattedAbiField,
  FormattedAbiOutput, FormattedAbiValue,
} from "./abiTypes";
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

/**
 * Decodes canonical constructor arguments using the published, validated ABI.
 * The encoded bytes are round-tripped through Viem so trailing or otherwise
 * non-canonical data cannot be presented as a trustworthy decoded value.
 */
export function decodeConstructorArguments(
  abi: unknown,
  encoded: unknown,
): readonly FormattedAbiOutput[] {
  const parsedABI = parseVerifiedABI(abi);
  const constructors = parsedABI.filter((item) => item.type === "constructor");
  if (constructors.length !== 1) {
    throw new AbiFormError("INVALID_ABI", "constructor");
  }
  if (typeof encoded !== "string" || !validHex(encoded, 0, ABI_LIMITS.bytesLength)) {
    throw new AbiFormError("INVALID_ABI_VALUE", "constructorArguments");
  }
  const constructor = constructors[0]!;
  const values = decodeAbiParameters(constructor.inputs, encoded as Hex);
  const canonical = encodeAbiParameters(constructor.inputs, values);
  if (canonical.toLowerCase() !== encoded.toLowerCase()) {
    throw new AbiFormError("INVALID_ABI_VALUE", "constructorArguments");
  }
  return formatParameterValues(constructor.inputs, values, "$constructor");
}

/**
 * Decodes one verified ABI against complete calldata. The selector and
 * arguments are round-tripped through Viem so trailing or non-canonical bytes
 * never become a displayed function call.
 */
export function decodeCalldata(
  abi: unknown,
  encoded: unknown,
): CalldataDecodeResult {
  if (typeof encoded !== "string" || !validHex(encoded, 4, ABI_LIMITS.bytesLength)) {
    return Object.freeze({ status: "malformed_calldata" });
  }
  const selector = encoded.slice(0, 10).toLowerCase() as Hex;
  let parsedABI: Abi;
  try {
    parsedABI = parseVerifiedABI(abi);
  } catch {
    return Object.freeze({ status: "abi_unavailable" });
  }

  const matches = parsedABI.filter((item): item is AbiFunction =>
    item.type === "function" && toFunctionSelector(canonicalFunctionSignature(item)) === selector,
  );
  if (matches.length === 0) {
    return Object.freeze({ status: "unknown_selector", selector });
  }

  const decoded: Array<Extract<CalldataDecodeResult, { status: "decoded" }>> = [];
  for (const fn of matches) {
    try {
      const singleABI = singleFunctionAbi(fn);
      const result = decodeFunctionData({ abi: singleABI, data: encoded.toLowerCase() as Hex });
      const canonical = encodeFunctionData({
        abi: singleABI,
        functionName: fn.name,
        args: result.args,
      });
      if (canonical.toLowerCase() !== encoded.toLowerCase()) continue;
      decoded.push(Object.freeze({
        status: "decoded",
        selector,
        signature: canonicalFunctionSignature(fn),
        args: formatParameterValues(fn.inputs, result.args, "$calldata"),
      }));
    } catch {
      // A selector match with invalid or non-canonical arguments is malformed.
    }
  }
  if (decoded.length === 0) {
    return Object.freeze({ status: "malformed_calldata", selector });
  }
  const signatures = [...new Set(decoded.map((candidate) => candidate.signature))].sort();
  if (signatures.length > 1) {
    return Object.freeze({ status: "ambiguous_abi_match", selector, signatures });
  }
  return decoded[0]!;
}

/** Merges independently verified ABI candidates without choosing conflicts. */
export function mergeCalldataResults(
  results: readonly CalldataDecodeResult[],
): CalldataDecodeResult {
  const decoded = results.filter((result): result is Extract<CalldataDecodeResult, { status: "decoded" }> =>
    result.status === "decoded",
  );
  const distinct = new Map<string, Extract<CalldataDecodeResult, { status: "decoded" }>>();
  for (const result of decoded) {
    const key = `${result.signature}\u0000${result.args.map((arg) => arg.display).join("\u0001")}`;
    if (!distinct.has(key)) distinct.set(key, result);
  }
  if (distinct.size === 1) return [...distinct.values()][0]!;
  if (distinct.size > 1) {
    return Object.freeze({
      status: "ambiguous_abi_match",
      selector: decoded[0]!.selector,
      signatures: [...new Set(decoded.map((result) => result.signature))].sort(),
    });
  }
  const ambiguous = results.find((result) => result.status === "ambiguous_abi_match");
  if (ambiguous) return ambiguous;
  const malformed = results.find((result) => result.status === "malformed_calldata");
  if (malformed) return malformed;
  const unknown = results.find((result) => result.status === "unknown_selector");
  if (unknown) return unknown;
  return Object.freeze({ status: "abi_unavailable" });
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
 * Validates and snapshots the transaction-time calldata projection returned by
 * the API. Unlike Viem results, public integer values are decimal strings and
 * tuple values are always positional arrays. Shape/value disagreements fail
 * closed before any partial tree is rendered.
 */
export function formatTransactionCalldataInputs(
  value: unknown,
): readonly FormattedAbiOutput[] {
  const schemaBudget = new AbiBudget();
  const values = snapshotArray(value, "$calldata", 256, "INVALID_ABI", schemaBudget);
  const parsed = values.map((item, index) =>
    parseTransactionCalldataParameter(item, `$calldata[${index}]`, schemaBudget, 0, true),
  );
  const valueBudget = new ValueBudget(ABI_LIMITS.outputNodes);
  const outputs = parsed.map(({ parameter, value: rawValue }, index) => {
    const formatted = formatTransactionCalldataValue(
      parameter,
      rawValue,
      `$calldata[${index}].value`,
      0,
      valueBudget,
      schemaBudget,
    );
    const output = {
      index,
      name: parameter.name ?? "",
      type: parameter.type,
      value: formatted,
      display: formattedValueText(formatted),
    };
    return Object.freeze(parameter.internalType === undefined
      ? output
      : { ...output, internalType: parameter.internalType });
  });
  return Object.freeze(outputs);
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
  if (["address", "bool", "string", "bytes", "tuple"].includes(type)) {
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
      kind: "array" as const,
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

function parseTransactionCalldataParameter(
  value: unknown,
  path: string,
  budget: AbiBudget,
  depth: number,
  includeValue: boolean,
): Readonly<{ parameter: AbiParameter; value: unknown }> {
  budget.addParameter(path);
  if (depth > ABI_LIMITS.depth) {
    throw new AbiFormError("ABI_LIMIT_EXCEEDED", path);
  }
  const record = snapshotRecord(value, path, budget);
  const allowed = new Set(["name", "type", "internal_type", "components"]);
  if (includeValue) allowed.add("value");
  assertAllowedKeys(record, allowed, path);
  if (!record.has("name") || !record.has("type") || !record.has("components") ||
    (includeValue && !record.has("value"))) {
    throw new AbiFormError("INVALID_ABI", path);
  }

  const name = requiredString(record, "name", path, budget);
  if (name !== "" && (!identifierPattern.test(name) || textEncoder.encode(name).byteLength > 4096)) {
    throw new AbiFormError("INVALID_ABI", `${path}.name`);
  }
  const type = requiredString(record, "type", path, budget);
  const parsedType = parseTransactionCalldataType(type, `${path}.type`);
  if (depth + parsedType.dimensions.length > ABI_LIMITS.depth) {
    throw new AbiFormError("ABI_LIMIT_EXCEEDED", `${path}.type`);
  }
  let internalType: string | undefined;
  if (record.has("internal_type")) {
    internalType = requiredString(record, "internal_type", path, budget);
    if (internalType === "" || internalType.trim() !== internalType ||
      textEncoder.encode(internalType).byteLength > 4096) {
      throw new AbiFormError("INVALID_ABI", `${path}.internal_type`);
    }
  }

  const rawComponents = snapshotArray(
    record.get("components"),
    `${path}.components`,
    256,
    "INVALID_ABI",
    budget,
  );
  if (parsedType.base === "tuple" && rawComponents.length === 0) {
    throw new AbiFormError("INVALID_ABI", `${path}.components`);
  }
  if (parsedType.base !== "tuple" && rawComponents.length !== 0) {
    throw new AbiFormError("INVALID_ABI", `${path}.components`);
  }
  const components = rawComponents.map((component, index) =>
    parseTransactionCalldataParameter(
      component,
      `${path}.components[${index}]`,
      budget,
      depth + parsedType.dimensions.length + 1,
      false,
    ).parameter,
  );

  const parameter: Record<string, unknown> = { name, type };
  if (internalType !== undefined) parameter.internalType = internalType;
  if (parsedType.base === "tuple") parameter.components = Object.freeze(components);
  return Object.freeze({ parameter: Object.freeze(parameter) as AbiParameter, value: record.get("value") });
}

function parseTransactionCalldataType(type: string, path: string): ParsedAbiType {
  if (textEncoder.encode(type).byteLength > 4096 || type.trim() !== type) {
    throw new AbiFormError("INVALID_ABI", path);
  }
  let remaining = type;
  const dimensions: ArrayDimension[] = [];
  while (remaining.endsWith("]")) {
    const match = /^(.*)\[([0-9]*)\]$/u.exec(remaining);
    if (!match || match[1] === undefined || match[2] === undefined) {
      throw new AbiFormError("INVALID_ABI", path);
    }
    let length: number | null = null;
    if (match[2] !== "") {
      if (!/^[1-9][0-9]*$/u.test(match[2])) {
        throw new AbiFormError("INVALID_ABI", path);
      }
      length = Number(match[2]);
      if (!Number.isSafeInteger(length) || length > ABI_LIMITS.outputNodes) {
        throw new AbiFormError("ABI_LIMIT_EXCEEDED", path);
      }
    }
    dimensions.push(Object.freeze({ length, suffix: `[${match[2]}]` }));
    remaining = match[1];
    if (dimensions.length > ABI_LIMITS.depth) {
      throw new AbiFormError("ABI_LIMIT_EXCEEDED", path);
    }
  }
  if (remaining.includes("[") || remaining.includes("]") ||
    (remaining !== "function" && !validBaseType(remaining))) {
    throw new AbiFormError("INVALID_ABI", path);
  }
  return Object.freeze({ base: remaining, dimensions: Object.freeze(dimensions) });
}

function formatTransactionCalldataValue(
  parameter: AbiParameter,
  value: unknown,
  path: string,
  depth: number,
  nodeBudget: ValueBudget,
  byteBudget: AbiBudget,
): FormattedAbiValue {
  nodeBudget.add(path);
  if (depth > ABI_LIMITS.depth) {
    throw new AbiFormError("ABI_VALUE_LIMIT_EXCEEDED", path);
  }
  const array = outerArray(parameter.type);
  if (array) {
    const values = snapshotArray(
      value,
      path,
      array.length ?? ABI_LIMITS.outputNodes,
      "INVALID_ABI_VALUE",
      byteBudget,
    );
    if (array.length !== null && values.length !== array.length) {
      throw new AbiFormError("INVALID_ABI_VALUE", path);
    }
    const element = parameterWithType(parameter, array.elementType);
    const items = values.map((item, index) => formatTransactionCalldataValue(
      element,
      item,
      `${path}[${index}]`,
      depth + 1,
      nodeBudget,
      byteBudget,
    ));
    const result = { kind: "array" as const, type: parameter.type, items: Object.freeze(items) };
    return Object.freeze(parameter.internalType === undefined
      ? result
      : { ...result, internalType: parameter.internalType });
  }

  const parsed = parseTransactionCalldataType(parameter.type, path);
  if (parsed.base === "tuple") {
    const components = "components" in parameter ? parameter.components : undefined;
    if (!components) throw new AbiFormError("INVALID_ABI", path);
    const values = snapshotArray(value, path, components.length, "INVALID_ABI_VALUE", byteBudget);
    if (values.length !== components.length) {
      throw new AbiFormError("INVALID_ABI_VALUE", path);
    }
    const fields = components.map((component, index) => {
      const field = {
        index,
        name: component.name ?? "",
        type: component.type,
        value: formatTransactionCalldataValue(
          component,
          values[index],
          `${path}.${index}`,
          depth + 1,
          nodeBudget,
          byteBudget,
        ),
      };
      return Object.freeze(component.internalType === undefined
        ? field
        : { ...field, internalType: component.internalType });
    });
    const result = { kind: "tuple" as const, type: parameter.type, fields: Object.freeze(fields) };
    return Object.freeze(parameter.internalType === undefined
      ? result
      : { ...result, internalType: parameter.internalType });
  }

  const result = {
    kind: "scalar" as const,
    type: parameter.type,
    text: formatTransactionCalldataScalar(parsed.base, value, path, byteBudget),
  };
  return Object.freeze(parameter.internalType === undefined
    ? result
    : { ...result, internalType: parameter.internalType });
}

function formatTransactionCalldataScalar(
  type: string,
  value: unknown,
  path: string,
  budget: AbiBudget,
): string {
  const integer = /^(u?int)([0-9]*)$/u.exec(type);
  if (integer) {
    if (typeof value !== "string") throw new AbiFormError("INVALID_ABI_VALUE", path);
    budget.addBytes(value, path);
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
    return value;
  }
  if (type === "bool") {
    if (typeof value !== "boolean") throw new AbiFormError("INVALID_ABI_VALUE", path);
    return String(value);
  }
  if (type === "address") {
    if (typeof value !== "string" || !isAddress(value, { strict: false })) {
      throw new AbiFormError("INVALID_ABI_VALUE", path);
    }
    budget.addBytes(value, path);
    return getAddress(value);
  }
  if (type === "string") {
    if (typeof value !== "string") throw new AbiFormError("INVALID_ABI_VALUE", path);
    budget.addBytes(value, path);
    if (textEncoder.encode(value).byteLength > ABI_LIMITS.stringBytes) {
      throw new AbiFormError("ABI_VALUE_LIMIT_EXCEEDED", path);
    }
    return value;
  }
  if (type === "function") {
    if (typeof value !== "string" || !validHex(value, 24, 24)) {
      throw new AbiFormError("INVALID_ABI_VALUE", path);
    }
    budget.addBytes(value, path);
    return value.toLowerCase();
  }
  if (type === "bytes") {
    if (typeof value !== "string" || !validHex(value, 0, ABI_LIMITS.bytesLength)) {
      throw new AbiFormError("INVALID_ABI_VALUE", path);
    }
    budget.addBytes(value, path);
    return value.toLowerCase();
  }
  const bytes = /^bytes([0-9]+)$/u.exec(type);
  if (bytes?.[1] !== undefined) {
    const length = Number(bytes[1]);
    if (typeof value !== "string" || !validHex(value, length, length)) {
      throw new AbiFormError("INVALID_ABI_VALUE", path);
    }
    budget.addBytes(value, path);
    return value.toLowerCase();
  }
  throw new AbiFormError("INVALID_ABI", path);
}

function formatParameterValues(
  parameters: readonly AbiParameter[],
  values: readonly unknown[],
  path: string,
): readonly FormattedAbiOutput[] {
  const budget = new ValueBudget(ABI_LIMITS.outputNodes);
  const outputs = parameters.map((parameter, index) => {
    const value = formatValue(parameter, values[index], `${path}[${index}]`, 0, budget);
    const output = {
      index,
      name: parameter.name ?? "",
      type: parameter.type,
      value,
      display: formattedValueText(value),
    };
    return Object.freeze(parameter.internalType === undefined
      ? output
      : { ...output, internalType: parameter.internalType });
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
    const result = {
      kind: "array" as const,
      type: parameter.type,
      items: Object.freeze(items),
    };
    return Object.freeze(parameter.internalType === undefined
      ? result
      : { ...result, internalType: parameter.internalType });
  }
  const parsed = parseType(parameter.type, path);
  if (parsed.base === "tuple") {
    const components = "components" in parameter ? parameter.components : undefined;
    if (!components) throw new AbiFormError("INVALID_ABI", path);
    const values = tupleValues(components, value, path);
    const fields = components.map((component, index) => {
      const field = {
        index,
        name: component.name ?? "",
        type: component.type,
        value: formatValue(component, values[index], `${path}.${index}`, depth + 1, budget),
      };
      return Object.freeze(component.internalType === undefined
        ? field
        : { ...field, internalType: component.internalType });
    });
    const result = {
      kind: "tuple" as const,
      type: parameter.type,
      fields: Object.freeze(fields),
    };
    return Object.freeze(parameter.internalType === undefined
      ? result
      : { ...result, internalType: parameter.internalType });
  }
  const result = {
    kind: "scalar" as const,
    type: parameter.type,
    text: formatScalar(parsed.base, value, path),
  };
  return Object.freeze(parameter.internalType === undefined
    ? result
    : { ...result, internalType: parameter.internalType });
}

function formatScalar(type: string, value: unknown, path: string): string {
  const integer = /^(u?int)([0-9]*)$/u.exec(type);
  if (integer) {
    const signed = integer[1] === "int";
    const bits = integer[2] === "" ? 256 : Number(integer[2]);
    const expectedType = bits <= 48 ? "number" : "bigint";
    if (typeof value !== expectedType) {
      throw new AbiFormError("INVALID_ABI_VALUE", path);
    }
    const parsed = typeof value === "number" ? BigInt(value) : value as bigint;
    const minimum = signed ? -(1n << BigInt(bits - 1)) : 0n;
    const maximum = signed ? (1n << BigInt(bits - 1)) - 1n : (1n << BigInt(bits)) - 1n;
    if (
      (typeof value === "number" && !Number.isSafeInteger(value)) ||
      parsed < minimum ||
      parsed > maximum
    ) {
      throw new AbiFormError("INVALID_ABI_VALUE", path);
    }
    return parsed.toString(10);
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
