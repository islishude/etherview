import * as sdk from "./heyapi/sdk.gen";
import type * as schemas from "./heyapi/types.gen";
import { apiClient, ApiError, requireEnvelope, requireNoContent, requireResponse } from "./client";

export type AddressWatch = schemas.AddressWatch;
export type WatchInput = schemas.WatchInput;
export type AddressExportRequest = schemas.AddressExportRequest;
export async function listWatches(signal?: AbortSignal) {
  return requireEnvelope(await sdk.listCurrentUserWatches({ client: apiClient, signal })).data;
}
export async function saveWatch(body: WatchInput, csrf: string, signal: AbortSignal, id?: string) {
  const header = { "X-CSRF-Token": csrf };
  return requireEnvelope(
    id
      ? await sdk.updateCurrentUserWatch({
          client: apiClient,
          body,
          path: { id },
          headers: header,
          signal,
        })
      : await sdk.createCurrentUserWatch({ client: apiClient, body, headers: header, signal }),
  ).data;
}
export async function deleteWatch(id: string, csrf: string, signal: AbortSignal) {
  requireNoContent(
    await sdk.deleteCurrentUserWatch({
      client: apiClient,
      path: { id },
      headers: { "X-CSRF-Token": csrf },
      signal,
    }),
  );
}
export async function listPushSubscriptions(signal?: AbortSignal) {
  return requireEnvelope(await sdk.listCurrentUserPushSubscriptions({ client: apiClient, signal }))
    .data;
}
export async function savePushSubscription(
  body: schemas.PushSubscriptionInput,
  csrf: string,
  signal: AbortSignal,
) {
  return requireEnvelope(
    await sdk.saveCurrentUserPushSubscription({
      client: apiClient,
      body,
      headers: { "X-CSRF-Token": csrf },
      signal,
    }),
  ).data;
}
export async function deletePushSubscription(id: string, csrf: string, signal: AbortSignal) {
  requireNoContent(
    await sdk.deleteCurrentUserPushSubscription({
      client: apiClient,
      path: { id },
      headers: { "X-CSRF-Token": csrf },
      signal,
    }),
  );
}
export async function downloadAddressCSV(
  body: AddressExportRequest,
  csrf: string,
  signal: AbortSignal,
  turnstileToken?: string,
) {
  const result = await sdk.exportCurrentUserAddressActivity({
    client: apiClient,
    body,
    headers: {
      "X-CSRF-Token": csrf,
      ...(turnstileToken ? { "X-Turnstile-Token": turnstileToken } : {}),
    },
    parseAs: "stream",
    signal,
  });
  const response = requireResponse(result);
  if (response.status === 204 || response.headers.get("Content-Length") === "0") {
    throw new ApiError(response.status);
  }
  const data = await response.blob();
  if (signal.aborted) return;
  const url = URL.createObjectURL(data);
  const anchor = document.createElement("a");
  anchor.href = url;
  const timestamp = (value: string) =>
    new Date(value).toISOString().slice(0, 19).replaceAll(":", "-") + "Z";
  anchor.download = `${body.address.toLowerCase()}-${timestamp(body.from)}-${timestamp(body.to)}-${body.kind}.csv`;
  document.body.append(anchor);
  anchor.click();
  anchor.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
