import { useMutation, useQuery } from "@tanstack/react-query";

import { apiClient, requireEnvelope } from "./client";
import type {
  BlockSummary,
  CursorPage,
  PendingSnapshot,
  TokenContract,
  TokenEvent,
  TransactionSummary,
  VerificationSubmission,
} from "./types";

export function useChainStatus() {
  return useQuery({
    queryKey: ["status"],
    queryFn: async () => requireEnvelope(await apiClient.GET("/status")).data,
    retry: false,
    staleTime: 5_000,
    refetchInterval: 12_000,
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

export function useBlocks(limit = 12) {
  return useQuery({
    queryKey: ["blocks", limit],
    queryFn: async (): Promise<CursorPage<BlockSummary>> => {
      const response = requireEnvelope(
        await apiClient.GET("/blocks", { params: { query: { limit } } }),
      );
      return { items: response.data, next_cursor: response.meta.next_cursor };
    },
    retry: false,
    staleTime: 5_000,
  });
}

export function useTransactions(limit = 12) {
  return useQuery({
    queryKey: ["transactions", limit],
    queryFn: async (): Promise<CursorPage<TransactionSummary>> => {
      const response = requireEnvelope(
        await apiClient.GET("/transactions", { params: { query: { limit } } }),
      );
      return { items: response.data, next_cursor: response.meta.next_cursor };
    },
    retry: false,
    staleTime: 5_000,
  });
}

export function usePendingTransactions(cursor: string | undefined, enabled = true, limit = 25) {
  return useQuery({
    queryKey: ["pending-transactions", cursor ?? null, limit],
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

export function useTokens(limit = 25) {
  return useQuery({
    queryKey: ["tokens", limit],
    queryFn: async (): Promise<CursorPage<TokenContract>> => {
      const response = requireEnvelope(
        await apiClient.GET("/tokens", { params: { query: { limit } } }),
      );
      return { items: response.data, next_cursor: response.meta.next_cursor };
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

export function useTokenTransfers(address: string, limit = 25, enabled = true) {
  return useQuery({
    queryKey: ["token", address, "transfers", limit],
    queryFn: async (): Promise<CursorPage<TokenEvent>> => {
      const response = requireEnvelope(
        await apiClient.GET("/tokens/{address}/transfers", {
          params: { path: { address }, query: { limit } },
        }),
      );
      return { items: response.data, next_cursor: response.meta.next_cursor };
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

export function useSearchResults(query: string) {
  return useQuery({
    queryKey: ["search", query],
    queryFn: async () =>
      requireEnvelope(
        await apiClient.GET("/search", { params: { query: { q: query } } }),
      ).data,
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

export function useSubmitVerification(apiKey: string) {
  return useMutation({
    mutationFn: async (submission: VerificationSubmission) =>
      requireEnvelope(
        await apiClient.POST("/verification/jobs", {
          body: submission,
          headers: { "X-API-Key": apiKey },
        }),
      ).data,
    gcTime: 0,
  });
}

export function useVerificationJob(id: string, apiKey: string, enabled = true) {
  return useQuery({
    queryKey: ["verification-job", id],
    queryFn: async () =>
      requireEnvelope(
        await apiClient.GET("/verification/jobs/{id}", {
          params: { path: { id } },
          headers: { "X-API-Key": apiKey },
        }),
      ).data,
    enabled: enabled && id.length > 0 && apiKey.length > 0,
    retry: false,
    gcTime: 0,
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      return status === "queued" || status === "running" ? 2_000 : false;
    },
  });
}

export function useVerifiedContract(
  address: string,
  codeHash: string,
  apiKey: string,
  requestRevision: number,
  enabled = true,
) {
  return useQuery({
    // The revision retries an edited credential without placing that credential in the cache key.
    queryKey: ["verified-contract", address, codeHash, requestRevision],
    queryFn: async () =>
      requireEnvelope(
        await apiClient.GET("/contracts/{address}/verification", {
          params: { path: { address }, query: { code_hash: codeHash } },
          headers: { "X-API-Key": apiKey },
        }),
      ).data,
    enabled:
      enabled && address.length > 0 && codeHash.length > 0 && apiKey.length > 0 && requestRevision > 0,
    retry: false,
    gcTime: 0,
    staleTime: 30_000,
  });
}
