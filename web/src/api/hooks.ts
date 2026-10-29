import * as sdk from "./heyapi/sdk.gen";
import { firstPage, type CursorRequest } from "./pagination";
import { chainQueryMeta, statusQueryMeta, userOperationQueryMeta } from "./chainEvents";
import { useQuery } from "@tanstack/react-query";

import { apiClient, requireEnvelope } from "./client";
import type {
  AggregateStats,
  AddressInternalTransaction,
  AddressTokenTransfer,
  AddressWithdrawal,
  BlockSummary,
  ChartInterval,
  ChartMetric,
  ChartMetricSeries,
  ChartOverview,
  CursorPage,
  ERC20Balance,
  GenesisAccount,
  NFTBalance,
  NFTMetadata,
  PendingSnapshot,
  SearchResult,
  TokenContract,
  TokenEvent,
  TokenHolderPage,
  TransactionSummary,
  TransactionDetail,
  UserOperationDetail,
  UserOperationSummary,
  VerificationSubmission,
} from "./types";

const liveRefetchInterval = 2_000;

export function useChainStatus() {
  return useQuery({
    queryKey: ["status"],
    meta: statusQueryMeta,
    queryFn: async ({ signal }) => {
      const response = requireEnvelope(await sdk.getStatus({ client: apiClient, signal }));
      return {
        ...response.data,
        coverage_start: response.meta.coverage_start,
        coverage_end: response.meta.coverage_end,
      };
    },
    retry: false,
    staleTime: liveRefetchInterval,
  });
}

export function useBlocks(page: CursorRequest = firstPage, limit = 12) {
  const { cursor, refreshGeneration } = page;
  return useQuery({
    queryKey: ["blocks", limit, cursor ?? null, refreshGeneration],
    meta: statusQueryMeta,
    queryFn: async ({ signal }): Promise<CursorPage<BlockSummary>> => {
      const response = requireEnvelope(
        await sdk.listBlocks({ client: apiClient, signal, query: { limit, cursor } }),
      );
      return {
        items: response.data,
        meta: response.meta,
        next_cursor: response.meta.next_cursor,
      };
    },
    retry: false,
    staleTime: liveRefetchInterval,
  });
}

export function useGenesisAccounts(page: CursorRequest = firstPage, limit = 25) {
  const { cursor, refreshGeneration } = page;
  return useQuery({
    queryKey: ["genesis-accounts", limit, cursor ?? null, refreshGeneration],
    queryFn: async ({ signal }): Promise<CursorPage<GenesisAccount>> => {
      const response = requireEnvelope(
        await sdk.listGenesisAccounts({ client: apiClient, signal, query: { limit, cursor } }),
      );
      return {
        items: response.data,
        meta: response.meta,
        next_cursor: response.meta.next_cursor,
      };
    },
    retry: false,
    staleTime: Number.POSITIVE_INFINITY,
  });
}

export function useTransactions(page: CursorRequest = firstPage, limit = 12) {
  const { cursor, refreshGeneration } = page;
  return useQuery({
    queryKey: ["transactions", limit, cursor ?? null, refreshGeneration],
    meta: chainQueryMeta,
    queryFn: async ({ signal }): Promise<CursorPage<TransactionSummary>> => {
      const response = requireEnvelope(
        await sdk.listTransactions({ client: apiClient, signal, query: { limit, cursor } }),
      );
      return {
        items: response.data,
        meta: response.meta,
        next_cursor: response.meta.next_cursor,
      };
    },
    retry: false,
    staleTime: liveRefetchInterval,
  });
}

export function useUserOperations(page: CursorRequest = firstPage, limit = 25) {
  const { cursor, refreshGeneration } = page;
  return useQuery({
    queryKey: ["user-operations", limit, cursor ?? null, refreshGeneration],
    meta: userOperationQueryMeta,
    queryFn: async ({ signal }): Promise<CursorPage<UserOperationSummary>> => {
      const response = requireEnvelope(
        await sdk.listUserOperations({ client: apiClient, signal, query: { limit, cursor } }),
      );
      return { items: response.data, meta: response.meta, next_cursor: response.meta.next_cursor };
    },
    retry: false,
    staleTime: liveRefetchInterval,
  });
}

export function useUserOperation(hash: string, enabled = true) {
  return useQuery({
    queryKey: ["user-operation", hash],
    meta: userOperationQueryMeta,
    queryFn: async ({ signal }): Promise<UserOperationDetail> =>
      requireEnvelope(await sdk.getUserOperation({ client: apiClient, signal, path: { hash } }))
        .data,
    enabled: enabled && hash.length > 0,
    retry: false,
    staleTime: 30_000,
  });
}

export function useTransactionUserOperations(
  hash: string,
  page: CursorRequest = firstPage,
  enabled = true,
) {
  const { cursor, refreshGeneration } = page;
  return useQuery({
    queryKey: ["transaction", hash, "user-operations", cursor ?? null, refreshGeneration],
    meta: chainQueryMeta,
    queryFn: async ({ signal }): Promise<CursorPage<UserOperationSummary>> => {
      const response = requireEnvelope(
        await sdk.listTransactionUserOperations({
          client: apiClient,
          signal,
          path: { hash },
          query: { cursor, limit: 25 },
        }),
      );
      return { items: response.data, meta: response.meta, next_cursor: response.meta.next_cursor };
    },
    enabled: enabled && hash.length > 0,
    retry: false,
    staleTime: 30_000,
  });
}

export function useAddressUserOperations(
  address: string,
  page: CursorRequest = firstPage,
  enabled = true,
) {
  const { cursor, refreshGeneration } = page;
  return useQuery({
    queryKey: ["address", address, "user-operations", cursor ?? null, refreshGeneration],
    meta: chainQueryMeta,
    queryFn: async ({ signal }): Promise<CursorPage<UserOperationSummary>> => {
      const response = requireEnvelope(
        await sdk.listAddressUserOperations({
          client: apiClient,
          signal,
          path: { address },
          query: { cursor, limit: 25 },
        }),
      );
      return { items: response.data, meta: response.meta, next_cursor: response.meta.next_cursor };
    },
    enabled: enabled && address.length > 0,
    retry: false,
    staleTime: 30_000,
  });
}

export function usePendingTransactions(
  page: CursorRequest = firstPage,
  enabled = true,
  limit = 25,
) {
  const { cursor, refreshGeneration } = page;
  return useQuery({
    queryKey: ["pending-transactions", cursor ?? null, limit, refreshGeneration],
    meta: chainQueryMeta,
    queryFn: async ({ signal }): Promise<PendingSnapshot> => {
      const response = requireEnvelope(
        await sdk.listPendingTransactions({ client: apiClient, signal, query: { limit, cursor } }),
      );
      return { items: response.data, meta: response.meta };
    },
    enabled,
    retry: false,
    staleTime: Number.POSITIVE_INFINITY,
    refetchOnReconnect: false,
    refetchOnWindowFocus: false,
  });
}

export function useBlock(identifier: string, enabled = true) {
  return useQuery({
    queryKey: ["block", identifier],
    meta: statusQueryMeta,
    queryFn: async ({ signal }) =>
      requireEnvelope(await sdk.getBlock({ client: apiClient, signal, path: { id: identifier } }))
        .data,
    enabled: enabled && identifier.length > 0,
    retry: false,
    staleTime: 5_000,
  });
}

export function useBlockTransactions(
  identifier: string,
  page: CursorRequest = firstPage,
  enabled = true,
) {
  const { cursor, refreshGeneration } = page;
  return useQuery({
    queryKey: ["block", identifier, "transactions", cursor ?? null, refreshGeneration],
    meta: chainQueryMeta,
    queryFn: async ({ signal }) => {
      const response = requireEnvelope(
        await sdk.listBlockTransactions({
          client: apiClient,
          signal,
          path: { id: identifier },
          query: { cursor, limit: 25 },
        }),
      );
      return {
        items: response.data,
        meta: response.meta,
        next_cursor: response.meta.next_cursor,
      };
    },
    enabled: enabled && identifier.length > 0,
    retry: false,
    staleTime: 30_000,
  });
}

export function useTransaction(hash: string, enabled = true) {
  return useQuery({
    queryKey: ["transaction", hash],
    meta: chainQueryMeta,
    queryFn: async ({ signal }): Promise<TransactionDetail> =>
      requireEnvelope(await sdk.getTransaction({ client: apiClient, signal, path: { hash } })).data,
    enabled: enabled && hash.length > 0,
    retry: false,
    staleTime: liveRefetchInterval,
    refetchInterval: (query) => {
      const detail = query.state.data;
      if (!detail || (detail.kind !== "pending" && detail.kind !== "replaced")) return false;
      const expiresAt = Date.parse(detail.transaction.expires_at);
      if (!Number.isFinite(expiresAt)) return false;
      const remaining = expiresAt - Date.now();
      return remaining > 0 ? Math.min(liveRefetchInterval, remaining) : false;
    },
  });
}

export function useTransactionCalldata(hash: string, enabled = true) {
  return useQuery({
    queryKey: ["transaction", hash, "calldata"],
    meta: chainQueryMeta,
    queryFn: async ({ signal }) =>
      requireEnvelope(
        await sdk.getTransactionCalldata({ client: apiClient, signal, path: { hash } }),
      ).data,
    enabled: enabled && hash.length > 0,
    retry: false,
    staleTime: 30_000,
  });
}

export function useTransactionFailure(hash: string, enabled = true) {
  return useQuery({
    queryKey: ["transaction", hash, "failure"],
    meta: chainQueryMeta,
    queryFn: async ({ signal }) =>
      requireEnvelope(
        await sdk.getTransactionFailure({ client: apiClient, signal, path: { hash } }),
      ).data,
    enabled: enabled && hash.length > 0,
    retry: false,
    staleTime: 30_000,
  });
}

export function useTransactionTrace(hash: string, enabled = true) {
  return useQuery({
    queryKey: ["transaction", hash, "trace"],
    meta: chainQueryMeta,
    queryFn: async ({ signal }) =>
      requireEnvelope(await sdk.getTransactionTrace({ client: apiClient, signal, path: { hash } }))
        .data,
    enabled: enabled && hash.length > 0,
    retry: false,
    staleTime: 30_000,
  });
}

export function useTransactionAuthorizations(
  hash: string,
  page: CursorRequest = firstPage,
  enabled = true,
) {
  const { cursor, refreshGeneration } = page;
  return useQuery({
    queryKey: ["transaction", hash, "authorizations", cursor ?? null, refreshGeneration],
    meta: chainQueryMeta,
    queryFn: async ({ signal }) => {
      const response = requireEnvelope(
        await sdk.listTransactionAuthorizations({
          client: apiClient,
          signal,
          path: { hash },
          query: { cursor, limit: 25 },
        }),
      );
      return { ...response.data, next_cursor: response.meta.next_cursor };
    },
    enabled: enabled && hash.length > 0,
    retry: false,
    staleTime: 30_000,
  });
}

export function useTransactionTokenTransfers(
  hash: string,
  page: CursorRequest = firstPage,
  enabled = true,
) {
  const { cursor, refreshGeneration } = page;
  return useQuery({
    queryKey: ["transaction", hash, "token-transfers", cursor ?? null, refreshGeneration],
    meta: chainQueryMeta,
    queryFn: async ({ signal }) => {
      const response = requireEnvelope(
        await sdk.listTransactionTokenTransfers({
          client: apiClient,
          signal,
          path: { hash },
          query: { cursor, limit: 25 },
        }),
      );
      return { ...response.data, next_cursor: response.meta.next_cursor };
    },
    enabled: enabled && hash.length > 0,
    retry: false,
    staleTime: 30_000,
  });
}

export function useTransactionInternalTransactions(
  hash: string,
  page: CursorRequest = firstPage,
  enabled = true,
) {
  const { cursor, refreshGeneration } = page;
  return useQuery({
    queryKey: ["transaction", hash, "internal-transactions", cursor ?? null, refreshGeneration],
    meta: chainQueryMeta,
    queryFn: async ({ signal }) => {
      const response = requireEnvelope(
        await sdk.listTransactionInternalTransactions({
          client: apiClient,
          signal,
          path: { hash },
          query: { cursor, limit: 25 },
        }),
      );
      return { ...response.data, next_cursor: response.meta.next_cursor };
    },
    enabled: enabled && hash.length > 0,
    retry: false,
    staleTime: 30_000,
  });
}

export function useTransactionLogs(hash: string, page: CursorRequest = firstPage, enabled = true) {
  const { cursor, refreshGeneration } = page;
  return useQuery({
    queryKey: ["transaction", hash, "logs", cursor ?? null, refreshGeneration],
    meta: chainQueryMeta,
    queryFn: async ({ signal }) => {
      const response = requireEnvelope(
        await sdk.listTransactionLogs({
          client: apiClient,
          signal,
          path: { hash },
          query: { cursor, limit: 25 },
        }),
      );
      return { ...response.data, next_cursor: response.meta.next_cursor };
    },
    enabled: enabled && hash.length > 0,
    retry: false,
    staleTime: 30_000,
  });
}

export function useTransactionStateChanges(
  hash: string,
  page: CursorRequest = firstPage,
  enabled = true,
) {
  const { cursor, refreshGeneration } = page;
  return useQuery({
    queryKey: ["transaction", hash, "state-changes", cursor ?? null, refreshGeneration],
    meta: chainQueryMeta,
    queryFn: async ({ signal }) => {
      const response = requireEnvelope(
        await sdk.listTransactionStateChanges({
          client: apiClient,
          signal,
          path: { hash },
          query: { cursor, limit: 25 },
        }),
      );
      return { ...response.data, next_cursor: response.meta.next_cursor };
    },
    enabled: enabled && hash.length > 0,
    retry: false,
    staleTime: 30_000,
  });
}

export function useAddress(address: string, enabled = true) {
  return useQuery({
    queryKey: ["address", address],
    meta: chainQueryMeta,
    queryFn: async ({ signal }) =>
      requireEnvelope(await sdk.getAddress({ client: apiClient, signal, path: { address } })).data,
    enabled: enabled && address.length > 0,
    retry: false,
    staleTime: 5_000,
  });
}

export function useAddressTransactions(
  address: string,
  page: CursorRequest = firstPage,
  enabled = true,
  limit = 25,
) {
  const { cursor, refreshGeneration } = page;
  return useQuery({
    queryKey: ["address", address, "transactions", cursor ?? null, limit, refreshGeneration],
    meta: chainQueryMeta,
    queryFn: async ({ signal }): Promise<CursorPage<TransactionSummary>> => {
      const response = requireEnvelope(
        await sdk.listAddressTransactions({
          client: apiClient,
          signal,
          path: { address },
          query: { limit, cursor },
        }),
      );
      return {
        items: response.data,
        meta: response.meta,
        next_cursor: response.meta.next_cursor,
      };
    },
    enabled: enabled && address.length > 0,
    retry: false,
    staleTime: liveRefetchInterval,
  });
}

export function useAddressWithdrawals(
  address: string,
  page: CursorRequest = firstPage,
  enabled = true,
  limit = 25,
) {
  const { cursor, refreshGeneration } = page;
  return useQuery({
    queryKey: ["address", address, "withdrawals", cursor ?? null, limit, refreshGeneration],
    meta: chainQueryMeta,
    queryFn: async ({ signal }): Promise<CursorPage<AddressWithdrawal>> => {
      const response = requireEnvelope(
        await sdk.listAddressWithdrawals({
          client: apiClient,
          signal,
          path: { address },
          query: { limit, cursor },
        }),
      );
      return {
        items: response.data,
        meta: response.meta,
        next_cursor: response.meta.next_cursor,
      };
    },
    enabled: enabled && address.length > 0,
    retry: false,
    staleTime: liveRefetchInterval,
  });
}

export function useAddressInternalTransactions(
  address: string,
  page: CursorRequest = firstPage,
  enabled = true,
  limit = 25,
) {
  const { cursor, refreshGeneration } = page;
  return useQuery({
    meta: { ...statusQueryMeta, clearOnReorg: true, clearOnStatus: true },
    queryKey: [
      "address",
      address,
      "internal-transactions",
      cursor ?? null,
      limit,
      refreshGeneration,
    ],
    queryFn: async ({ signal }): Promise<CursorPage<AddressInternalTransaction>> => {
      const response = requireEnvelope(
        await sdk.listAddressInternalTransactions({
          client: apiClient,
          signal,
          path: { address },
          query: { limit, cursor },
        }),
      );
      return {
        items: response.data,
        meta: response.meta,
        next_cursor: response.meta.next_cursor,
      };
    },
    enabled: enabled && address.length > 0,
    retry: false,
    staleTime: liveRefetchInterval,
  });
}

export function useAddressERC20Transfers(
  address: string,
  page: CursorRequest = firstPage,
  enabled = true,
  limit = 25,
) {
  const { cursor, refreshGeneration } = page;
  return useAddressTokenActivity(
    "erc20-transfers",
    "/addresses/{address}/erc20-transfers",
    address,
    cursor,
    limit,
    refreshGeneration,
    enabled,
  );
}

export function useAddressNFTTransfers(
  address: string,
  page: CursorRequest = firstPage,
  enabled = true,
  limit = 25,
) {
  const { cursor, refreshGeneration } = page;
  return useAddressTokenActivity(
    "nft-transfers",
    "/addresses/{address}/nft-transfers",
    address,
    cursor,
    limit,
    refreshGeneration,
    enabled,
  );
}

function useAddressTokenActivity(
  kind: "erc20-transfers" | "nft-transfers",
  path: "/addresses/{address}/erc20-transfers" | "/addresses/{address}/nft-transfers",
  address: string,
  cursor: string | undefined,
  limit: number,
  refreshGeneration: number,
  enabled: boolean,
) {
  return useQuery({
    queryKey: ["address", address, kind, cursor ?? null, limit, refreshGeneration],
    meta: { ...statusQueryMeta, clearOnReorg: true, clearOnStatus: true },
    queryFn: async ({ signal }): Promise<CursorPage<AddressTokenTransfer>> => {
      const response = requireEnvelope(
        path === "/addresses/{address}/erc20-transfers"
          ? await sdk.listAddressErc20Transfers({
              client: apiClient,
              signal,
              path: { address },
              query: { limit, cursor },
            })
          : await sdk.listAddressNftTransfers({
              client: apiClient,
              signal,
              path: { address },
              query: { limit, cursor },
            }),
      );
      return {
        items: response.data,
        meta: response.meta,
        next_cursor: response.meta.next_cursor,
      };
    },
    enabled: enabled && address.length > 0,
    retry: false,
    staleTime: liveRefetchInterval,
  });
}

export function useTokens(page: CursorRequest = firstPage, limit = 25) {
  const { cursor, refreshGeneration } = page;
  return useQuery({
    queryKey: ["tokens", limit, cursor ?? null, refreshGeneration],
    meta: { ...statusQueryMeta, clearOnReorg: true, clearOnStatus: true },
    queryFn: async ({ signal }): Promise<CursorPage<TokenContract>> => {
      const response = requireEnvelope(
        await sdk.listTokens({ client: apiClient, signal, query: { limit, cursor } }),
      );
      return {
        items: response.data,
        meta: response.meta,
        next_cursor: response.meta.next_cursor,
      };
    },
    retry: false,
    staleTime: 30_000,
  });
}

export function useToken(address: string, enabled = true) {
  return useQuery({
    queryKey: ["token", address],
    meta: { ...statusQueryMeta, clearOnReorg: true, clearOnStatus: true },
    queryFn: async ({ signal }) =>
      requireEnvelope(await sdk.getToken({ client: apiClient, signal, path: { address } })).data,
    enabled: enabled && address.length > 0,
    retry: false,
    staleTime: 30_000,
  });
}

export function useTokenTransfers(
  address: string,
  page: CursorRequest = firstPage,
  enabled = true,
  limit = 25,
) {
  const { cursor, refreshGeneration } = page;
  return useQuery({
    queryKey: ["token", address, "transfers", limit, cursor ?? null, refreshGeneration],
    meta: { ...statusQueryMeta, clearOnReorg: true, clearOnStatus: true },
    queryFn: async ({ signal }): Promise<CursorPage<TokenEvent>> => {
      const response = requireEnvelope(
        await sdk.listTokenTransfers({
          client: apiClient,
          signal,
          path: { address },
          query: { limit, cursor },
        }),
      );
      return {
        items: response.data,
        meta: response.meta,
        next_cursor: response.meta.next_cursor,
      };
    },
    enabled: enabled && address.length > 0,
    retry: false,
    staleTime: 10_000,
  });
}

export function useTokenHolders(
  address: string,
  page: CursorRequest = firstPage,
  enabled = true,
  limit = 50,
) {
  const { cursor, refreshGeneration } = page;
  return useQuery({
    queryKey: ["token", address, "holders", limit, cursor ?? null, refreshGeneration],
    meta: chainQueryMeta,
    queryFn: async ({ signal }): Promise<TokenHolderPage> => {
      const response = requireEnvelope(
        await sdk.listTokenHolders({
          client: apiClient,
          signal,
          path: { address },
          query: { limit, cursor },
        }),
      );
      return {
        items: response.data,
        meta: response.meta,
        next_cursor: response.meta.next_cursor,
      };
    },
    enabled: enabled && address.length > 0,
    retry: false,
    staleTime: liveRefetchInterval,
  });
}

export function useAddressNFTBalances(
  address: string,
  page: CursorRequest = firstPage,
  enabled = true,
  limit = 25,
) {
  const { cursor, refreshGeneration } = page;
  return useQuery({
    queryKey: ["address", address, "nfts", cursor ?? null, limit, refreshGeneration],
    meta: { ...statusQueryMeta, clearOnReorg: true, clearOnStatus: true },
    queryFn: async ({ signal }): Promise<CursorPage<NFTBalance>> => {
      const response = requireEnvelope(
        await sdk.listAddressNftBalances({
          client: apiClient,
          signal,
          path: { address },
          query: { limit, cursor },
        }),
      );
      return {
        items: response.data,
        meta: response.meta,
        next_cursor: response.meta.next_cursor,
      };
    },
    enabled: enabled && address.length > 0,
    retry: false,
    staleTime: 10_000,
  });
}

export function useAddressERC20Balances(
  address: string,
  page: CursorRequest = firstPage,
  enabled = true,
  limit = 25,
) {
  const { cursor, refreshGeneration } = page;
  return useQuery({
    queryKey: ["address", address, "erc20-balances", cursor ?? null, limit, refreshGeneration],
    meta: { ...statusQueryMeta, clearOnReorg: true, clearOnStatus: true },
    queryFn: async ({ signal }): Promise<CursorPage<ERC20Balance>> => {
      const response = requireEnvelope(
        await sdk.listAddressErc20Balances({
          client: apiClient,
          signal,
          path: { address },
          query: { limit, cursor },
        }),
      );
      return {
        items: response.data,
        meta: response.meta,
        next_cursor: response.meta.next_cursor,
      };
    },
    enabled: enabled && address.length > 0,
    retry: false,
    staleTime: 10_000,
  });
}

export function useNFTOwnership(address: string, tokenID: string, enabled = true) {
  return useQuery({
    queryKey: ["nft", address, tokenID],
    meta: chainQueryMeta,
    queryFn: async ({ signal }) =>
      requireEnvelope(
        await sdk.getNftOwner({ client: apiClient, signal, path: { address, token_id: tokenID } }),
      ).data,
    enabled: enabled && address.length > 0 && tokenID.length > 0,
    retry: false,
    staleTime: 10_000,
  });
}

export function useNFTMetadata(address: string, tokenID: string, enabled = true) {
  return useQuery({
    queryKey: ["nft", address, tokenID, "metadata"],
    meta: chainQueryMeta,
    queryFn: async ({ signal }): Promise<NFTMetadata> =>
      requireEnvelope(
        await sdk.getNftMetadata({
          client: apiClient,
          signal,
          path: { address, token_id: tokenID },
        }),
      ).data,
    enabled: enabled && address.length > 0 && tokenID.length > 0,
    retry: false,
    staleTime: 10_000,
  });
}

export function useSearchResults(query: string, page: CursorRequest = firstPage, limit = 20) {
  const { cursor, refreshGeneration } = page;
  return useQuery({
    queryKey: ["search", query, cursor ?? null, limit, refreshGeneration],
    meta: chainQueryMeta,
    queryFn: async ({ signal }): Promise<CursorPage<SearchResult>> => {
      const response = requireEnvelope(
        await sdk.search({ client: apiClient, signal, query: { q: query, cursor, limit } }),
      );
      return {
        items: response.data,
        meta: response.meta,
        next_cursor: response.meta.next_cursor,
      };
    },
    enabled: query.trim().length > 0,
    retry: false,
    staleTime: 30_000,
  });
}

export function useBlockStats(fromBlock: string, toBlock: string, enabled = true) {
  return useQuery({
    queryKey: ["block-stats", fromBlock, toBlock],
    meta: chainQueryMeta,
    queryFn: async ({ signal }) =>
      requireEnvelope(
        await sdk.getBlockStats({
          client: apiClient,
          signal,
          query: { from_block: fromBlock, to_block: toBlock },
        }),
      ).data,
    enabled: enabled && fromBlock.length > 0 && toBlock.length > 0,
    retry: false,
    staleTime: 30_000,
  });
}

export function useAggregateStats(fromBlock: string, toBlock: string, enabled = true) {
  return useQuery({
    queryKey: ["aggregate-stats", fromBlock, toBlock],
    meta: chainQueryMeta,
    queryFn: async ({ signal }): Promise<AggregateStats> =>
      requireEnvelope(
        await sdk.getAggregateStats({
          client: apiClient,
          signal,
          query: { from_block: fromBlock, to_block: toBlock },
        }),
      ).data,
    enabled: enabled && fromBlock.length > 0 && toBlock.length > 0,
    retry: false,
    staleTime: 30_000,
  });
}

export function useChartOverview() {
  return useQuery({
    queryKey: ["chart-overview"],
    meta: chainQueryMeta,
    queryFn: async ({ signal }): Promise<ChartOverview> =>
      requireEnvelope(await sdk.getChartOverview({ client: apiClient, signal })).data,
    retry: false,
    staleTime: 30_000,
  });
}

export function useChartMetric(
  metric: ChartMetric,
  fromTime: string,
  toTime: string,
  interval: ChartInterval,
  enabled = true,
) {
  return useQuery({
    queryKey: ["chart-metric", metric, fromTime, toTime, interval],
    meta: chainQueryMeta,
    queryFn: async ({ signal }): Promise<ChartMetricSeries> =>
      requireEnvelope(
        await sdk.getChartMetric({
          client: apiClient,
          signal,
          path: { metric },
          query: {
            from_time: fromTime,
            to_time: toTime,
            interval,
          },
        }),
      ).data,
    enabled,
    retry: false,
    staleTime: 30_000,
  });
}

export function useCompilerCatalog(language: VerificationSubmission["language"], enabled = true) {
  return useQuery({
    queryKey: ["verifier-compilers", language],
    queryFn: async ({ signal }) =>
      requireEnvelope(
        await sdk.listVerifierCompilers({ client: apiClient, signal, query: { language } }),
      ).data,
    enabled,
    retry: false,
    staleTime: 60_000,
  });
}
