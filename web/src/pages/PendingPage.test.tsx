import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import i18n from "@/i18n";
import { makeRouter } from "@/router";
import { ThemeProvider } from "@/theme/ThemeProvider";
import { WalletProvider } from "@/wallet/WalletProvider";

const meta = { request_id: "pending-web-test", chain_id: "1" };
const snapshotMeta = {
  ...meta,
  capability: "complete" as const,
  snapshot_id: "42",
  snapshot_at: "2026-07-20T10:00:00Z",
  expires_at: "2026-07-20T10:02:00Z",
  endpoint: "head-primary",
  transaction_count: "2",
};

describe("pending transaction route", () => {
  beforeEach(async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date("2026-07-20T10:00:30Z"));
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("renders an immutable snapshot and passes the opaque cursor on keyboard pagination", async () => {
    const firstHash = `0x${"aa".repeat(32)}`;
    const secondHash = `0x${"bb".repeat(32)}`;
    const from = `0x${"11".repeat(20)}`;
    const to = `0x${"22".repeat(20)}`;
    const opaqueCursor = "snapshot:42+next/page?2";
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === "/api/v1/config") return configResponse(true);
      if (path === "/api/v1/pending?limit=25") {
        return Response.json({
          data: [pendingTransaction(firstHash, from, to, "7")],
          meta: { ...snapshotMeta, next_cursor: opaqueCursor },
        });
      }
      if (path === "/api/v1/pending?limit=25&cursor=snapshot%3A42%2Bnext%2Fpage%3F2") {
        return Response.json({
          data: [pendingTransaction(secondHash, from, undefined, "8")],
          meta: snapshotMeta,
        });
      }
      return Response.json(
        { error: { code: "NOT_FOUND", message: `unexpected request: ${path}` } },
        { status: 404 },
      );
    });
    vi.stubGlobal("fetch", fetcher);

    renderPendingRoute();

    expect(await screen.findByRole("heading", { name: "Immutable node snapshot" })).toBeVisible();
    expect(screen.getByText("head-primary")).toBeVisible();
    expect(screen.getByRole("columnheader", { name: "Fees (wei per gas)" })).toBeVisible();
    expect(screen.getByRole("columnheader", { name: "First seen" })).toBeVisible();
    expect(screen.getByRole("columnheader", { name: "Last seen" })).toBeVisible();
    expect(screen.getByText("0xaaaaaa…aaaaaa")).toBeVisible();
    expect(document.querySelector('time[datetime="2026-07-20T10:00:00Z"]')).not.toBeNull();
    expect(document.querySelector('time[datetime="2026-07-20T10:02:00Z"]')).not.toBeNull();

    const user = userEvent.setup();
    const nextPage = screen.getByRole("button", { name: "Next page" });
    nextPage.focus();
    await user.keyboard("{Enter}");

    expect(await screen.findByText("0xbbbbbb…bbbbbb")).toBeVisible();
    expect(screen.getByText("Contract creation")).toBeVisible();
    expect(screen.getByText("Page 2")).toBeVisible();
    expect(fetcher).toHaveBeenCalledWith(
      "/api/v1/pending?limit=25&cursor=snapshot%3A42%2Bnext%2Fpage%3F2",
      expect.objectContaining({ method: "GET" }),
    );
  });

  it("keeps a successful empty snapshot distinct from unavailability", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input);
        if (path === "/api/v1/config") return configResponse(true);
        if (path === "/api/v1/pending?limit=25") {
          return Response.json({
            data: [],
            meta: { ...snapshotMeta, transaction_count: "0" },
          });
        }
        return Response.json({ error: { code: "NOT_FOUND", message: "not found" } }, { status: 404 });
      }),
    );

    renderPendingRoute();

    expect(
      await screen.findByText("The latest successful snapshot contains no pending transactions."),
    ).toBeVisible();
    expect(screen.getByRole("heading", { name: "Immutable node snapshot" })).toBeVisible();
    expect(screen.queryByRole("heading", { name: "Pending transaction snapshot is unavailable" })).toBeNull();
    expect(screen.getByRole("button", { name: "Next page" })).toBeDisabled();
  });

  it("does not call the pending endpoint when the feature is disabled and localizes the state", async () => {
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === "/api/v1/config") return configResponse(false);
      return Response.json({ error: { code: "NOT_FOUND", message: "not found" } }, { status: 404 });
    });
    vi.stubGlobal("fetch", fetcher);

    renderPendingRoute();

    expect(
      await screen.findByRole("heading", { name: "Pending transaction indexing is disabled" }),
    ).toBeVisible();
    expect(screen.getByText("feature_disabled")).toBeVisible();
    expect(fetcher.mock.calls.some(([input]) => String(input).startsWith("/api/v1/pending"))).toBe(false);

    await userEvent.setup().click(screen.getByRole("button", { name: "切换到中文" }));
    expect(await screen.findByRole("heading", { name: "待处理交易索引已关闭" })).toBeVisible();
  });

  it("renders the typed 503 state and reason instead of an empty result", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input);
        if (path === "/api/v1/config") return configResponse(true);
        if (path === "/api/v1/pending?limit=25") {
          return Response.json(
            {
              error: {
                code: "mempool_unavailable",
                message: "pending snapshot unavailable",
                details: {
                  state: "failed",
                  reason: "rpc_request_failed",
                  last_attempt_at: "2026-07-20T09:59:00Z",
                },
                request_id: "pending-error-test",
              },
            },
            { status: 503 },
          );
        }
        return Response.json({ error: { code: "NOT_FOUND", message: "not found" } }, { status: 404 });
      }),
    );

    renderPendingRoute();

    expect(
      await screen.findByRole("heading", { name: "Pending transaction snapshot is unavailable" }),
    ).toBeVisible();
    expect(screen.getByText("failed")).toBeVisible();
    expect(screen.getByText("rpc_request_failed")).toBeVisible();
    expect(document.querySelector('time[datetime="2026-07-20T09:59:00Z"]')).not.toBeNull();
    expect(
      screen.queryByText("The latest successful snapshot contains no pending transactions."),
    ).toBeNull();
  });

  it("recovers an invalid opaque page cursor with a fresh first-page request", async () => {
    const hash = `0x${"cc".repeat(32)}`;
    const from = `0x${"44".repeat(20)}`;
    const cursor = "expired+snapshot/next?page=2";
    let firstPageRequests = 0;
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === "/api/v1/config") return configResponse(true);
      if (path === "/api/v1/pending?limit=25") {
        firstPageRequests += 1;
        return Response.json({
          data: [pendingTransaction(hash, from, undefined, "9")],
          meta: { ...snapshotMeta, next_cursor: cursor },
        });
      }
      if (path === "/api/v1/pending?limit=25&cursor=expired%2Bsnapshot%2Fnext%3Fpage%3D2") {
        return Response.json({
          error: {
            code: "invalid_cursor",
            message: "cursor expired",
            request_id: "pending-cursor-test",
          },
        }, { status: 400 });
      }
      return Response.json({ error: { code: "NOT_FOUND", message: "not found" } }, { status: 404 });
    });
    vi.stubGlobal("fetch", fetcher);
    renderPendingRoute();

    const user = userEvent.setup();
    await screen.findByText("0xcccccc…cccccc");
    await user.click(screen.getByRole("button", { name: "Next page" }));
    expect(await screen.findByText("This page cursor is no longer valid")).toBeVisible();

    await user.click(screen.getByRole("button", { name: "Restart from the first page" }));
    expect(await screen.findByText("0xcccccc…cccccc")).toBeVisible();
    expect(firstPageRequests).toBe(2);
  });

  it("expires an authoritative empty snapshot and suppresses it while refreshing", async () => {
    vi.useRealTimers();
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-20T10:00:00Z"));
    let requests = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input);
        if (path === "/api/v1/config") return configResponse(true);
        if (path === "/api/v1/pending?limit=25") {
          requests += 1;
          if (requests === 1) {
            return Response.json({
              data: [],
              meta: {
                ...snapshotMeta,
                snapshot_at: "2026-07-20T10:00:00Z",
                expires_at: "2026-07-20T10:00:02Z",
                transaction_count: "0",
              },
            });
          }
          return Response.json({
            error: {
              code: "mempool_unavailable",
              message: "snapshot unavailable",
              details: { state: "unavailable", reason: "snapshot_expired" },
              request_id: "pending-expiry-test",
            },
          }, { status: 503 });
        }
        return Response.json({ error: { code: "NOT_FOUND", message: "not found" } }, { status: 404 });
      }),
    );
    renderPendingRoute();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(
      screen.getByText("The latest successful snapshot contains no pending transactions."),
    ).toBeVisible();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2_000);
      await Promise.resolve();
    });
    expect(
      screen.queryByText("The latest successful snapshot contains no pending transactions."),
    ).toBeNull();
    expect(screen.getByRole("heading", { name: "Pending transaction snapshot is unavailable" })).toBeVisible();
    expect(screen.getByText("snapshot_expired")).toBeVisible();
  });
});

function configResponse(mempool: boolean): Response {
  return Response.json({
    data: {
      chain_id: "1",
      chain_name: "Testnet",
      native_symbol: "ETH",
      native_name: "Ether",
      native_decimals: 18,
      features: { mempool },
    },
    meta,
  });
}

function pendingTransaction(hash: string, from: string, to: string | undefined, nonce: string) {
  return {
    hash,
    from,
    ...(to ? { to } : {}),
    nonce,
    value: "1000000000000000000",
    gas: "21000",
    gas_price: "30000000000",
    max_fee_per_gas: "40000000000",
    max_priority_fee_per_gas: "2000000000",
    input: "0x",
    endpoint: "head-primary",
    first_seen_at: "2026-07-20T09:59:30Z",
    last_seen_at: "2026-07-20T09:59:50Z",
    expires_at: "2026-07-20T10:02:00Z",
  };
}

function renderPendingRoute() {
  const router = makeRouter(createMemoryHistory({ initialEntries: ["/pending"] }));
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <WalletProvider>
          <RouterProvider router={router} />
        </WalletProvider>
      </ThemeProvider>
    </QueryClientProvider>,
  );
}
