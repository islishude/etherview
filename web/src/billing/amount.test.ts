import { describe, expect, it } from "vitest";
import { amountInputLength, formatTokenAmount, parseTokenAmount, topupAmount } from "./amount";
import type { BillingConfig } from "@/api/billing";

describe("whole-token amounts", () => {
  it.each([0, 6, 18, 255])("round trips precision %s without floating point", (decimals) => {
    for (const atomic of ["1", "1250000", ((1n << 256n) - 1n).toString()]) {
      const value = formatTokenAmount(atomic, decimals);
      expect(value.length).toBeLessThanOrEqual(amountInputLength(decimals));
      expect(parseTokenAmount(value, decimals)).toBe(atomic);
    }
  });
  it("converts decimal inputs and normalizes leading zeros", () => {
    expect(parseTokenAmount("001.250000", 6)).toBe("1250000");
    expect(formatTokenAmount("1250000", 6)).toBe("1.25");
    expect(formatTokenAmount("0", 255)).toBe("0");
    expect(parseTokenAmount("9007199254740993.000001", 6)).toBe("9007199254740993000001");
  });
  it.each([
    "",
    "0",
    "0.000000",
    "-1",
    "+1",
    "1e3",
    "1,000",
    ".1",
    "1.",
    " 1",
    "1.0000001",
    "1.0000000",
  ])("rejects %s", (value) => {
    expect(parseTokenAmount(value, 6)).toBeUndefined();
  });
  it("rejects overflow and fractions with zero precision", () => {
    expect(parseTokenAmount((1n << 256n).toString(), 0)).toBeUndefined();
    expect(parseTokenAmount("1.0", 0)).toBeUndefined();
  });
  it("checks bounds after conversion", () => {
    const config = {
      asset_decimals: 6,
      minimum_topup_amount_atomic: "10",
      maximum_topup_amount_atomic: "1250000",
    } as BillingConfig;
    expect(topupAmount("0.000009", config)).toBeUndefined();
    expect(topupAmount("0.000010", config)).toBe("10");
    expect(topupAmount("1.25", config)).toBe("1250000");
    expect(topupAmount("1.250001", config)).toBeUndefined();
  });
});
