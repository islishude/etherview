import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import type { SearchResult } from "@/api/heyapi/types.gen";
import { useNameFreshness } from "./useNameFreshness";

afterEach(() => vi.useRealTimers());

it("withdraws each search name at its own expiry without a stream or request", () => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-10-29T00:00:00Z"));
  const client = new QueryClient();
  const items: SearchResult[] = [1, 2].map((seconds) => ({
    kind: "address",
    key: `address-${seconds}`,
    rank: seconds,
    label: `name${seconds}.eth`,
    name_source: "ens",
    name_publication: "1",
    name_valid_until: new Date(Date.now() + seconds * 1000).toISOString(),
  }));
  const { result } = renderHook(() => useNameFreshness(items), {
    wrapper: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    ),
  });
  expect(result.current.items).toHaveLength(2);
  act(() => vi.advanceTimersByTime(1000));
  expect(result.current.items).toEqual([items[1]]);
  act(() => vi.advanceTimersByTime(1000));
  expect(result.current.items).toEqual([]);
  expect(result.current.withdrawn).toBe(true);
});
