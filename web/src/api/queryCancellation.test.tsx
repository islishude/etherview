import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { useTransaction, useBlocks } from "./hooks";
import * as queries from "./hooks";
import { usePublicConfig } from "./publicConfig";
import { useBlockBlobs, useBlockConsensus } from "./consensus";
import { useContractProxy, useVerifiedContractArtifact } from "@/contracts/proxy";
import { useAddressDelegation } from "@/contracts/delegation";

const hash = `0x${"11".repeat(32)}`;
const address = `0x${"22".repeat(20)}`;
afterEach(() => {
  vi.unstubAllGlobals();
});

it.each([
  { name: "transaction", useResource: () => useTransaction(hash) },
  { name: "chain status", useResource: () => queries.useChainStatus() },
  { name: "public config", useResource: () => usePublicConfig() },
  { name: "user operation", useResource: () => queries.useUserOperation(hash) },
  { name: "block", useResource: () => queries.useBlock(hash) },
  { name: "calldata", useResource: () => queries.useTransactionCalldata(hash) },
  { name: "failure", useResource: () => queries.useTransactionFailure(hash) },
  { name: "trace", useResource: () => queries.useTransactionTrace(hash) },
  { name: "address", useResource: () => queries.useAddress(address) },
  { name: "token", useResource: () => queries.useToken(address) },
  { name: "NFT ownership", useResource: () => queries.useNFTOwnership(address, "1") },
  { name: "NFT metadata", useResource: () => queries.useNFTMetadata(address, "1") },
  { name: "block statistics", useResource: () => queries.useBlockStats("1", "2") },
  { name: "aggregate statistics", useResource: () => queries.useAggregateStats("1", "2") },
  { name: "chart overview", useResource: () => queries.useChartOverview() },
  {
    name: "chart metric",
    useResource: () =>
      queries.useChartMetric(
        "transactions",
        "2026-01-01T00:00:00Z",
        "2026-01-02T00:00:00Z",
        "hour",
      ),
  },
  { name: "verification job", useResource: () => queries.useVerificationJob("job", "secret", 1) },
  { name: "compiler catalog", useResource: () => queries.useCompilerCatalog("solidity") },
  { name: "blob metadata", useResource: () => useBlockBlobs(hash) },

  { name: "block list", useResource: () => useBlocks() },
  { name: "consensus", useResource: () => useBlockConsensus(hash) },
  { name: "proxy", useResource: () => useContractProxy(address) },
  { name: "artifact", useResource: () => useVerifiedContractArtifact(address) },
  { name: "delegation", useResource: () => useAddressDelegation(address) },
])("aborts the $name HTTP request when its last observer unmounts", async ({ useResource }) => {
  let signal: AbortSignal | null | undefined;
  vi.stubGlobal(
    "fetch",
    vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
      signal = init?.signal;
      return new Promise<Response>((_resolve, reject) => {
        signal?.addEventListener("abort", () => reject(signal?.reason), { once: true });
      });
    }),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const rendered = renderHook(
    () => {
      useResource();
    },
    { wrapper },
  );
  const shared = renderHook(
    () => {
      useResource();
    },
    { wrapper },
  );
  await waitFor(() => expect(signal).toBeDefined());
  expect(vi.mocked(fetch)).toHaveBeenCalledTimes(1);
  rendered.unmount();
  expect(signal?.aborted).toBe(false);
  shared.unmount();
  expect(signal?.aborted).toBe(true);
  client.clear();
});

it("aborts cancelled chain reads and excludes late results from a replacement request", async () => {
  const pending: { signal: AbortSignal; resolve: (response: Response) => void }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(
      (_input: RequestInfo | URL, init?: RequestInit) =>
        new Promise<Response>((resolve) => {
          pending.push({ signal: init!.signal!, resolve });
        }),
    ),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const rendered = renderHook(() => useBlocks(), { wrapper });
  await waitFor(() => expect(pending).toHaveLength(1));
  await act(async () => {
    await client.cancelQueries({ queryKey: ["blocks"] });
  });
  expect(pending[0]!.signal.aborted).toBe(true);
  act(() => {
    void client.invalidateQueries({ queryKey: ["blocks"] });
  });
  await waitFor(() => expect(pending).toHaveLength(2));
  await act(async () => {
    pending[1]!.resolve(Response.json({ data: [], meta: { chain_id: "1", request_id: "new" } }));
  });
  await waitFor(() => expect(rendered.result.current.data?.meta.request_id).toBe("new"));
  await act(async () => {
    pending[0]!.resolve(Response.json({ data: [], meta: { chain_id: "1", request_id: "old" } }));
  });
  expect(rendered.result.current.data?.meta.request_id).toBe("new");
  rendered.unmount();
  client.clear();
});
