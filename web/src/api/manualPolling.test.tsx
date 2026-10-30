import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { useBlockConsensus, useBlockBlobs } from "./consensus";
import { useBlockRequests } from "./requests";
import { firstPage } from "./pagination";
import {
  useTransaction,
  useAddressTransactions,
  useAddressInternalTransactions,
  useAddressERC20Transfers,
  useAddressNFTTransfers,
  useAddressERC20Balances,
  useAddressNFTBalances,
} from "./hooks";

const identity = `0x${"11".repeat(32)}`;
const address = `0x${"22".repeat(20)}`;
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
it.each([
  { name: "consensus", useResource: () => useBlockConsensus(identity) },
  { name: "blobs", useResource: () => useBlockBlobs(identity) },
  { name: "requests", useResource: () => useBlockRequests(identity, "", firstPage) },
  { name: "pending detail", useResource: () => useTransaction(identity) },
  { name: "address transactions", useResource: () => useAddressTransactions(address) },
  { name: "internal calls", useResource: () => useAddressInternalTransactions(address) },
  { name: "ERC20 transfers", useResource: () => useAddressERC20Transfers(address) },
  { name: "NFT transfers", useResource: () => useAddressNFTTransfers(address) },
  { name: "ERC20 balances", useResource: () => useAddressERC20Balances(address) },
  { name: "NFT balances", useResource: () => useAddressNFTBalances(address) },
])("does not poll $name while the page stays open", async ({ useResource }) => {
  const fetcher = vi.fn(async () =>
    Response.json({
      data: {
        kind: "pending",
        transaction: { expires_at: new Date(Date.now() + 600_000).toISOString() },
      },
      meta: { chain_id: "1", request_id: "manual" },
    }),
  );
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false },
    },
  });
  const { result } = renderHook<{ isSuccess: boolean }, unknown>(useResource, {
    wrapper: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    ),
  });
  await waitFor(() => expect(result.current.isSuccess).toBe(true));
  vi.useFakeTimers();
  await act(async () => {
    await vi.advanceTimersByTimeAsync(60_000);
  });
  expect(fetcher).toHaveBeenCalledTimes(1);
});
