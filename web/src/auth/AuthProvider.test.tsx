import { useState } from "react";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { AuthChallenge, AuthSession } from "@/api/auth";
import { ApiError } from "@/api/client";
import { WalletBoundaryError } from "@/wallet/eip6963";
import { AuthProvider, useAuth } from "./AuthProvider";

const mocks = vi.hoisted(() => ({
  createAuthChallenge: vi.fn(),
  getAuthSession: vi.fn(),
  logoutAuthSession: vi.fn(),
  revokeAdminUserSessions: vi.fn(),
  updateAdminUser: vi.fn(),
  updateCurrentUser: vi.fn(),
  verifyAuthChallenge: vi.fn(),
  publicConfig: {
    data: {
      chain_id: "1",
      features: { user_auth: true },
    },
    isPending: false,
  },
  wallet: {
    active: {
      uuid: "00000000-0000-4000-8000-000000000001",
      name: "Test Wallet",
      account: "0x1111111111111111111111111111111111111111",
      chainID: "1",
      revision: 1,
    },
    connect: vi.fn(),
    isActiveWallet: vi.fn(),
    signSIWEChallenge: vi.fn(),
  },
}));

vi.mock("@/api/auth", () => ({
  createAuthChallenge: mocks.createAuthChallenge,
  getAuthSession: mocks.getAuthSession,
  logoutAuthSession: mocks.logoutAuthSession,
  revokeAdminUserSessions: mocks.revokeAdminUserSessions,
  updateAdminUser: mocks.updateAdminUser,
  updateCurrentUser: mocks.updateCurrentUser,
  verifyAuthChallenge: mocks.verifyAuthChallenge,
}));

vi.mock("@/api/hooks", () => ({
  usePublicConfig: () => mocks.publicConfig,
}));

vi.mock("@/wallet/WalletProvider", () => ({
  useWallet: () => mocks.wallet,
}));

const csrfToken = "c".repeat(43);
const replacementCSRFToken = "r".repeat(43);
const signature = `0x${"ab".repeat(65)}`;
const challengeID = "018f3b52-0b3d-7bf1-b65f-6f214827cb42";

describe("AuthProvider", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.publicConfig.data.chain_id = "1";
    mocks.publicConfig.data.features.user_auth = true;
    mocks.publicConfig.isPending = false;
    mocks.wallet.active = initialWallet();
    mocks.wallet.connect.mockImplementation(async () => {
      const connected = initialWallet();
      mocks.wallet.active = connected;
      return connected;
    });
    mocks.wallet.isActiveWallet.mockImplementation(
      (expected: ReturnType<typeof initialWallet>) =>
        sameWallet(mocks.wallet.active, expected),
    );
    mocks.wallet.signSIWEChallenge.mockResolvedValue(signature);
    mocks.getAuthSession.mockResolvedValue({ authenticated: false });
    mocks.logoutAuthSession.mockResolvedValue(undefined);
    mocks.updateCurrentUser.mockResolvedValue({
      ...userRecord(),
      display_name: "Updated",
    });
    mocks.updateAdminUser.mockResolvedValue(userRecord());
    mocks.revokeAdminUserSessions.mockResolvedValue("1");
  });

  it("keeps CSRF only in provider memory and sends it on generated-client writes", async () => {
    const storageWrite = vi.spyOn(window.localStorage, "setItem");
    mocks.getAuthSession.mockResolvedValue(authenticatedSession());

    renderAuth();
    expect(await screen.findByTestId("auth-state")).toHaveTextContent("authenticated");

    await userEvent.setup().click(screen.getByRole("button", { name: "Update profile" }));
    expect(mocks.updateCurrentUser).toHaveBeenCalledWith(csrfToken, "Updated");
    expect(storageWrite).not.toHaveBeenCalled();
    expect(window.localStorage.length).toBe(0);
    expect(document.body).not.toHaveTextContent(csrfToken);
  });

  it("performs challenge, exact wallet signing, and verification in order", async () => {
    const challenge = authChallenge();
    mocks.createAuthChallenge.mockResolvedValue(challenge);
    mocks.verifyAuthChallenge.mockResolvedValue(authenticatedSession());

    renderAuth();
    expect(await screen.findByTestId("auth-state")).toHaveTextContent("anonymous");
    await userEvent.setup().click(screen.getByRole("button", { name: "Log in" }));

    expect(await screen.findByTestId("auth-state")).toHaveTextContent("authenticated");
    expect(mocks.createAuthChallenge).toHaveBeenCalledWith(
      mocks.wallet.active.account,
    );
    expect(mocks.wallet.signSIWEChallenge).toHaveBeenCalledWith(
      challenge,
      initialWallet(),
    );
    expect(mocks.verifyAuthChallenge).toHaveBeenCalledWith(challengeID, signature);
    expect(mocks.createAuthChallenge.mock.invocationCallOrder[0]).toBeLessThan(
      mocks.wallet.signSIWEChallenge.mock.invocationCallOrder[0]!,
    );
    expect(mocks.wallet.signSIWEChallenge.mock.invocationCallOrder[0]).toBeLessThan(
      mocks.verifyAuthChallenge.mock.invocationCallOrder[0]!,
    );
  });

  it.each([
    [
      "invalid challenge ID",
      {
        challenge_id: "not-a-uuid",
        message: "server-authored SIWE",
        expires_at: "2099-01-01T00:05:00Z",
      },
    ],
    [
      "empty message",
      {
        challenge_id: challengeID,
        message: "",
        expires_at: "2099-01-01T00:05:00Z",
      },
    ],
    [
      "expired response",
      {
        challenge_id: challengeID,
        message: "server-authored SIWE",
        expires_at: "2020-01-01T00:05:00Z",
      },
    ],
  ])("rejects a %s before invoking the wallet", async (_name, challenge) => {
    mocks.createAuthChallenge.mockResolvedValue(challenge);

    renderAuth();
    await screen.findByTestId("auth-state");
    await userEvent.setup().click(screen.getByRole("button", { name: "Log in" }));

    expect(await screen.findByTestId("auth-error")).toHaveTextContent(
      "INVALID_AUTH_RESPONSE",
    );
    expect(mocks.wallet.signSIWEChallenge).not.toHaveBeenCalled();
    expect(mocks.verifyAuthChallenge).not.toHaveBeenCalled();
  });

  it("connects a selected wallet before creating and signing a challenge", async () => {
    const challenge = authChallenge();
    mocks.wallet.active = undefined as never;
    mocks.createAuthChallenge.mockResolvedValue(challenge);
    mocks.verifyAuthChallenge.mockResolvedValue(authenticatedSession());

    renderAuth();
    await screen.findByTestId("auth-state");
    await userEvent.setup().click(
      screen.getByRole("button", { name: "Log in with selected wallet" }),
    );

    expect(await screen.findByTestId("auth-state")).toHaveTextContent(
      "authenticated",
    );
    expect(mocks.wallet.connect).toHaveBeenCalledWith(initialWallet().uuid);
    expect(mocks.wallet.connect.mock.invocationCallOrder[0]).toBeLessThan(
      mocks.createAuthChallenge.mock.invocationCallOrder[0]!,
    );
    expect(mocks.createAuthChallenge).toHaveBeenCalledWith(
      initialWallet().account,
    );
  });

  it("does not create a challenge when the selected wallet is on another chain", async () => {
    mocks.wallet.active = undefined as never;
    mocks.wallet.connect.mockImplementation(async () => {
      const connected = { ...initialWallet(), chainID: "2" };
      mocks.wallet.active = connected;
      return connected;
    });

    renderAuth();
    await screen.findByTestId("auth-state");
    await userEvent.setup().click(
      screen.getByRole("button", { name: "Log in with selected wallet" }),
    );

    expect(await screen.findByTestId("auth-error")).toHaveTextContent(
      "CHAIN_MISMATCH",
    );
    expect(mocks.createAuthChallenge).not.toHaveBeenCalled();
    expect(mocks.wallet.signSIWEChallenge).not.toHaveBeenCalled();
  });

  it("reports a rejected connection and permits a clean retry", async () => {
    mocks.wallet.active = undefined as never;
    mocks.wallet.connect.mockRejectedValueOnce(
      new WalletBoundaryError("USER_REJECTED"),
    );

    renderAuth();
    await screen.findByTestId("auth-state");
    const selectedLogin = screen.getByRole("button", {
      name: "Log in with selected wallet",
    });
    await userEvent.setup().click(selectedLogin);
    expect(await screen.findByTestId("auth-error")).toHaveTextContent(
      "USER_REJECTED",
    );
    expect(mocks.createAuthChallenge).not.toHaveBeenCalled();

    mocks.createAuthChallenge.mockResolvedValue(authChallenge());
    mocks.verifyAuthChallenge.mockResolvedValue(authenticatedSession());
    await userEvent.setup().click(selectedLogin);
    expect(await screen.findByTestId("auth-state")).toHaveTextContent(
      "authenticated",
    );
  });

  it.each([
    ["disconnect", undefined],
    ["account", { account: "0x2222222222222222222222222222222222222222" }],
    ["chain", { chainID: "2" }],
    ["provider", { uuid: "00000000-0000-4000-8000-000000000002" }],
    ["revision", { revision: 2 }],
  ])(
    "clears local authentication and best-effort logs out after a wallet %s change",
    async (_name, update) => {
      mocks.getAuthSession.mockResolvedValue(authenticatedSession());
      const view = renderAuth();
      expect(await screen.findByTestId("auth-state")).toHaveTextContent("authenticated");

      mocks.wallet.active = update
        ? { ...initialWallet(), ...update }
        : undefined as never;
      view.rerender(authTree());

      expect(await screen.findByTestId("auth-state")).toHaveTextContent("anonymous");
      expect(mocks.logoutAuthSession).toHaveBeenCalledWith(csrfToken);
    },
  );

  it.each([
    ["disconnected", undefined],
    [
      "different account",
      { account: "0x2222222222222222222222222222222222222222" },
    ],
    ["different chain", { chainID: "2" }],
  ])(
    "rejects an initially restored session when the wallet is %s",
    async (_name, update) => {
      mocks.wallet.active = update
        ? { ...initialWallet(), ...update }
        : undefined as never;
      mocks.getAuthSession.mockResolvedValue(authenticatedSession());

      renderAuth();

      expect(await screen.findByTestId("auth-state")).toHaveTextContent("anonymous");
      expect(mocks.logoutAuthSession).toHaveBeenCalledWith(csrfToken);
    },
  );

  it.each([
    "",
    " leading",
    "trailing ",
    "control\u0000name",
    "x".repeat(65),
  ])("rejects a non-canonical returned display name %j", async (displayName) => {
    mocks.getAuthSession.mockResolvedValue(
      authenticatedSession({
        user: { ...userRecord(), display_name: displayName },
      }),
    );

    renderAuth();

    expect(await screen.findByTestId("auth-state")).toHaveTextContent("anonymous");
    expect(screen.getByTestId("auth-error")).toHaveTextContent(
      "INVALID_AUTH_RESPONSE",
    );
    expect(mocks.logoutAuthSession).toHaveBeenCalledWith(csrfToken);
  });

  it("revokes a server-created session when wallet identity changes during verify", async () => {
    let resolveVerification: ((value: AuthSession) => void) | undefined;
    mocks.createAuthChallenge.mockResolvedValue(authChallenge());
    mocks.verifyAuthChallenge.mockReturnValue(
      new Promise<AuthSession>((resolve) => {
        resolveVerification = resolve;
      }),
    );

    const view = renderAuth();
    await screen.findByTestId("auth-state");
    await userEvent.setup().click(screen.getByRole("button", { name: "Log in" }));
    await waitFor(() => expect(mocks.verifyAuthChallenge).toHaveBeenCalledOnce());

    mocks.wallet.active = { ...initialWallet(), revision: 2 };
    view.rerender(authTree());
    await act(async () => {
      resolveVerification?.(
        authenticatedSession({ csrf_token: replacementCSRFToken }),
      );
    });

    expect(screen.getByTestId("auth-state")).toHaveTextContent("anonymous");
    expect(mocks.logoutAuthSession).toHaveBeenCalledWith(replacementCSRFToken);
  });

  it("revokes a malformed verified session and reports only a stable local code", async () => {
    mocks.createAuthChallenge.mockResolvedValue(authChallenge());
    mocks.verifyAuthChallenge.mockResolvedValue(
      authenticatedSession({
        csrf_token: replacementCSRFToken,
        user: {
          ...userRecord(),
          address: "not-an-address",
        },
      }),
    );

    renderAuth();
    await screen.findByTestId("auth-state");
    await userEvent.setup().click(screen.getByRole("button", { name: "Log in" }));

    expect(await screen.findByTestId("auth-error")).toHaveTextContent(
      "INVALID_AUTH_RESPONSE",
    );
    expect(mocks.logoutAuthSession).toHaveBeenCalledWith(replacementCSRFToken);
  });

  it("never renders hostile provider or API error text", async () => {
    const providerFailure = new WalletBoundaryError("REQUEST_FAILED");
    providerFailure.message =
      "secret provider error https://wallet.invalid/?credential=private";
    mocks.createAuthChallenge.mockResolvedValue(authChallenge());
    mocks.wallet.signSIWEChallenge.mockRejectedValue(providerFailure);

    renderAuth();
    await screen.findByTestId("auth-state");
    await userEvent.setup().click(screen.getByRole("button", { name: "Log in" }));
    expect(await screen.findByTestId("auth-error")).toHaveTextContent("REQUEST_FAILED");
    expect(document.body).not.toHaveTextContent("secret provider error");

    mocks.getAuthSession.mockRejectedValue(
      new ApiError(503, {
        error: {
          code: "user_auth_unavailable",
          message: "secret database endpoint",
          request_id: "request-1",
        },
      }),
    );
    await userEvent.setup().click(screen.getByRole("button", { name: "Refresh" }));
    expect(await screen.findByTestId("auth-error")).toHaveTextContent(
      "user_auth_unavailable",
    );
    expect(document.body).not.toHaveTextContent("secret database endpoint");
  });

  it("rejects a profile response that changes the authenticated identity", async () => {
    mocks.getAuthSession.mockResolvedValue(authenticatedSession());
    mocks.updateCurrentUser.mockResolvedValue({
      ...userRecord(),
      address: "0x2222222222222222222222222222222222222222",
      display_name: "Updated",
      role: "admin",
    });

    renderAuth();
    expect(await screen.findByTestId("auth-state")).toHaveTextContent("authenticated");
    await userEvent.setup().click(screen.getByRole("button", { name: "Update profile" }));

    expect(await screen.findByTestId("auth-error")).toHaveTextContent(
      "INVALID_AUTH_RESPONSE",
    );
    expect(screen.getByTestId("auth-state")).toHaveTextContent("authenticated");
    expect(screen.getByText("Alice")).toBeVisible();
  });

  it("does not let a delayed profile write resurrect a cleared wallet session", async () => {
    let resolveUpdate: ((value: ReturnType<typeof userRecord>) => void) | undefined;
    mocks.getAuthSession.mockResolvedValue(authenticatedSession());
    mocks.updateCurrentUser.mockReturnValue(
      new Promise((resolve) => {
        resolveUpdate = resolve;
      }),
    );

    const view = renderAuth();
    expect(await screen.findByTestId("auth-state")).toHaveTextContent("authenticated");
    await userEvent.setup().click(screen.getByRole("button", { name: "Update profile" }));
    await waitFor(() => expect(mocks.updateCurrentUser).toHaveBeenCalledOnce());

    mocks.wallet.active = { ...initialWallet(), chainID: "2", revision: 2 };
    view.rerender(authTree());
    await act(async () => {
      resolveUpdate?.({ ...userRecord(), display_name: "Updated" });
    });

    expect(screen.getByTestId("auth-state")).toHaveTextContent("anonymous");
    expect(screen.queryByText("Updated")).not.toBeInTheDocument();
  });
});

function AuthHarness() {
  const auth = useAuth();
  const [, rerender] = useState(0);
  return (
    <div>
      <output data-testid="auth-state">
        {auth.session.authenticated ? "authenticated" : "anonymous"}
      </output>
      {auth.session.user?.display_name && <span>{auth.session.user.display_name}</span>}
      {auth.error && <output data-testid="auth-error">{auth.error}</output>}
      <button type="button" onClick={() => void auth.login()}>
        Log in
      </button>
      <button
        type="button"
        onClick={() => void auth.login(initialWallet().uuid)}
      >
        Log in with selected wallet
      </button>
      <button type="button" onClick={() => void auth.refresh()}>
        Refresh
      </button>
      <button
        type="button"
        onClick={() => {
          void auth.updateDisplayName("Updated").catch(() => {});
          rerender((value) => value + 1);
        }}
      >
        Update profile
      </button>
    </div>
  );
}

function renderAuth() {
  return render(authTree());
}

function authTree() {
  return (
    <AuthProvider>
      <AuthHarness />
    </AuthProvider>
  );
}

function initialWallet() {
  return {
    uuid: "00000000-0000-4000-8000-000000000001",
    name: "Test Wallet",
    account: "0x1111111111111111111111111111111111111111",
    chainID: "1",
    revision: 1,
  };
}

function sameWallet(
  current: ReturnType<typeof initialWallet>,
  expected: ReturnType<typeof initialWallet>,
) {
  return (
    current.uuid === expected.uuid &&
    current.account === expected.account &&
    current.chainID === expected.chainID &&
    current.revision === expected.revision
  );
}

function authChallenge(): AuthChallenge {
  const origin = new URL(window.location.origin);
  const expiresAt = "2099-01-01T00:05:00.000Z";
  return {
    challenge_id: challengeID,
    expires_at: expiresAt,
    message:
      `${origin.protocol.slice(0, -1)}://${origin.host} wants you to sign in with your Ethereum account:\n` +
      `${initialWallet().account}\n\n\n` +
      `URI: ${origin.origin}\n` +
      "Version: 1\n" +
      "Chain ID: 1\n" +
      "Nonce: abcdefghijklmnopqrstuvwx\n" +
      "Issued At: 2026-01-01T00:00:00.000Z\n" +
      `Expiration Time: ${expiresAt}\n` +
      `Request ID: ${challengeID}`,
  };
}

function userRecord() {
  return {
    id: "018f3b52-0b3d-7bf1-b65f-6f214827cb41",
    chain_id: "1",
    address: "0x1111111111111111111111111111111111111111",
    role: "admin" as const,
    status: "active" as const,
    display_name: "Alice",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    last_login_at: "2026-01-01T00:00:00Z",
  };
}

function authenticatedSession(
  overrides: Partial<AuthSession> = {},
): AuthSession {
  return {
    authenticated: true,
    csrf_token: csrfToken,
    expires_at: "2099-01-01T00:00:00Z",
    user: userRecord(),
    ...overrides,
  };
}
