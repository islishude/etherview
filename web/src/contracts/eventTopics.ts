import { encodeAbiParameters, keccak256, stringToHex, type AbiParameter, type Hex } from "viem";
import { ABI_LIMITS, AbiFormError, parseAbiArguments, type AbiInputNode } from "./abi";

// Event indexed encoding has no ABI head/tail offsets or array length words.
// Only the complete top-level complex value is hashed; children are concatenated.
export function encodeIndexedTopics(
  parameters: readonly AbiParameter[],
  nodes: readonly AbiInputNode[],
): Hex[] {
  const values = parseAbiArguments(parameters, nodes);
  const budget = { bytes: 0, nodes: 0 };
  return parameters.map((parameter, index) => {
    const encoded = encodeIndexed(parameter, values[index], false, 0, budget);
    return isHashedEventType(parameter.type) ? keccak256(encoded) : encoded;
  });
}
export function isHashedEventType(type: string) {
  return type === "string" || type === "bytes" || type === "tuple" || type.endsWith("]");
}
function encodeIndexed(
  parameter: AbiParameter,
  value: unknown,
  nested: boolean,
  depth: number,
  budget: { bytes: number; nodes: number },
): Hex {
  if (++budget.nodes > ABI_LIMITS.inputNodes || depth > ABI_LIMITS.depth)
    throw new AbiFormError("ABI_VALUE_LIMIT_EXCEEDED", "$event");
  const array = /^(.*)\[([0-9]*)\]$/u.exec(parameter.type);
  if (array) {
    const element = { ...parameter, type: array[1]! } as AbiParameter;
    return join(
      (value as readonly unknown[]).map((item) =>
        encodeIndexed(element, item, true, depth + 1, budget),
      ),
    );
  }
  if (parameter.type === "tuple") {
    const components = "components" in parameter ? parameter.components : [];
    return join(
      components.map((child, index) =>
        encodeIndexed(child, (value as readonly unknown[])[index], true, depth + 1, budget),
      ),
    );
  }
  let encoded: Hex;
  if (parameter.type === "string" || parameter.type === "bytes") {
    const raw = parameter.type === "string" ? stringToHex(value as string) : (value as Hex);
    encoded = nested ? `0x${raw.slice(2).padEnd(Math.ceil((raw.length - 2) / 64) * 64, "0")}` : raw;
  } else {
    encoded = encodeAbiParameters([parameter], [value] as never);
  }
  budget.bytes += (encoded.length - 2) / 2;
  if (budget.bytes > ABI_LIMITS.bytesLength)
    throw new AbiFormError("ABI_VALUE_LIMIT_EXCEEDED", "$event");
  return encoded;
}
function join(values: readonly Hex[]): Hex {
  return `0x${values.map((value) => value.slice(2)).join("")}`;
}
