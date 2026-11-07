import * as sdk from "./heyapi/sdk.gen";
import type * as schemas from "./heyapi/types.gen";
import { apiClient, requireEnvelope, requireNoContent } from "./client";

export type AuthSession = schemas.AuthSession;
export type AuthChallenge = schemas.AuthChallenge;
export type User = schemas.User;
export type AdminUserUpdate = schemas.AdminUserUpdate;

export async function getAuthSession(): Promise<AuthSession> {
  return requireEnvelope(await sdk.getAuthSession({ client: apiClient })).data;
}

export async function createAuthChallenge(address: string) {
  return requireEnvelope(await sdk.createAuthChallenge({ client: apiClient, body: { address } }))
    .data;
}

export async function verifyAuthChallenge(
  challengeID: string,
  signature: string,
): Promise<AuthSession> {
  return requireEnvelope(
    await sdk.verifyAuthChallenge({
      client: apiClient,
      body: { challenge_id: challengeID, signature },
    }),
  ).data;
}

export async function logoutAuthSession(csrfToken: string): Promise<void> {
  requireNoContent(
    await sdk.logoutAuthSession({ client: apiClient, headers: { "X-CSRF-Token": csrfToken } }),
  );
}

export async function listAdminUsers(
  limit: number,
  cursor?: string,
  q?: string,
  signal?: AbortSignal,
) {
  return requireEnvelope(
    await sdk.listAdminUsers({ client: apiClient, query: { limit, cursor, q }, signal }),
  );
}

export async function updateAdminUser(
  csrfToken: string,
  id: string,
  update: AdminUserUpdate,
): Promise<User> {
  return requireEnvelope(
    await sdk.updateAdminUser({
      client: apiClient,
      path: { id },
      headers: { "X-CSRF-Token": csrfToken },
      body: update,
    }),
  ).data;
}

export async function revokeAdminUserSessions(csrfToken: string, id: string): Promise<string> {
  return requireEnvelope(
    await sdk.revokeAdminUserSessions({
      client: apiClient,
      path: { id },
      headers: { "X-CSRF-Token": csrfToken },
    }),
  ).data.revoked_sessions;
}

export async function getAdminOverview(signal?: AbortSignal) {
  return requireEnvelope(await sdk.getAdminOverview({ client: apiClient, signal })).data;
}
