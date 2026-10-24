import * as sdk from "./heyapi/sdk.gen";
import type * as schemas from "./heyapi/types.gen";
import { apiClient, requireEnvelope, requireNoContent } from "./client";

export type APIKeyScope = schemas.ApiKeyScope;
export type UserAPIKey = schemas.UserApiKey;
export type UserAPIKeyIssued = schemas.UserApiKeyIssued;
export type UserAPIKeyPage = schemas.UserApiKeyPage;

export async function listCurrentUserAPIKeys(limit: number, cursor?: string) {
  return requireEnvelope(
    await sdk.listCurrentUserApiKeys({ client: apiClient, query: { limit, cursor } }),
  );
}

export async function createCurrentUserAPIKey(
  csrfToken: string,
  name: string,
  scopes: APIKeyScope[],
): Promise<UserAPIKeyIssued> {
  return requireEnvelope(
    await sdk.createCurrentUserApiKey({
      client: apiClient,
      headers: { "X-CSRF-Token": csrfToken },
      body: { name, scopes },
    }),
  ).data;
}

export async function rotateCurrentUserAPIKey(
  csrfToken: string,
  prefix: string,
): Promise<UserAPIKeyIssued> {
  return requireEnvelope(
    await sdk.rotateCurrentUserApiKey({
      client: apiClient,
      path: { prefix },
      headers: { "X-CSRF-Token": csrfToken },
    }),
  ).data;
}

export async function revokeCurrentUserAPIKey(csrfToken: string, prefix: string): Promise<void> {
  requireNoContent(
    await sdk.revokeCurrentUserApiKey({
      client: apiClient,
      path: { prefix },
      headers: { "X-CSRF-Token": csrfToken },
    }),
  );
}
