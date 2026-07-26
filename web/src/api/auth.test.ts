import { afterEach, describe, expect, it, vi } from "vitest";

import {
  createAuthChallenge,
  listAdminUsers,
  logoutAuthSession,
  revokeAdminUserSessions,
  updateAdminUser,
  updateCurrentUser,
  verifyAuthChallenge,
} from "./auth";

const address = "0x1111111111111111111111111111111111111111";
const challengeID = "018f3b52-0b3d-7bf1-b65f-6f214827cb42";
const userID = "018f3b52-0b3d-7bf1-b65f-6f214827cb41";
const csrfToken = "c".repeat(43);
const signature = `0x${"ab".repeat(65)}`;

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("generated authentication API adapter", () => {
  it("keeps the SIWE message and signature in same-origin JSON bodies", async () => {
    const storageWrite = vi.spyOn(window.localStorage, "setItem");
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        envelope({
          challenge_id: challengeID,
          message: "server-authored SIWE message",
          expires_at: "2099-01-01T00:05:00Z",
        }),
      )
      .mockResolvedValueOnce(
        envelope({
          authenticated: true,
          csrf_token: csrfToken,
          expires_at: "2099-01-08T00:00:00Z",
          user: userRecord(),
        }),
      );
    vi.stubGlobal("fetch", fetcher);

    await createAuthChallenge(address);
    await verifyAuthChallenge(challengeID, signature);

    const [challengeURL, challengeRequest] = fetcher.mock.calls[0]!;
    const [verifyURL, verifyRequest] = fetcher.mock.calls[1]!;
    expect(challengeURL).toBe("/api/v1/auth/challenge");
    expect(challengeRequest).toMatchObject({
      method: "POST",
      credentials: "same-origin",
      cache: "no-store",
      body: JSON.stringify({ address }),
    });
    expect(verifyURL).toBe("/api/v1/auth/verify");
    expect(verifyRequest).toMatchObject({
      method: "POST",
      credentials: "same-origin",
      cache: "no-store",
      body: JSON.stringify({ challenge_id: challengeID, signature }),
    });
    expect(String(verifyURL)).not.toContain(signature);
    expect(storageWrite).not.toHaveBeenCalled();
    expect(window.localStorage.length).toBe(0);
  });

  it("sends the in-memory CSRF value only in the generated header parameter", async () => {
    const fetcher = vi.fn<typeof fetch>(async (input) => {
      const url = String(input);
      if (url === "/api/v1/auth/logout") {
        return new Response(null, { status: 204 });
      }
      if (url === `/api/v1/admin/users/${userID}/sessions/revoke`) {
        return envelope({ revoked_sessions: "2" });
      }
      return envelope(userRecord());
    });
    vi.stubGlobal("fetch", fetcher);

    await logoutAuthSession(csrfToken);
    await updateCurrentUser(csrfToken, "Alice");
    await updateAdminUser(csrfToken, userID, { role: "admin" });
    expect(await revokeAdminUserSessions(csrfToken, userID)).toBe("2");

    expect(fetcher).toHaveBeenCalledTimes(4);
    for (const [url, request] of fetcher.mock.calls) {
      expect(String(url)).not.toContain(csrfToken);
      expect(new Headers(request?.headers).get("X-CSRF-Token")).toBe(csrfToken);
      expect(request).toMatchObject({
        credentials: "same-origin",
        cache: "no-store",
      });
    }
  });

  it("returns the generated user-list envelope and preserves an opaque cursor", async () => {
    const cursor = "opaque +/?=:cursor";
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      envelope([userRecord()], { next_cursor: cursor }),
    );
    vi.stubGlobal("fetch", fetcher);

    const response = await listAdminUsers(25, cursor);

    expect(response.data).toEqual([userRecord()]);
    expect(response.meta.next_cursor).toBe(cursor);
    expect(fetcher.mock.calls[0]?.[0]).toBe(
      "/api/v1/admin/users?limit=25&cursor=opaque%20%2B%2F%3F%3D%3Acursor",
    );
  });
});

function envelope(data: unknown, meta: Record<string, unknown> = {}) {
  return Response.json({
    data,
    meta: {
      request_id: "auth-api-test",
      chain_id: "1",
      ...meta,
    },
  });
}

function userRecord() {
  return {
    id: userID,
    chain_id: "1",
    address,
    role: "admin",
    status: "active",
    display_name: "Alice",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    last_login_at: "2026-01-01T00:00:00Z",
  };
}
