import { useQuery } from "@tanstack/react-query";
import { getBlockRequests } from "./heyapi/sdk.gen";
import type { ExecutionRequestType } from "./heyapi/types.gen";
import { apiClient, requireEnvelope } from "./client";
import { statusQueryMeta } from "./chainEvents";
import type { CursorRequest } from "./pagination";

export function useBlockRequests(
  hash: string,
  type: ExecutionRequestType | "",
  page: CursorRequest,
) {
  return useQuery({
    queryKey: ["block-requests", hash, type, page],
    meta: { ...statusQueryMeta, pagination: page.identity },
    enabled: Boolean(hash),
    queryFn: async ({ signal }) =>
      requireEnvelope(
        await getBlockRequests({
          client: apiClient,
          signal,
          path: { id: hash },
          query: { request_type: type || undefined, cursor: page.cursor, limit: 50 },
        }),
      ),
    retry: false,
  });
}
