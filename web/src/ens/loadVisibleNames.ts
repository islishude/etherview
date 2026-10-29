import { listAddressNames } from "@/api/heyapi/sdk.gen";
import { apiClient, ApiError, requireEnvelope } from "@/api/client";
import type { PrimaryName } from "@/api/heyapi/types.gen";

export async function loadVisibleNames(
  addresses: string[],
  token: string | undefined,
  signal: AbortSignal,
  reset: () => void,
) {
  for (let attempt = 0; attempt < 2; attempt++) {
    const names = new Map<string, PrimaryName>();
    let publication: string | undefined;
    try {
      for (let offset = 0; offset < addresses.length; offset += 100) {
        signal.throwIfAborted();
        const response = requireEnvelope(
          await listAddressNames({
            client: apiClient,
            signal,
            query: { addresses: addresses.slice(offset, offset + 100).join(","), snapshot: token },
          }),
        ).data;
        signal.throwIfAborted();
        if (
          (token && response.snapshot !== token) ||
          (publication && response.publication !== publication)
        )
          throw new Error("name snapshot changed");
        token = response.snapshot;
        publication = response.publication;
        for (const item of response.items)
          if (
            item.state === "resolved" &&
            item.primary_name &&
            Date.parse(item.primary_name.valid_until) > Date.now()
          )
            names.set(item.address.toLowerCase(), item.primary_name);
      }
      return { names, token };
    } catch (error) {
      signal.throwIfAborted();
      if (!(error instanceof ApiError && error.code === "invalid_cursor" && attempt === 0))
        throw error;
      token = undefined;
      reset();
    }
  }
  throw new Error("invalid name snapshot");
}
