import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import * as sdk from "./heyapi/sdk.gen";
import { apiClient, requireEnvelope } from "./client";
import { statusQueryMeta } from "./chainEvents";
import type { CursorRequest } from "./pagination";

export interface AddressLogRange {
  from_block?: string;
  to_block?: string;
}
export interface AddressLogFilter extends AddressLogRange {
  event_signature?: string;
  topic_index?: number;
  topic?: string;
}
export function validLogRange(from: string, to: string) {
  const valid = (v: string) =>
    !v || (/^(0|[1-9][0-9]*)$/u.test(v) && v.length <= 78 && BigInt(v) < 1n << 256n);
  return valid(from) && valid(to) && (!from || !to || BigInt(from) <= BigInt(to));
}
export function useAddressLogs(address: string, filter: AddressLogFilter, page: CursorRequest) {
  return useQuery({
    queryKey: ["address", address, "logs", filter, page.cursor, page.refreshGeneration],
    meta: { ...statusQueryMeta, pagination: page.identity },
    queryFn: async ({ signal }) =>
      requireEnvelope(
        await sdk.listAddressLogs({
          client: apiClient,
          signal,
          path: { address },
          query: { ...filter, cursor: page.cursor, limit: 25 },
        }),
      ),
    enabled: validLogRange(filter.from_block ?? "", filter.to_block ?? ""),
    retry: false,
  });
}
export function useAddressLogEvents(
  address: string,
  name: string,
  range: AddressLogRange,
  page: CursorRequest,
) {
  return useInfiniteQuery({
    queryKey: ["address", address, "log-events", name, range, page.refreshGeneration],
    meta: { ...statusQueryMeta, pagination: page.identity },
    initialPageParam: undefined as string | undefined,
    queryFn: async ({ signal, pageParam }) =>
      requireEnvelope(
        await sdk.listAddressLogEvents({
          client: apiClient,
          signal,
          path: { address },
          query: { ...range, name, cursor: pageParam, limit: 25 },
        }),
      ),
    getNextPageParam: (last) => last.meta.next_cursor || undefined,
    retry: false,
  });
}
