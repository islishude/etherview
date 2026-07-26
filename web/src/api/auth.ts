import type { components } from "./schema.gen";
import { apiClient, requireEnvelope, requireNoContent } from "./client";

export type AuthSession = components["schemas"]["AuthSession"];
export type AuthChallenge = components["schemas"]["AuthChallenge"];
export type User = components["schemas"]["User"];
export type AdminUserUpdate = components["schemas"]["AdminUserUpdate"];

export async function getAuthSession(): Promise<AuthSession> {
  return requireEnvelope(await apiClient.GET("/auth/session")).data;
}

export async function createAuthChallenge(address: string) {
  return requireEnvelope(
    await apiClient.POST("/auth/challenge", { body: { address } }),
  ).data;
}

export async function verifyAuthChallenge(
  challengeID: string,
  signature: string,
): Promise<AuthSession> {
  return requireEnvelope(
    await apiClient.POST("/auth/verify", {
      body: { challenge_id: challengeID, signature },
    }),
  ).data;
}

export async function logoutAuthSession(csrfToken: string): Promise<void> {
  requireNoContent(
    await apiClient.POST("/auth/logout", {
      params: { header: { "X-CSRF-Token": csrfToken } },
    }),
  );
}

export async function updateCurrentUser(
  csrfToken: string,
  displayName: string | null,
): Promise<User> {
  return requireEnvelope(
    await apiClient.PATCH("/users/me", {
      params: { header: { "X-CSRF-Token": csrfToken } },
      body: { display_name: displayName },
    }),
  ).data;
}

export async function listAdminUsers(limit: number, cursor?: string) {
  return requireEnvelope(
    await apiClient.GET("/admin/users", {
      params: { query: { limit, cursor } },
    }),
  );
}

export async function updateAdminUser(
  csrfToken: string,
  id: string,
  update: AdminUserUpdate,
): Promise<User> {
  return requireEnvelope(
    await apiClient.PATCH("/admin/users/{id}", {
      params: {
        path: { id },
        header: { "X-CSRF-Token": csrfToken },
      },
      body: update,
    }),
  ).data;
}

export async function revokeAdminUserSessions(
  csrfToken: string,
  id: string,
): Promise<string> {
  return requireEnvelope(
    await apiClient.POST("/admin/users/{id}/sessions/revoke", {
      params: {
        path: { id },
        header: { "X-CSRF-Token": csrfToken },
      },
    }),
  ).data.revoked_sessions;
}
