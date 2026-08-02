import {
  encodeErrorResult,
  encodeFunctionData,
  getAddress,
  type AbiFunction,
  type AbiParameter,
} from "viem";
import { describe, expect, it, vi } from "vitest";

import {
  ABI_LIMITS,
  AbiFormError,
  assertAbiInputTreeWithinLimits,
  canonicalFunctionSignature,
  createAbiArrayItem,
  createAbiInputTree,
  decodeRevert,
  formatAbiResult,
  parseAbiArguments,
  parseVerifiedABI,
  partitionAbiFunctions,
  singleFunctionAbi,
  type AbiInputNode,
} from "./abi";

const addressA = "0x1111111111111111111111111111111111111111";
const addressB = "0x2222222222222222222222222222222222222222";

describe("verified ABI parsing", () => {
  it("detaches and freezes a strict compiler-style ABI", () => {
    const source = validAbiFixture();
    const abi = parseVerifiedABI(JSON.stringify(source));

    expect(abi).toHaveLength(source.length);
    expect(Object.isFrozen(abi)).toBe(true);
    expect(Object.isFrozen(abi[0])).toBe(true);
    const configure = abi.find(
      (item): item is AbiFunction => item.type === "function" && item.name === "configure",
    );
    expect(configure).toBeDefined();
    expect(Object.isFrozen(configure?.inputs)).toBe(true);
    expect(Object.isFrozen(configure?.inputs[0])).toBe(true);
    expect(Object.isFrozen(configure?.inputs[0] && "components" in configure.inputs[0]
      ? configure.inputs[0].components
      : undefined)).toBe(true);

    source[0] = { type: "receive", stateMutability: "payable" };
    expect(abi[0]?.type).toBe("function");
  });

  it("rejects hostile shapes and fixed resource-limit violations", () => {
    const getter = vi.fn(() => "function");
    const accessor = Object.create(null) as Record<string, unknown>;
    Object.defineProperty(accessor, "type", { enumerable: true, get: getter });
    expect(() => parseVerifiedABI([accessor])).toThrowError(AbiFormError);
    expect(getter).not.toHaveBeenCalled();

    expect(() => parseVerifiedABI([{ ...simpleFunction("f", "uint256"), extra: true }]))
      .toThrowError(expect.objectContaining({ code: "INVALID_ABI" }));
    expect(() => parseVerifiedABI(new Array(2))).toThrowError(AbiFormError);
    expect(() => parseVerifiedABI([Object.assign(Object.create({}), simpleFunction("f", "uint256"))]))
      .toThrowError(AbiFormError);
    expect(() => parseVerifiedABI("{"))
      .toThrowError(expect.objectContaining({ code: "INVALID_ABI_JSON" }));
    expect(() => parseVerifiedABI(`[${" ".repeat(ABI_LIMITS.jsonBytes)}]`))
      .toThrowError(expect.objectContaining({ code: "ABI_LIMIT_EXCEEDED" }));

    const tooMany = Array.from(
      { length: ABI_LIMITS.items + 1 },
      () => ({ type: "receive", stateMutability: "payable" }),
    );
    expect(() => parseVerifiedABI(tooMany))
      .toThrowError(expect.objectContaining({ code: "ABI_LIMIT_EXCEEDED" }));
    expect(() => parseVerifiedABI([simpleFunction("large", `uint256[${ABI_LIMITS.fixedArrayLength + 1}]`)]))
      .toThrowError(expect.objectContaining({ code: "ABI_LIMIT_EXCEEDED" }));
    expect(() => parseVerifiedABI([
      simpleFunction("deep", `uint256${"[]".repeat(ABI_LIMITS.depth + 1)}`),
    ])).toThrowError(expect.objectContaining({ code: "ABI_LIMIT_EXCEEDED" }));
  });

  it("normalizes canonical overload signatures and rejects canonical duplicates", () => {
    const abi = parseVerifiedABI([
      simpleFunction("lookup", "uint"),
      simpleFunction("lookup", "tuple[][2]", [{ name: "id", type: "uint" }, { name: "who", type: "address" }]),
      { type: "function", name: "store", stateMutability: "payable", inputs: [], outputs: [] },
      { type: "function", name: "version", stateMutability: "pure", inputs: [], outputs: [{ type: "string" }] },
    ]);
    const functions = abi.filter((item): item is AbiFunction => item.type === "function");

    expect(functions.map(canonicalFunctionSignature)).toEqual([
      "lookup(uint256)",
      "lookup((uint256,address)[][2])",
      "store()",
      "version()",
    ]);
    const partitioned = partitionAbiFunctions(abi);
    expect(partitioned.read.map((entry) => entry.signature)).toEqual([
      "lookup(uint256)",
      "lookup((uint256,address)[][2])",
      "version()",
    ]);
    expect(partitioned.write.map((entry) => [entry.signature, entry.payable])).toEqual([
      ["store()", true],
    ]);
    expect(partitioned.read[0]?.abi).toEqual(singleFunctionAbi(functions[0]!));

    expect(() => parseVerifiedABI([
      simpleFunction("same", "uint"),
      simpleFunction("same", "uint256"),
    ])).toThrowError(expect.objectContaining({ code: "INVALID_ABI" }));
  });
});

describe("ABI argument trees", () => {
  it("builds and converts positional tuple and nested-array inputs", () => {
    const fn = inputFixtureFunction();
    const initial = createAbiInputTree(fn.inputs);

    expect(initial).toEqual([
      {
        kind: "tuple",
        type: "tuple",
        fields: [
          { kind: "scalar", type: "address", value: "" },
          { kind: "scalar", type: "uint8", value: "" },
          { kind: "scalar", type: "bytes4", value: "" },
          { kind: "scalar", type: "bool", value: "" },
          { kind: "scalar", type: "string", value: "" },
        ],
      },
      { kind: "array", type: "int16[2][]", fixedLength: null, items: [] },
      {
        kind: "array",
        type: "tuple[][2]",
        fixedLength: 2,
        items: [
          { kind: "array", type: "tuple[]", fixedLength: null, items: [] },
          { kind: "array", type: "tuple[]", fixedLength: null, items: [] },
        ],
      },
    ]);
    expect(createAbiArrayItem(fn.inputs[1]!)).toEqual({
      kind: "array",
      type: "int16[2]",
      fixedLength: 2,
      items: [
        { kind: "scalar", type: "int16", value: "" },
        { kind: "scalar", type: "int16", value: "" },
      ],
    });

    const tree: readonly AbiInputNode[] = [
      tuple("tuple", [
        scalar("address", addressA),
        scalar("uint8", "255"),
        scalar("bytes4", "0xAABBCCDD"),
        scalar("bool", "true"),
        scalar("string", "配置"),
      ]),
      array("int16[2][]", null, [
        array("int16[2]", 2, [scalar("int16", "-32768"), scalar("int16", "32767")]),
        array("int16[2]", 2, [scalar("int16", "0"), scalar("int16", "1")]),
      ]),
      array("tuple[][2]", 2, [
        array("tuple[]", null, [tuple("tuple", [scalar("address", addressB), scalar("uint256", "9")])]),
        array("tuple[]", null, []),
      ]),
    ];
    const args = parseAbiArguments(fn.inputs, tree);

    expect(args).toEqual([
      [getAddress(addressA), 255n, "0xaabbccdd", true, "配置"],
      [[-32768n, 32767n], [0n, 1n]],
      [[[getAddress(addressB), 9n]], []],
    ]);
    expect(() => encodeFunctionData({
      abi: singleFunctionAbi(fn),
      functionName: fn.name,
      args,
    })).not.toThrow();
  });

  it("enforces scalar boundaries without JavaScript number coercion", () => {
    expect(parseScalarArgument("uint8", "255")).toBe(255n);
    expect(parseScalarArgument("int8", "-128")).toBe(-128n);
    expect(parseScalarArgument("address", addressA)).toBe(getAddress(addressA));
    expect(parseScalarArgument("bytes4", "0x01020304")).toBe("0x01020304");
    expect(parseScalarArgument("bytes", "0xA0ff")).toBe("0xa0ff");
    expect(parseScalarArgument("function", `0x${"11".repeat(24)}`)).toBe(`0x${"11".repeat(24)}`);
    expect(parseScalarArgument("bool", "false")).toBe(false);
    expect(parseScalarArgument("string", "hello")).toBe("hello");

    for (const [type, value] of [
      ["uint8", "256"],
      ["uint8", "01"],
      ["int8", "-129"],
      ["int8", "-0"],
      ["address", "0x1234"],
      ["bytes4", "0x0102"],
      ["bytes", "0x0"],
      ["function", `0x${"11".repeat(23)}`],
      ["bool", "TRUE"],
    ]) {
      expect(() => parseScalarArgument(type!, value!)).toThrowError(
        expect.objectContaining({ code: "INVALID_ABI_VALUE" }),
      );
    }
    expect(() => parseScalarArgument("string", "x".repeat(ABI_LIMITS.stringBytes + 1)))
      .toThrowError(expect.objectContaining({ code: "ABI_VALUE_LIMIT_EXCEEDED" }));
    expect(() => parseScalarArgument("bytes", `0x${"00".repeat(ABI_LIMITS.bytesLength + 1)}`))
      .toThrowError(expect.objectContaining({ code: "INVALID_ABI_VALUE" }));
  });

  it("rejects malformed fixed and oversized dynamic array state", () => {
    const fixed = parameter("uint256[2]");
    expect(() => parseAbiArguments([fixed], [array("uint256[2]", 2, [scalar("uint256", "1")])]))
      .toThrowError(AbiFormError);

    const dynamic = parameter("uint256[]");
    const items = Array.from(
      { length: ABI_LIMITS.dynamicArrayLength + 1 },
      () => scalar("uint256", "1"),
    );
    expect(() => parseAbiArguments([dynamic], [array("uint256[]", null, items)]))
      .toThrowError(expect.objectContaining({ code: "ABI_VALUE_LIMIT_EXCEEDED" }));
  });

  it("enforces the input-node budget across independently added dynamic items", () => {
    const dynamic = parameter("uint256[256][]");
    const item = createAbiArrayItem(dynamic);
    const withinBudget = [
      array("uint256[256][]", null, Array.from({ length: 15 }, () => item)),
    ];
    const overBudget = [
      array("uint256[256][]", null, Array.from({ length: 16 }, () => item)),
    ];

    expect(() => assertAbiInputTreeWithinLimits(withinBudget)).not.toThrow();
    expect(() => assertAbiInputTreeWithinLimits(overBudget)).toThrowError(
      expect.objectContaining({ code: "ABI_VALUE_LIMIT_EXCEEDED" }),
    );
  });
});

describe("ABI result and revert formatting", () => {
  it("formats bigint, tuple, array, address, bytes, and multiple outputs structurally", () => {
    const abi = parseVerifiedABI([{
      type: "function",
      name: "summary",
      stateMutability: "view",
      inputs: [],
      outputs: [
        { name: "total", type: "uint256" },
        {
          name: "data",
          type: "tuple",
          components: [
            { name: "owner", type: "address" },
            { name: "values", type: "uint16[]" },
            { name: "label", type: "string" },
          ],
        },
        { name: "digest", type: "bytes2" },
      ],
    }]);
    const fn = abi[0] as AbiFunction;
    const formatted = formatAbiResult(fn, [
      12345678901234567890n,
      { owner: addressA, values: [1n, 65535n], label: "ready" },
      "0xAABB",
    ]);

    expect(formatted.map((output) => output.display)).toEqual([
      "12345678901234567890",
      `(owner: ${getAddress(addressA)}, values: [1, 65535], label: ready)`,
      "0xaabb",
    ]);
    expect(formatted[1]?.value).toEqual({
      kind: "tuple",
      type: "tuple",
      fields: [
        { index: 0, name: "owner", type: "address", value: { kind: "scalar", type: "address", text: getAddress(addressA) } },
        {
          index: 1,
          name: "values",
          type: "uint16[]",
          value: {
            kind: "array",
            type: "uint16[]",
            items: [
              { kind: "scalar", type: "uint16", text: "1" },
              { kind: "scalar", type: "uint16", text: "65535" },
            ],
          },
        },
        { index: 2, name: "label", type: "string", value: { kind: "scalar", type: "string", text: "ready" } },
      ],
    });
    expect(Object.isFrozen(formatted)).toBe(true);

    const single = parseVerifiedABI([{
      type: "function", name: "value", stateMutability: "view", inputs: [], outputs: [{ type: "uint256" }],
    }])[0] as AbiFunction;
    expect(formatAbiResult(single, 7n)[0]?.display).toBe("7");
    expect(() => formatAbiResult(fn, [1n])).toThrowError(AbiFormError);
  });

  it("decodes custom and Solidity builtin reverts without exposing decoder errors", () => {
    const abi = parseVerifiedABI([{
      type: "error",
      name: "Unauthorized",
      inputs: [{ name: "caller", type: "address" }, { name: "required", type: "uint256" }],
    }]);
    const customData = encodeErrorResult({
      abi,
      errorName: "Unauthorized",
      args: [addressA, 5n],
    });
    expect(decodeRevert(abi, customData)).toEqual({
      errorName: "Unauthorized",
      signature: "Unauthorized(address,uint256)",
      args: [
        {
          index: 0,
          name: "caller",
          type: "address",
          value: { kind: "scalar", type: "address", text: getAddress(addressA) },
          display: getAddress(addressA),
        },
        {
          index: 1,
          name: "required",
          type: "uint256",
          value: { kind: "scalar", type: "uint256", text: "5" },
          display: "5",
        },
      ],
      display: `Unauthorized(address,uint256): ${getAddress(addressA)}, 5`,
    });

    const errorAbi = parseVerifiedABI([{
      type: "error", name: "Error", inputs: [{ name: "message", type: "string" }],
    }]);
    const errorData = encodeErrorResult({ abi: errorAbi, errorName: "Error", args: ["denied"] });
    expect(decodeRevert(parseVerifiedABI([]), errorData)?.display).toBe("Error(string): denied");

    const panicAbi = parseVerifiedABI([{
      type: "error", name: "Panic", inputs: [{ name: "code", type: "uint256" }],
    }]);
    const panicData = encodeErrorResult({ abi: panicAbi, errorName: "Panic", args: [17n] });
    expect(decodeRevert(parseVerifiedABI([]), panicData)?.display).toBe("Panic(uint256): 17");
    expect(decodeRevert(abi, "0xdeadbeef")).toBeUndefined();
    expect(decodeRevert(abi, "not hex")).toBeUndefined();
    expect(decodeRevert(abi, `0x${"00".repeat(ABI_LIMITS.bytesLength + 1)}`)).toBeUndefined();
  });
});

function validAbiFixture(): Array<Record<string, unknown>> {
  return [
    {
      type: "function",
      name: "configure",
      stateMutability: "nonpayable",
      inputs: [{
        name: "config",
        type: "tuple",
        internalType: "struct Fixture.Config",
        components: [{ name: "owner", type: "address" }, { name: "threshold", type: "uint8" }],
      }],
      outputs: [],
    },
    { type: "event", name: "Configured", anonymous: false, inputs: [{ name: "owner", type: "address", indexed: true }] },
    { type: "error", name: "Unauthorized", inputs: [{ name: "caller", type: "address" }] },
    { type: "receive", stateMutability: "payable" },
  ];
}

function simpleFunction(
  name: string,
  type: string,
  components?: readonly Record<string, unknown>[],
): Record<string, unknown> {
  return {
    type: "function",
    name,
    stateMutability: "view",
    inputs: [{ type, ...(components ? { components } : {}) }],
    outputs: [],
  };
}

function inputFixtureFunction(): AbiFunction {
  const abi = parseVerifiedABI([{
    type: "function",
    name: "configure",
    stateMutability: "nonpayable",
    inputs: [
      {
        name: "config",
        type: "tuple",
        components: [
          { name: "owner", type: "address" },
          { name: "threshold", type: "uint8" },
          { name: "digest", type: "bytes4" },
          { name: "enabled", type: "bool" },
          { name: "label", type: "string" },
        ],
      },
      { name: "deltas", type: "int16[2][]" },
      {
        name: "batches",
        type: "tuple[][2]",
        components: [{ name: "recipient", type: "address" }, { name: "amount", type: "uint256" }],
      },
    ],
    outputs: [],
  }]);
  return abi[0] as AbiFunction;
}

function parameter(type: string): AbiParameter {
  const abi = parseVerifiedABI([simpleFunction("value", type)]);
  return (abi[0] as AbiFunction).inputs[0]!;
}

function parseScalarArgument(type: string, value: string): unknown {
  return parseAbiArguments([parameter(type)], [scalar(type, value)])[0];
}

function scalar(type: string, value: string): AbiInputNode {
  return { kind: "scalar", type, value };
}

function tuple(type: string, fields: readonly AbiInputNode[]): AbiInputNode {
  return { kind: "tuple", type, fields };
}

function array(
  type: string,
  fixedLength: number | null,
  items: readonly AbiInputNode[],
): AbiInputNode {
  return { kind: "array", type, fixedLength, items };
}
