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

    renderExplorer(`/tx/${transactionHash}?tab=trace`);

    expect(await screen.findByText("调用追踪 数据不可用", { exact: true })).toBeVisible();
    expect(screen.getByText(/增强阶段报告为 失败/)).toBeVisible();
    expect(screen.getByText("stage_unavailable", { exact: true })).toBeVisible();

    const user = userEvent.setup();
    await user.click(screen.getByRole("tab", { name: "概览" }));
    const [addressLink] = screen.getAllByRole("link", { name: address });
    if (!addressLink) throw new Error("transaction address link is missing");
    await user.click(addressLink);
    expect(await screen.findByRole("heading", { name: "地址摘要" })).toBeVisible();
    expect(screen.queryByText("委托外部账户", { exact: true })).not.toBeInTheDocument();
    expect(screen.queryByText("delegated_eoa", { exact: true })).not.toBeInTheDocument();
  });

  it("deep-links transaction tabs and lazily loads only the selected subresource", async () => {
    const requested: string[] = [];
    let logFetches = 0;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      requested.push(url.pathname);
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
          value: "0",
          gas: "21000",
          input: "0x",
          status: "success",
          canonical: true,
          finality: "safe",
          completeness: completeness(),
        });
      }
      if (url.pathname === `/api/v1/transactions/${transactionHash}/logs`) {
        logFetches++;
        return envelope({
          state: "complete",
          chain_id: "1",
          block_number: "12",
          block_hash: logFetches === 1 ? olderHash : canonicalHash,
          transaction_hash: transactionHash,
          canonical: true,
          finality: "safe",
          items: [],
        });
      }
      if (url.pathname === `/api/v1/transactions/${transactionHash}/trace`) {
        return envelope({
          state: "complete",
          chain_id: "1",
          block_number: "12",
          block_hash: canonicalHash,
          transaction_hash: transactionHash,
          canonical: true,
          finality: "safe",
          frames: [],
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}?tab=logs`);

    expect(await screen.findByText("This transaction emitted no logs.")).toBeVisible();
    expect(logFetches).toBe(2);
    expect(requested).toContain(`/api/v1/transactions/${transactionHash}/logs`);
    expect(requested).not.toContain(`/api/v1/transactions/${transactionHash}/token-transfers`);
    expect(requested).not.toContain(`/api/v1/transactions/${transactionHash}/trace`);
    expect(requested).not.toContain(`/api/v1/transactions/${transactionHash}/state-changes`);

    const logsTab = screen.getByRole("tab", { name: "Logs" });
    logsTab.focus();
    await userEvent.setup().keyboard("{ArrowRight}");

    expect(await screen.findByText("The trace completed without call frames.")).toBeVisible();
    expect(requested).toContain(`/api/v1/transactions/${transactionHash}/trace`);
    expect(screen.getByRole("tab", { name: "Trace" })).toHaveFocus();
  });

  it("renders decoded log arguments first and keeps raw topics and data disclosed", async () => {
    const eventTopic = `0x${"77".repeat(32)}`;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/transactions/${transactionHash}`) {
        return envelope({
          hash: transactionHash, block_hash: canonicalHash, block_number: "12",
          transaction_index: 0, from: address, to: address, nonce: "1", value: "0",
          gas: "21000", input: "0x", status: "success", canonical: true,
          finality: "safe", completeness: completeness(),
        });
      }
      if (url.pathname === `/api/v1/transactions/${transactionHash}/logs`) {
        return envelope({
          state: "complete", chain_id: "1", block_number: "12",
          block_hash: canonicalHash, transaction_hash: transactionHash,
          canonical: true, finality: "safe",
          items: [{
            address, log_index: "0", topics: [eventTopic], data: "0x1234",
            decoding: {
              status: "decoded", event_name: "Changed", signature: "Changed(string)",
              confidence: "verified", abi_source: { kind: "exact_address", address, code_hash: canonicalHash },
              arguments: [{ name: "message", type: "string", indexed: true, hashed: true, value: eventTopic }],
              candidates: ["Changed(string)"],
            },
          }],
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}?tab=logs`);

    expect(await screen.findByText("Changed(string)")).toBeVisible();
    expect(screen.getByText("Decoded")).toBeVisible();
    expect(screen.getByText("indexed value hash")).toBeVisible();
    const raw = screen.getByText("Raw topics and data");
    expect(raw).toBeVisible();
    await userEvent.setup().click(raw);
    expect(screen.getByText("0x1234")).toBeVisible();
    expect(screen.getAllByText(eventTopic)).toHaveLength(2);
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
      const detailSection = screen.getByText("Transaction summary").closest("section");
      if (!detailSection) throw new Error("transaction summary section is missing");
      await user.click(within(detailSection).getByText("More details"));
      const copyButtons = within(detailSection).getAllByRole("button", { name: "Copy" });
      expect(copyButtons).toHaveLength(4);
      for (const button of copyButtons) {
        expect(button).toBeVisible();
        expect(getComputedStyle(button).pointerEvents).not.toBe("none");
      }

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

  it("renders a receipt-backed contract address and creation label", async () => {
    const contractAddress = "0x52908400098527886E0F7030069857D2E4169EE7";
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = requestURL(input).pathname;
      if (path === "/api/v1/config") return configResponse();
      if (path === `/api/v1/transactions/${transactionHash}`) {
        return envelope({
          hash: transactionHash,
          block_hash: canonicalHash,
          block_number: "12",
          transaction_index: 0,
          from: address,
          to: null,
          contract_address: contractAddress,
          nonce: "1",
          value: "0",
          gas: "21000",
          input: "0x6000",
          status: "success",
          canonical: true,
          finality: "safe",
          completeness: completeness(),
        });
      }
      return notFound();
    }));

    const user = userEvent.setup();
    renderExplorer(`/tx/${transactionHash}`);

    const toLabel = await screen.findByText("To", { exact: true });
    const toRow = toLabel.closest(".transaction-detail-row") as HTMLElement | null;
    if (!toRow) throw new Error("transaction recipient row is missing");
    const contractLink = within(toRow).getByRole("link", { name: contractAddress });
    expect(contractLink).toHaveAttribute("href", `/address/${contractAddress}`);
    expect(within(toRow).getByText("Contract creation")).toBeVisible();
    expect(within(toRow).getByRole("button", { name: "Copy" })).toBeVisible();

    await user.click(screen.getByText("More details"));
    expect(screen.queryByRole("heading", { name: "Data completeness" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "切换到中文" }));
    expect(within(toRow).getByText("创建合约")).toBeVisible();
  });

  it("does not fabricate an address for a failed contract creation", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = requestURL(input).pathname;
      if (path === "/api/v1/config") return configResponse();
      if (path === `/api/v1/transactions/${transactionHash}`) {
        return envelope({
          hash: transactionHash,
          block_hash: canonicalHash,
          block_number: "12",
          transaction_index: 0,
          from: address,
          to: null,
          nonce: "1",
          value: "0",
          gas: "21000",
          input: "0x6000",
          status: "failed",
          canonical: true,
          finality: "safe",
          completeness: completeness(),
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);

    const toLabel = await screen.findByText("To", { exact: true });
    const toRow = toLabel.closest(".transaction-detail-row") as HTMLElement | null;
    if (!toRow) throw new Error("transaction recipient row is missing");
    expect(within(toRow).getByText("Contract creation")).toBeVisible();
    expect(within(toRow).queryByRole("link")).not.toBeInTheDocument();
    expect(within(toRow).queryByRole("button", { name: "Copy" })).not.toBeInTheDocument();
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

    await userEvent.setup().click(await screen.findByText("More details"));
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

    await userEvent.setup().click(await screen.findByText("More details"));
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

    await userEvent.setup().click(await screen.findByText("More details"));
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
    expect(within(detailValueRow).getByText("2.1 ETH")).toBeVisible();

    await userEvent.setup().click(screen.getByText("More details"));
    const effectiveGasPriceLabel = await screen.findByText("Effective gas price (gwei)");
    const effectiveGasPriceRow = effectiveGasPriceLabel.closest(".detail-item") as HTMLElement | null;
    if (!effectiveGasPriceRow) throw new Error("effective gas price detail row missing");
    expect(within(effectiveGasPriceRow).getByText("2")).toBeVisible();

    const transactionFeeLabel = await screen.findByText("Transaction fee");
    const transactionFeeRow = transactionFeeLabel.closest(".detail-item") as HTMLElement | null;
    if (!transactionFeeRow) throw new Error("transaction fee detail row missing");
    expect(within(transactionFeeRow).getByText("0.000042 ETH")).toBeVisible();

    const burnedLabel = await screen.findByText("Burned");
    const burnedRow = burnedLabel.closest(".detail-item") as HTMLElement | null;
    if (!burnedRow) throw new Error("burned detail row missing");
    expect(within(burnedRow).getByText("0.000021 ETH")).toBeVisible();

    expect(screen.queryByText("Gas price")).not.toBeInTheDocument();

    await userEvent.setup().click(screen.getByRole("tab", { name: "Trace" }));
    expect(await screen.findByText("1 ETH")).toBeVisible();
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

    expect(within(blockRow).getByText("11 confirmations")).toBeVisible();

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
    expect(within(detailNativeBalanceRow).getByText("1 ETH")).toBeVisible();
  });

  it("loads address activity tabs on demand and exposes contracts without summary hashes", async () => {
    const requestedPaths: string[] = [];
    const createdAddress = `0x${"55".repeat(20)}`;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      requestedPaths.push(url.pathname);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/addresses/${address}`) {
        return envelope({
          address,
          name: "Activity contract",
          type: "contract",
          balance: "1000000000000000000",
          nonce: "7",
          at_block: canonicalHash,
          completeness: completeness(),
          code_hash: olderHash,
          origin: {
            kind: "contract_creation",
            state: "found",
            source_address: createdAddress,
            transaction_hash: transactionHash,
          },
        });
      }
      if (url.pathname === `/api/v1/addresses/${address}/transactions`) {
        return envelope([{
          hash: transactionHash,
          status: "success",
          block_hash: canonicalHash,
          block_number: "12",
          block_timestamp: "2026-07-28T08:00:00Z",
          from: address,
          to: address,
          transaction_index: 0,
          nonce: "7",
          value: "1000000000000000000",
          gas: "21000",
          input: "0x",
          completeness: completeness(),
          finality: "safe",
          canonical: true,
        }]);
      }
      if (url.pathname === `/api/v1/addresses/${address}/internal-transactions`) {
        return envelope([{
          block_hash: canonicalHash,
          block_number: "12",
          block_timestamp: "2026-07-28T08:00:00Z",
          transaction_hash: transactionHash,
          transaction_index: "0",
          path: [0],
          depth: 1,
          call_type: "create",
          from: address,
          created_address: createdAddress,
          value: "2",
          reverted: false,
        }]);
      }
      if (url.pathname === `/api/v1/addresses/${address}/nfts`) {
        return envelope([]);
      }
      if (url.pathname === `/api/v1/addresses/${address}/erc20-balances`) {
        return envelope([{
          chain_id: "1",
          owner: address,
          token_address: createdAddress,
          balance: "1234500",
          confidence: "rpc_exact",
          name: "Asset Token",
          symbol: "AST",
          decimals: 4,
        }]);
      }
      return notFound();
    }));

    renderExplorer(`/address/${address}`);

    expect(await screen.findByRole("link", { name: "Contract" })).toHaveAttribute(
      "href",
      `/address/${address}#code`,
    );
    expect(screen.getByRole("heading", { level: 1, name: "Contract" })).toBeVisible();
    const summary = screen.getByRole("heading", { name: "Address summary" }).closest("section");
    if (!summary) throw new Error("address summary is missing");
    expect(within(summary).queryByText("Type")).not.toBeInTheDocument();
    expect(within(summary).queryByText(address)).not.toBeInTheDocument();
    expect(within(summary).getByRole("link", { name: createdAddress })).toHaveAttribute(
      "href",
      `/address/${createdAddress}?tab=transactions`,
    );
    expect(within(summary).getByRole("link", { name: transactionHash })).toHaveAttribute(
      "href",
      `/tx/${transactionHash}?tab=overview`,
    );
    await userEvent.setup().click(screen.getByRole("button", { name: "Show QR code" }));
    const dialog = screen.getByRole("dialog", { name: "Address QR code" });
    expect(dialog).toHaveFocus();
    expect(within(dialog).getByText(`ethereum:${address}@1`)).toBeVisible();
    await userEvent.setup().keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.queryByText("Data completeness")).not.toBeInTheDocument();
    expect(screen.queryByText("Code hash")).not.toBeInTheDocument();
    expect(screen.queryByText("State block hash")).not.toBeInTheDocument();
    const selfDirection = await screen.findByText("SELF");
    expect(selfDirection).toBeVisible();
    const transactionRow = selfDirection.closest("tr");
    if (!transactionRow) throw new Error("address transaction row is missing");
    expect(
      within(transactionRow).queryByRole("link", { name: shorten(address) }),
    ).not.toBeInTheDocument();
    expect(
      within(transactionRow).getAllByRole("button", { name: "Copy" }),
    ).toHaveLength(2);
    expect(requestedPaths).toContain(`/api/v1/addresses/${address}/transactions`);
    expect(requestedPaths).not.toContain(`/api/v1/addresses/${address}/internal-transactions`);
    expect(requestedPaths).not.toContain(`/api/v1/addresses/${address}/nfts`);
    expect(requestedPaths).not.toContain(`/api/v1/addresses/${address}/erc20-balances`);

    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "Internal Transactions" }));
    const createdLabel = await screen.findByText("Created address");
    expect(createdLabel).toBeVisible();
    const internalRow = createdLabel.closest("tr");
    if (!internalRow) throw new Error("address internal transaction row is missing");
    expect(
      within(internalRow).queryByRole("link", { name: shorten(address) }),
    ).not.toBeInTheDocument();
    expect(
      within(internalRow).getByRole("link", { name: shorten(createdAddress) }),
    ).toHaveAttribute("href", `/address/${createdAddress}?tab=transactions`);
    expect(
      within(internalRow).getAllByRole("button", { name: "Copy" }),
    ).toHaveLength(2);
    expect(within(internalRow).getByText("OUT")).toBeVisible();
    expect(requestedPaths).toContain(`/api/v1/addresses/${address}/internal-transactions`);
    expect(requestedPaths).not.toContain(`/api/v1/addresses/${address}/nfts`);

    await user.click(screen.getByRole("link", { name: "Assets" }));
    expect(await screen.findByText(/No positive NFT balances were observed/)).toBeVisible();
    expect(await screen.findByText("Asset Token")).toBeVisible();
    expect(screen.getByText("123.45 AST")).toBeVisible();
    expect(requestedPaths).toContain(`/api/v1/addresses/${address}/nfts`);
    expect(requestedPaths).toContain(`/api/v1/addresses/${address}/erc20-balances`);
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

  it("clears a contract hash for an EOA and returns to transactions", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/addresses/${address}`) {
        return envelope({
          address,
          type: "eoa",
          balance: "0",
          nonce: "0",
          at_block: canonicalHash,
          completeness: completeness(),
        });
      }
      if (url.pathname === `/api/v1/addresses/${address}/transactions`) return envelope([]);
      return notFound();
    }));

    renderExplorer(`/address/${address}#code`);

    const addressTabs = await screen.findByRole("navigation", { name: "Address activity sections" });
    const transactions = within(addressTabs).getByRole("link", { name: "Transactions" });
    await waitFor(() => expect(transactions).toHaveAttribute("aria-current", "page"));
    expect(screen.queryByRole("link", { name: "Contract" })).not.toBeInTheDocument();
    expect(await screen.findByText("No matching address activity is available in this snapshot.")).toBeVisible();
  });

  it("refreshes the first transaction page when a newly indexed transaction becomes visible", async () => {
    let transactionRequests = 0;
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
        transactionRequests += 1;
        if (transactionRequests === 1) return envelope([]);
        return envelope([{
          hash: transactionHash,
          status: "success",
          block_hash: canonicalHash,
          block_number: "12",
          from: address,
          to: address,
          transaction_index: 0,
          nonce: "0",
          value: "1",
          gas: "21000",
          gas_price: "1",
          completeness: completeness(),
          finality: "latest",
          canonical: true,
        }]);
      }
      return notFound();
    }));

    renderExplorer("/transactions");

    expect(await screen.findByText("No canonical transactions are available in this snapshot.")).toBeVisible();
    expect(await screen.findByText(shorten(transactionHash), {}, { timeout: 3_500 })).toBeVisible();
    expect(transactionRequests).toBeGreaterThanOrEqual(2);
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
