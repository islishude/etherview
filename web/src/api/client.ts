import { createClient } from "./heyapi/client";

import type { StreamHeadEventsData } from "./heyapi/types.gen";
import type { ApiEnvelope, ApiErrorPayload, ApiMeta } from "./types";

type Fetcher = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

interface ClientResult<T> {
  data?: T;
  error?: unknown;
  response?: Response;
}

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly details?: unknown;
  readonly requestId?: string;

  constructor(
    status: number,
    payload?: ApiErrorPayload,
    fallback?: { code: string; message: string },
  ) {
    const apiError = payload?.error;
    super(apiError?.message ?? fallback?.message ?? `Explorer API request failed (${status})`);
    this.name = "ApiError";
    this.status = status;
    this.code = apiError?.code ?? fallback?.code ?? "HTTP_ERROR";
    this.details = apiError?.details;
    this.requestId = apiError?.request_id;
  }
}

export function createExplorerClient(fetcher: Fetcher = dynamicFetch) {
  const origin = sameOrigin();
  const client = createClient({
    baseUrl: `${origin}/api/v1`,
    cache: "no-store",
    credentials: "same-origin",
    headers: { Accept: "application/json" },
    parseAs: "json",
    fetch: (input, init) => {
      // Keep the SDK-owned Request and its abort controller alive together.
      const request =
        input instanceof Request && init === undefined ? input : new Request(input, init);
      return makeRelativeFetcher(origin, fetcher)(request);
    },
  });
  // Hey API captures transport/parse failures as results by default. Preserve
  // rejection for cancellation, network failures, and invalid JSON on success.
  client.interceptors.error.use((error, response) => {
    if (!response || response.ok) throw error;
    return error;
  });
  return client;
}

export function sameOriginAPIPath<Path extends StreamHeadEventsData["url"]>(
  path: Path,
): `/api/v1${Path & string}` {
  return `/api/v1${path}`;
}

export function requireEnvelope<T extends ApiEnvelope<unknown, ApiMeta>>(
  result: ClientResult<T>,
): T {
  const response = requireResponse(result);
  if (!isEnvelope(result.data)) {
    throw new ApiError(response.status, undefined, {
      code: "INVALID_RESPONSE",
      message: "Explorer API returned an invalid response envelope",
    });
  }
  return result.data as T;
}

export function requireResponse(result: ClientResult<unknown>): Response {
  if (!result.response) throw result.error ?? new TypeError("Explorer API returned no response");
  if (!result.response.ok || result.error !== undefined) {
    throw new ApiError(result.response.status, isApiError(result.error) ? result.error : undefined);
  }
  return result.response;
}

export function requireNoContent(result: ClientResult<unknown>): void {
  const response = requireResponse(result);
  if (response.status !== 204) throw new ApiError(response.status);
}

function sameOrigin(): string {
  if (typeof window === "undefined") return "http://localhost";
  return window.location.origin;
}

function makeRelativeFetcher(origin: string, fetcher: Fetcher) {
  return async (request: Request): Promise<Response> => {
    const url = new URL(request.url);
    if (url.origin !== origin || !url.pathname.startsWith("/api/v1/")) {
      throw new TypeError(
        "Explorer API requests must stay within the same-origin /api/v1 boundary",
      );
    }
    const hasBody = request.method !== "GET" && request.method !== "HEAD";
    const body = hasBody ? await request.clone().text() : undefined;
    return fetcher(`${url.pathname}${url.search}`, {
      method: request.method,
      headers: request.headers,
      credentials: request.credentials,
      cache: request.cache,
      redirect: request.redirect,
      referrer: request.referrer,
      referrerPolicy: request.referrerPolicy,
      integrity: request.integrity,
      keepalive: request.keepalive,
      mode: request.mode,
      signal: request.signal,
      ...(body === undefined ? {} : { body }),
    });
  };
}

function dynamicFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  return globalThis.fetch(input, init);
}

function isEnvelope(payload: unknown): payload is ApiEnvelope<unknown, ApiMeta> {
  return (
    typeof payload === "object" &&
    payload !== null &&
    "data" in payload &&
    "meta" in payload &&
    typeof payload.meta === "object" &&
    payload.meta !== null &&
    "request_id" in payload.meta &&
    typeof payload.meta.request_id === "string" &&
    "chain_id" in payload.meta &&
    typeof payload.meta.chain_id === "string"
  );
}

function isApiError(payload: unknown): payload is ApiErrorPayload {
  if (typeof payload !== "object" || payload === null || !("error" in payload)) {
    return false;
  }

  const error = payload.error;
  return (
    typeof error === "object" &&
    error !== null &&
    "code" in error &&
    typeof error.code === "string" &&
    "message" in error &&
    typeof error.message === "string" &&
    "request_id" in error &&
    typeof error.request_id === "string"
  );
}

export const apiClient = createExplorerClient();
