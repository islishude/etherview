import { encodeAbiParameters, encodeFunctionData } from "viem";
import { describe, expect, it, vi } from "vitest";

import {
  ABI_LIMITS,
  decodeCalldata,
  decodeConstructorArguments,
  parseVerifiedABI,
  partitionAbiFunctions,
} from "./abi";
import historicalABIs from "./testdata/historical-solidity-abi.json";

describe("historical Solidity ABI normalization", () => {
  it.each(historicalABIs)("accepts the unmodified $compilerVersion output", ({ version, abi }) => {
    const parsed = parseVerifiedABI(abi);
    expect(parseVerifiedABI(JSON.stringify(abi))).toEqual(parsed);
    const functions = partitionAbiFunctions(parsed);
    expect(functions.read.map((entry) => entry.signature).sort()).toEqual(
      version === "0.4.15" ? ["read()"] : ["pureRead()", "read()"],
    );
    expect(functions.write.map((entry) => [entry.signature, entry.payable]).sort()).toEqual([
      ["deposit()", true],
      ["write(uint256)", false],
    ]);
    expect(parsed.find((item) => item.type === "constructor")).toEqual({
      type: "constructor",
      inputs: [],
      stateMutability: "payable",
    });
    expect(parsed.find((item) => item.type === "fallback")).toEqual({
      type: "fallback",
      stateMutability: "payable",
    });
    expect(parsed.some((item) => item.type === "receive")).toBe(version === "0.6.0");
    expect(decodeConstructorArguments(abi, "0x")).toEqual([]);
    const call = encodeFunctionData({
      abi: [
        {
          type: "function",
          name: "write",
          inputs: [{ type: "uint256" }],
          outputs: [],
          stateMutability: "nonpayable",
        },
      ],
      functionName: "write",
      args: [17n],
    });
    expect(decodeCalldata(abi, call)).toMatchObject({
      status: "decoded",
      signature: "write(uint256)",
    });
  });

  it.each([
    { flags: {}, expected: "nonpayable" },
    { flags: { constant: false }, expected: "nonpayable" },
    { flags: { payable: false }, expected: "nonpayable" },
    { flags: { constant: false, payable: false }, expected: "nonpayable" },
    { flags: { constant: true }, expected: "view" },
    { flags: { constant: true, payable: false }, expected: "view" },
    { flags: { payable: true }, expected: "payable" },
    { flags: { constant: false, payable: true }, expected: "payable" },
  ])("normalizes omitted fields with $flags to $expected", ({ flags, expected }) => {
    const parsed = parseVerifiedABI([{ name: "f", inputs: [], ...flags }]);
    expect(parsed).toEqual([
      { type: "function", name: "f", inputs: [], outputs: [], stateMutability: expected },
    ]);
    expect(Object.isFrozen(parsed[0])).toBe(true);
    const functions = partitionAbiFunctions(parsed);
    expect(functions.read).toHaveLength(expected === "view" ? 1 : 0);
    expect(functions.write).toHaveLength(expected === "view" ? 0 : 1);
  });

  it("rejects mutually contradictory historical flags", () => {
    expect(() =>
      parseVerifiedABI([{ name: "f", inputs: [], constant: true, payable: true }]),
    ).toThrowError(expect.objectContaining({ code: "INVALID_ABI" }));
  });

  it.each(["constant", "payable", "type", "stateMutability", "outputs"])(
    "does not treat invalid %s as an omission",
    (key) => {
      for (const invalid of [undefined, null, "", 0, 1, {}]) {
        expect(() => parseVerifiedABI([{ name: "f", inputs: [], [key]: invalid }])).toThrowError(
          expect.objectContaining({ code: "INVALID_ABI", path: `$[0].${key}` }),
        );
      }
      const getter = vi.fn();
      const item = Object.defineProperty({ name: "f", inputs: [] }, key, {
        enumerable: true,
        get: getter,
      });
      expect(() => parseVerifiedABI([item])).toThrowError(
        expect.objectContaining({ code: "INVALID_ABI" }),
      );
      expect(getter).not.toHaveBeenCalled();
    },
  );

  it("retains constructor, fallback, receive, event and parameter boundaries", () => {
    for (const item of [
      { name: "f" },
      { name: "E", inputs: [], anonymous: false },
      { type: "constructor", name: "C", inputs: [] },
      { type: "constructor", inputs: [], outputs: [] },
      { type: "constructor", inputs: [], constant: true },
      { type: "fallback", inputs: [] },
      { type: "fallback", outputs: [] },
      { type: "fallback", name: "" },
      { type: "fallback", constant: true },
      { type: "receive", payable: true },
      { type: "receive", stateMutability: "nonpayable" },
      { type: "receive", stateMutability: "payable", inputs: [] },
      { type: "event", name: "E", inputs: [], constant: false },
      { type: "error", name: "E", inputs: [], payable: false },
      { name: "f", inputs: [{ name: "x" }] },
      { name: "f", inputs: [{ type: "address", payable: true }] },
      { name: "f", inputs: [], extra: false },
    ]) {
      expect(() => parseVerifiedABI([item])).toThrowError(
        expect.objectContaining({ code: "INVALID_ABI" }),
      );
    }
    expect(parseVerifiedABI([{ type: "constructor", inputs: [] }, { type: "fallback" }])).toEqual([
      { type: "constructor", inputs: [], stateMutability: "nonpayable" },
      { type: "fallback", stateMutability: "nonpayable" },
    ]);
  });

  it("retains optional internalType and recursive tuple components", () => {
    const inputs = [
      {
        name: "config",
        type: "tuple",
        components: [
          {
            name: "values",
            type: "tuple[]",
            internalType: "struct C.Value[]",
            components: [{ name: "n", type: "uint256" }],
          },
        ],
      },
    ];
    const abi = parseVerifiedABI([{ name: "f", inputs, constant: true }]);
    const entry = partitionAbiFunctions(abi).read[0]!;
    expect(entry.signature).toBe("f(((uint256)[]))");
    expect(entry.fn.inputs).toEqual(inputs);
    expect(Object.isFrozen(entry.fn.inputs[0])).toBe(true);
    const encoded = encodeAbiParameters(inputs, [{ values: [{ n: 9n }] }]);
    expect(
      decodeConstructorArguments([{ type: "constructor", inputs }], encoded)[0]?.display,
    ).toContain("9");
    inputs[0]!.components[0]!.components[0]!.name = "changed";
    expect(JSON.stringify(entry.fn.inputs)).not.toContain("changed");
    expect(() => parseVerifiedABI([{ name: "f", inputs: [{ type: "tuple" }] }])).toThrowError();
  });

  it("keeps resource, prototype and duplicate-signature protections on the omission path", () => {
    const item = { name: "f", inputs: [] };
    expect(() =>
      parseVerifiedABI([Object.assign(Object.create({ constant: true }), item)]),
    ).toThrowError();
    expect(() => parseVerifiedABI(new Array(2))).toThrowError();
    expect(() =>
      parseVerifiedABI([
        { ...item, constant: true },
        { ...item, type: "function", outputs: [], stateMutability: "view" },
      ]),
    ).toThrowError();
    for (const source of [
      `[${" ".repeat(ABI_LIMITS.jsonBytes)}${JSON.stringify(item)}]`,
      Array.from({ length: ABI_LIMITS.items + 1 }, () => item),
      [
        {
          ...item,
          inputs: Array.from({ length: ABI_LIMITS.parameters + 1 }, () => ({ type: "bool" })),
        },
      ],
      [{ ...item, inputs: [{ type: `uint256[${ABI_LIMITS.fixedArrayLength + 1}]` }] }],
      [{ ...item, inputs: [{ type: `uint256${"[]".repeat(ABI_LIMITS.depth + 1)}` }] }],
    ]) {
      expect(() => parseVerifiedABI(source)).toThrowError(
        expect.objectContaining({ code: "ABI_LIMIT_EXCEEDED" }),
      );
    }
  });
});
