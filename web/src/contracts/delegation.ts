import { firstPage, type CursorRequest } from "@/api/pagination";
import { useQuery } from "@tanstack/react-query";

import { apiClient, requireEnvelope } from "@/api/client";
import type { components } from "@/api/schema.gen";

export type DelegationBinding = components["schemas"]["DelegationBinding"];
export type DelegationHistoryItem = components["schemas"]["DelegationHistoryItem"];

export async function getAddressDelegation(
  address: string,
  signal?: AbortSignal,
): Promise<DelegationBinding> {
  return requireEnvelope(
    await apiClient.GET("/addresses/{address}/delegation", {
      signal,
      params: { path: { address } },
    }),
  ).data;
}

export function useAddressDelegation(address: string, enabled = true) {
  return useQuery({
    queryKey: ["address", address, "delegation"],
    queryFn: ({ signal }) => getAddressDelegation(address, signal),
    enabled: enabled && address.length > 0,
    retry: false,
    staleTime: 0,
  });
}

export function useAddressDelegationHistory(
  address: string,
  page: CursorRequest = firstPage,
  enabled = true,
  limit = 20,
) {
  const { cursor, refreshGeneration } = page;
  return useQuery({
    queryKey: ["address", address, "delegations", cursor ?? null, limit, refreshGeneration],
    queryFn: async ({ signal }) => {
      const response = requireEnvelope(
        await apiClient.GET("/addresses/{address}/delegations", {
          signal,
          params: { path: { address }, query: { cursor, limit } },
        }),
      );
      return {
        items: response.data,
        meta: response.meta,
        nextCursor: response.meta.next_cursor,
      };
    },
    enabled: enabled && address.length > 0,
    retry: false,
    staleTime: 30_000,
  });
}
