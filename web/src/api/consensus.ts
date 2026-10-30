import * as sdk from "./heyapi/sdk.gen";
import { useQuery } from "@tanstack/react-query";
import { apiClient, requireEnvelope, requireResponse } from "./client";
import { statusQueryMeta } from "./chainEvents";

export function useBlockConsensus(id: string, enabled = true) {
  return useQuery({
    queryKey: ["block-consensus", id],
    meta: statusQueryMeta,
    enabled,
    queryFn: async ({ signal }) =>
      requireEnvelope(await sdk.getBlockConsensus({ client: apiClient, signal, path: { id } }))
        .data,
    retry: false,
  });
}
export function useBlockBlobs(id: string, enabled = true) {
  return useQuery({
    queryKey: ["block-blobs", id],
    meta: statusQueryMeta,
    enabled,
    queryFn: async ({ signal }) =>
      requireEnvelope(await sdk.getBlockBlobs({ client: apiClient, signal, path: { id } })).data,
    retry: false,
  });
}
export async function readBlob(hash: string, index: string) {
  const result = await sdk.getBlobContent({
    client: apiClient,
    path: { hash, index },
    parseAs: "stream",
  });
  const response = requireResponse(result);
  const data = await response.arrayBuffer();
  if (data.byteLength !== 131072) throw new Error("blob_storage_unavailable");
  const disposition = response.headers.get("Content-Disposition");
  const filename = disposition?.match(
    /^attachment; filename=(blob-(?:0|[1-9][0-9]*)-(?:0|[1-9][0-9]*)\.bin)$/,
  )?.[1];
  if (!filename || !filename.endsWith(`-${index}.bin`)) throw new Error("blob_storage_unavailable");
  return { data, filename };
}
