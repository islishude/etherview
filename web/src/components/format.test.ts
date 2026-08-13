import { describe, expect, it } from "vitest";

import {
  formatEtherFromGwei,
  formatGweiFromWei,
  formatNativeAmount,
  formatPercentageRatio,
  formatRelativeTimestamp,
  formatTokenAmount,
} from "./format";

describe("formatEtherFromGwei", () => {
  it("converts protocol Gwei quantities to exact Ether values", () => {
    expect(formatEtherFromGwei("3200000000", "en")).toBe("3.2");
    expect(formatEtherFromGwei("1", "en")).toBe("0.000000001");
  });
});

describe("formatRelativeTimestamp", () => {
  const now = new Date("2026-07-28T08:00:00Z").getTime();

  it("localizes complete elapsed units", () => {
    expect(formatRelativeTimestamp("2026-07-28T07:59:15Z", "en", now))
      .toBe("45 seconds ago");
    expect(formatRelativeTimestamp("2026-07-28T07:59:00Z", "en", now))
      .toBe("1 minute ago");
    expect(formatRelativeTimestamp("2026-07-28T06:00:00Z", "en", now))
      .toBe("2 hours ago");
    expect(formatRelativeTimestamp("2026-07-25T08:00:00Z", "en", now))
      .toBe("3 days ago");
    expect(formatRelativeTimestamp("2026-07-28T07:59:00Z", "zh-CN", now))
      .toBe("1分钟前");
  });

  it("formats future timestamps relative to now", () => {
    expect(formatRelativeTimestamp("2026-07-28T08:02:00Z", "en", now))
      .toBe("in 2 minutes");
    expect(formatRelativeTimestamp("2026-07-28T08:02:00Z", "zh-CN", now))
      .toBe("2分钟后");
  });

  it("returns malformed timestamps unchanged", () => {
    expect(formatRelativeTimestamp("not-a-timestamp", "en", now))
      .toBe("not-a-timestamp");
  });
});

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

describe("formatPercentageRatio", () => {
  it("rounds an exact bigint ratio to two decimal places", () => {
    expect(formatPercentageRatio("430551", "567028", "en")).toBe("75.93%");
    expect(formatPercentageRatio("1000000000000000000000001", "1000000000000000000000000", "en"))
      .toBe("100.00%");
  });

  it("fails closed for missing, malformed, or zero totals", () => {
    expect(formatPercentageRatio(undefined, "1", "en")).toBeUndefined();
    expect(formatPercentageRatio("1", "0", "en")).toBeUndefined();
    expect(formatPercentageRatio("invalid", "2", "en")).toBeUndefined();
  });
});

describe("formatTokenAmount", () => {
  it("scales ERC-20 atomic values without number conversion", () => {
    expect(formatTokenAmount("1234500", 6, "en")).toBe("1.2345");
    expect(formatTokenAmount("2100", 0, "en")).toBe("2,100");
    expect(formatTokenAmount("1", 20, "en")).toBe("0.00000000000000000001");
    expect(formatTokenAmount("1", 255, "en")).toBe(`0.${"0".repeat(254)}1`);
  });

  it("falls back to the raw integer when decimals are unavailable", () => {
    expect(formatTokenAmount("1234500", undefined, "en")).toBe("1,234,500");
  });
});
