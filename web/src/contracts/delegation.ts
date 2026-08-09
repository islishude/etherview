import { useQuery } from "@tanstack/react-query";

import { apiClient, requireEnvelope } from "@/api/client";
import type { components } from "@/api/schema.gen";

export type DelegationBinding = components["schemas"]["DelegationBinding"];
export type DelegationHistoryItem = components["schemas"]["DelegationHistoryItem"];

export async function getAddressDelegation(address: string): Promise<DelegationBinding> {
  return requireEnvelope(
    await apiClient.GET("/addresses/{address}/delegation", {
      params: { path: { address } },
    }),
  ).data;
}

export function useAddressDelegation(address: string, enabled = true) {
  return useQuery({
    queryKey: ["address", address, "delegation"],
    queryFn: () => getAddressDelegation(address),
    enabled: enabled && address.length > 0,
    retry: false,
    staleTime: 0,
  });
}

export function useAddressDelegationHistory(
  address: string,
  cursor?: string,
  limit = 20,
  enabled = true,
) {
  return useQuery({
    queryKey: ["address", address, "delegations", cursor ?? null, limit],
    queryFn: async () => {
      const response = requireEnvelope(
        await apiClient.GET("/addresses/{address}/delegations", {
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
