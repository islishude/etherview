import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import i18n from "@/i18n";
import { shorten } from "@/components/format";
import { makeRouter } from "@/router";
import { AuthProvider } from "@/auth/AuthProvider";
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

  it("copies transaction addresses from the detail page", async () => {
    const txFrom = `0x${"55".repeat(20)}`;
    const txTo = `0x${"66".repeat(20)}`;
    const writeText = vi.fn().mockResolvedValue(undefined);

    const originalClipboard = (navigator as Navigator & { clipboard?: { writeText: typeof writeText } }).clipboard;
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText },
      configurable: true,
      writable: true,
    });

    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = requestURL(input).pathname;
      const meta = { request_id: "tx-copy-web-test", chain_id: "1" };
      if (path === "/api/v1/config") return configResponse();
      if (path === `/api/v1/transactions/${transactionHash}`) {
        return Response.json({
          data: {
            hash: transactionHash,
            block_hash: canonicalHash,
            block_number: "12",
            transaction_index: 0,
            from: txFrom,
            to: txTo,
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
          },
          meta,
        });
      }
      if (path === `/api/v1/transactions/${transactionHash}/trace`) {
        return Response.json({
          data: {
            state: "complete",
            frames: [],
          },
          meta,
        });
      }
      return notFound();
    }));

    try {
      const user = userEvent.setup();
      renderExplorer(`/tx/${transactionHash}`);

      const fromLink = await screen.findByRole("link", { name: txFrom });
      const toLink = await screen.findByRole("link", { name: txTo });
    const fromContainer = fromLink.closest(".copyable-field") as HTMLElement | null;
    const toContainer = toLink.closest(".copyable-field") as HTMLElement | null;
      if (!fromContainer || !toContainer) throw new Error("copyable transaction field missing");

      const fromCopy = within(fromContainer).getByRole("button", { name: "Copy" });
      const toCopy = within(toContainer).getByRole("button", { name: "Copy" });

    await user.click(fromCopy);
    await user.click(toCopy);

    expect(fromCopy).toHaveTextContent("✓");
    expect(toCopy).toHaveTextContent("✓");

      fromCopy.focus();
      expect(fromCopy).toHaveFocus();
    } finally {
      Object.defineProperty(navigator, "clipboard", {
        value: originalClipboard,
        configurable: true,
      });
    }
  });

  it("renders transaction type 2 as a semantic label", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      const meta = { request_id: "tx-copy-web-test", chain_id: "1" };
      if (path === "/api/v1/config") return configResponse();
      if (path === `/api/v1/transactions/${transactionHash}`) {
        return Response.json({
          data: {
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
          },
          meta,
        });
      }
      if (path === `/api/v1/transactions/${transactionHash}/trace`) {
        return Response.json({
          data: {
            state: "complete",
            frames: [],
          },
          meta,
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);

    const typeLabel = await screen.findByText("Type");
    const typeItem = typeLabel.closest("div");
    if (!typeItem) throw new Error("transaction type field missing");
    expect(screen.getByText("EIP-1559")).toBeVisible();
    expect(within(typeItem).queryByText("2", { exact: true })).not.toBeInTheDocument();
  });

  it("renders hex tx type 0x2 as a semantic label", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      const meta = { request_id: "tx-copy-web-test", chain_id: "1" };
      if (path === "/api/v1/config") return configResponse();
      if (path === `/api/v1/transactions/${transactionHash}`) {
        return Response.json({
          data: {
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
            type: "0x2",
            input: "0x",
            status: "success",
            canonical: true,
            finality: "safe",
            completeness: completeness(),
          },
          meta,
        });
      }
      if (path === `/api/v1/transactions/${transactionHash}/trace`) {
        return Response.json({
          data: {
            state: "complete",
            frames: [],
          },
          meta,
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);

    const typeLabel = await screen.findByText("Type");
    const typeItem = typeLabel.closest("div");
    if (!typeItem) throw new Error("transaction type field missing");
    expect(screen.getByText("EIP-1559")).toBeVisible();
    expect(within(typeItem).queryByText("0x2", { exact: true })).not.toBeInTheDocument();
  });

  it("falls back to raw value for unknown tx type", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      const meta = { request_id: "tx-copy-web-test", chain_id: "1" };
      if (path === "/api/v1/config") return configResponse();
      if (path === `/api/v1/transactions/${transactionHash}`) {
        return Response.json({
          data: {
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
            type: "9",
            input: "0x",
            status: "success",
            canonical: true,
            finality: "safe",
            completeness: completeness(),
          },
          meta,
        });
      }
      if (path === `/api/v1/transactions/${transactionHash}/trace`) {
        return Response.json({
          data: {
            state: "complete",
            frames: [],
          },
          meta,
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);

    const typeLabel = await screen.findByText("Type");
    const typeItem = typeLabel.closest("div");
    if (!typeItem) throw new Error("transaction type field missing");
    expect(within(typeItem).getByText("9", { exact: true })).toBeVisible();
    expect(within(typeItem).queryByText("EIP-1559")).not.toBeInTheDocument();
  });

  it("renders ETH-formatted values on transaction detail and trace", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = requestURL(input).pathname;
      const meta = { request_id: "tx-copy-web-test", chain_id: "1" };
      if (path === "/api/v1/config") return configResponse();
      if (path === `/api/v1/transactions/${transactionHash}`) {
        return Response.json({
          data: {
            hash: transactionHash,
            block_hash: canonicalHash,
            block_number: "12",
            transaction_index: 0,
            from: address,
            to: address,
              nonce: "1",
              value: "2100000000000000000",
              gas: "21000",
              effective_gas_price: "2000000000",
              tx_fee_wei: "42000000000000",
              burned_wei: "21000000000000",
              gas_price: "1000000000",
              type: "2",
              input: "0x",
              status: "success",
              canonical: true,
              finality: "safe",
            completeness: completeness(),
          },
          meta,
        });
      }
      if (path === `/api/v1/transactions/${transactionHash}/trace`) {
        return Response.json({
          data: {
            state: "complete",
            frames: [{
              call_type: "call",
              type: "call",
              value: "1000000000000000000",
              path: ["root"],
              from: address,
              to: address,
            }],
          },
          meta,
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);

    const detailSection = (await screen.findByText("Transaction summary"))
      .closest("section");
    if (!detailSection) throw new Error("transaction summary section is missing");
    const detailValueLabel = await within(detailSection).findByText("Value (ETH)");
    const detailValueRow = detailValueLabel.closest(".detail-item") as HTMLElement | null;
    if (!detailValueRow) throw new Error("transaction value detail row missing");
    expect(within(detailValueRow).getByText("2.1")).toBeVisible();

    const effectiveGasPriceLabel = await screen.findByText("Effective gas price (gwei)");
    const effectiveGasPriceRow = effectiveGasPriceLabel.closest(".detail-item") as HTMLElement | null;
    if (!effectiveGasPriceRow) throw new Error("effective gas price detail row missing");
    expect(within(effectiveGasPriceRow).getByText("2")).toBeVisible();

    const transactionFeeLabel = await screen.findByText("Transaction fee (ETH)");
    const transactionFeeRow = transactionFeeLabel.closest(".detail-item") as HTMLElement | null;
    if (!transactionFeeRow) throw new Error("transaction fee detail row missing");
    expect(within(transactionFeeRow).getByText("0.000042")).toBeVisible();

    const burnedLabel = await screen.findByText("Burned (ETH)");
    const burnedRow = burnedLabel.closest(".detail-item") as HTMLElement | null;
    if (!burnedRow) throw new Error("burned detail row missing");
    expect(within(burnedRow).getByText("0.000021")).toBeVisible();

    expect(screen.queryByText("Gas price")).not.toBeInTheDocument();

    const traceRow = await screen.findByRole("row", { name: /root/ });
    expect(within(traceRow).getByText("1")).toBeVisible();
  });

  it("renders transaction confirmations and block timestamp in /tx detail", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = requestURL(input).pathname;
      const meta = { request_id: "tx-copy-web-test", chain_id: "1" };
      if (path === "/api/v1/config") return configResponse();
      if (path === `/api/v1/transactions/${transactionHash}`) {
        return Response.json({
          data: {
            hash: transactionHash,
            block_hash: canonicalHash,
            block_number: "12",
            block_timestamp: "1970-01-01T00:01:40Z",
            confirmations: "11",
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
          },
          meta,
        });
      }
      if (path === `/api/v1/transactions/${transactionHash}/trace`) {
        return Response.json({
          data: {
            state: "complete",
            frames: [],
          },
          meta,
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);

    const blockLabel = await screen.findByText("Block");
    const blockRow = blockLabel.closest(".detail-item") as HTMLElement | null;
    if (!blockRow) throw new Error("block detail row missing");
    expect(within(blockRow).getByRole("link", { name: "12" })).toHaveAttribute(
      "href",
      `/blocks/${canonicalHash}`,
    );

    expect(screen.queryByText("Block hash")).not.toBeInTheDocument();

    const confirmationsLabel = await screen.findByText("Confirmations");
    const confirmationsRow = confirmationsLabel.closest(".detail-item") as HTMLElement | null;
    if (!confirmationsRow) throw new Error("confirmations detail row missing");
    expect(within(confirmationsRow).getByText("11")).toBeVisible();

    const blockTimestampLabel = await screen.findByText("Block timestamp");
    const blockTimestampRow = blockTimestampLabel.closest(".detail-item") as HTMLElement | null;
    if (!blockTimestampRow) throw new Error("block timestamp detail row missing");
    expect(within(blockTimestampRow).getByText("Jan 1, 1970", { exact: false })).toBeVisible();
  });

  it("renders ETH-formatted native balance on address page", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = requestURL(input).pathname;
      if (path === "/api/v1/config") return configResponse();
      if (path === `/api/v1/addresses/${address}`) {
        return envelope({
          address,
          type: "eoa",
          balance: "1000000000000000000",
          nonce: "0",
          at_block: canonicalHash,
          completeness: completeness(),
          code_hash: canonicalHash,
        });
      }
      if (path === `/api/v1/addresses/${address}/nfts`) {
        return envelope([]);
      }
      return notFound();
    }));

    renderExplorer(`/address/${address}`);

    const detailNativeBalance = await screen.findByText("Native balance (ETH)");
    const detailNativeBalanceRow = detailNativeBalance.closest(".detail-item") as HTMLElement | null;
    if (!detailNativeBalanceRow) throw new Error("address native balance row missing");
    expect(within(detailNativeBalanceRow).getByText("1")).toBeVisible();
  });

  it("renders ETH-formatted values on transactions list", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = requestURL(input).pathname;
      if (path === "/api/v1/config") return configResponse();
      if (path === "/api/v1/status") {
        return statusResponse({
          core_ready: true,
          latest_block: "12",
          indexed_block: "12",
          highest_covered_block: "12",
          backfill_complete: true,
          lag: "0",
        });
      }
      if (path === "/api/v1/transactions") {
        return envelope([{
          hash: transactionHash,
          status: "success",
          block_hash: canonicalHash,
          block_number: "12",
          from: address,
          to: address,
          transaction_index: 0,
          nonce: "0",
          value: "1500000000000000000",
          gas: "21000",
          gas_price: "1000000000",
          completeness: completeness(),
          finality: "safe",
          canonical: true,
        }]);
      }
      if (path === `/api/v1/addresses/${address}/nfts`) {
        return notFound();
      }
      return notFound();
    }));

    renderExplorer("/transactions");

    const txRowHash = await screen.findByText(shorten(transactionHash));
    const txRow = txRowHash.closest("tr");
    if (!txRow) throw new Error("transactions row missing");
    expect(within(txRow).getByText("1.5")).toBeVisible();
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
          <AuthProvider>
            <RouterProvider router={router} />
          </AuthProvider>
        </WalletProvider>
      </ThemeProvider>
    </QueryClientProvider>,
  );
}
