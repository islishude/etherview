import type { components } from "./schema.gen";
import { apiClient, requireEnvelope, requireNoContent } from "./client";

export type APIKeyScope = components["schemas"]["APIKeyScope"];
export type UserAPIKey = components["schemas"]["UserAPIKey"];
export type UserAPIKeyIssued = components["schemas"]["UserAPIKeyIssued"];
export type UserAPIKeyPage = components["schemas"]["UserAPIKeyPage"];

export async function listCurrentUserAPIKeys(limit: number, cursor?: string) {
  return requireEnvelope(
    await apiClient.GET("/users/me/api-keys", {
      params: { query: { limit, cursor } },
    }),
  );
}

export async function createCurrentUserAPIKey(
  csrfToken: string,
  name: string,
  scopes: APIKeyScope[],
): Promise<UserAPIKeyIssued> {
  return requireEnvelope(
    await apiClient.POST("/users/me/api-keys", {
      params: { header: { "X-CSRF-Token": csrfToken } },
      body: { name, scopes },
    }),
  ).data;
}

export async function rotateCurrentUserAPIKey(
  csrfToken: string,
  prefix: string,
): Promise<UserAPIKeyIssued> {
  return requireEnvelope(
    await apiClient.POST("/users/me/api-keys/{prefix}/rotate", {
      params: {
        path: { prefix },
        header: { "X-CSRF-Token": csrfToken },
      },
    }),
  ).data;
}

export async function revokeCurrentUserAPIKey(
  csrfToken: string,
  prefix: string,
): Promise<void> {
  requireNoContent(
    await apiClient.DELETE("/users/me/api-keys/{prefix}", {
      params: {
        path: { prefix },
        header: { "X-CSRF-Token": csrfToken },
      },
    }),
  );
}
