import { useQuery } from "@tanstack/react-query";
import { getPublicConfig } from "./heyapi/sdk.gen";
import { apiClient, requireEnvelope } from "./client";

// Shell consumers must not pull the route-only explorer hooks into the initial bundle.
export function usePublicConfig() {
  return useQuery({
    queryKey: ["config"],
    queryFn: async ({ signal }) =>
      requireEnvelope(await getPublicConfig({ client: apiClient, signal })).data,
    retry: false,
    staleTime: Number.POSITIVE_INFINITY,
  });
}
