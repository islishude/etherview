import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import i18n from "@/i18n";
import { makeRouter } from "@/router";
import { ThemeProvider } from "@/theme/ThemeProvider";
import { WalletProvider } from "@/wallet/WalletProvider";

const canonicalHash = `0x${"11".repeat(32)}`;
const olderHash = `0x${"22".repeat(32)}`;
const orphanHash = `0x${"33".repeat(32)}`;
const parentHash = `0x${"00".repeat(32)}`;
const address = `0x${"44".repeat(20)}`;
const transactionHash = `0x${"aa".repeat(32)}`;

describe("core explorer pages", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("returns opaque block cursors unchanged and keeps coverage islands distinct", async () => {
    const opaqueCursor = "opaque +/?=:cursor";
    const requestedCursors: Array<string | null> = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === "/api/v1/status") {
        return statusResponse({
          core_ready: false,
          latest_block: "21",
          indexed_block: "12",
          highest_covered_block: "20",
          backfill_complete: false,
          lag: "9",
        }, { coverage_start: "10", coverage_end: "12" });
      }
      if (url.pathname === "/api/v1/blocks") {
        const cursor = url.searchParams.get("cursor");
        requestedCursors.push(cursor);
        if (cursor === opaqueCursor) {
          return envelope([block("11", olderHash)]);
        }
        return envelope([block("12", canonicalHash)], { next_cursor: opaqueCursor });
      }
      return notFound();
    }));

    renderExplorer("/blocks");

    expect(await screen.findByText("10 – 12")).toBeVisible();
    expect(screen.getByText(/separate live island/)).toBeVisible();
    expect(screen.getByRole("link", { name: "12" })).toHaveAttribute(
      "href",
      `/blocks/${canonicalHash}`,
    );

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Next page" }));
    expect(await screen.findByRole("link", { name: "11" })).toBeVisible();
    expect(screen.getByText("Page 2")).toBeVisible();
    expect(requestedCursors).toContain(opaqueCursor);

    await user.click(screen.getByRole("button", { name: "Previous page" }));
    expect(await screen.findByRole("link", { name: "12" })).toBeVisible();
    expect(screen.getByText("Page 1")).toBeVisible();
  });

  it("restarts an invalid search cursor and opens retained orphans by exact hash", async () => {
    const opaqueCursor = "search/snapshot?generation=7 + exact";
    const requestedCursors: Array<string | null> = [];
    let firstPageFetches = 0;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === "/api/v1/search") {
        const cursor = url.searchParams.get("cursor");
        requestedCursors.push(cursor);
        if (cursor === opaqueCursor) {
          return Response.json({
            error: {
              code: "invalid_cursor",
              message: "cursor is stale after canonical change",
              request_id: "core-pages-test",
            },
          }, { status: 400 });
        }
        firstPageFetches += 1;
        return envelope([{
          kind: "block",
          key: orphanHash,
          label: "Orphan block #12",
          rank: 100,
          canonical: false,
        }], { next_cursor: opaqueCursor });
      }
      if (url.pathname === `/api/v1/blocks/${orphanHash}`) {
        return envelope(block("12", orphanHash, false));
      }
      return notFound();
    }));

    renderExplorer("/search?q=orphan");
    const user = userEvent.setup();
    const result = await screen.findByRole("link", { name: /Orphan block #12/ });
    expect(firstPageFetches).toBe(1);
    expect(result).toHaveAttribute("href", `/blocks/${orphanHash}`);
    expect(screen.getByText("Orphan", { exact: true })).toBeVisible();

    await user.click(screen.getByRole("button", { name: "Next page" }));
    expect(await screen.findByText("This page cursor is no longer valid")).toBeVisible();
    expect(requestedCursors).toContain(opaqueCursor);
    await user.click(screen.getByRole("button", { name: "Restart from the first page" }));
    await waitFor(() => expect(firstPageFetches).toBe(2));
    expect(requestedCursors).toEqual([null, opaqueCursor, null]);

    await user.click(await screen.findByRole("link", { name: /Orphan block #12/ }));
    expect(await screen.findByRole("heading", { name: "Retained orphan block" })).toBeVisible();
    expect(screen.getAllByText(orphanHash).length).toBeGreaterThan(0);

    await user.click(screen.getByRole("button", { name: "切换到中文" }));
    expect(await screen.findByRole("heading", { name: "已保留孤块" })).toBeVisible();
    expect(screen.getByText("孤链", { exact: true })).toBeVisible();
  });

  it("renders exact-state capability loss instead of a fabricated zero address", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/addresses/${address}`) {
        return Response.json({
          error: {
            code: "capability_unavailable",
            message: "required capability is unavailable",
            details: {
              capability: "state",
              state: "unavailable",
              code: "exact_state_unavailable",
            },
            request_id: "core-pages-test",
          },
        }, { status: 503 });
      }
      return notFound();
    }));

    renderExplorer(`/address/${address}`);

    expect(await screen.findByText("Exact state capability is unavailable")).toBeVisible();
    expect(screen.getByText(/no empty result was inferred/)).toBeVisible();
    expect(screen.getByText("exact_state_unavailable")).toBeVisible();
    expect(screen.queryByRole("heading", { name: "Address summary" })).not.toBeInTheDocument();
    expect(screen.queryByText("0", { exact: true })).not.toBeInTheDocument();
  });

  it("localizes unavailable stages and account types while retaining a stable code", async () => {
    await i18n.changeLanguage("zh");
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/transactions/${transactionHash}`) {
        return envelope({
          hash: transactionHash,
          block_hash: canonicalHash,
          block_number: "12",
          transaction_index: 0,
          from: address,
          to: address,
          nonce: "1",
          value: "2",
          gas: "21000",
          gas_price: "1000000000",
          type: "2",
          input: "0x",
          status: "success",
          canonical: true,
          finality: "safe",
          completeness: completeness(),
        });
      }
      if (url.pathname === `/api/v1/transactions/${transactionHash}/trace`) {
        return Response.json({
          error: {
            code: "stage_unavailable",
            message: "required enrichment stage is unavailable",
            details: { stage: "trace", state: "failed", block_number: "12" },
            request_id: "core-pages-test",
          },
        }, { status: 503 });
      }
      if (url.pathname === `/api/v1/addresses/${address}`) {
        return envelope({
          address,
          type: "delegated_eoa",
          balance: "900719925474099312345",
          nonce: "1",
          code_hash: canonicalHash,
          at_block: canonicalHash,
          completeness: completeness(),
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);

    expect(await screen.findByText("调用追踪 数据不可用", { exact: true })).toBeVisible();
    expect(screen.getByText(/增强阶段报告为 失败/)).toBeVisible();
    expect(screen.getByText("stage_unavailable", { exact: true })).toBeVisible();

    const user = userEvent.setup();
    const [addressLink] = screen.getAllByRole("link", { name: address });
    if (!addressLink) throw new Error("transaction address link is missing");
    await user.click(addressLink);
    expect(await screen.findByRole("heading", { name: "地址摘要" })).toBeVisible();
    expect(screen.getByText("委托外部账户", { exact: true })).toBeVisible();
    expect(screen.queryByText("delegated_eoa", { exact: true })).not.toBeInTheDocument();
  });
});

function block(number: string, hash: string, canonical = true) {
  return {
    hash,
    number,
    parent_hash: parentHash,
    timestamp: "2026-01-01T00:00:00Z",
    miner: address,
    transaction_count: 1,
    gas_used: "21000",
    gas_limit: "30000000",
    base_fee_per_gas: "1000000000",
    canonical,
    finality: canonical ? "safe" : "orphan",
    completeness: completeness(),
  };
}

function statusResponse(overrides: Record<string, unknown>, meta: Record<string, unknown> = {}) {
  return envelope({
    chain_id: "1",
    core_ready: true,
    latest_block: "12",
    indexed_block: "12",
    highest_covered_block: "12",
    backfill_complete: true,
    safe_block: "12",
    finalized_block: "10",
    lag: "0",
    completeness: completeness(),
    ...overrides,
  }, meta);
}

function configResponse() {
  return envelope({
    chain_id: "1",
    chain_name: "Core Testnet",
    native_symbol: "ETH",
    native_name: "Ether",
    native_decimals: 18,
    features: {},
  });
}

function completeness() {
  return { core: "complete", trace: "unavailable", metadata: "pending", state: "complete" };
}

function envelope(data: unknown, meta: Record<string, unknown> = {}) {
  return Response.json({
    data,
    meta: { request_id: "core-pages-test", chain_id: "1", ...meta },
  });
}

function notFound() {
  return Response.json({
    error: { code: "not_found", message: "not found", request_id: "core-pages-test" },
  }, { status: 404 });
}

function requestURL(input: RequestInfo | URL) {
  return new URL(String(input), "http://etherview.test");
}

function renderExplorer(path: string) {
  const router = makeRouter(createMemoryHistory({ initialEntries: [path] }));
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } },
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
