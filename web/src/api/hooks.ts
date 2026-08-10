import { useMutation, useQuery } from "@tanstack/react-query";

import { apiClient, requireEnvelope } from "./client";
import type {
  AggregateStats,
  AddressInternalTransaction,
  AddressTokenTransfer,
  BlockSummary,
  ChartInterval,
  ChartMetric,
  ChartMetricSeries,
  ChartOverview,
  CursorPage,
  ERC20Balance,
  GenesisAccount,
  NFTBalance,
  PendingSnapshot,
  SearchResult,
  TokenContract,
  TokenEvent,
  TransactionSummary,
  VerificationSubmission,
} from "./types";

const liveRefetchInterval = 2_000;

export function useChainStatus() {
  return useQuery({
    queryKey: ["status"],
    queryFn: async () => {
      const response = requireEnvelope(await apiClient.GET("/status"));
      return {
        ...response.data,
        coverage_start: response.meta.coverage_start,
        coverage_end: response.meta.coverage_end,
      };
    },
    retry: false,
    staleTime: liveRefetchInterval,
    refetchInterval: liveRefetchInterval,
  });
}

export function usePublicConfig() {
  return useQuery({
    queryKey: ["config"],
    queryFn: async () => requireEnvelope(await apiClient.GET("/config")).data,
    retry: false,
    staleTime: Number.POSITIVE_INFINITY,
  });
}

export function useBlocks(limit = 12, cursor?: string, refreshGeneration = 0) {
  return useQuery({
    queryKey: ["blocks", limit, cursor ?? null, refreshGeneration],
    queryFn: async (): Promise<CursorPage<BlockSummary>> => {
      const response = requireEnvelope(
        await apiClient.GET("/blocks", { params: { query: { limit, cursor } } }),
      );
      return {
        items: response.data,
        meta: response.meta,
        next_cursor: response.meta.next_cursor,
      };
    },
    retry: false,
    staleTime: liveRefetchInterval,
    refetchInterval: cursor ? false : liveRefetchInterval,
  });
}

export function useGenesisAccounts(
  limit = 25,
  cursor?: string,
  refreshGeneration = 0,
) {
  return useQuery({
    queryKey: ["genesis-accounts", limit, cursor ?? null, refreshGeneration],
    queryFn: async (): Promise<CursorPage<GenesisAccount>> => {
      const response = requireEnvelope(
        await apiClient.GET("/genesis/accounts", {
          params: { query: { limit, cursor } },
        }),
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

export function useTransactions(limit = 12, cursor?: string, refreshGeneration = 0) {
  return useQuery({
    queryKey: ["transactions", limit, cursor ?? null, refreshGeneration],
    queryFn: async (): Promise<CursorPage<TransactionSummary>> => {
      const response = requireEnvelope(
        await apiClient.GET("/transactions", { params: { query: { limit, cursor } } }),
      );
      return {
        items: response.data,
        meta: response.meta,
        next_cursor: response.meta.next_cursor,
      };
    },
    retry: false,
    staleTime: liveRefetchInterval,
    refetchInterval: cursor ? false : liveRefetchInterval,
  });
}

export function usePendingTransactions(
  cursor: string | undefined,
  enabled = true,
  limit = 25,
  refreshGeneration = 0,
) {
  return useQuery({
    queryKey: ["pending-transactions", cursor ?? null, limit, refreshGeneration],
    queryFn: async (): Promise<PendingSnapshot> => {
      const response = requireEnvelope(
        await apiClient.GET("/pending", { params: { query: { limit, cursor } } }),
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
    queryFn: async () =>
      requireEnvelope(
        await apiClient.GET("/blocks/{id}", { params: { path: { id: identifier } } }),
      ).data,
    enabled: enabled && identifier.length > 0,
    retry: false,
    staleTime: 5_000,
  });
}

export function useBlockTransactions(
  identifier: string,
  cursor?: string,
  enabled = true,
) {
  return useQuery({
    queryKey: ["block", identifier, "transactions", cursor ?? null],
    queryFn: async () => {
      const response = requireEnvelope(
        await apiClient.GET("/blocks/{id}/transactions", {
          params: { path: { id: identifier }, query: { cursor, limit: 25 } },
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
    queryFn: async () =>
      requireEnvelope(
        await apiClient.GET("/transactions/{hash}", { params: { path: { hash } } }),
      ).data,
    enabled: enabled && hash.length > 0,
    retry: false,
    staleTime: 5_000,
  });
}

export function useTransactionCalldata(hash: string, enabled = true) {
  return useQuery({
    queryKey: ["transaction", hash, "calldata"],
    queryFn: async () =>
      requireEnvelope(
        await apiClient.GET("/transactions/{hash}/calldata", { params: { path: { hash } } }),
      ).data,
    enabled: enabled && hash.length > 0,
    retry: false,
    staleTime: 30_000,
  });
}

export function useTransactionTrace(hash: string, enabled = true) {
  return useQuery({
    queryKey: ["transaction", hash, "trace"],
    queryFn: async () =>
      requireEnvelope(
        await apiClient.GET("/transactions/{hash}/trace", { params: { path: { hash } } }),
      ).data,
    enabled: enabled && hash.length > 0,
    retry: false,
    staleTime: 30_000,
  });
}

export function useTransactionAuthorizations(
  hash: string,
  cursor?: string,
  enabled = true,
) {
  return useQuery({
    queryKey: ["transaction", hash, "authorizations", cursor ?? null],
    queryFn: async () => {
      const response = requireEnvelope(
        await apiClient.GET("/transactions/{hash}/authorizations", {
          params: { path: { hash }, query: { cursor, limit: 25 } },
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
  cursor?: string,
  enabled = true,
) {
  return useQuery({
    queryKey: ["transaction", hash, "token-transfers", cursor ?? null],
    queryFn: async () => {
      const response = requireEnvelope(
        await apiClient.GET("/transactions/{hash}/token-transfers", {
          params: { path: { hash }, query: { cursor, limit: 25 } },
        }),
      );
      return { ...response.data, next_cursor: response.meta.next_cursor };
    },
    enabled: enabled && hash.length > 0,
    retry: false,
    staleTime: 30_000,
  });
}

export function useTransactionLogs(hash: string, cursor?: string, enabled = true) {
  return useQuery({
    queryKey: ["transaction", hash, "logs", cursor ?? null],
    queryFn: async () => {
      const response = requireEnvelope(
        await apiClient.GET("/transactions/{hash}/logs", {
          params: { path: { hash }, query: { cursor, limit: 25 } },
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
  cursor?: string,
  enabled = true,
) {
  return useQuery({
    queryKey: ["transaction", hash, "state-changes", cursor ?? null],
    queryFn: async () => {
      const response = requireEnvelope(
        await apiClient.GET("/transactions/{hash}/state-changes", {
          params: { path: { hash }, query: { cursor, limit: 25 } },
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
    queryFn: async () =>
      requireEnvelope(
        await apiClient.GET("/addresses/{address}", { params: { path: { address } } }),
      ).data,
    enabled: enabled && address.length > 0,
    retry: false,
    staleTime: 5_000,
  });
}

export function useAddressTransactions(
  address: string,
  cursor?: string,
  limit = 25,
  refreshGeneration = 0,
  enabled = true,
) {
  return useQuery({
    queryKey: ["address", address, "transactions", cursor ?? null, limit, refreshGeneration],
    queryFn: async (): Promise<CursorPage<TransactionSummary>> => {
      const response = requireEnvelope(
        await apiClient.GET("/addresses/{address}/transactions", {
          params: { path: { address }, query: { limit, cursor } },
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
    refetchInterval: cursor ? false : liveRefetchInterval,
  });
}

export function useAddressInternalTransactions(
  address: string,
  cursor?: string,
  limit = 25,
  refreshGeneration = 0,
  enabled = true,
) {
  return useQuery({
    queryKey: ["address", address, "internal-transactions", cursor ?? null, limit, refreshGeneration],
    queryFn: async (): Promise<CursorPage<AddressInternalTransaction>> => {
      const response = requireEnvelope(
        await apiClient.GET("/addresses/{address}/internal-transactions", {
          params: { path: { address }, query: { limit, cursor } },
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
    refetchInterval: cursor ? false : liveRefetchInterval,
  });
}

export function useAddressERC20Transfers(
  address: string,
  cursor?: string,
  limit = 25,
  refreshGeneration = 0,
  enabled = true,
) {
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
  cursor?: string,
  limit = 25,
  refreshGeneration = 0,
  enabled = true,
) {
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
  path:
    | "/addresses/{address}/erc20-transfers"
    | "/addresses/{address}/nft-transfers",
  address: string,
  cursor: string | undefined,
  limit: number,
  refreshGeneration: number,
  enabled: boolean,
) {
  return useQuery({
    queryKey: ["address", address, kind, cursor ?? null, limit, refreshGeneration],
    queryFn: async (): Promise<CursorPage<AddressTokenTransfer>> => {
      const response = requireEnvelope(
        path === "/addresses/{address}/erc20-transfers"
          ? await apiClient.GET("/addresses/{address}/erc20-transfers", {
              params: { path: { address }, query: { limit, cursor } },
            })
          : await apiClient.GET("/addresses/{address}/nft-transfers", {
              params: { path: { address }, query: { limit, cursor } },
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
    refetchInterval: cursor ? false : liveRefetchInterval,
  });
}

export function useTokens(limit = 25, cursor?: string, refreshGeneration = 0) {
  return useQuery({
    queryKey: ["tokens", limit, cursor ?? null, refreshGeneration],
    queryFn: async (): Promise<CursorPage<TokenContract>> => {
      const response = requireEnvelope(
        await apiClient.GET("/tokens", { params: { query: { limit, cursor } } }),
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
    queryFn: async () =>
      requireEnvelope(
        await apiClient.GET("/tokens/{address}", { params: { path: { address } } }),
      ).data,
    enabled: enabled && address.length > 0,
    retry: false,
    staleTime: 30_000,
  });
}

export function useTokenTransfers(
  address: string,
  limit = 25,
  cursor?: string,
  refreshGeneration = 0,
  enabled = true,
) {
  return useQuery({
    queryKey: ["token", address, "transfers", limit, cursor ?? null, refreshGeneration],
    queryFn: async (): Promise<CursorPage<TokenEvent>> => {
      const response = requireEnvelope(
        await apiClient.GET("/tokens/{address}/transfers", {
          params: { path: { address }, query: { limit, cursor } },
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

export function useAddressNFTBalances(
  address: string,
  cursor?: string,
  limit = 25,
  refreshGeneration = 0,
  enabled = true,
) {
  return useQuery({
    queryKey: ["address", address, "nfts", cursor ?? null, limit, refreshGeneration],
    queryFn: async (): Promise<CursorPage<NFTBalance>> => {
      const response = requireEnvelope(
        await apiClient.GET("/addresses/{address}/nfts", {
          params: { path: { address }, query: { limit, cursor } },
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
  cursor?: string,
  limit = 25,
  refreshGeneration = 0,
  enabled = true,
) {
  return useQuery({
    queryKey: ["address", address, "erc20-balances", cursor ?? null, limit, refreshGeneration],
    queryFn: async (): Promise<CursorPage<ERC20Balance>> => {
      const response = requireEnvelope(
        await apiClient.GET("/addresses/{address}/erc20-balances", {
          params: { path: { address }, query: { limit, cursor } },
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
    queryFn: async () =>
      requireEnvelope(
        await apiClient.GET("/nfts/{address}/{token_id}", {
          params: { path: { address, token_id: tokenID } },
        }),
      ).data,
    enabled: enabled && address.length > 0 && tokenID.length > 0,
    retry: false,
    staleTime: 10_000,
  });
}

export function useSearchResults(
  query: string,
  cursor?: string,
  limit = 20,
  refreshGeneration = 0,
) {
  return useQuery({
    queryKey: ["search", query, cursor ?? null, limit, refreshGeneration],
    queryFn: async (): Promise<CursorPage<SearchResult>> => {
      const response = requireEnvelope(
        await apiClient.GET("/search", { params: { query: { q: query, cursor, limit } } }),
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
    queryFn: async () =>
      requireEnvelope(
        await apiClient.GET("/stats/blocks", {
          params: { query: { from_block: fromBlock, to_block: toBlock } },
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
    queryFn: async (): Promise<AggregateStats> =>
      requireEnvelope(
        await apiClient.GET("/stats/summary", {
          params: { query: { from_block: fromBlock, to_block: toBlock } },
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
    queryFn: async (): Promise<ChartOverview> =>
      requireEnvelope(await apiClient.GET("/stats/charts/overview")).data,
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
    queryFn: async (): Promise<ChartMetricSeries> =>
      requireEnvelope(
        await apiClient.GET("/stats/charts/{metric}", {
          params: {
            path: { metric },
            query: {
              from_time: fromTime,
              to_time: toTime,
              interval,
            },
          },
        }),
      ).data,
    enabled,
    retry: false,
    staleTime: 30_000,
  });
}

export function useSubmitVerification(address: string, apiKey: string) {
  return useMutation({
    mutationFn: async (submission: VerificationSubmission) =>
      requireEnvelope(
        await apiClient.POST("/contracts/{address}/verification", {
          body: submission,
          headers: { "X-API-Key": apiKey },
          params: { path: { address } },
        }),
      ).data,
    gcTime: 0,
  });
}

export function useVerificationJob(
  id: string,
  apiKey: string,
  requestRevision = 0,
  enabled = true,
) {
  return useQuery({
    // The revision retries an edited credential without placing that credential in the cache key.
    queryKey: ["verification-job", id, requestRevision],
    queryFn: async () =>
      requireEnvelope(
        await apiClient.GET("/verifier/jobs/{id}", {
          params: { path: { id } },
          headers: { "X-API-Key": apiKey },
        }),
      ).data,
    enabled: enabled && id.length > 0 && apiKey.length > 0 && requestRevision > 0,
    retry: false,
    gcTime: 0,
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      return status === "queued" || status === "running" ? 2_000 : false;
    },
  });
}

export function useCompilerCatalog(
  language: VerificationSubmission["language"],
  enabled = true,
) {
  return useQuery({
    queryKey: ["verifier-compilers", language],
    queryFn: async () =>
      requireEnvelope(
        await apiClient.GET("/verifier/compilers", {
          params: { query: { language } },
        }),
      ).data,
    enabled,
    retry: false,
    staleTime: 60_000,
  });
}
