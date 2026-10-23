import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { expect, it, vi } from "vitest";
import { useIdentityRecovery } from "./identityRecovery";

it("waits for both resources and does not repeat a settled attempt", async () => {
  let completeOverview!: () => void;
  let completeResource!: () => void;
  const overview = vi.fn(
    () =>
      new Promise<void>((resolve) => {
        completeOverview = resolve;
      }),
  );
  const resource = vi.fn(
    () =>
      new Promise<void>((resolve) => {
        completeResource = resolve;
      }),
  );
  const { result, rerender } = renderHook(() =>
    useIdentityRecovery("mismatch", true, overview, resource),
  );
  expect(result.current).toBe(true);
  expect(overview).toHaveBeenCalledTimes(1);
  expect(resource).toHaveBeenCalledTimes(1);
  await act(async () => {
    completeOverview();
  });
  expect(result.current).toBe(true);
  await act(async () => {
    completeResource();
  });
  expect(result.current).toBe(false);
  rerender();
  expect(resource).toHaveBeenCalledTimes(1);
});

it("restarts an aborted recovery under root StrictMode with fresh cached mismatches", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(["overview"], "A");
  client.setQueryData(["resource"], "B");
  const requests: { signal: AbortSignal; resolve: (value: string) => void }[] = [];
  const queryFn = ({ signal }: { signal: AbortSignal }) =>
    new Promise<string>((resolve, reject) => {
      requests.push({ signal, resolve });
      signal.addEventListener("abort", () => reject(signal.reason), { once: true });
    });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const { result, rerender, unmount } = renderHook(
    () => {
      const overview = useQuery({ queryKey: ["overview"], staleTime: 30_000, queryFn });
      const resource = useQuery({ queryKey: ["resource"], staleTime: 30_000, queryFn });
      return useIdentityRecovery(
        overview.data !== resource.data ? "A:B" : undefined,
        true,
        overview.refetch,
        resource.refetch,
      );
    },
    { wrapper, reactStrictMode: true },
  );
  expect(requests.some(({ signal }) => signal.aborted)).toBe(true);
  await waitFor(() => expect(requests.filter(({ signal }) => !signal.aborted)).toHaveLength(2));
  expect(result.current).toBe(true);
  const active = requests.filter(({ signal }) => !signal.aborted);
  await act(async () => active[0]!.resolve("A"));
  expect(result.current).toBe(true);
  await act(async () => active[1]!.resolve("A"));
  await waitFor(() => expect(result.current).toBe(false));
  expect(client.getQueryData(["resource"])).toBe("A");
  const attempts = requests.length;
  rerender();
  expect(requests).toHaveLength(attempts);
  unmount();
  client.clear();
});

it.each([
  { name: "a different identity", identities: ["old", "new"] },
  { name: "the same identity after an intervening identity", identities: ["old", "new", "old"] },
])("keeps a late attempt from settling $name", async ({ identities }) => {
  const completions: (() => void)[] = [];
  const overview = vi.fn(async () => undefined);
  const resource = vi.fn(
    () =>
      new Promise<void>((resolve) => {
        completions.push(resolve);
      }),
  );
  const { result, rerender } = renderHook(
    ({ mismatch, enabled }) => useIdentityRecovery(mismatch, enabled, overview, resource),
    { initialProps: { mismatch: "old", enabled: false } },
  );
  expect(resource).not.toHaveBeenCalled();
  for (const mismatch of identities) rerender({ mismatch, enabled: true });
  await act(async () => {
    for (const complete of completions.slice(0, -1)) complete();
  });
  expect(result.current).toBe(true);
  await act(async () => {
    completions.at(-1)!();
  });
  await waitFor(() => expect(result.current).toBe(false));
  expect(resource).toHaveBeenCalledTimes(identities.length);
  const mismatch = identities.at(-1)!;
  rerender({ mismatch, enabled: false });
  rerender({ mismatch, enabled: true });
  expect(result.current).toBe(false);
  expect(resource).toHaveBeenCalledTimes(identities.length);
});
