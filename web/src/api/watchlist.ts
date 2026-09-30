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
export async function listNotifications(cursor = "", unreadOnly = false, signal?: AbortSignal) {
  return requireEnvelope(
    await apiClient.GET("/users/me/notifications", {
      params: { query: { cursor: cursor || undefined, unread_only: unreadOnly } },
      signal,
    }),
  ).data;
}
export async function readNotification(
  id: string,
  through: boolean,
  csrf: string,
  signal: AbortSignal,
) {
  const header = { "X-CSRF-Token": csrf };
  requireNoContent(
    through
      ? await apiClient.POST("/users/me/notifications/read-through", {
          body: { through_id: id },
          params: { header },
          signal,
        })
      : await apiClient.POST("/users/me/notifications/{id}/read", {
          params: { path: { id }, header },
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
