import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import axe from "axe-core";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AuthProvider } from "@/auth/AuthProvider";
import i18n from "@/i18n";
import { makeRouter } from "@/router";
import { ThemeProvider } from "@/theme/ThemeProvider";
import {
  EIP6963_ANNOUNCE_EVENT,
  EIP6963_REQUEST_EVENT,
  type EIP1193Provider,
  type EIP1193RequestArguments,
} from "@/wallet/eip6963";
import { WalletProvider } from "@/wallet/WalletProvider";

const currentUserID = "018f3b52-0b3d-7bf1-b65f-6f214827cb61";
const paymentID = "018f3b52-0b3d-7bf1-b65f-6f214827cb66";
const address = "0x1111111111111111111111111111111111111111";
const asset = "0x3333333333333333333333333333333333333333";
const recipient = "0x4444444444444444444444444444444444444444";
const payer = "0x5555555555555555555555555555555555555555";
const transactionHash = `0x${"6".repeat(64)}`;
const csrfToken = "c".repeat(43);
const challengeID = "018f3b52-0b3d-7bf1-b65f-6f214827cb67";
const signature = `0x${"a".repeat(130)}`;
const personalCursor = "personal/ledger + page=2?exact=true/#";
const adminCursor = "admin/ledger + page=2?exact=true/#";
const apiKeyPrefix = "ev_private-prefix";
const amount = "340282366920938463463374607431768211455";
const count = "900719925474099312345";
const requestListeners: EventListener[] = [];

interface RecordedRequest {
  request?: RequestInit;
  url: string;
}

describe("billing pages", () => {
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

  it("renders personal history from the Cookie session without wallet-derived attribution", async () => {
    const requests: RecordedRequest[] = [];
    const fake = fakeProvider();
    registerProvider(fake);
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async (
          input: RequestInfo | URL,
          request?: RequestInit,
        ): Promise<Response> => {
          const url = String(input);
          requests.push({ request, url });
          if (url === "/api/v1/config") return configResponse();
          if (url === "/api/v1/auth/session") {
            return envelope({ authenticated: false });
          }
          if (url === "/api/v1/auth/challenge") {
            return envelope(authSIWEChallenge());
          }
          if (url === "/api/v1/auth/verify") {
            return envelope(authSession("user"));
          }
          if (url === "/api/v1/billing/config") return billingConfigResponse();
          if (url === "/api/v1/billing/account") return billingAccountResponse();
          if (url.startsWith("/api/v1/billing/topup-intents?")) return envelope([]);
          if (url.startsWith("/api/v1/billing/payments?")) {
            const parsed = relativeURL(url);
            return parsed.searchParams.get("cursor") === personalCursor
              ? envelope([payment({ state: "settled" })])
              : envelope(
                  [
                    payment({
                      api_key_prefix: apiKeyPrefix,
                      user_id: currentUserID,
                    }),
                  ],
                  { next_cursor: personalCursor },
                );
          }
          return notFound();
        },
      ),
    );

    await renderRoute("/account?tab=billing");
    expect(await screen.findByRole("heading", { name: "Account", level: 1 })).toBeVisible();
    const user = userEvent.setup();
    await connectTestWallet(user);
    await user.click(
      document.querySelector(".auth-action-panel button") as HTMLElement,
    );

    expect(
      await screen.findByRole("heading", { name: "Payment history" }),
    ).toBeVisible();
    expect(screen.getByText("Wallet connected", { exact: true })).toBeVisible();
    expect(
      screen.getByText(/follows the HttpOnly Cookie session/),
    ).toBeVisible();
    expect(screen.getAllByText(amount, { exact: true }).length).toBeGreaterThan(0);
    expect(document.body).not.toHaveTextContent(currentUserID);
    expect(document.body).not.toHaveTextContent(apiKeyPrefix);
    expect(document.body).not.toHaveTextContent(csrfToken);
    expect([...storageValues()]).not.toContain(csrfToken);

    await user.click(
      screen.getByRole("button", { name: "Next page" }),
    );
    await waitFor(() => {
      expect(
        requests.some(({ url }) => {
          if (!url.startsWith("/api/v1/billing/payments?")) return false;
          return relativeURL(url).searchParams.get("cursor") === personalCursor;
        }),
      ).toBe(true);
    });

    for (const { request, url } of billingRequests(requests)) {
      expect(relativeURL(url).searchParams.get("cursor")).toBe(
        url.includes("cursor=") ? personalCursor : null,
      );
      const headers = new Headers(request?.headers);
      expect(headers.has("PAYMENT-SIGNATURE")).toBe(false);
      expect(headers.has("X-CSRF-Token")).toBe(false);
      expect(request?.method).toBe("GET");
    }

    const scan = await axe.run(document, {
      runOnly: {
        type: "tag",
        values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"],
      },
      rules: { "color-contrast": { enabled: false } },
    });
    expect(scan.violations, JSON.stringify(scan.violations, null, 2)).toEqual([]);
  });

  it("shows exact admin summary and attribution, validates filters, and preserves the ledger cursor", async () => {
    const requests: RecordedRequest[] = [];
    const fake = fakeProvider();
    registerProvider(fake);
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async (
          input: RequestInfo | URL,
          request?: RequestInit,
        ): Promise<Response> => {
          const url = String(input);
          requests.push({ request, url });
          if (url === "/api/v1/config") return configResponse();
          if (url === "/api/v1/auth/session") {
            return envelope({ authenticated: false });
          }
          if (url === "/api/v1/auth/challenge") {
            return envelope(authSIWEChallenge());
          }
          if (url === "/api/v1/auth/verify") {
            return envelope(authSession("admin"));
          }
          if (url.startsWith("/api/v1/admin/billing/summary")) {
            return envelope(summary());
          }
          if (url.startsWith("/api/v1/admin/billing/payments?")) {
            const parsed = relativeURL(url);
            return envelope(
              [
                payment({
                  api_key_prefix: apiKeyPrefix,
                  failure_code: "settlement_unknown",
                  state: "settling",
                  user_id: currentUserID,
                }),
              ],
              parsed.searchParams.get("cursor") === adminCursor
                ? {}
                : { next_cursor: adminCursor },
            );
          }
          return notFound();
        },
      ),
    );

    await renderRoute("/admin/billing");
    const user = userEvent.setup();
    await connectTestWallet(user);
    await signInFromWalletMenu(user);

    expect(
      await screen.findByRole("heading", { name: "Billing administration" }),
    ).toBeVisible();
    expect(
      await screen.findByText("Settlement unknown", { exact: true }),
    ).toBeVisible();
    expect(screen.getByText(currentUserID, { exact: true })).toBeVisible();
    expect(screen.getByText(apiKeyPrefix, { exact: true })).toBeVisible();
    expect(screen.getAllByText(amount, { exact: true }).length).toBeGreaterThan(0);
    expect(screen.getAllByText(count, { exact: true }).length).toBeGreaterThan(0);
    expect(
      screen.queryByRole("option", { name: "getVerifiedContract" }),
    ).not.toBeInTheDocument();
    for (const operation of [
      "listTransactionTokenTransfers",
      "listTransactionInternalTransactions",
      "listTransactionLogs",
      "listTransactionStateChanges",
      "listAddressTransactions",
      "listAddressWithdrawals",
      "listAddressInternalTransactions",
      "listAddressERC20Transfers",
      "listAddressNFTTransfers",
      "listAddressERC20Balances",
    ]) {
      expect(screen.getByRole("option", { name: operation })).toBeInTheDocument();
    }

    const networkInput = screen.getByRole("textbox", { name: "Network" });
    await user.type(networkInput, "ethereum-mainnet");
    const requestCount = requests.length;
    await user.click(screen.getByRole("button", { name: "Apply filters" }));
    expect(
      await screen.findByRole("alert"),
    ).toHaveTextContent("Network must use the eip155:");
    expect(requests).toHaveLength(requestCount);

    await user.clear(networkInput);
    await user.type(networkInput, "eip155:84532");
    await user.selectOptions(
      screen.getByRole("combobox", { name: "State" }),
      "settling",
    );
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Operation" }),
      "getBlock",
    );
    await user.type(screen.getByRole("textbox", { name: "Asset" }), asset);
    fireEvent.change(screen.getByLabelText("From time"), {
      target: { value: "2026-06-01T00:00" },
    });
    fireEvent.change(screen.getByLabelText("To time"), {
      target: { value: "2026-07-03T00:00" },
    });
    await user.click(screen.getByRole("button", { name: "Apply filters" }));
    expect(
      await screen.findByRole("alert"),
    ).toHaveTextContent("cannot exceed 31 days");

    fireEvent.change(screen.getByLabelText("From time"), {
      target: { value: "2026-07-02T00:00" },
    });
    await user.click(screen.getByRole("button", { name: "Apply filters" }));
    const expectedFromTime = new Date("2026-07-02T00:00").toISOString();
    const expectedToTime = new Date("2026-07-03T00:00").toISOString();

    await waitFor(() => {
      expect(
        requests.some(({ url }) => {
          if (!url.startsWith("/api/v1/admin/billing/payments?")) return false;
          const query = relativeURL(url).searchParams;
          return (
            query.get("state") === "settling" &&
            query.get("operation") === "getBlock" &&
            query.get("network") === "eip155:84532" &&
            query.get("asset") === asset &&
            query.get("from_time") === expectedFromTime &&
            query.get("to_time") === expectedToTime
          );
        }),
      ).toBe(true);
    });

    await user.click(screen.getByRole("button", { name: "Next page" }));
    await waitFor(() => {
      expect(
        requests.some(({ url }) => {
          if (!url.startsWith("/api/v1/admin/billing/payments?")) return false;
          return relativeURL(url).searchParams.get("cursor") === adminCursor;
        }),
      ).toBe(true);
    });
    for (const { request } of billingRequests(requests)) {
      const headers = new Headers(request?.headers);
      expect(headers.has("PAYMENT-SIGNATURE")).toBe(false);
      expect(headers.has("X-CSRF-Token")).toBe(false);
      expect(request?.method).toBe("GET");
    }
    expect(document.body).not.toHaveTextContent(csrfToken);
    expect([...storageValues()]).not.toContain(csrfToken);

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

async function renderRoute(path: string) {
  const router = makeRouter(createMemoryHistory({ initialEntries: [path] }));
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, refetchOnWindowFocus: false },
    },
  });
  await router.load();
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

function authSession(role: "user" | "admin") {
  return {
    authenticated: true,
    csrf_token: csrfToken,
    expires_at: "2099-01-08T00:00:00Z",
    user: {
      id: currentUserID,
      chain_id: "1",
      address,
      role,
      status: "active",
      display_name: "Billing User",
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
      last_login_at: "2026-07-26T00:00:00Z",
    },
  };
}

function configResponse() {
  return envelope({
    chain_id: "1",
    chain_name: "Billing Testnet",
    native_symbol: "ETH",
    native_name: "Ether",
    native_decimals: 18,
    features: { user_auth: true, api_billing: true },
  });
}

function billingConfigResponse() {
  return envelope({
    api_billing_enabled: true,
    x402_topups_enabled: true,
    network: "eip155:1",
    asset,
    recipient,
    minimum_topup_amount_atomic: "1",
    maximum_topup_amount_atomic: amount,
    asset_transfer_methods: ["eip3009", "permit2"],
    operations: {},
    asset_eip712_name: "Billing Token",
    asset_eip712_version: "1",
  });
}

function billingAccountResponse() {
  return envelope({
    user_id: currentUserID,
    total_credit_atomic: amount,
    total_debit_atomic: "0",
    reserved_atomic: "0",
    available_atomic: amount,
    created_at: "2026-07-25T23:58:00Z",
    updated_at: "2026-07-26T00:00:00Z",
  });
}

function payment(overrides: Record<string, unknown> = {}) {
  return {
    id: paymentID,
    operation: "getBlock",
    state: "settled",
    network: "eip155:84532",
    asset,
    amount_atomic: amount,
    recipient,
    payer,
    transaction_hash: transactionHash,
    created_at: "2026-07-25T23:58:00Z",
    updated_at: "2026-07-26T00:00:00Z",
    settled_at: "2026-07-26T00:00:00Z",
    ...overrides,
  };
}

function summary() {
  return {
    amount_atomic: amount,
    from_time: "2026-07-25T00:00:00Z",
    payment_count: count,
    rows: [
      {
        amount_atomic: amount,
        asset,
        network: "eip155:84532",
        operation: "getBlock",
        payment_count: count,
        state: "settled",
      },
    ],
    to_time: "2026-07-26T00:00:00Z",
  };
}

function authSIWEChallenge() {
  const origin = new URL(window.location.origin);
  const expiresAt = "2099-01-01T00:05:00.000Z";
  return {
    challenge_id: challengeID,
    expires_at: expiresAt,
    message:
      `${origin.protocol.slice(0, -1)}://${origin.host} wants you to sign in with your Ethereum account:\n` +
      `${address}\n\n\n` +
      `URI: ${origin.origin}\n` +
      "Version: 1\n" +
      "Chain ID: 1\n" +
      "Nonce: abcdefghijklmnopqrstuvwx\n" +
      "Issued At: 2026-01-01T00:00:00.000Z\n" +
      `Expiration Time: ${expiresAt}\n` +
      `Request ID: ${challengeID}`,
  };
}

function envelope(data: unknown, meta: Record<string, unknown> = {}) {
  return Response.json({
    data,
    meta: {
      request_id: "billing-page-test",
      chain_id: "1",
      ...meta,
    },
  });
}

function notFound() {
  return Response.json(
    {
      error: {
        code: "NOT_FOUND",
        message: "not found",
        request_id: "billing-page-test",
      },
    },
    { status: 404 },
  );
}

function relativeURL(url: string) {
  return new URL(url, "http://localhost");
}

function billingRequests(requests: RecordedRequest[]) {
  return requests.filter(({ url }) => url.includes("/billing/"));
}

function storageValues() {
  return Array.from(
    { length: window.localStorage.length },
    (_, index) =>
      window.localStorage.getItem(window.localStorage.key(index) ?? ""),
  );
}

function fakeProvider(): EIP1193Provider {
  return {
    request: vi.fn(async (request: EIP1193RequestArguments) => {
      if (request.method === "eth_requestAccounts") return [address];
      if (request.method === "eth_accounts") return [address];
      if (request.method === "eth_chainId") return "0x1";
      if (request.method === "personal_sign") return signature;
      throw new Error("unexpected wallet method");
    }) as EIP1193Provider["request"],
    on: vi.fn(),
    removeListener: vi.fn(),
  };
}

function registerProvider(provider: EIP1193Provider) {
  const listener: EventListener = () => {
    window.dispatchEvent(
      new CustomEvent(EIP6963_ANNOUNCE_EVENT, {
        detail: {
          info: {
            uuid: "00000000-0000-4000-8000-000000000066",
            name: "Billing Wallet",
            icon: "data:image/png;base64,",
            rdns: "org.etherview.billing-test",
          },
          provider,
        },
      }),
    );
  };
  requestListeners.push(listener);
  window.addEventListener(EIP6963_REQUEST_EVENT, listener);
}

async function connectTestWallet(
  user: ReturnType<typeof userEvent.setup>,
) {
  await user.click(
    await screen.findByText("Connect wallet", { selector: "summary" }),
  );
  await user.click(
    await screen.findByRole("button", { name: /Billing Wallet/ }),
  );
  await waitFor(() => {
    expect(document.querySelector(".wallet-summary")).toHaveTextContent(
      "0x1111…1111",
    );
  });
  await user.click(
    screen.getByText("0x1111…1111", { selector: "summary" }),
  );
}

async function signInFromWalletMenu(
  user: ReturnType<typeof userEvent.setup>,
) {
  await user.click(
    screen.getByText("0x1111…1111", { selector: "summary" }),
  );
  const section = document.querySelector<HTMLElement>(
    ".wallet-auth-section",
  );
  expect(section).not.toBeNull();
  const button = section?.querySelector<HTMLButtonElement>("button");
  expect(button).not.toBeNull();
  await user.click(button as HTMLButtonElement);
}
