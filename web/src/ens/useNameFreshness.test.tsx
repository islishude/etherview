import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import type { SearchResult } from "@/api/heyapi/types.gen";
import { notifyNames } from "./nameEvents";
import { useNameFreshness } from "./useNameFreshness";

afterEach(() => vi.useRealTimers());

it("withdraws profile fields while retaining known names at expiry without a stream or request", () => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-10-29T00:00:00Z"));
  const client = new QueryClient();
  const items: SearchResult[] = [1, 2].map((seconds) => ({
    kind: "name",
    key: `address-${seconds}`,
    rank: seconds,
    label: `name${seconds}.eth`,
    name_details: {
      name: `name${seconds}.eth`,
      state: "indexed",
      registration_state: "registered",
      registrant: { state: "value", value: "0x123" },
      controller: { state: "pending" },
      expiration_time: { state: "pending" },
      resolved_address: { state: "pending" },
      profile_observation: {
        publication: "1",
        observed_block_number: "1",
        observed_block_hash: `0x${"1".repeat(64)}`,
        valid_until: new Date(Date.now() + seconds * 1000).toISOString(),
      },
    },
  }));
  const { result } = renderHook(() => useNameFreshness(items), {
    wrapper: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    ),
  });
  expect(result.current.items).toHaveLength(2);
  act(() => notifyNames(client, "refresh"));
  expect(result.current.withdrawn).toBe(false);
  expect(result.current.items?.[0]?.name_details?.registrant.state).toBe("value");
  act(() => vi.advanceTimersByTime(1000));
  expect(result.current.items).toHaveLength(2);
  expect(result.current.items?.[0]?.name_details?.registrant.state).toBe("pending");
  expect(result.current.items?.[1]?.name_details?.registrant.state).toBe("value");
  act(() => vi.advanceTimersByTime(1000));
  expect(result.current.items).toHaveLength(2);
  expect(result.current.items?.[1]?.name_details?.registrant.state).toBe("pending");
  expect(result.current.withdrawn).toBe(false);
  act(() => notifyNames(client, "invalidate"));
  expect(result.current.withdrawn).toBe(true);
});
