import type { components } from "./schema.gen";
import { apiClient, ApiError, requireEnvelope, requireNoContent } from "./client";

export type AddressWatch = components["schemas"]["AddressWatch"];
export type WatchInput = components["schemas"]["WatchInput"];
export type AddressExportRequest = components["schemas"]["AddressExportRequest"];
export async function listWatches(signal?: AbortSignal) {
  return requireEnvelope(await apiClient.GET("/users/me/watchlist", { signal })).data;
}
export async function saveWatch(body: WatchInput, csrf: string, signal: AbortSignal, id?: string) {
  const header = { "X-CSRF-Token": csrf };
  return requireEnvelope(
    id
      ? await apiClient.PATCH("/users/me/watchlist/{id}", {
          body,
          params: { path: { id }, header },
          signal,
        })
      : await apiClient.POST("/users/me/watchlist", { body, params: { header }, signal }),
  ).data;
}
export async function deleteWatch(id: string, csrf: string, signal: AbortSignal) {
  requireNoContent(
    await apiClient.DELETE("/users/me/watchlist/{id}", {
      params: { path: { id }, header: { "X-CSRF-Token": csrf } },
      signal,
    }),
  );
}
export async function listPushSubscriptions(signal?: AbortSignal) {
  return requireEnvelope(await apiClient.GET("/users/me/push-subscriptions", { signal })).data;
}
export async function savePushSubscription(
  body: components["schemas"]["PushSubscriptionInput"],
  csrf: string,
  signal: AbortSignal,
) {
  return requireEnvelope(
    await apiClient.POST("/users/me/push-subscriptions", {
      body,
      params: { header: { "X-CSRF-Token": csrf } },
      signal,
    }),
  ).data;
}
export async function deletePushSubscription(id: string, csrf: string, signal: AbortSignal) {
  requireNoContent(
    await apiClient.DELETE("/users/me/push-subscriptions/{id}", {
      params: { path: { id }, header: { "X-CSRF-Token": csrf } },
      signal,
    }),
  );
}
export async function downloadAddressCSV(
  body: AddressExportRequest,
  csrf: string,
  signal: AbortSignal,
) {
  const result = await apiClient.POST("/users/me/exports/address-activity", {
    body,
    params: { header: { "X-CSRF-Token": csrf } },
    parseAs: "blob",
    signal,
  });
  if (!result.response.ok || !result.data) {
    throw new ApiError(result.response.status, result.error);
  }
  if (signal.aborted) return;
  const url = URL.createObjectURL(result.data);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = "address-activity.csv";
  document.body.append(anchor);
  anchor.click();
  anchor.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
