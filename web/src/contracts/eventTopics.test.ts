import { describe, expect, it } from "vitest";
import type { AbiParameter } from "viem";
import fixture from "./fixtures/indexed-events.json";
import { createAbiInputTree, parseVerifiedABI, type AbiInputNode } from "./abi";
import { encodeIndexedTopics } from "./eventTopics";

function input(parameter: AbiParameter, value: unknown): AbiInputNode {
  const node = createAbiInputTree([parameter])[0]!;
  if (node.kind === "array") {
    const element = {
      ...parameter,
      type: parameter.type.replace(/\[[0-9]*\]$/u, ""),
    } as AbiParameter;
    return { ...node, items: (value as unknown[]).map((item) => input(element, item)) };
  }
  if (node.kind === "tuple") {
    const components = "components" in parameter ? parameter.components : [];
    return { ...node, fields: components.map((child, i) => input(child, (value as unknown[])[i])) };
  }
  return { ...node, value: String(value) };
}
const abi = parseVerifiedABI(fixture.abi);
describe("indexed event encoding", () => {
  for (const [index, vector] of fixture.cases.entries()) {
    it(`matches Solidity-emitted ${vector.name} log ${index}`, () => {
      const event = abi.find((item) => item.type === "event" && item.name === vector.name);
      if (!event || event.type !== "event") throw new Error("missing event");
      const parameters = event.inputs.filter((item) => item.indexed);
      expect(
        encodeIndexedTopics(
          parameters,
          parameters.map((parameter, i) => input(parameter, vector.values[i])),
        ),
      ).toEqual(vector.topics.slice(event.anonymous ? 0 : 1));
    });
  }
  it("rejects malformed bytes, integer overflow, and cumulative encoded work", () => {
    for (const [type, value] of [
      ["bytes", "0x1"],
      ["uint8", "256"],
      ["int8", "-129"],
    ]) {
      const parameter = { type: type! };
      expect(() => encodeIndexedTopics([parameter], [input(parameter, value)])).toThrow();
    }
    const p = { type: "bytes[]" };
    const value = Array.from({ length: 3 }, () => `0x${"ff".repeat(65536)}`);
    expect(() => encodeIndexedTopics([p], [input(p, value)])).toThrow();
  });
  it("encodes an empty array and keeps bytes32 as an unhashed word", () => {
    const p = { type: "bytes32" };
    const word = `0x${"ab".repeat(32)}`;
    expect(encodeIndexedTopics([p], [input(p, word)])).toEqual([word]);
    expect(
      encodeIndexedTopics([{ type: "uint256[]" }], [input({ type: "uint256[]" }, [])]),
    ).toEqual([fixture.cases[0]!.topics[1]]);
  });
});
