import { describe, expect, it } from "vitest";

import { formatGweiFromWei, formatNativeAmount } from "./format";

describe("formatNativeAmount", () => {
  it("renders zero as 0", () => {
    expect(formatNativeAmount("0", "en")).toBe("0");
  });

  it("converts wei to ether with decimal precision", () => {
    expect(formatNativeAmount("1", "en")).toBe("0.000000000000000001");
    expect(formatNativeAmount("1000000000000000000", "en")).toBe("1");
    expect(formatNativeAmount("2100000000000000000", "en")).toBe("2.1");
  });

  it("formats decimal output with locale and truncates fractional digits", () => {
    expect(formatNativeAmount("1234567890000000000", "en", 18)).toBe("1.23456789");
    expect(formatNativeAmount("123456789012345678901", "en", 20)).toBe("1.234567890123456789");
  });

  it("uses locale grouping for integer part", () => {
    expect(formatNativeAmount("2100000000000000000000", "en")).toBe("2,100");
  });
});

describe("formatGweiFromWei", () => {
  it("renders wei as whole-number gwei", () => {
    expect(formatGweiFromWei("2000000000", "en")).toBe("2");
  });

  it("renders wei as decimal gwei", () => {
    expect(formatGweiFromWei("2500000001", "en")).toBe("2.500000001");
  });
});
