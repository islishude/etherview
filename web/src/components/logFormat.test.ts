import { describe, expect, it } from "vitest";

import { flattenLogArgument, formatTopicValue, isAnonymousDecodedLog } from "./logFormat";

describe("formatTopicValue", () => {
  const topic = `0x${"00".repeat(32)}`;

  it("keeps the original topic in hex mode", () => {
    const mixedCase = `0x${"Ab".repeat(32)}`;
    expect(formatTopicValue(mixedCase, "hex")).toBe(mixedCase);
  });

  it("extracts and checksums the final address word", () => {
    expect(formatTopicValue(`${"0x"}${"00".repeat(12)}52908400098527886E0F7030069857D2E4169EE7`, "address"))
      .toBe("0x52908400098527886E0F7030069857D2E4169EE7");
  });

  it("decodes padded UTF-8 text", () => {
    expect(formatTopicValue(`0x68656c6c6f${"00".repeat(27)}`, "text"))
      .toBe("hello");
  });

  it("returns undefined for empty or invalid UTF-8 text", () => {
    expect(formatTopicValue(topic, "text")).toBeUndefined();
    expect(formatTopicValue(`0x${"ff".repeat(32)}`, "text")).toBeUndefined();
  });

  it("formats values larger than the safe integer range without precision loss", () => {
    const large = `0x${"00".repeat(16)}${"12345678901234567890123456789012"}`;
    expect(formatTopicValue(large, "number")).toBe("24197857199965561741520400062332047378");
  });

  it("fails closed for malformed non-hex topics", () => {
    expect(formatTopicValue("0x1234", "address")).toBeUndefined();
    expect(formatTopicValue("0x1234", "number")).toBeUndefined();
  });
});

describe("isAnonymousDecodedLog", () => {
  const indexedArguments = [{ indexed: true }, { indexed: false }, { indexed: true }];

  it("identifies decoded anonymous events by their indexed topic count", () => {
    expect(isAnonymousDecodedLog("decoded", 2, indexedArguments)).toBe(true);
  });

  it("keeps the signature topic reserved for decoded non-anonymous events", () => {
    expect(isAnonymousDecodedLog("decoded", 3, indexedArguments)).toBe(false);
  });

  it("fails closed for unavailable decoding and inconsistent topic counts", () => {
    expect(isAnonymousDecodedLog("unknown", 2, indexedArguments)).toBe(false);
    expect(isAnonymousDecodedLog("decoded", 1, indexedArguments)).toBe(false);
  });
});

describe("flattenLogArgument", () => {
  it("uses jq-style paths without a leading dot", () => {
    const rows = flattenLogArgument({
      name: "pair",
      type: "tuple",
      indexed: false,
      value: ["7", "0x1234"],
    }, 0);

    expect(rows.map((row) => row.path)).toEqual(["pair", "pair[0]", "pair[1]"]);
    expect(rows[0]?.indexed).toBe(false);
    expect(rows[1]?.indexed).toBeUndefined();
  });

  it("flattens nested arrays and anonymous arguments", () => {
    const rows = flattenLogArgument({
      name: "",
      type: "uint256[][]",
      indexed: false,
      value: [["1", "2"], ["3"]],
    }, 2);

    expect(rows.map((row) => row.path)).toEqual([
      "[2]", "[2][0]", "[2][0][0]", "[2][0][1]", "[2][1]", "[2][1][0]",
    ]);
    expect(rows[1]?.type).toBe("uint256[]");
    expect(rows[2]?.type).toBe("uint256");
  });

  it("supports object values without inventing unsafe identifiers", () => {
    const rows = flattenLogArgument({
      name: "value",
      type: "struct Example",
      indexed: false,
      value: { owner: "0x1", "amount-total": "2" },
    }, 0);

    expect(rows.map((row) => row.path)).toEqual(["value", "value.owner", 'value["amount-total"]']);
  });

  it("stops recursive expansion at the configured depth", () => {
    const rows = flattenLogArgument({
      name: "items",
      type: "uint256[]",
      indexed: false,
      value: [[[["1"]]]],
    }, 0, 2);

    expect(rows.map((row) => row.path)).toEqual(["items", "items[0]", "items[0][0]"]);
  });

  it("bounds recursive rows for large composite values", () => {
    const rows = flattenLogArgument({
      name: "values",
      type: "uint256[]",
      indexed: false,
      value: Array.from({ length: 100 }, (_, index) => String(index)),
    }, 0, 16, 5);

    expect(rows).toHaveLength(5);
    expect(rows.at(-1)?.path).toBe("values[3]");
  });
});
