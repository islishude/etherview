import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, expect, it, vi } from "vitest";
import * as queries from "./hooks";
import { firstPage, useCursorHistory, type CursorRequest } from "./pagination";
import {
  useContractDiamondCuts,
  useContractProxyInitializations,
  useContractProxyUpgrades,
} from "@/contracts/proxy";
import { useAddressDelegationHistory } from "@/contracts/delegation";

const hash = `0x${"11".repeat(32)}`;
const address = `0x${"22".repeat(20)}`;
afterEach(() => {
  vi.unstubAllGlobals();
});

it("discards cursors on every identity transition, including returning to an earlier identity", () => {
  const { result, rerender } = renderHook(useCursorHistory, { initialProps: "first" });
  act(() => {
    result.current.next("opaque +/?=");
  });
  expect(result.current.request.cursor).toBe("opaque +/?=");
  rerender("second");
  expect(result.current.page).toBe(1);
  rerender("first");
  expect(result.current.request).toEqual({
    cursor: undefined,
    refreshGeneration: 0,
    identity: "first",
  });
  act(() => {
    result.current.reset();
  });
  const generation = result.current.request.refreshGeneration;
  expect(generation).toBeGreaterThan(0);
  act(() => {
    result.current.previous();
  });
  expect(result.current.page).toBe(1);
  expect(result.current.request.refreshGeneration).toBe(generation);
});

it.each(["remount", "identity round trip"])(
  "fetches a new snapshot when restarting after %s with an existing query cache",
  async (transition) => {
    const requestedBlocks: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        requestedBlocks.push(new URL(String(input), "http://etherview.test").pathname);
        return Response.json({
          data: [],
          meta: { chain_id: "1", request_id: String(requestedBlocks.length) },
        });
      }),
    );
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
    const useBlockPage = (identity: string) => {
      const pager = useCursorHistory(identity);
      const query = queries.useBlockTransactions(identity, pager.request);
      return { pager, query };
    };
    let rendered = renderHook(useBlockPage, { wrapper, initialProps: hash });
    await waitFor(() => expect(rendered.result.current.query.isSuccess).toBe(true));
    act(() => rendered.result.current.pager.reset());
    await waitFor(() => expect(requestedBlocks).toHaveLength(2));
    await waitFor(() => expect(rendered.result.current.query.isSuccess).toBe(true));
    const firstRestart = rendered.result.current.query.data?.meta.request_id;
    if (transition === "remount") {
      rendered.unmount();
      rendered = renderHook(useBlockPage, { wrapper, initialProps: hash });
    } else {
      rendered.rerender(`0x${"33".repeat(32)}`);
      await waitFor(() => expect(rendered.result.current.query.isSuccess).toBe(true));
      rendered.rerender(hash);
    }
    act(() => rendered.result.current.pager.reset());
    await waitFor(() =>
      expect(requestedBlocks.filter((path) => path.includes(hash))).toHaveLength(3),
    );
    await waitFor(() => expect(rendered.result.current.query.isSuccess).toBe(true));
    expect(rendered.result.current.query.data?.meta.request_id).not.toBe(firstRestart);
    rendered.unmount();
    client.clear();
  },
);

const paginatedQueries: { name: string; useResource: (page: CursorRequest) => unknown }[] = [
  { name: "blocks", useResource: (page) => queries.useBlocks(page) },
  { name: "genesis", useResource: (page) => queries.useGenesisAccounts(page) },
  { name: "transactions", useResource: (page) => queries.useTransactions(page) },
  { name: "user operations", useResource: (page) => queries.useUserOperations(page) },
  { name: "pending", useResource: (page) => queries.usePendingTransactions(page) },
  { name: "block transactions", useResource: (page) => queries.useBlockTransactions(hash, page) },
  {
    name: "transaction user operations",
    useResource: (page) => queries.useTransactionUserOperations(hash, page),
  },
  {
    name: "transaction authorizations",
    useResource: (page) => queries.useTransactionAuthorizations(hash, page),
  },
  {
    name: "transaction transfers",
    useResource: (page) => queries.useTransactionTokenTransfers(hash, page),
  },
  {
    name: "transaction internal calls",
    useResource: (page) => queries.useTransactionInternalTransactions(hash, page),
  },
  { name: "transaction logs", useResource: (page) => queries.useTransactionLogs(hash, page) },
  {
    name: "transaction state",
    useResource: (page) => queries.useTransactionStateChanges(hash, page),
  },
  {
    name: "address transactions",
    useResource: (page) => queries.useAddressTransactions(address, page),
  },
  {
    name: "address withdrawals",
    useResource: (page) => queries.useAddressWithdrawals(address, page),
  },
  {
    name: "address internal calls",
    useResource: (page) => queries.useAddressInternalTransactions(address, page),
  },
  {
    name: "address ERC20 transfers",
    useResource: (page) => queries.useAddressERC20Transfers(address, page),
  },
  {
    name: "address NFT transfers",
    useResource: (page) => queries.useAddressNFTTransfers(address, page),
  },
  { name: "address NFTs", useResource: (page) => queries.useAddressNFTBalances(address, page) },
  {
    name: "address ERC20 balances",
    useResource: (page) => queries.useAddressERC20Balances(address, page),
  },
  {
    name: "address user operations",
    useResource: (page) => queries.useAddressUserOperations(address, page),
  },
  { name: "tokens", useResource: (page) => queries.useTokens(page) },
  { name: "token transfers", useResource: (page) => queries.useTokenTransfers(address, page) },
  { name: "token holders", useResource: (page) => queries.useTokenHolders(address, page) },
  { name: "search", useResource: (page) => queries.useSearchResults("query", page) },
  { name: "proxy upgrades", useResource: (page) => useContractProxyUpgrades(address, page) },
  {
    name: "proxy initializations",
    useResource: (page) => useContractProxyInitializations(address, page),
  },
  { name: "diamond cuts", useResource: (page) => useContractDiamondCuts(address, page) },
  { name: "delegation history", useResource: (page) => useAddressDelegationHistory(address, page) },
];

it.each(paginatedQueries)(
  "restarts $name with a fresh snapshot, preserves cursors and aborts abandoned pages",
  async ({ useResource }) => {
    const cursors: (string | null)[] = [];
    let abandonedSignal: AbortSignal | null | undefined;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        cursors.push(new URL(String(input), "http://etherview.test").searchParams.get("cursor"));
        if (cursors.at(-1) === "abandoned-page") {
          abandonedSignal = init?.signal;
          return new Promise<Response>((_resolve, reject) => {
            abandonedSignal?.addEventListener("abort", () => reject(abandonedSignal?.reason), {
              once: true,
            });
          });
        }
        return Response.json({ data: [], meta: { chain_id: "1", request_id: "page" } });
      }),
    );
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
    const rendered = renderHook(
      ({ page }) => {
        useResource(page);
      },
      { wrapper, initialProps: { page: firstPage } },
    );
    await waitFor(() => expect(client.isFetching()).toBe(0));
    rendered.rerender({ page: { cursor: "opaque +/?=", refreshGeneration: 0 } });
    await waitFor(() => expect(cursors).toEqual([null, "opaque +/?="]));
    await waitFor(() => expect(client.isFetching()).toBe(0));
    rendered.rerender({ page: { refreshGeneration: 1 } });
    await waitFor(() => expect(cursors).toEqual([null, "opaque +/?=", null]));
    await waitFor(() => expect(client.isFetching()).toBe(0));
    rendered.rerender({ page: { cursor: "abandoned-page", refreshGeneration: 1 } });
    await waitFor(() => expect(abandonedSignal).toBeDefined());
    rendered.unmount();
    expect(abandonedSignal?.aborted).toBe(true);
    client.clear();
  },
);
