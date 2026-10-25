import { useMutation } from "@tanstack/react-query";
import { apiClient, requireEnvelope } from "./client";
import { importSourcifyContract } from "./heyapi/sdk.gen";

export function useImportSourcifyContract(apiKey: string) {
  return useMutation({
    mutationFn: async ({ address, chainID }: { address: string; chainID: string }) =>
      requireEnvelope(
        await importSourcifyContract({
          client: apiClient,
          path: { address },
          body: { chain_id: chainID },
          headers: { "X-API-Key": apiKey },
        }),
      ).data,
    retry: false,
    gcTime: 0,
  });
}
