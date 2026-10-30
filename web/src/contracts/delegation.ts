import { chainQueryMeta } from "@/api/chainEvents";
import * as sdk from "../api/heyapi/sdk.gen";
import { firstPage, type CursorRequest } from "@/api/pagination";
import { useQuery } from "@tanstack/react-query";

import { apiClient, requireEnvelope } from "@/api/client";
import type * as schemas from "@/api/heyapi/types.gen";

export type DelegationBinding = schemas.DelegationBinding;
export type DelegationHistoryItem = schemas.DelegationHistoryItem;

export async function getAddressDelegation(
  address: string,
  signal?: AbortSignal,
): Promise<DelegationBinding> {
  return requireEnvelope(
    await sdk.getAddressDelegation({ client: apiClient, signal, path: { address } }),
  ).data;
}

export function useAddressDelegation(address: string, enabled = true) {
  return useQuery({
    queryKey: ["address", address, "delegation"],
    meta: chainQueryMeta,
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
    meta: { ...chainQueryMeta, pagination: page.identity },
    queryFn: async ({ signal }) => {
      const response = requireEnvelope(
        await sdk.listAddressDelegations({
          client: apiClient,
          signal,
          path: { address },
          query: { cursor, limit },
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
