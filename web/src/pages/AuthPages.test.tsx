import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import axe from "axe-core";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AuthProvider } from "@/auth/AuthProvider";
import i18n from "@/i18n";
import { makeRouter } from "@/router";
import { ThemeProvider } from "@/theme/ThemeProvider";
import {
  EIP6963_ANNOUNCE_EVENT,
  EIP6963_REQUEST_EVENT,
  type EIP1193Event,
  type EIP1193Provider,
  type EIP1193RequestArguments,
} from "@/wallet/eip6963";
import { WalletProvider } from "@/wallet/WalletProvider";

const account = "0x1111111111111111111111111111111111111111";
const targetAccount = "0x2222222222222222222222222222222222222222";
const userID = "018f3b52-0b3d-7bf1-b65f-6f214827cb41";
const targetUserID = "018f3b52-0b3d-7bf1-b65f-6f214827cb42";
const challengeID = "018f3b52-0b3d-7bf1-b65f-6f214827cb43";
const csrfToken = "c".repeat(43);
const signature = `0x${"ab".repeat(65)}`;
const requestListeners: EventListener[] = [];

describe("authentication pages", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
    document.title = "Etherview";
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    for (const listener of requestListeners.splice(0)) {
      window.removeEventListener(EIP6963_REQUEST_EVENT, listener);
    }
  });

  it("renders the disabled feature without inventing user authority", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      if (String(input) === "/api/v1/config") return configResponse(false);
      return notFound();
    }));

    renderRoute("/account");

    expect(
      await screen.findByRole("heading", {
        name: "User authentication is disabled",
      }),
    ).toBeVisible();
    expect(screen.queryByRole("link", { name: "User admin" })).not.toBeInTheDocument();
    expect(screen.queryByText("User session", { exact: true })).not.toBeInTheDocument();
  });

  it("keeps direct sign-in disabled when no injected wallet is discovered", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      if (String(input) === "/api/v1/config") return configResponse(true);
      if (String(input) === "/api/v1/auth/session") {
        return envelope({ authenticated: false });
      }
      return notFound();
    }));

    renderRoute("/account");
    await screen.findByRole("heading", { name: "Wallet connection" });

    await waitFor(() => expect(accountPageSignInButton()).toBeDisabled());
    expect(
      screen.getByText(
        "Install or unlock a browser wallet, then refresh discovery.",
      ),
    ).toBeInTheDocument();
  });

  it("separates wallet connection from a restored user session and saves a bounded profile", async () => {
    const fake = fakeProvider();
    registerProvider(fake.provider);
    const requests: Array<{ url: string; request?: RequestInit }> = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      requests.push({ url, request });
      if (url === "/api/v1/config") return configResponse(true);
      if (url === "/api/v1/auth/session") {
        return envelope({ authenticated: false });
      }
      if (url === "/api/v1/auth/challenge") {
        return envelope(authSIWEChallenge());
      }
      if (url === "/api/v1/auth/verify") {
        return envelope(authSession(userRecord()));
      }
      if (url === "/api/v1/users/me" && request?.method === "PATCH") {
        const body = JSON.parse(String(request.body)) as { display_name: string | null };
        return envelope({ ...userRecord(), display_name: body.display_name });
      }
      return notFound();
    }));

    renderRoute("/account");
    const user = userEvent.setup();
    await screen.findByRole("heading", { name: "Wallet connection" });
    await waitFor(() => expect(accountPageSignInButton()).toBeEnabled());
    await user.click(accountPageSignInButton());

    expect(
      await screen.findByRole("heading", { name: "Wallet connection" }),
    ).toBeVisible();
    expect(screen.getByText("Wallet connected", { exact: true })).toBeVisible();
    expect(
      screen.getAllByText("User authenticated", { exact: true }),
    ).toHaveLength(2);
    expect(screen.getByRole("link", { name: "User admin" })).toBeVisible();
    expect(document.body).not.toHaveTextContent(csrfToken);
    expect([...storageValues()]).not.toContain(csrfToken);

    const displayName = screen.getByRole("textbox", {
      name: "Display name",
    });
    await user.clear(displayName);
    await user.type(displayName, "  Updated profile  ");
    await user.click(screen.getByRole("button", { name: "Save profile" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Profile saved.");

    const profileRequest = requests.find(
      ({ url, request }) =>
        url === "/api/v1/users/me" && request?.method === "PATCH",
    );
    expect(new Headers(profileRequest?.request?.headers).get("X-CSRF-Token")).toBe(
      csrfToken,
    );
    expect(profileRequest?.request?.body).toBe(
      JSON.stringify({ display_name: "Updated profile" }),
    );
    expect(profileRequest?.url).not.toContain(csrfToken);

    await user.clear(displayName);
    await user.type(displayName, "x".repeat(65));
    await user.click(screen.getByRole("button", { name: "Save profile" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Use at most 64 Unicode characters",
    );
    expect(
      requests.filter(
        ({ url, request }) =>
          url === "/api/v1/users/me" && request?.method === "PATCH",
      ),
    ).toHaveLength(1);

    const scan = await axe.run(document, {
      runOnly: {
        type: "tag",
        values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"],
      },
      rules: { "color-contrast": { enabled: false } },
    });
    expect(scan.violations, JSON.stringify(scan.violations, null, 2)).toEqual([]);

    await user.click(screen.getByRole("button", { name: "切换到中文" }));
    expect(
      await screen.findByRole("heading", { name: "钱包连接" }),
    ).toBeVisible();
    expect(screen.getAllByText("用户已登录", { exact: true })).toHaveLength(2);
  });

  it("logs in through the exact generated API and bounded personal_sign sequence", async () => {
    const fake = fakeProvider();
    registerProvider(fake.provider);
    const requests: Array<{ url: string; request?: RequestInit }> = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      requests.push({ url, request });
      if (url === "/api/v1/config") return configResponse(true);
      if (url === "/api/v1/auth/session") {
        return envelope({ authenticated: false });
      }
      if (url === "/api/v1/auth/challenge") {
        return envelope(authSIWEChallenge());
      }
      if (url === "/api/v1/auth/verify") {
        return envelope(authSession(userRecord()));
      }
      return notFound();
    }));

    renderRoute("/account");
    const user = userEvent.setup();
    await screen.findByRole("heading", { name: "Wallet connection" });
    await waitFor(() => expect(accountPageSignInButton()).toBeEnabled());
    await user.click(accountPageSignInButton());
    expect(
      await screen.findAllByText("User authenticated", { exact: true }),
    ).toHaveLength(2);
    expect(fake.request).toHaveBeenCalledWith({
      method: "personal_sign",
      params: [
        `0x${[...new TextEncoder().encode(authSIWEChallenge().message)]
          .map((byte) => byte.toString(16).padStart(2, "0"))
          .join("")}`,
        account,
      ],
    });
    expect(
      fake.request.mock.calls
        .map(([request]) => request.method)
        .filter((method) => method === "personal_sign"),
    ).toEqual(["personal_sign"]);

    const challengeRequest = requests.find(
      ({ url }) => url === "/api/v1/auth/challenge",
    );
    const verifyRequest = requests.find(
      ({ url }) => url === "/api/v1/auth/verify",
    );
    expect(challengeRequest?.request?.body).toBe(JSON.stringify({ address: account }));
    expect(verifyRequest?.request?.body).toBe(
      JSON.stringify({ challenge_id: challengeID, signature }),
    );
    expect(String(verifyRequest?.url)).not.toContain(signature);
  });

  it("manages scoped API keys only from the deep-linked account workspace", async () => {
    const token = `evk_abcdefghij_${"a".repeat(43)}`;
    const requests: Array<{ url: string; request?: RequestInit }> = [];
    vi.stubGlobal("confirm", vi.fn(() => true));
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      requests.push({ url, request });
      if (url === "/api/v1/config") {
        return envelope({
          chain_id: "1",
          chain_name: "Auth Testnet",
          native_symbol: "ETH",
          native_name: "Ether",
          native_decimals: 18,
          features: { user_auth: true, user_api_keys: true },
        });
      }
      if (url === "/api/v1/auth/session") return envelope(authSession(userRecord()));
      if (url.startsWith("/api/v1/users/me/api-keys") && request?.method === "POST") {
        return envelope({
          token,
          key: {
            prefix: "abcdefghij",
            name: "Production indexer",
            scopes: ["api:read", "contract:verify"],
            rate_per_second: 20,
            burst: 40,
            status: "active",
            created_at: "2026-08-10T00:00:00Z",
          },
        });
      }
      if (url.startsWith("/api/v1/users/me/api-keys") && request?.method === "DELETE") {
        return new Response(null, { status: 204 });
      }
      if (url.startsWith("/api/v1/users/me/api-keys")) {
        return envelope({
          items: [{
            prefix: "abcdefghij",
            name: "Existing reader",
            scopes: ["api:read"],
            rate_per_second: 20,
            burst: 40,
            status: "active",
            created_at: "2026-08-09T00:00:00Z",
          }],
          policy: {
            rate_per_second: 20,
            burst: 40,
            maximum_active: 5,
            active_count: 1,
            allowed_scopes: ["api:read", "contract:verify"],
          },
        });
      }
      return notFound();
    }));

    renderRoute("/account?tab=api-keys");
    const user = userEvent.setup();
    expect(await screen.findByRole("heading", { name: "API Keys" })).toBeVisible();
    expect(window.location.pathname).not.toBe("/users");
    await user.type(screen.getByRole("textbox", { name: "Key name" }), "Production indexer");
    await user.click(screen.getByRole("checkbox", { name: /Contract verification/ }));
    await user.click(screen.getByRole("button", { name: "Create key" }));

    const dialog = await screen.findByRole("dialog", { name: "Save your API key now" });
    expect(dialog).toHaveFocus();
    expect(dialog).toHaveTextContent(token);
    expect([...storageValues()]).not.toContain(token);
    expect(window.location.href).not.toContain(token);
    const createRequest = requests.find(
      ({ url, request }) => url === "/api/v1/users/me/api-keys" && request?.method === "POST",
    );
    expect(new Headers(createRequest?.request?.headers).get("X-CSRF-Token")).toBe(csrfToken);
    expect(createRequest?.request?.body).toBe(JSON.stringify({
      name: "Production indexer",
      scopes: ["api:read", "contract:verify"],
    }));

    await user.click(screen.getByRole("button", { name: "I saved the token" }));
    expect(screen.queryByText(token)).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Revoke" }));
    await waitFor(() => expect(requests.some(
      ({ url, request }) => url === "/api/v1/users/me/api-keys/abcdefghij" && request?.method === "DELETE",
    )).toBe(true));
  });

  it("chooses among multiple wallets inline and connects only the selected provider", async () => {
    const alpha = fakeProvider();
    const beta = fakeProvider();
    registerProvider(alpha.provider, {
      uuid: "00000000-0000-4000-8000-000000000011",
      name: "Alpha Wallet",
      rdns: "org.etherview.alpha",
    });
    registerProvider(beta.provider, {
      uuid: "00000000-0000-4000-8000-000000000012",
      name: "Beta Wallet",
      rdns: "org.etherview.beta",
    });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      switch (String(input)) {
        case "/api/v1/config":
          return configResponse(true);
        case "/api/v1/auth/session":
          return envelope({ authenticated: false });
        case "/api/v1/auth/challenge":
          return envelope(authSIWEChallenge());
        case "/api/v1/auth/verify":
          return envelope(authSession(userRecord()));
        default:
          return notFound();
      }
    }));

    renderRoute("/account");
    const user = userEvent.setup();
    await screen.findByRole("heading", { name: "Wallet connection" });
    await waitFor(() => expect(accountPageSignInButton()).toBeEnabled());
    await user.click(accountPageSignInButton());

    const chooser = screen.getByRole("group", {
      name: "Choose a wallet to sign in",
    });
    await user.click(
      within(chooser).getByRole("button", { name: /Beta Wallet/u }),
    );

    expect(
      await screen.findAllByText("User authenticated", { exact: true }),
    ).toHaveLength(2);
    expect(
      alpha.request.mock.calls.some(([request]) => request.method === "eth_requestAccounts"),
    ).toBe(false);
    expect(
      beta.request.mock.calls.some(([request]) => request.method === "eth_requestAccounts"),
    ).toBe(true);
  });

  it("provides accessible admin mutation, revocation, and opaque pagination controls", async () => {
    const fake = fakeProvider();
    registerProvider(fake.provider);
    const opaqueCursor = "users/snapshot + page=2";
    const requests: Array<{ url: string; request?: RequestInit }> = [];
    let target = userRecord({
      id: targetUserID,
      address: targetAccount,
      role: "user",
      display_name: null,
    });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      requests.push({ url, request });
      if (url === "/api/v1/config") return configResponse(true);
      if (url === "/api/v1/auth/session") {
        return envelope({ authenticated: false });
      }
      if (url === "/api/v1/auth/challenge") {
        return envelope(authSIWEChallenge());
      }
      if (url === "/api/v1/auth/verify") {
        return envelope(authSession(userRecord()));
      }
      if (url.startsWith("/api/v1/admin/users?")) {
        if (url.includes("cursor=")) {
          return envelope([userRecord()], {});
        }
        return envelope([target], { next_cursor: opaqueCursor });
      }
      if (
        url === `/api/v1/admin/users/${targetUserID}` &&
        request?.method === "PATCH"
      ) {
        const update = JSON.parse(String(request.body)) as {
          role?: "user" | "admin";
          status?: "active" | "disabled";
        };
        target = { ...target, ...update };
        return envelope(target);
      }
      if (
        url === `/api/v1/admin/users/${targetUserID}/sessions/revoke` &&
        request?.method === "POST"
      ) {
        return envelope({ revoked_sessions: "3" });
      }
      return notFound();
    }));

    renderRoute("/admin/users");
    const user = userEvent.setup();
    await signInFromWalletMenu(user);

    expect(
      await screen.findByRole("heading", { name: "User administration" }),
    ).toBeVisible();
    expect(await screen.findByText(targetAccount)).toBeVisible();
    await user.selectOptions(
      screen.getByRole("combobox", { name: `Role for ${targetAccount}` }),
      "admin",
    );
    await user.selectOptions(
      screen.getByRole("combobox", { name: `Status for ${targetAccount}` }),
      "disabled",
    );
    await user.click(screen.getByRole("button", { name: "Save user" }));
    expect(await screen.findByText("Updated 0x222222…222222.")).toBeVisible();

    const updateRequest = requests.find(
      ({ url, request }) =>
        url === `/api/v1/admin/users/${targetUserID}` &&
        request?.method === "PATCH",
    );
    expect(updateRequest?.request?.body).toBe(
      JSON.stringify({ role: "admin", status: "disabled" }),
    );
    expect(new Headers(updateRequest?.request?.headers).get("X-CSRF-Token")).toBe(
      csrfToken,
    );

    await user.click(screen.getByRole("button", { name: "Revoke sessions" }));
    expect(
      await screen.findByText("Revoked 3 session(s) for 0x222222…222222."),
    ).toBeVisible();
    const revokeRequest = requests.find(
      ({ url, request }) =>
        url === `/api/v1/admin/users/${targetUserID}/sessions/revoke` &&
        request?.method === "POST",
    );
    expect(new Headers(revokeRequest?.request?.headers).get("X-CSRF-Token")).toBe(
      csrfToken,
    );

    await user.click(screen.getByRole("button", { name: "Next page" }));
    await waitFor(() => {
      expect(
        requests.some(({ url }) =>
          url.includes(
            "cursor=users%2Fsnapshot%20%2B%20page%3D2",
          ),
        ),
      ).toBe(true);
    });

    const scan = await axe.run(document, {
      runOnly: {
        type: "tag",
        values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"],
      },
      rules: { "color-contrast": { enabled: false } },
    });
    expect(scan.violations, JSON.stringify(scan.violations, null, 2)).toEqual([]);
  });
});

function renderRoute(path: string) {
  const router = makeRouter(createMemoryHistory({ initialEntries: [path] }));
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, refetchOnWindowFocus: false },
    },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <WalletProvider>
          <AuthProvider>
            <RouterProvider router={router} />
          </AuthProvider>
        </WalletProvider>
      </ThemeProvider>
    </QueryClientProvider>,
  );
}

function accountPageSignInButton() {
  const panel = document.querySelector<HTMLElement>(".auth-action-panel");
  expect(panel).not.toBeNull();
  return within(panel as HTMLElement).getByRole("button", {
    name: "Sign in with Ethereum",
  });
}

async function signInFromWalletMenu(
  user: ReturnType<typeof userEvent.setup>,
) {
  await user.click(
    await screen.findByText("Connect wallet", { selector: "summary" }),
  );
  const section = document.querySelector<HTMLElement>(".wallet-auth-section");
  expect(section).not.toBeNull();
  const signIn = within(section as HTMLElement).getByRole("button", {
    name: "Sign in with Ethereum",
  });
  await waitFor(() => expect(signIn).toBeEnabled());
  await user.click(signIn);
}

function configResponse(enabled: boolean) {
  return envelope({
    chain_id: "1",
    chain_name: "Auth Testnet",
    native_symbol: "ETH",
    native_name: "Ether",
    native_decimals: 18,
    features: { user_auth: enabled },
  });
}

function authSIWEChallenge() {
  const origin = new URL(window.location.origin);
  const expiresAt = "2099-01-01T00:05:00.000Z";
  return {
    challenge_id: challengeID,
    expires_at: expiresAt,
    message:
      `${origin.protocol.slice(0, -1)}://${origin.host} wants you to sign in with your Ethereum account:\n` +
      `${account}\n\n\n` +
      `URI: ${origin.origin}\n` +
      "Version: 1\n" +
      "Chain ID: 1\n" +
      "Nonce: abcdefghijklmnopqrstuvwx\n" +
      "Issued At: 2026-01-01T00:00:00.000Z\n" +
      `Expiration Time: ${expiresAt}\n` +
      `Request ID: ${challengeID}`,
  };
}

function authSession(user: ReturnType<typeof userRecord>) {
  return {
    authenticated: true,
    csrf_token: csrfToken,
    expires_at: "2099-01-08T00:00:00Z",
    user,
  };
}

function userRecord(
  overrides: Partial<{
    id: string;
    chain_id: string;
    address: string;
    role: "user" | "admin";
    status: "active" | "disabled";
    display_name: string | null;
    created_at: string;
    updated_at: string;
    last_login_at: string;
  }> = {},
) {
  return {
    id: userID,
    chain_id: "1",
    address: account,
    role: "admin" as const,
    status: "active" as const,
    display_name: "Alice" as string | null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    last_login_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

function envelope(data: unknown, meta: Record<string, unknown> = {}) {
  return Response.json({
    data,
    meta: {
      request_id: "auth-page-test",
      chain_id: "1",
      ...meta,
    },
  });
}

function notFound() {
  return Response.json(
    {
      error: {
        code: "not_found",
        message: "not found",
        request_id: "auth-page-test",
      },
    },
    { status: 404 },
  );
}

function storageValues(): string[] {
  const values: string[] = [];
  for (let index = 0; index < window.localStorage.length; index += 1) {
    const key = window.localStorage.key(index);
    if (key) values.push(window.localStorage.getItem(key) ?? "");
  }
  return values;
}

function registerProvider(
  provider: EIP1193Provider,
  info: Partial<{
    uuid: string;
    name: string;
    rdns: string;
  }> = {},
) {
  const detail = {
    info: {
      uuid: info.uuid ?? "00000000-0000-4000-8000-000000000001",
      name: info.name ?? "Test Wallet",
      icon: "data:image/png;base64,",
      rdns: info.rdns ?? "org.etherview.test",
    },
    provider,
  };
  const listener: EventListener = () => {
    window.dispatchEvent(new CustomEvent(EIP6963_ANNOUNCE_EVENT, { detail }));
  };
  requestListeners.push(listener);
  window.addEventListener(EIP6963_REQUEST_EVENT, listener);
}

function fakeProvider() {
  const listeners = new Map<EIP1193Event, Set<(value: unknown) => void>>();
  const request = vi.fn(
    async ({ method }: EIP1193RequestArguments): Promise<unknown> => {
      switch (method) {
        case "eth_requestAccounts":
        case "eth_accounts":
          return [account];
        case "eth_chainId":
          return "0x1";
        case "personal_sign":
          return signature;
        default:
          throw new Error(`unexpected method ${method}`);
      }
    },
  );
  const provider: EIP1193Provider = {
    request: request as EIP1193Provider["request"],
    on(event, listener) {
      const current = listeners.get(event) ?? new Set();
      current.add(listener);
      listeners.set(event, current);
    },
    removeListener(event, listener) {
      listeners.get(event)?.delete(listener);
    },
  };
  return { provider, request };
}
