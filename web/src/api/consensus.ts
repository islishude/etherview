import { useQuery } from "@tanstack/react-query";
import { apiClient, requireEnvelope } from "./client";
import { statusQueryMeta } from "./chainEvents";

export function useBlockConsensus(id: string, enabled = true) {
  return useQuery({
    queryKey: ["block-consensus", id],
    meta: statusQueryMeta,
    enabled,
    queryFn: async () =>
      requireEnvelope(await apiClient.GET("/blocks/{id}/consensus", { params: { path: { id } } }))
        .data,
    refetchInterval: 12000,
    retry: false,
  });
}
export function useBlockBlobs(id: string, enabled = true) {
  return useQuery({
    queryKey: ["block-blobs", id],
    meta: statusQueryMeta,
    enabled,
    queryFn: async () =>
      requireEnvelope(await apiClient.GET("/blocks/{id}/blobs", { params: { path: { id } } })).data,
    refetchInterval: 12000,
    retry: false,
  });
}
export async function readBlob(hash: string, index: string) {
  const result = await apiClient.GET("/blocks/{hash}/blobs/{index}/content", {
    params: { path: { hash, index } },
    parseAs: "arrayBuffer",
  });
  if (!result.response.ok || !result.data || result.data.byteLength !== 131072)
    throw new Error("blob_storage_unavailable");
  return result.data;
}
