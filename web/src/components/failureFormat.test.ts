import { describe, expect, it } from "vitest";

import { flattenFailureArguments } from "./failureFormat";
import type { FormattedAbiOutput } from "@/contracts/abi";

describe("flattenFailureArguments", () => {
  it("uses jq-style leaf paths without a leading dot or composite rows", () => {
    const values: FormattedAbiOutput[] = [{
      index: 0, name: "sender", type: "address", display: "0x1",
      value: { kind: "scalar", type: "address", text: "0x1" },
    }, {
      index: 1, name: "amount", type: "uint256", display: "42",
      value: { kind: "scalar", type: "uint256", text: "42" },
    }, {
      index: 2, name: "pair", type: "tuple", display: "",
      value: {
        kind: "tuple", type: "tuple", fields: [{
          index: 0, name: "owner", type: "address",
          value: { kind: "scalar", type: "address", text: "0x2" },
        }],
      },
    }, {
      index: 3, name: "values", type: "uint256[]", display: "",
      value: {
        kind: "array", type: "uint256[]", items: [
          { kind: "scalar", type: "uint256", text: "7" },
          { kind: "scalar", type: "uint256", text: "8" },
        ],
      },
    }, {
      index: 4, name: "items", type: "uint256[][]", display: "",
      value: {
        kind: "array", type: "uint256[][]", items: [{
          kind: "array", type: "uint256[]", items: [
            { kind: "scalar", type: "uint256", text: "9" },
            { kind: "scalar", type: "uint256", text: "10" },
            { kind: "scalar", type: "uint256", text: "11" },
          ],
        }],
      },
    }, {
      index: 5, name: "", type: "bool", display: "true",
      value: { kind: "scalar", type: "bool", text: "true" },
    }];

    const result = flattenFailureArguments(values);
    expect(result.rows.map((row) => row.path)).toEqual([
      "sender", "amount", "pair[0]", "values[0]", "values[1]",
      "items[0][0]", "items[0][1]", "items[0][2]", "[5]",
    ]);
    expect(result.rows.find((row) => row.path === "pair[0]")?.type).toBe("address");
    expect(result.rows.some((row) => ["pair", "values", "items", "items[0]"].includes(row.path))).toBe(false);
  });

  it("keeps empty composites visible as terminal leaves and reports truncation", () => {
    const values: FormattedAbiOutput[] = [{
      index: 0, name: "values", type: "uint256[]", display: "[]",
      value: { kind: "array", type: "uint256[]", items: [] },
    }, {
      index: 1, name: "items", type: "uint256[]", display: "",
      value: {
        kind: "array", type: "uint256[]", items: [
          { kind: "scalar", type: "uint256", text: "1" },
          { kind: "scalar", type: "uint256", text: "2" },
        ],
      },
    }];
    expect(flattenFailureArguments(values, 2)).toEqual({
      rows: [
        { path: "values", type: "uint256[]", data: "[]" },
        { path: "items[0]", type: "uint256", data: "1" },
      ],
      truncated: true,
    });
  });
});
