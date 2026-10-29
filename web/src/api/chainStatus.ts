import { useQuery } from "@tanstack/react-query";
import { getStatus } from "./heyapi/sdk.gen";
import { apiClient, requireEnvelope } from "./client";

export function useChainStatus(enabled = true) {
  return useQuery({
    queryKey: ["status"],
    enabled,
    meta: { chainEvents: ["head", "reorg", "status", "names"] },
    queryFn: async ({ signal }) => {
      const response = requireEnvelope(await getStatus({ client: apiClient, signal }));
      return {
        ...response.data,
        coverage_start: response.meta.coverage_start,
        coverage_end: response.meta.coverage_end,
      };
    },
    retry: false,
    staleTime: 2_000,
  });
}
