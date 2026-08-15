import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { act, render, screen, waitFor, within } from "@testing-library/react";
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
const delegatedAddress = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266";
const clearedDelegationAddress = `0x${"77".repeat(20)}`;
const delegateAddress = "0x5FbDB2315678afecb367f032d93F642f64180aa3";
const delegateCodeHash = `0x${"55".repeat(32)}`;
const delegationBlockHash = `0x${"66".repeat(32)}`;
const delegationTransactionHash = `0x${"bb".repeat(32)}`;

describe("core explorer pages", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    vi.useRealTimers();
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

  it("deep-links block tabs, renders withdrawals, and loads block transactions lazily", async () => {
    const requested: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      requested.push(url.pathname);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/blocks/${canonicalHash}`) {
        return envelope({
          ...block("12", canonicalHash),
          withdrawals: [{
            index: "7",
            validator_index: "42",
            address,
            amount: "3200000000",
          }],
        });
      }
      if (url.pathname === `/api/v1/blocks/${canonicalHash}/transactions`) {
        return envelope([{
          hash: transactionHash,
          block_hash: canonicalHash,
          block_number: "12",
          transaction_index: 0,
          from: address,
          to: delegatedAddress,
          value: "1000000000000000000",
          gas: "21000",
          gas_used: "21000",
          status: "success",
          canonical: true,
          finality: "safe",
          completeness: completeness(),
        }], { next_cursor: "block-next" });
      }
      return notFound();
    }));

    renderExplorer(`/blocks/${canonicalHash}?tab=transactions`);

    const tabs = await screen.findByRole("tablist", { name: "Block detail sections" });
    expect(within(tabs).getByRole("tab", { name: "Transactions" })).toHaveAttribute("aria-selected", "true");
    expect(await screen.findByRole("link", { name: shorten(transactionHash) })).toHaveAttribute(
      "href", `/tx/${transactionHash}?tab=overview`,
    );
    expect(screen.queryByRole("columnheader", { name: "Method" })).not.toBeInTheDocument();
    expect(requested).toContain(`/api/v1/blocks/${canonicalHash}/transactions`);
    const withdrawalsTab = within(tabs).getByRole("tab", { name: "Withdrawals" });
    expect(withdrawalsTab).toHaveAttribute("aria-selected", "false");
    expect(screen.queryByRole("heading", { name: "Withdrawals" })).not.toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(withdrawalsTab);
    expect(await screen.findByRole("heading", { name: "Withdrawals" })).toBeVisible();
    expect(screen.getByText("3.2 Ether")).toBeVisible();
    expect(withdrawalsTab).toHaveAttribute("aria-selected", "true");
    await user.click(within(tabs).getByRole("tab", { name: "Overview" }));
    expect(screen.queryByText("Data completeness")).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Withdrawals" })).not.toBeInTheDocument();
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
          has_delegation_history: true,
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

  it("polls a basic mempool detail through pending, replaced, and included states", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-13T00:00:00Z"));
    const predecessorHash = `0x${"99".repeat(32)}`;
    const replacementHash = `0x${"bb".repeat(32)}`;
    const requested: string[] = [];
    let detailRequests = 0;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      requested.push(url.pathname);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/transactions/${transactionHash}`) {
        detailRequests += 1;
        const observed = mempoolTransaction(transactionHash, {
          replaces_hash: predecessorHash,
          expires_at: "2026-08-13T00:00:10Z",
        });
        if (detailRequests === 1) {
          return envelope({ kind: "pending", transaction: observed });
        }
        if (detailRequests === 2) {
          return envelope({
            kind: "replaced",
            transaction: observed,
            replacement_hash: replacementHash,
            replaced_at: "2026-08-13T00:00:02Z",
          });
        }
        return envelope({
          kind: "included",
          transaction: {
            hash: transactionHash,
            block_hash: canonicalHash,
            block_number: "12",
            transaction_index: 0,
            from: address,
            nonce: "7",
            value: "1000000000000000000",
            gas: "100000",
            input: "0x6000",
            status: "success",
            canonical: true,
            finality: "safe",
            contract_address: delegatedAddress,
            completeness: completeness(),
          },
        });
      }
      if (url.pathname === `/api/v1/transactions/${transactionHash}/token-transfers`) {
        return envelope({
          state: "complete",
          chain_id: "1",
          block_number: "12",
          block_hash: canonicalHash,
          transaction_hash: transactionHash,
          canonical: true,
          finality: "safe",
          items: [],
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });

    expect(screen.getByRole("heading", { name: "Waiting for confirmation" })).toBeVisible();
    expect(screen.getByText("Contract creation", { exact: true })).toBeVisible();
    expect(screen.getByRole("textbox", { name: "Raw calldata (Hex)" })).toHaveValue("0x6000");
    expect(screen.getByRole("link", { name: predecessorHash })).toHaveAttribute(
      "href",
      `/tx/${predecessorHash}?tab=overview`,
    );
    expect(document.querySelector('[data-status="pending"] svg.lucide-clock-3')).not.toBeNull();
    expect(screen.queryByRole("tablist", { name: "Transaction detail sections" })).toBeNull();
    expect(requested.some((path) => path.startsWith(`/api/v1/transactions/${transactionHash}/`))).toBe(false);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2_000);
    });
    expect(screen.getByRole("heading", { name: "Transaction replaced" })).toBeVisible();
    expect(screen.getByRole("link", { name: replacementHash })).toHaveAttribute(
      "href",
      `/tx/${replacementHash}?tab=overview`,
    );
    expect(document.querySelector('[data-status="replaced"] svg.lucide-arrow-right-left')).not.toBeNull();
    expect(requested.some((path) => path.startsWith(`/api/v1/transactions/${transactionHash}/`))).toBe(false);

    await act(async () => {
      await i18n.changeLanguage("zh");
    });
    expect(screen.getByRole("heading", { name: "交易已被替换" })).toBeVisible();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2_000);
    });
    expect(screen.getByRole("tablist", { name: "交易详情分区" })).toBeVisible();
    expect(document.querySelector('[data-status="success"] svg.lucide-circle-check')).not.toBeNull();
    expect(requested).toContain(`/api/v1/transactions/${transactionHash}/token-transfers`);
    expect(detailRequests).toBe(3);
  });

  it("hides an expired mempool observation and does not poll beyond its retention", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-13T00:00:00Z"));
    let detailRequests = 0;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/transactions/${transactionHash}`) {
        detailRequests += 1;
        return envelope({
          kind: "pending",
          transaction: mempoolTransaction(transactionHash, {
            expires_at: "2026-08-13T00:00:01Z",
          }),
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(screen.getByRole("heading", { name: "Waiting for confirmation" })).toBeVisible();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    expect(screen.getByRole("heading", { name: "Pending transaction snapshot is unavailable" })).toBeVisible();
    expect(screen.getByText("snapshot_expired", { exact: true })).toBeVisible();
    expect(screen.queryByRole("heading", { name: "Waiting for confirmation" })).toBeNull();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(detailRequests).toBeLessThanOrEqual(2);
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

  it.each([
    {
      name: "empty-calldata contract calls",
      input: "0x",
      value: "1",
      resolution: "direct",
      english: "Contract interaction",
      chinese: "调用合约",
    },
    {
      name: "non-empty-calldata EOA transactions",
      input: "0x1234",
      value: "1",
      resolution: "empty",
      english: "EOA transaction",
      chinese: "EOA 交易",
    },
    {
      name: "ordinary native transfers",
      input: "0x",
      value: "0",
      resolution: "empty",
      english: "Native asset transfer",
      chinese: "原生资产转账",
    },
  ] as const)("classifies $name from transaction-time execution code", async ({
    input,
    value,
    resolution,
    english,
    chinese,
  }) => {
    const requested: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (request: RequestInfo | URL) => {
      const url = requestURL(request);
      requested.push(url.pathname);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/transactions/${transactionHash}`) {
        return envelope({
          hash: transactionHash, block_hash: canonicalHash, block_number: "12",
          transaction_index: 0, from: delegatedAddress, to: address, nonce: "1", value,
          gas: "21000", input, status: "success", canonical: true,
          finality: "safe", completeness: completeness(),
        });
      }
      if (url.pathname === `/api/v1/transactions/${transactionHash}/calldata`) {
        return envelope({
          chain_id: "1", block_number: "12", block_hash: canonicalHash,
          transaction_hash: transactionHash, transaction_index: "0", state: "complete", input,
          execution: resolution === "direct"
            ? { context_address: address, address, code_hash: canonicalHash, resolution }
            : { context_address: address, resolution },
          decoding: {
            status: resolution === "empty" ? "not_applicable" : "unavailable",
            inputs: [], candidates: [],
          },
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);
    expect(await screen.findByText(english, { exact: true })).toBeVisible();
    expect(requested).toContain(`/api/v1/transactions/${transactionHash}/calldata`);

    await userEvent.setup().click(screen.getByRole("button", { name: "切换到中文" }));
    expect(await screen.findByText(chinese, { exact: true })).toBeVisible();
  });

  it("classifies contract creation without requesting execution evidence", async () => {
    const requested: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (request: RequestInfo | URL) => {
      const url = requestURL(request);
      requested.push(url.pathname);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/transactions/${transactionHash}`) {
        return envelope({
          hash: transactionHash, block_hash: canonicalHash, block_number: "12",
          transaction_index: 0, from: address, nonce: "1", value: "0",
          gas: "100000", input: "0x6000", status: "success", canonical: true,
          finality: "safe", completeness: completeness(), contract_address: delegatedAddress,
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);
    const action = (await screen.findByText("Transaction action", { exact: true }))
      .closest(".transaction-action-card") as HTMLElement | null;
    if (!action) throw new Error("transaction action card missing");
    expect(within(action).getByText("Contract creation", { exact: true })).toBeVisible();
    expect(requested).not.toContain(`/api/v1/transactions/${transactionHash}/calldata`);

    await userEvent.setup().click(screen.getByRole("button", { name: "切换到中文" }));
    await waitFor(() => expect(within(action).getByText("创建合约", { exact: true })).toBeVisible());
  });

  it("shows a fail-closed loading action without briefly inferring a contract call", async () => {
    const calldata = "0x1234";
    let resolveCalldata: ((response: Response) => void) | undefined;
    const pendingCalldata = new Promise<Response>((resolve) => {
      resolveCalldata = resolve;
    });
    vi.stubGlobal("fetch", vi.fn(async (request: RequestInfo | URL) => {
      const url = requestURL(request);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/transactions/${transactionHash}`) {
        return envelope({
          hash: transactionHash, block_hash: canonicalHash, block_number: "12",
          transaction_index: 0, from: delegatedAddress, to: address, nonce: "1", value: "0",
          gas: "21000", input: calldata, status: "success", canonical: true,
          finality: "safe", completeness: completeness(),
        });
      }
      if (url.pathname === `/api/v1/transactions/${transactionHash}/calldata`) {
        return pendingCalldata;
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);
    expect(await screen.findByText("Determining transaction action", { exact: true })).toBeVisible();
    expect(screen.queryByText("Contract interaction", { exact: true })).not.toBeInTheDocument();

    await userEvent.setup().click(screen.getByRole("button", { name: "切换到中文" }));
    expect(await screen.findByText("正在判定交易动作", { exact: true })).toBeVisible();
    await act(async () => {
      resolveCalldata?.(envelope({
        chain_id: "1", block_number: "12", block_hash: canonicalHash,
        transaction_hash: transactionHash, transaction_index: "0", state: "complete", input: calldata,
        execution: { context_address: address, resolution: "empty" },
        decoding: { status: "not_applicable", inputs: [], candidates: [] },
      }));
    });
    expect(await screen.findByText("EOA 交易", { exact: true })).toBeVisible();
  });

  it.each(["request failure", "unavailable execution identity"] as const)(
    "fails closed when transaction action evidence has a $mode",
    async (mode) => {
      const calldata = "0x1234";
      vi.stubGlobal("fetch", vi.fn(async (request: RequestInfo | URL) => {
        const url = requestURL(request);
        if (url.pathname === "/api/v1/config") return configResponse();
        if (url.pathname === `/api/v1/transactions/${transactionHash}`) {
          return envelope({
            hash: transactionHash, block_hash: canonicalHash, block_number: "12",
            transaction_index: 0, from: delegatedAddress, to: address, nonce: "1", value: "0",
            gas: "21000", input: calldata, status: "success", canonical: true,
            finality: "safe", completeness: completeness(),
          });
        }
        if (url.pathname === `/api/v1/transactions/${transactionHash}/calldata`) {
          if (mode === "request failure") {
            return Response.json({
              error: {
                code: "temporary_failure",
                message: "execution evidence is temporarily unavailable",
                request_id: "core-pages-test",
              },
            }, { status: 503 });
          }
          return envelope({
            chain_id: "1", block_number: "12", block_hash: canonicalHash,
            transaction_hash: transactionHash, transaction_index: "0", state: "complete", input: calldata,
            execution: { context_address: address, resolution: "unavailable" },
            decoding: { status: "unavailable", inputs: [], candidates: [] },
          });
        }
        return notFound();
      }));

      renderExplorer(`/tx/${transactionHash}`);
      expect(await screen.findByText("Transaction action unavailable", { exact: true })).toBeVisible();
      expect(screen.queryByText("Contract interaction", { exact: true })).not.toBeInTheDocument();
      expect(screen.queryByText("EOA transaction", { exact: true })).not.toBeInTheDocument();

      await userEvent.setup().click(screen.getByRole("button", { name: "切换到中文" }));
      expect(await screen.findByText("无法判断交易动作", { exact: true })).toBeVisible();
    },
  );

  it("separates decoded and raw calldata with compact copyable ABI evidence", async () => {
    const calldata = `0xa9059cbb${"0".repeat(24)}${"44".repeat(20)}${"0".repeat(63)}c`;
    const requested: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      requested.push(url.pathname);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/transactions/${transactionHash}`) {
        return envelope({
          hash: transactionHash, block_hash: canonicalHash, block_number: "12",
          transaction_index: 0, from: address, to: address, nonce: "1", value: "0",
          gas: "21000", input: calldata, status: "success", canonical: true,
          finality: "safe", completeness: completeness(),
        });
      }
      if (url.pathname === `/api/v1/transactions/${transactionHash}/calldata`) {
        return envelope({
          chain_id: "1", block_number: "12", block_hash: canonicalHash,
          transaction_hash: transactionHash, transaction_index: "0", state: "complete",
          input: calldata,
          execution: {
            context_address: address, address, code_hash: canonicalHash, resolution: "direct",
          },
          decoding: {
            status: "decoded", function_name: "transfer", signature: "transfer(address,uint256)",
            inputs: [
              { name: "recipient", type: "address", value: `0x${"44".repeat(20)}`, components: [] },
              { name: "amount", type: "uint256", value: "12", components: [] },
            ],
            candidates: [], confidence: "verified",
            abi_source: { kind: "exact_address", address, code_hash: canonicalHash },
          },
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);
    expect(await screen.findByText("Contract interaction", { exact: true })).toBeVisible();
    const user = userEvent.setup();
    const writeText = vi.spyOn(navigator.clipboard, "writeText");
    try {
      await user.click(await screen.findByText("More details"));

      const decoded = await screen.findByRole("region", { name: "Decoded calldata · transfer(address,uint256)" });
      const raw = screen.getByRole("region", { name: "Raw calldata" });
      expect(within(decoded).getAllByText("transfer(address,uint256)")).toHaveLength(1);
      const tree = within(decoded).getByRole("group", { name: "transfer(address,uint256)" });
      expect(within(tree).getByText("Params", { exact: true })).toBeVisible();
      expect(within(tree).getByText("Type", { exact: true })).toBeVisible();
      expect(within(tree).getByText("Data", { exact: true })).toBeVisible();
      const evidence = within(decoded).getByLabelText("ABI evidence");
      expect(within(evidence).getByText("Transaction-time execution")).toBeVisible();
      expect(within(evidence).getByText("Direct code")).toBeVisible();
      expect(within(evidence).getByText("ABI source · exact_address")).toBeVisible();
      expect(within(evidence).getAllByRole("link", { name: address })).toHaveLength(2);
      expect(within(evidence).getAllByRole("button", { name: "Copy" })).toHaveLength(2);
      expect(within(decoded).getAllByText("12", { exact: true }).length).toBeGreaterThan(0);

      const rawValue = within(raw).getByRole("textbox", { name: "Raw calldata (Hex)" });
      expect(rawValue).toHaveAttribute("readonly");
      expect(rawValue).toHaveAttribute("wrap", "soft");
      expect(rawValue).toHaveValue(calldata);
      expect(within(raw).getByRole("button", { name: "View as UTF-8" })).toBeVisible();
      await user.click(within(raw).getByRole("button", { name: "Copy" }));
      expect(writeText).toHaveBeenCalledWith(calldata);
      expect(requested).toContain(`/api/v1/transactions/${transactionHash}/calldata`);
      expect(requested).not.toContain(`/api/v1/transactions/${transactionHash}/failure`);
      expect(requested).not.toContain(`/api/v1/contracts/${address}/verification`);
      expect(requested).not.toContain(`/api/v1/contracts/${address}/proxy`);
    } finally {
      writeText.mockRestore();
    }
  });

  it("renders named structs and nested arrays as a bounded keyboard tree in both locales", async () => {
    const calldata = "0x12345678";
    const requested: string[] = [];
    const nestedValue = [
      `0x${"44".repeat(20)}`,
      [["7", true, [["1"], []]], ["8", false, []]],
    ];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      requested.push(url.pathname);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/transactions/${transactionHash}`) {
        return envelope({
          hash: transactionHash, block_hash: canonicalHash, block_number: "12",
          transaction_index: 0, from: address, to: address, nonce: "1", value: "0",
          gas: "21000", input: calldata, status: "success", canonical: true,
          finality: "safe", completeness: completeness(),
        });
      }
      if (url.pathname === `/api/v1/transactions/${transactionHash}/calldata`) {
        return envelope({
          chain_id: "1", block_number: "12", block_hash: canonicalHash,
          transaction_hash: transactionHash, transaction_index: "0", state: "complete",
          input: calldata,
          execution: { context_address: address, address, code_hash: canonicalHash, resolution: "direct" },
          decoding: {
            status: "decoded", function_name: "configure",
            signature: "configure((address,(uint16,bool,uint8[][])[]),uint256[][2])",
            inputs: [
              {
                name: "config", type: "tuple", internal_type: "struct Fixture.Config",
                components: [
                  { name: "owner", type: "address", components: [] },
                  {
                    name: "rules", type: "tuple[]", internal_type: "struct Fixture.Rule[]",
                    components: [
                      { name: "threshold", type: "uint16", components: [] },
                      { name: "enabled", type: "bool", components: [] },
                      { name: "buckets", type: "uint8[][]", components: [] },
                    ],
                  },
                ],
                value: nestedValue,
              },
              { name: "matrix", type: "uint256[][2]", value: [[], ["9", "10"]], components: [] },
            ],
            candidates: [], confidence: "verified",
            abi_source: { kind: "exact_address", address, code_hash: canonicalHash },
          },
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);
    const user = userEvent.setup();
    await user.click(await screen.findByText("More details"));
    const decoded = await screen.findByRole("region", {
      name: "Decoded calldata · configure((address,(uint16,bool,uint8[][])[]),uint256[][2])",
    });
    expect(within(decoded).getByText("config.owner", { exact: true })).toBeVisible();
    expect(within(decoded).getByText("config.rules", { exact: true })).toBeVisible();
    expect(within(decoded).getAllByText("#0", { exact: true }).length).toBeGreaterThan(0);
    expect(within(decoded).getByText("Config", { exact: true })).toBeVisible();
    expect(within(decoded).getByText("Rule[]", { exact: true })).toBeVisible();
    expect(within(decoded).getAllByText("2 items", { exact: true }).length).toBeGreaterThan(0);
    expect(within(decoded).getByText("10", { exact: true })).toBeVisible();
    expect(within(decoded).queryByText(JSON.stringify(nestedValue), { exact: true })).not.toBeInTheDocument();

    const shallow = decoded.querySelector<HTMLDetailsElement>("details.calldata-depth-2");
    const deep = decoded.querySelector<HTMLDetailsElement>("details.calldata-depth-3");
    expect(shallow?.open).toBe(true);
    expect(deep?.open).toBe(false);
    const deepSummary = deep?.querySelector<HTMLElement>("summary");
    if (!deep || !deepSummary) throw new Error("deep calldata summary missing");
    deepSummary.focus();
    await user.keyboard("{Enter}");
    expect(deep.open).toBe(true);
    expect(deepSummary).toHaveFocus();

    await user.click(screen.getByRole("button", { name: "切换到中文" }));
    expect(await within(decoded).findAllByText("2 项", { exact: true })).not.toHaveLength(0);
    expect(screen.getByRole("textbox", { name: "原始 calldata（十六进制）" })).toHaveValue(calldata);
    expect(requested).not.toContain(`/api/v1/contracts/${address}/verification`);
    expect(requested).not.toContain(`/api/v1/contracts/${address}/proxy`);
    expect(requested).not.toContain(`/api/v1/addresses/${address}/delegation`);
  });

  it("keeps raw calldata when a decoded parameter response is structurally inconsistent", async () => {
    const calldata = "0x12345678";
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/transactions/${transactionHash}`) {
        return envelope({
          hash: transactionHash, block_hash: canonicalHash, block_number: "12",
          transaction_index: 0, from: address, to: address, nonce: "1", value: "0",
          gas: "21000", input: calldata, status: "success", canonical: true,
          finality: "safe", completeness: completeness(),
        });
      }
      if (url.pathname === `/api/v1/transactions/${transactionHash}/calldata`) {
        return envelope({
          chain_id: "1", block_number: "12", block_hash: canonicalHash,
          transaction_hash: transactionHash, transaction_index: "0", state: "complete", input: calldata,
          execution: { context_address: address, address, code_hash: canonicalHash, resolution: "direct" },
          decoding: {
            status: "decoded", function_name: "broken", signature: "broken((uint256,bool))",
            inputs: [{
              name: "pair", type: "tuple", value: ["1"],
              components: [
                { name: "value", type: "uint256", components: [] },
                { name: "enabled", type: "bool", components: [] },
              ],
            }],
            candidates: [], confidence: "verified",
            abi_source: { kind: "exact_address", address, code_hash: canonicalHash },
          },
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);
    await userEvent.setup().click(await screen.findByText("More details"));
    expect(await screen.findByText("Decoded parameter structure is unavailable. Raw calldata remains available below.")).toBeVisible();
    expect(screen.queryByText('["1"]', { exact: true })).not.toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Raw calldata (Hex)" })).toHaveValue(calldata);
  });

  it("classifies an exact-address decoded call when execution identity is unavailable", async () => {
    const calldata = `0xa9059cbb${"0".repeat(24)}${"44".repeat(20)}${"0".repeat(63)}c`;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/transactions/${transactionHash}`) {
        return envelope({
          hash: transactionHash, block_hash: canonicalHash, block_number: "12",
          transaction_index: 0, from: address, to: address, nonce: "1", value: "0",
          gas: "21000", input: calldata, status: "success", canonical: true,
          finality: "safe", completeness: completeness(),
        });
      }
      if (url.pathname === `/api/v1/transactions/${transactionHash}/calldata`) {
        return envelope({
          chain_id: "1", block_number: "12", block_hash: canonicalHash,
          transaction_hash: transactionHash, transaction_index: "0", state: "complete",
          input: calldata,
          execution: { context_address: address, resolution: "unavailable" },
          decoding: {
            status: "decoded", function_name: "transfer", signature: "transfer(address,uint256)",
            inputs: [
              { name: "recipient", type: "address", value: `0x${"44".repeat(20)}`, components: [] },
              { name: "amount", type: "uint256", value: "12", components: [] },
            ],
            candidates: [], confidence: "verified",
            abi_source: { kind: "exact_address", address, code_hash: canonicalHash },
          },
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);
    expect(await screen.findByText("Contract interaction", { exact: true })).toBeVisible();
    expect(screen.queryByText("Transaction action unavailable", { exact: true })).not.toBeInTheDocument();
    await userEvent.setup().click(await screen.findByText("More details"));
    expect(await screen.findByRole("region", {
      name: "Decoded calldata · transfer(address,uint256)",
    })).toBeVisible();
    expect(screen.getByText("Execution evidence unavailable", { exact: true })).toBeVisible();
  });

  it("decodes an ordinary call from its transaction-time EIP-7702 delegate", async () => {
    const delegatedAddress = `0x${"66".repeat(20)}`;
    const delegateAddress = `0x${"77".repeat(20)}`;
    const calldata = `0x55241077${"0".repeat(63)}2a`;
    const requested: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      requested.push(url.pathname);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/transactions/${transactionHash}`) {
        return envelope({
          hash: transactionHash, block_hash: canonicalHash, block_number: "12",
          transaction_index: 0, from: address, to: delegatedAddress, nonce: "1", value: "0",
          gas: "21000", type: "2", input: calldata, status: "success", canonical: true,
          finality: "safe", completeness: completeness(),
        });
      }
      if (url.pathname === `/api/v1/transactions/${transactionHash}/calldata`) {
        return envelope({
          chain_id: "1", block_number: "12", block_hash: canonicalHash,
          transaction_hash: transactionHash, transaction_index: "0", state: "complete", input: calldata,
          execution: {
            context_address: delegatedAddress, address: delegateAddress,
            code_hash: canonicalHash, resolution: "eip7702_delegate",
          },
          decoding: {
            status: "decoded", function_name: "setValue", signature: "setValue(uint256)",
            inputs: [{ name: "value", type: "uint256", value: "42", components: [] }], candidates: [],
            confidence: "verified",
            abi_source: { kind: "exact_address", address: delegateAddress, code_hash: canonicalHash },
          },
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);
    expect(await screen.findByText("Contract interaction", { exact: true })).toBeVisible();
    await userEvent.setup().click(await screen.findByText("More details"));
    const decoded = await screen.findByRole("region", { name: "Decoded calldata · setValue(uint256)" });
    expect(within(decoded).getByText("EIP-7702 delegate code")).toBeVisible();
    expect(within(decoded).getAllByRole("link", { name: delegateAddress })).toHaveLength(2);
    expect(within(decoded).getByText("42", { exact: true })).toBeVisible();
    expect(requested).toContain(`/api/v1/transactions/${transactionHash}/calldata`);
    expect(requested).not.toContain(`/api/v1/addresses/${delegatedAddress}/delegation`);
    expect(requested).not.toContain(`/api/v1/contracts/${delegatedAddress}/verification`);
  });

  it("keeps raw calldata and reports no executable code after a type-4 clearing authorization", async () => {
    const delegatedAddress = `0x${"66".repeat(20)}`;
    const calldata = "0x55241077";
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/transactions/${transactionHash}`) {
        return envelope({
          hash: transactionHash, block_hash: canonicalHash, block_number: "12",
          transaction_index: 0, from: address, to: delegatedAddress, nonce: "1", value: "0",
          gas: "21000", type: "4", input: calldata, status: "success", canonical: true,
          finality: "safe", completeness: completeness(),
        });
      }
      if (url.pathname === `/api/v1/transactions/${transactionHash}/calldata`) {
        return envelope({
          chain_id: "1", block_number: "12", block_hash: canonicalHash,
          transaction_hash: transactionHash, transaction_index: "0", state: "complete", input: calldata,
          execution: { context_address: delegatedAddress, resolution: "empty" },
          decoding: { status: "not_applicable", inputs: [], candidates: [] },
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);
    expect(await screen.findByText("EOA transaction", { exact: true })).toBeVisible();
    expect(screen.queryByText("Contract interaction", { exact: true })).not.toBeInTheDocument();
    await userEvent.setup().click(await screen.findByText("More details"));
    expect(await screen.findByText(/No executable code at transaction execution time/u)).toBeVisible();
    expect(screen.getByRole("textbox", { name: "Raw calldata (Hex)" })).toHaveValue(calldata);
    expect(screen.getByRole("tab", { name: "Authorizations" })).toBeVisible();
  });

  it("refetches a mismatched calldata identity once and then fails closed", async () => {
    const calldata = "0x55241077";
    let transactionFetches = 0;
    let calldataFetches = 0;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/transactions/${transactionHash}`) {
        transactionFetches += 1;
        return envelope({
          hash: transactionHash, block_hash: canonicalHash, block_number: "12",
          transaction_index: 0, from: address, to: address, nonce: "1", value: "0",
          gas: "21000", input: calldata, status: "success", canonical: true,
          finality: "safe", completeness: completeness(),
        });
      }
      if (url.pathname === `/api/v1/transactions/${transactionHash}/calldata`) {
        calldataFetches += 1;
        return envelope({
          chain_id: "1", block_number: "12", block_hash: canonicalHash,
          transaction_hash: transactionHash, transaction_index: "0", state: "complete",
          input: "0xdeadbeef",
          execution: { context_address: address, address, code_hash: canonicalHash, resolution: "direct" },
          decoding: { status: "unknown", inputs: [], candidates: [] },
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);
    await userEvent.setup().click(await screen.findByText("More details"));
    await waitFor(() => {
      expect(transactionFetches).toBe(2);
      expect(calldataFetches).toBe(2);
    });
    expect(screen.getByText("Transaction action unavailable", { exact: true })).toBeVisible();
    expect(screen.queryByText("Contract interaction", { exact: true })).not.toBeInTheDocument();
    expect(screen.getByText("The canonical transaction inclusion changed. Refreshing this tab will load the new block identity.")).toBeVisible();
    expect(screen.getByRole("textbox", { name: "Raw calldata (Hex)" })).toHaveValue(calldata);
  });

  it("keeps raw calldata in Hex when UTF-8 conversion is unavailable", async () => {
    const calldata = "0xffff";
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/transactions/${transactionHash}`) {
        return envelope({
          hash: transactionHash, block_hash: canonicalHash, block_number: "12",
          transaction_index: 0, from: address, to: address, nonce: "1", value: "0",
          gas: "21000", input: calldata, status: "success", canonical: true,
          finality: "safe", completeness: completeness(),
        });
      }
      return notFound();
    }));
    renderExplorer(`/tx/${transactionHash}`);
    const user = userEvent.setup();
    await user.click(await screen.findByText("More details"));
    const raw = screen.getByRole("region", { name: "Raw calldata" });
    await user.click(within(raw).getByRole("button", { name: "View as UTF-8" }));
    expect(screen.getByText("UTF-8 is unavailable for these bytes")).toBeVisible();
    expect(within(raw).getByRole("textbox", { name: "Raw calldata (Hex)" })).toHaveValue(calldata);
    expect(within(raw).getByRole("button", { name: "View as UTF-8" })).toBeVisible();
    expect(within(raw).queryByRole("button", { name: "View as Hex" })).not.toBeInTheDocument();
  });

  it("switches valid raw calldata between complete UTF-8 and Hex values", async () => {
    const calldata = "0x74657374";
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/transactions/${transactionHash}`) {
        return envelope({
          hash: transactionHash, block_hash: canonicalHash, block_number: "12",
          transaction_index: 0, from: address, to: address, nonce: "1", value: "0",
          gas: "21000", input: calldata, status: "success", canonical: true,
          finality: "safe", completeness: completeness(),
        });
      }
      return notFound();
    }));
    renderExplorer(`/tx/${transactionHash}`);
    const user = userEvent.setup();
    await user.click(await screen.findByText("More details"));
    const raw = screen.getByRole("region", { name: "Raw calldata" });
    await user.click(within(raw).getByRole("button", { name: "View as UTF-8" }));
    expect(within(raw).getByRole("textbox", { name: "Raw calldata (UTF-8)" })).toHaveValue("test");
    await user.click(within(raw).getByRole("button", { name: "View as Hex" }));
    expect(within(raw).getByRole("textbox", { name: "Raw calldata (Hex)" })).toHaveValue(calldata);
  });

  it("activates the Authorizations tab and loads transaction authorization tuples", async () => {
    const requested: string[] = [];
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
          to: delegatedAddress,
          nonce: "1",
          type: "4",
          value: "0",
          gas: "21000",
          input: "0x",
          status: "success",
          canonical: true,
          finality: "safe",
          completeness: completeness(),
        });
      }
      if (url.pathname === `/api/v1/transactions/${transactionHash}/token-transfers`) {
        return envelope({
          state: "complete",
          chain_id: "1",
          block_number: "12",
          block_hash: canonicalHash,
          transaction_hash: transactionHash,
          transaction_index: 0,
          items: [],
        });
      }
      if (url.pathname === `/api/v1/transactions/${transactionHash}/authorizations`) {
        return envelope({
          state: "complete",
          chain_id: "1",
          block_number: "12",
          block_hash: canonicalHash,
          transaction_hash: transactionHash,
          transaction_index: 0,
          items: [{
            application_status: "applied",
            authority: address,
            chain_id: "1",
            delegate: delegateAddress,
            index: "0",
            nonce: "1",
            r: canonicalHash,
            s: canonicalHash,
            signature_status: "valid",
            y_parity: 0,
          }],
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);

    const user = userEvent.setup();
    const authorizationsTab = await screen.findByRole("tab", { name: "Authorizations" });
    expect(screen.getByRole("tab", { name: "Access list" })).toBeVisible();
    expect(screen.queryByRole("tab", { name: "Blob" })).not.toBeInTheDocument();
    await user.click(authorizationsTab);

    expect(authorizationsTab).toHaveAttribute("aria-selected", "true");
    expect(await screen.findByText("Authorization #0")).toBeVisible();
    expect(screen.getByText("applied", { exact: true })).toBeVisible();
    expect(requested).toContain(`/api/v1/transactions/${transactionHash}/authorizations`);
  });

  it("renders topics and data directly in collapsed details and converts anonymous topic zero", async () => {
    const eventTopic = `0x${"77".repeat(32)}`;
    const addressTopic = "0x00000000000000000000000052908400098527886e0f7030069857d2e4169ee7";
    const textTopic = `0x68656c6c6f${"00".repeat(27)}`;
    const numberTopic = `0x${"0".repeat(63)}2`;
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
          items: [
            {
              address, log_index: "0", topics: [eventTopic, addressTopic, textTopic, numberTopic], data: "0x1234",
              decoding: {
                status: "decoded", event_name: "Changed", signature: "Changed(tuple,uint256[],address,string,uint256)",
                confidence: "verified", abi_source: { kind: "exact_address", address, code_hash: canonicalHash },
                arguments: [
                  { name: "pair", type: "tuple", indexed: false, hashed: false, value: ["7", address] },
                  { name: "values", type: "uint256[]", indexed: false, hashed: false, value: ["1", "2"] },
                  { name: "owner", type: "address", indexed: true, hashed: false, value: address },
                  { name: "message", type: "string", indexed: true, hashed: true, value: textTopic },
                  { name: "count", type: "uint256", indexed: true, hashed: false, value: "2" },
                ],
                candidates: ["Changed(tuple,uint256[],address,string,uint256)"],
                attribution: { mode: "exact_trace", trace_path: [0, 1], execution_address: address },
              },
            },
            {
              address, log_index: "1", topics: [addressTopic, textTopic, numberTopic], data: "0xabcd",
              decoding: {
                status: "decoded", event_name: "AnonymousChanged", signature: "AnonymousChanged(address,string,uint256)",
                confidence: "verified", abi_source: { kind: "exact_address", address, code_hash: canonicalHash },
                arguments: [
                  { name: "owner", type: "address", indexed: true, hashed: false, value: address },
                  { name: "message", type: "string", indexed: true, hashed: true, value: textTopic },
                  { name: "count", type: "uint256", indexed: true, hashed: false, value: "2" },
                ],
                candidates: ["AnonymousChanged(address,string,uint256)"],
                attribution: { mode: "address_fallback", trace_path: [] },
              },
            },
          ],
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}?tab=logs`);

    expect(await screen.findByText("Changed(tuple,uint256[],address,string,uint256)")).toBeVisible();
    expect(await screen.findByText("AnonymousChanged(address,string,uint256)")).toBeVisible();
    expect(screen.queryByText("Decoded")).not.toBeInTheDocument();
    const cards = document.querySelectorAll<HTMLElement>(".transaction-log");
    const card = cards[0];
    const anonymousCard = cards[1];
    if (!card || !anonymousCard) throw new Error("transaction log cards are missing");
    const user = userEvent.setup();
    const argumentsTable = within(card).getByRole("table", { name: "Event arguments" });
    for (const heading of ["Name", "Type", "Indexed", "Data"]) {
      expect(within(argumentsTable).getByRole("columnheader", { name: heading })).toBeVisible();
    }
    expect(within(argumentsTable).getByText("pair", { exact: true })).toBeVisible();
    expect(within(argumentsTable).getByText("pair[0]", { exact: true })).toBeVisible();
    expect(within(argumentsTable).getByText("pair[1]", { exact: true })).toBeVisible();
    expect(within(argumentsTable).getByText("values[0]", { exact: true })).toBeVisible();
    expect(within(argumentsTable).queryByText(".pair[0]", { exact: true })).not.toBeInTheDocument();
    expect(within(argumentsTable).getAllByRole("button", { name: "Copy" }).length).toBeGreaterThanOrEqual(7);
    expect(card.querySelectorAll(":scope > .transaction-log-topics")).toHaveLength(0);
    const raw = within(card).getByText("More details");
    expect(raw).toBeVisible();
    expect(raw.closest("details")).not.toHaveAttribute("open");
    await user.click(raw);
    expect(within(card).getByRole("heading", { name: "ABI provenance" })).toBeVisible();
    expect(within(card).getByText("Exact address", { exact: true })).toBeVisible();
    expect(within(card).getByText("Execution context", { exact: true })).toBeVisible();
    expect(within(card).getByText("Exact Trace frame", { exact: true })).toBeVisible();
    expect(within(card).getByText("[0, 1]", { exact: true })).toBeVisible();
    expect(within(card).queryByText("Raw topics and data")).not.toBeInTheDocument();
    expect(within(card).getByRole("heading", { name: "Topics" })).toBeVisible();
    expect(within(card).queryByRole("combobox", { name: "Topic 0 display mode" })).not.toBeInTheDocument();
    const addressMode = within(card).getByRole("combobox", { name: "Topic 1 display mode" });
    await user.selectOptions(addressMode, "address");
    expect(within(card).getByText("0x52908400098527886E0F7030069857D2E4169EE7", { exact: true })).toBeVisible();
    await user.selectOptions(within(card).getByRole("combobox", { name: "Topic 2 display mode" }), "text");
    expect(within(card).getByText("hello", { exact: true })).toBeVisible();
    const numberMode = within(card).getByRole("combobox", { name: "Topic 3 display mode" });
    await user.selectOptions(numberMode, "number");
    const numberTopicRow = numberMode.closest<HTMLElement>(".transaction-topic");
    if (!numberTopicRow) throw new Error("number topic row is missing");
    expect(within(numberTopicRow).getByText("2", { exact: true })).toBeVisible();
    const addressTopicRow = within(card).getByRole("combobox", { name: "Topic 1 display mode" }).closest<HTMLElement>(".transaction-topic");
    if (!addressTopicRow) throw new Error("address topic row is missing");
    expect(within(addressTopicRow).getByRole("button", { name: "Copy" })).toBeVisible();
    expect(within(numberTopicRow).getByRole("button", { name: "Copy" })).toBeVisible();
    const dataSection = card.querySelector<HTMLElement>(".transaction-log-data");
    if (!dataSection) throw new Error("transaction log data section is missing");
    expect(within(dataSection).getByRole("heading", { name: "Data", level: 3 })).toBeVisible();
    expect(within(dataSection).getByText("0x1234")).toBeVisible();
    expect(dataSection.querySelector("dl")).not.toBeInTheDocument();

    const anonymousDetails = within(anonymousCard).getByText("More details");
    expect(anonymousDetails.closest("details")).not.toHaveAttribute("open");
    await user.click(anonymousDetails);
    const anonymousTopicZeroMode = within(anonymousCard).getByRole("combobox", { name: "Topic 0 display mode" });
    await user.selectOptions(anonymousTopicZeroMode, "address");
    expect(within(anonymousCard).getByText("0x52908400098527886E0F7030069857D2E4169EE7", { exact: true })).toBeVisible();
    await user.selectOptions(within(anonymousCard).getByRole("combobox", { name: "Topic 1 display mode" }), "text");
    expect(within(anonymousCard).getByText("hello", { exact: true })).toBeVisible();
    const anonymousNumberMode = within(anonymousCard).getByRole("combobox", { name: "Topic 2 display mode" });
    await user.selectOptions(anonymousNumberMode, "number");
    const anonymousNumberRow = anonymousNumberMode.closest<HTMLElement>(".transaction-topic");
    if (!anonymousNumberRow) throw new Error("anonymous number topic row is missing");
    expect(anonymousNumberRow.querySelector(".copyable-field code")).toHaveTextContent("2");
    expect(within(anonymousCard).getByText("0xabcd", { exact: true })).toBeVisible();
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
          data: { kind: "included", transaction: {
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
          } },
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
    expect(screen.queryByRole("tab", { name: "Access list" })).not.toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "Blob" })).not.toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "Authorizations" })).not.toBeInTheDocument();

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

  it("renders custom failure arguments as Name Type Data jq-style leaf rows", async () => {
    const requested: string[] = [];
    const revertData = `0x${"de".repeat(68)}`;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      requested.push(url.pathname);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/transactions/${transactionHash}`) {
        return envelope({
          hash: transactionHash, block_hash: canonicalHash, block_number: "12",
          transaction_index: 0, from: address, to: delegatedAddress, nonce: "1", value: "0",
          gas: "100000", input: "0x12345678", status: "failed", canonical: true,
          finality: "safe", completeness: completeness(),
        });
      }
      if (url.pathname === `/api/v1/transactions/${transactionHash}/failure`) {
        return envelope({
          chain_id: "1", block_number: "12", block_hash: canonicalHash,
          transaction_hash: transactionHash, transaction_index: "0", state: "complete",
          error: "execution reverted", revert_data: revertData,
          execution: {
            context_address: delegatedAddress, address: delegatedAddress,
            code_hash: delegateCodeHash, resolution: "direct",
          },
          decoding: {
            status: "decoded",
            error_name: "Complex",
            signature: "Complex(address,uint256,bool,(address,uint16),uint256[],uint256[][])",
            arguments: [
              { name: "sender", type: "address", value: address, components: [] },
              { name: "amount", type: "uint256", value: "42", components: [] },
              { name: "", type: "bool", value: true, components: [] },
              {
                name: "pair", type: "tuple", value: [delegatedAddress, "7"], components: [
                  { name: "owner", type: "address", components: [] },
                  { name: "value", type: "uint16", components: [] },
                ],
              },
              { name: "values", type: "uint256[]", value: ["8", "9"], components: [] },
              {
                name: "items", type: "uint256[][]", value: [["10", "11", "12"]], components: [],
              },
            ],
            candidates: [], confidence: "verified",
            abi_source: { kind: "exact_address", address: delegatedAddress, code_hash: delegateCodeHash },
          },
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);
    const table = await screen.findByRole("table", { name: "Failure arguments" });
    expect(within(table).getByText("Name", { exact: true })).toBeVisible();
    expect(within(table).getByText("Type", { exact: true })).toBeVisible();
    expect(within(table).getByText("Data", { exact: true })).toBeVisible();
    for (const path of ["sender", "amount", "[2]", "pair[0]", "pair[1]", "values[1]", "items[0][2]"]) {
      expect(within(table).getByText(path, { exact: true })).toBeVisible();
    }
    for (const composite of ["pair", "values", "items", "items[0]"]) {
      expect(within(table).queryByText(composite, { exact: true })).not.toBeInTheDocument();
    }
    expect(within(table).getAllByText("address", { exact: true })).toHaveLength(2);
    expect(within(table).getByText("42", { exact: true })).toBeVisible();
    expect(screen.getByText("Complex(address,uint256,bool,(address,uint16),uint256[],uint256[][])")).toBeVisible();
    expect(screen.getByText("Revert data", { exact: true })).toBeVisible();
    const statusRow = screen.getByText("Status", { exact: true }).closest(".transaction-detail-row");
    const failureRow = screen.getByText("Failure reason", { exact: true }).closest(".transaction-detail-row");
    expect(statusRow?.nextElementSibling).toBe(failureRow);
    expect(requested).toContain(`/api/v1/transactions/${transactionHash}/failure`);

    await userEvent.setup().click(screen.getByRole("button", { name: "切换到中文" }));
    expect(await screen.findByRole("table", { name: "失败参数" })).toBeVisible();
    expect(screen.getByText("失败原因", { exact: true })).toBeVisible();
  });

  it("renders Solidity Panic as concise error text without an ABI table", async () => {
    let failureRequests = 0;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/transactions/${transactionHash}`) {
        return envelope({
          hash: transactionHash, block_hash: canonicalHash, block_number: "12",
          transaction_index: 0, from: address, to: delegatedAddress, nonce: "1", value: "0",
          gas: "100000", input: "0x12345678", status: "failed", canonical: true,
          finality: "safe", completeness: completeness(),
        });
      }
      if (url.pathname === `/api/v1/transactions/${transactionHash}/failure`) {
        failureRequests += 1;
        return envelope({
          chain_id: "1", block_number: "12", block_hash: canonicalHash,
          transaction_hash: transactionHash, transaction_index: "0", state: "complete",
          error: "execution reverted", revert_data: `0x4e487b71${"0".repeat(62)}12`,
          decoding: {
            status: "decoded", error_name: "Panic", signature: "Panic(uint256)",
            reason: "division or modulo by zero",
            arguments: [{ name: "code", type: "uint256", value: "18", components: [] }],
            candidates: [], abi_source: { kind: "builtin" },
          },
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);
    expect(await screen.findByText("division or modulo by zero")).toBeVisible();
    expect(screen.queryByText("Panic(uint256)", { exact: true })).not.toBeInTheDocument();
    expect(screen.queryByRole("table", { name: "Failure arguments" })).not.toBeInTheDocument();
    expect(screen.queryByText("code", { exact: true })).not.toBeInTheDocument();
    expect(screen.queryByText("18", { exact: true })).not.toBeInTheDocument();
    expect(screen.queryByText("Revert data", { exact: true })).not.toBeInTheDocument();
    expect(failureRequests).toBe(1);
  });

  it("renders Solidity Error string as concise revert text without an ABI table", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/transactions/${transactionHash}`) {
        return envelope({
          hash: transactionHash, block_hash: canonicalHash, block_number: "12",
          transaction_index: 0, from: address, to: delegatedAddress, nonce: "1", value: "0",
          gas: "100000", input: "0x12345678", status: "failed", canonical: true,
          finality: "safe", completeness: completeness(),
        });
      }
      if (url.pathname === `/api/v1/transactions/${transactionHash}/failure`) {
        return envelope({
          chain_id: "1", block_number: "12", block_hash: canonicalHash,
          transaction_hash: transactionHash, transaction_index: "0", state: "complete",
          error: "execution reverted", revert_data: "0x08c379a0",
          decoding: {
            status: "decoded", error_name: "Error", signature: "Error(string)",
            reason: "insufficient balance",
            arguments: [{ name: "message", type: "string", value: "insufficient balance", components: [] }],
            candidates: [], abi_source: { kind: "builtin" },
          },
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);
    expect(await screen.findByText("insufficient balance")).toBeVisible();
    expect(screen.queryByText("Error(string)", { exact: true })).not.toBeInTheDocument();
    expect(screen.queryByRole("table", { name: "Failure arguments" })).not.toBeInTheDocument();
    expect(screen.queryByText("message", { exact: true })).not.toBeInTheDocument();
    expect(screen.queryByText("Revert data", { exact: true })).not.toBeInTheDocument();
  });

  it("refetches a mismatched failure identity once and then fails closed", async () => {
    let failureRequests = 0;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/transactions/${transactionHash}`) {
        return envelope({
          hash: transactionHash, block_hash: canonicalHash, block_number: "12",
          transaction_index: 0, from: address, to: delegatedAddress, nonce: "1", value: "0",
          gas: "100000", input: "0x12345678", status: "failed", canonical: true,
          finality: "safe", completeness: completeness(),
        });
      }
      if (url.pathname === `/api/v1/transactions/${transactionHash}/failure`) {
        failureRequests += 1;
        return envelope({
          chain_id: "1", block_number: "12", block_hash: orphanHash,
          transaction_hash: transactionHash, transaction_index: "0", state: "complete",
          error: "execution reverted",
          decoding: { status: "unknown", arguments: [], candidates: [] },
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);
    await waitFor(() => expect(failureRequests).toBe(2));
    expect(await screen.findByText(
      "The canonical transaction inclusion changed. Refreshing this tab will load the new block identity.",
    )).toBeVisible();
    expect(screen.queryByRole("table", { name: "Failure arguments" })).not.toBeInTheDocument();
  });

  it("renders transaction type 2 as a semantic label", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      const meta = { request_id: "tx-copy-web-test", chain_id: "1" };
      if (path === "/api/v1/config") return configResponse();
      if (path === `/api/v1/transactions/${transactionHash}`) {
        return Response.json({
          data: { kind: "included", transaction: {
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
          } },
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
          data: { kind: "included", transaction: {
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
          } },
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
          data: { kind: "included", transaction: {
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
          } },
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
          data: { kind: "included", transaction: {
            hash: transactionHash,
            block_hash: canonicalHash,
            block_number: "12",
            transaction_index: 0,
            from: address,
            to: address,
              nonce: "1",
              value: "2100000000000000000",
              gas: "567028",
              gas_used: "430551",
              base_fee_per_gas: "112489733",
              effective_gas_price: "2000000000",
              tx_fee_wei: "42000000000000",
              burned_wei: "21000000000000",
              gas_price: "1000000000",
              max_fee_per_gas: "151663696",
              max_priority_fee_per_gas: "28319880",
              type: "2",
              input: "0x",
              status: "success",
              canonical: true,
              finality: "safe",
            completeness: completeness(),
          } },
          meta,
        });
      }
      if (path === `/api/v1/transactions/${transactionHash}/trace`) {
        return Response.json({
          data: {
            state: "complete",
            frames: [{
              call_type: "CALL",
              value: "1000000000000000000",
              path: [],
              parent_path: [],
              depth: 0,
              from: address,
              to: address,
              direct_reverted: false,
              reverted: false,
              execution: {
                context_address: address,
                address,
                code_hash: `0x${"11".repeat(32)}`,
                resolution: "direct",
              },
              decoding: {
                kind: "function",
                status: "decoded",
                function_name: "receive",
                signature: "receive()",
                inputs: [],
                output_status: "empty",
                outputs: [],
                candidates: ["receive()"],
                confidence: "verified",
                abi_source: { kind: "exact_address", address, code_hash: `0x${"11".repeat(32)}` },
              },
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
    const gasUsageLabel = await screen.findByText("Gas Limit & Usage by Txn");
    const gasUsageRow = gasUsageLabel.closest(".detail-item") as HTMLElement | null;
    if (!gasUsageRow) throw new Error("gas usage detail row missing");
    expect(within(gasUsageRow).getByText("567,028 | 430,551 (75.93%)")).toBeVisible();

    const gasFeesLabel = await screen.findByText("Gas Fees");
    const gasFeesRow = gasFeesLabel.closest(".detail-item") as HTMLElement | null;
    if (!gasFeesRow) throw new Error("gas fee settings row missing");
    expect(gasFeesRow).toHaveTextContent("Base: 0.112489733 Gwei");
    expect(gasFeesRow).toHaveTextContent("Max: 0.151663696 Gwei");
    expect(gasFeesRow).toHaveTextContent("Max Priority: 0.02831988 Gwei");

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
    expect(screen.getByText("receive()", { exact: true })).toBeVisible();
    expect(screen.queryByText("Unknown function selector")).not.toBeInTheDocument();
  });

  it("uses gas price for legacy max and max-priority fee settings", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = requestURL(input).pathname;
      if (path === "/api/v1/config") return configResponse();
      if (path === `/api/v1/transactions/${transactionHash}`) {
        return envelope({
          hash: transactionHash, block_hash: canonicalHash, block_number: "12",
          transaction_index: 0, from: address, to: address, nonce: "1", value: "0",
          gas: "21000", gas_used: "21000", gas_price: "151663696",
          base_fee_per_gas: "112489733", type: "0", input: "0x", status: "success",
          canonical: true, finality: "safe", completeness: completeness(),
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);
    await userEvent.setup().click(await screen.findByText("More details"));
    const gasFeesRow = screen.getByText("Gas Fees").closest(".detail-item") as HTMLElement | null;
    if (!gasFeesRow) throw new Error("legacy gas fee settings row missing");
    expect(gasFeesRow).toHaveTextContent("Base: 0.112489733 Gwei");
    expect(gasFeesRow).toHaveTextContent("Max: 0.151663696 Gwei");
    expect(gasFeesRow).toHaveTextContent("Max Priority: 0.151663696 Gwei");
  });

  it("shows blob base and max fees in both Overview and Blob", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = requestURL(input).pathname;
      if (path === "/api/v1/config") return configResponse();
      if (path === `/api/v1/transactions/${transactionHash}`) {
        return envelope({
          hash: transactionHash, block_hash: canonicalHash, block_number: "12",
          transaction_index: 0, from: address, to: address, nonce: "1", value: "0",
          gas: "21000", gas_used: "21000", base_fee_per_gas: "112489733",
          blob_base_fee_per_gas: "1000000", max_fee_per_gas: "151663696",
          max_priority_fee_per_gas: "28319880", max_fee_per_blob_gas: "1000000000",
          access_list: [], blob_versioned_hashes: [canonicalHash], type: "3", input: "0x",
          status: "success", canonical: true, finality: "safe", completeness: completeness(),
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);
    const user = userEvent.setup();
    await user.click(await screen.findByText("More details"));
    const overviewBlobFees = screen.getByText("Blob Gas Fees").closest(".detail-item") as HTMLElement | null;
    if (!overviewBlobFees) throw new Error("overview blob fee settings row missing");
    expect(overviewBlobFees).toHaveTextContent("Blob Base Fee: 0.001 Gwei");
    expect(overviewBlobFees).toHaveTextContent("Max: 1 Gwei");

    await user.click(screen.getByRole("tab", { name: "Blob" }));
    const blobPanel = await screen.findByRole("tabpanel");
    expect(blobPanel).toHaveTextContent("Blob Base Fee: 0.001 Gwei");
    expect(blobPanel).toHaveTextContent("Max: 1 Gwei");
    expect(within(blobPanel).getByText(canonicalHash)).toBeVisible();
  });

  it("reports an ordinary EOA transfer trace as having no executable code", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = requestURL(input).pathname;
      const meta = { request_id: "tx-transfer-trace-web-test", chain_id: "1" };
      if (path === "/api/v1/config") return configResponse();
      if (path === `/api/v1/transactions/${transactionHash}`) {
        return Response.json({
          data: { kind: "included", transaction: {
            hash: transactionHash,
            block_hash: canonicalHash,
            block_number: "12",
            transaction_index: 0,
            from: address,
            to: delegatedAddress,
            nonce: "1",
            value: "1000000000000000000",
            gas: "21000",
            gas_price: "1000000000",
            type: "2",
            input: "0x",
            status: "success",
            canonical: true,
            finality: "safe",
            completeness: completeness(),
          } },
          meta,
        });
      }
      if (path === `/api/v1/transactions/${transactionHash}/trace`) {
        return Response.json({
          data: {
            state: "complete",
            frames: [{
              call_type: "CALL",
              value: "1000000000000000000",
              path: [],
              parent_path: [],
              depth: 0,
              from: address,
              to: delegatedAddress,
              input: "0x",
              output: "0x",
              direct_reverted: false,
              reverted: false,
              execution: { context_address: delegatedAddress, resolution: "empty" },
              decoding: {
                kind: "function",
                status: "not_applicable",
                inputs: [],
                output_status: "not_applicable",
                outputs: [],
                candidates: [],
                warning: "call execution code is empty",
              },
            }],
          },
          meta,
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);
    await userEvent.setup().click(await screen.findByRole("tab", { name: "Trace" }));
    expect((await screen.findAllByText("No executable code"))[0]).toBeVisible();
    expect(screen.queryByText("Unknown function selector")).not.toBeInTheDocument();

    await userEvent.setup().click(screen.getByRole("button", { name: "切换到中文" }));
    expect((await screen.findAllByText("无可执行代码"))[0]).toBeVisible();
    expect(screen.queryByText("未知函数选择器")).not.toBeInTheDocument();
  });

  it("shows successful internal ETH transfers in a lazy tab before token transfers", async () => {
    const createdAddress = `0x${"55".repeat(20)}`;
    const requestedPaths: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      requestedPaths.push(url.pathname);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/transactions/${transactionHash}`) {
        return envelope({
          hash: transactionHash, block_hash: canonicalHash, block_number: "12",
          transaction_index: 0, from: address, to: delegatedAddress, nonce: "1",
          value: "0", gas: "21000", input: "0x", status: "success",
          canonical: true, finality: "safe", completeness: completeness(),
        });
      }
      if (url.pathname === `/api/v1/transactions/${transactionHash}/internal-transactions`) {
        const cursor = url.searchParams.get("cursor");
        return envelope({
          state: "complete", chain_id: "1", block_number: "12",
          block_hash: canonicalHash, transaction_hash: transactionHash,
          transaction_index: "0",
          items: cursor === "internal-next" ? [{
            path: [1], depth: 1, call_type: "CREATE2", from: address,
            created_address: createdAddress, value: "2000000000000000000",
          }] : [{
            path: [0], depth: 1, call_type: "CALL", from: address,
            to: delegatedAddress, value: "1250000000000000000",
          }],
        }, cursor ? {} : { next_cursor: "internal-next" });
      }
      if (url.pathname === `/api/v1/transactions/${transactionHash}/token-transfers`) {
        return envelope({
          state: "complete", chain_id: "1", block_number: "12",
          block_hash: canonicalHash, transaction_hash: transactionHash,
          transaction_index: "0", items: [{
            chain_id: "1", block_number: "12", block_hash: canonicalHash,
            log_index: "0", sub_index: "0", transaction_hash: transactionHash,
            token_address: createdAddress, standard: "erc20", kind: "transfer",
            from: address, to: delegatedAddress, amount: "1234500", decimals: 6,
            confidence: "high",
          }],
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);

    expect(screen.queryByRole("heading", { name: "Internal Transactions" }))
      .not.toBeInTheDocument();
    expect(requestedPaths).not.toContain(
      `/api/v1/transactions/${transactionHash}/internal-transactions`,
    );
    const internalTab = await screen.findByRole("tab", { name: "Internal Transactions" });
    const tokenTab = screen.getByRole("tab", { name: "Token transfers" });
    expect(internalTab.nextElementSibling).toBe(tokenTab);

    const user = userEvent.setup();
    await user.click(internalTab);
    const section = (await screen.findByRole("heading", { name: "Internal Transactions" }))
      .closest("section");
    if (!section) throw new Error("internal-transactions section is missing");
    expect(within(section).getByText("CALL", { exact: true })).toBeVisible();
    expect(within(section).getByText("1.25", { exact: true })).toBeVisible();
    expect(within(section).getByRole("link", { name: shorten(delegatedAddress) }))
      .toHaveAttribute("href", `/address/${delegatedAddress}`);
    expect(requestedPaths).not.toContain(`/api/v1/transactions/${transactionHash}/trace`);

    await user.click(within(section).getByRole("button", { name: "Next page" }));
    expect(await within(section).findByText("CREATE2", { exact: true })).toBeVisible();
    expect(within(section).getByText("2", { exact: true })).toBeVisible();
    expect(within(section).getByText("Created address", { exact: true })).toBeVisible();

    await user.click(screen.getByRole("button", { name: "切换到中文" }));
    expect(await screen.findByRole("heading", { name: "内部交易" })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Switch to English" }));
    await user.click(tokenTab);
    expect(await screen.findByText("1.2345", { exact: true })).toBeVisible();
  });

  it("renders transaction confirmations and block timestamp in /tx detail", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = requestURL(input).pathname;
      const meta = { request_id: "tx-copy-web-test", chain_id: "1" };
      if (path === "/api/v1/config") return configResponse();
      if (path === `/api/v1/transactions/${transactionHash}`) {
        return Response.json({
          data: { kind: "included", transaction: {
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
          } },
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
          has_delegation_history: false,
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

  it("renders Genesis for both EOA origin fields without address or transaction links", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = requestURL(input).pathname;
      if (path === "/api/v1/config") return configResponse();
      if (path === `/api/v1/addresses/${address}`) {
        return envelope({
          address,
          type: "eoa",
          balance: "0",
          nonce: "0",
          at_block: canonicalHash,
          completeness: completeness(),
          has_delegation_history: false,
          origin: { kind: "funding", state: "genesis" },
        });
      }
      if (path === `/api/v1/addresses/${address}/nfts`) return envelope([]);
      return notFound();
    }));

    renderExplorer(`/address/${address}`);

    const fundedBy = await screen.findByText("Funded by");
    const fundedByRow = fundedBy.closest(".detail-item") as HTMLElement | null;
    if (!fundedByRow) throw new Error("funded-by row is missing");
    expect(within(fundedByRow).getByText("Genesis")).toBeVisible();
    const fundingTransaction = screen.getByText("Funding transaction");
    const fundingTransactionRow = fundingTransaction.closest(".detail-item") as HTMLElement | null;
    if (!fundingTransactionRow) throw new Error("funding-transaction row is missing");
    expect(within(fundingTransactionRow).getByText("Genesis")).toBeVisible();
    expect(within(fundedByRow).queryByRole("link")).not.toBeInTheDocument();
    expect(within(fundingTransactionRow).queryByRole("link")).not.toBeInTheDocument();
  });

  it("renders Genesis for predeploy contract origin fields", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = requestURL(input).pathname;
      if (path === "/api/v1/config") return configResponse();
      if (path === `/api/v1/addresses/${address}`) {
        return envelope({
          address,
          name: "Activity contract",
          type: "contract",
          balance: "0",
          nonce: "0",
          at_block: canonicalHash,
          completeness: completeness(),
          code_hash: canonicalHash,
          has_delegation_history: false,
          origin: { kind: "contract_creation", state: "genesis" },
        });
      }
      if (path === `/api/v1/addresses/${address}/nfts`) return envelope([]);
      return notFound();
    }));

    renderExplorer(`/address/${address}`);

    const creator = await screen.findByText("Contract creator");
    const creatorRow = creator.closest(".detail-item") as HTMLElement | null;
    if (!creatorRow) throw new Error("contract-creator row is missing");
    expect(within(creatorRow).getByText("Genesis")).toBeVisible();
    const creationTransaction = screen.getByText("Creation transaction");
    const creationTransactionRow = creationTransaction.closest(".detail-item") as HTMLElement | null;
    if (!creationTransactionRow) throw new Error("creation-transaction row is missing");
    expect(within(creationTransactionRow).getByText("Genesis")).toBeVisible();
    expect(within(creatorRow).queryByRole("link")).not.toBeInTheDocument();
    expect(within(creationTransactionRow).queryByRole("link")).not.toBeInTheDocument();
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
          has_delegation_history: false,
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
          method: "transferTokensWithAnIntentionallyLongMethodName",
          method_signature: "transferTokensWithAnIntentionallyLongMethodName(address,uint256)",
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
      if (url.pathname === `/api/v1/addresses/${address}/withdrawals`) {
        if (url.searchParams.get("cursor") === "withdrawal-next") {
          return envelope([{
            index: "1",
            validator_index: "101",
            address,
            amount: "1000000000",
            block_number: "9",
            block_hash: transactionHash,
            block_timestamp: "2026-07-26T08:00:00Z",
          }]);
        }
        return envelope([{
          index: "10",
          validator_index: "110",
          address,
          amount: "3200000000",
          block_number: "12",
          block_hash: canonicalHash,
          block_timestamp: "2026-07-28T08:00:00Z",
        }, {
          index: "2",
          validator_index: "102",
          address,
          amount: "1",
          block_number: "10",
          block_hash: olderHash,
          block_timestamp: "2026-07-27T08:00:00Z",
        }], { next_cursor: "withdrawal-next" });
      }
      if (url.pathname === `/api/v1/addresses/${address}/erc20-transfers`) {
        return envelope([{
          block_hash: canonicalHash,
          block_number: "12",
          block_timestamp: "2026-07-28T08:00:00Z",
          transaction_hash: transactionHash,
          transaction_index: "0",
          log_index: "1",
          sub_index: "0",
          token_address: createdAddress,
          standard: "erc20",
          kind: "transfer",
          from: address,
          to: createdAddress,
          amount: "1234500",
          decimals: 6,
          confidence: "high",
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

    const contractLink = await screen.findByRole("link", { name: "Contract" });
    const addressTabs = screen.getByRole("navigation", { name: "Address activity sections" });
    expect(within(addressTabs).getAllByRole("link").slice(0, 6).map((link) => link.textContent)).toEqual([
      "Transactions", "Internal Transactions", "Withdrawals", "ERC-20 Transfers", "NFT Transfers", "Assets",
    ]);
    expect(contractLink).toHaveAttribute(
      "href",
      `/address/${address}#code`,
    );
    expect(contractLink).toHaveClass("transaction-tab");
    expect(contractLink).not.toHaveClass("active", "contract-entry");
    expect(screen.getByRole("heading", { level: 1, name: "Contract" })).toBeVisible();
    const summary = screen.getByRole("heading", { name: "Address summary" }).closest("section");
    if (!summary) throw new Error("address summary is missing");
    expect(within(summary).queryByText("Activity contract")).not.toBeInTheDocument();
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
    const transactionTable = transactionRow.closest("table");
    if (!transactionTable) throw new Error("address transaction table is missing");
    expect(within(transactionTable).getAllByRole("columnheader").map((header) => header.textContent)).toEqual([
      "Hash", "Method", "Block", "Timestamp", "Status", "From", "Direction", "To", "Value (ETH)", "Finality",
    ]);
    const transactionCells = within(transactionRow).getAllByRole("cell");
    expect(transactionCells).toHaveLength(10);
    expect(transactionCells[4]?.querySelector('[data-status="success"]')).not.toBeNull();
    expect(transactionCells[4]?.querySelector(".transaction-status-group")?.childElementCount).toBe(1);
    expect(within(transactionCells[9]!).getByText("Safe", { exact: true })).toBeVisible();
    expect(screen.getByRole("columnheader", { name: "Method" })).toBeVisible();
    expect(screen.getByRole("columnheader", { name: "Direction" })).toBeVisible();
    expect(screen.queryByRole("columnheader", { name: "table.direction" })).not.toBeInTheDocument();
    const addressMethod = within(transactionRow).getByText(
      "transferTokensWithAnIntentionallyLongMethodName",
    );
    expect(addressMethod).toHaveClass("transaction-method");
    expect(addressMethod).toHaveAttribute(
      "aria-label",
      "transferTokensWithAnIntentionallyLongMethodName(address,uint256)",
    );
    expect(addressMethod).toHaveAttribute(
      "title",
      "transferTokensWithAnIntentionallyLongMethodName(address,uint256)",
    );
    expect(
      within(transactionRow).queryByRole("link", { name: shorten(address) }),
    ).not.toBeInTheDocument();
    expect(
      within(transactionRow).getAllByRole("button", { name: "Copy" }),
    ).toHaveLength(2);
    expect(requestedPaths).toContain(`/api/v1/addresses/${address}/transactions`);
    expect(requestedPaths).not.toContain(`/api/v1/addresses/${address}/internal-transactions`);
    expect(requestedPaths).not.toContain(`/api/v1/addresses/${address}/withdrawals`);
    expect(requestedPaths).not.toContain(`/api/v1/addresses/${address}/nfts`);
    expect(requestedPaths).not.toContain(`/api/v1/addresses/${address}/erc20-balances`);

    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "Internal Transactions" }));
    const createdLabel = await screen.findByText("Created address");
    expect(screen.queryByRole("columnheader", { name: "Method" })).not.toBeInTheDocument();
    expect(createdLabel).toBeVisible();
    const internalRow = createdLabel.closest("tr");
    if (!internalRow) throw new Error("address internal transaction row is missing");
    expect(screen.queryByRole("columnheader", { name: "Finality" })).not.toBeInTheDocument();
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

    await user.click(screen.getByRole("link", { name: "Withdrawals" }));
    const withdrawalTable = await screen.findByRole("table", { name: "Withdrawals" });
    expect(within(withdrawalTable).queryByRole("columnheader", { name: "Finality" })).not.toBeInTheDocument();
    const withdrawalRows = within(withdrawalTable).getAllByRole("row").slice(1);
    expect(withdrawalRows).toHaveLength(2);
    expect(withdrawalRows[0]).toHaveTextContent("10");
    expect(withdrawalRows[1]).toHaveTextContent("2");
    expect(within(withdrawalRows[0]!).getByText("3.2 Ether")).toBeVisible();
    expect(within(withdrawalRows[1]!).getByText("0.000000001 Ether")).toBeVisible();
    expect(within(withdrawalRows[0]!).getByRole("link", { name: "12" })).toHaveAttribute(
      "href", `/blocks/${canonicalHash}`,
    );
    expect(requestedPaths).toContain(`/api/v1/addresses/${address}/withdrawals`);
    await user.click(screen.getByRole("button", { name: "Next page" }));
    expect(await screen.findByText("Page 2", { exact: true })).toBeVisible();
    expect(within(screen.getByRole("table", { name: "Withdrawals" })).getByText("1 Ether", { exact: true })).toBeVisible();

    await user.click(screen.getByRole("link", { name: "ERC-20 Transfers" }));
    expect(await screen.findByText("1.2345", { exact: true })).toBeVisible();
    expect(screen.queryByRole("columnheader", { name: "Finality" })).not.toBeInTheDocument();
    expect(requestedPaths).toContain(`/api/v1/addresses/${address}/erc20-transfers`);

    await user.click(screen.getByRole("link", { name: "NFT Transfers" }));
    expect(screen.queryByRole("columnheader", { name: "Finality" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("link", { name: "Assets" }));
    expect(await screen.findByText(/No positive NFT balances were observed/)).toBeVisible();
    expect(await screen.findByText("Asset Token")).toBeVisible();
    expect(screen.queryByRole("columnheader", { name: "Finality" })).not.toBeInTheDocument();
    expect(screen.getByText("123.45 AST")).toBeVisible();
    expect(requestedPaths).toContain(`/api/v1/addresses/${address}/nfts`);
    expect(requestedPaths).toContain(`/api/v1/addresses/${address}/erc20-balances`);

  });

  it("deep-links an empty address withdrawal history without loading transactions", async () => {
    const requestedPaths: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      requestedPaths.push(url.pathname);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/addresses/${address}`) {
        return envelope({
          address,
          type: "eoa",
          balance: "0",
          nonce: "0",
          at_block: canonicalHash,
          completeness: completeness(),
          has_delegation_history: false,
        });
      }
      if (url.pathname === `/api/v1/addresses/${address}/withdrawals`) return envelope([]);
      return notFound();
    }));

    renderExplorer(`/address/${address}?tab=withdrawals`);

    expect(await screen.findByText("This address has no withdrawals in this snapshot.")).toBeVisible();
    expect(screen.getByRole("link", { name: "Withdrawals" })).toHaveAttribute("aria-current", "page");
    expect(requestedPaths).toContain(`/api/v1/addresses/${address}/withdrawals`);
    expect(requestedPaths).not.toContain(`/api/v1/addresses/${address}/transactions`);
  });

  it("renders delegated-account panels with the shared detail and pagination contracts", async () => {
    const nextCursor = "delegation-next";
    const requestedPaths: string[] = [];
    const delegation = {
      authority: delegatedAddress,
      status: "delegated" as const,
      chain_id: "1",
      block_number: "100",
      block_hash: delegationBlockHash,
      delegate: delegateAddress,
      delegate_code_hash: delegateCodeHash,
    };
    const artifact = {
      kind: "verification_success" as const,
      resolution: "exact_address" as const,
      target: {
        chain_id: "1",
        address: delegateAddress,
        code_hash: delegateCodeHash,
        block_number: "100",
        block_hash: delegationBlockHash,
      },
      source: {
        address: delegateAddress,
        code_hash: delegateCodeHash,
        valid_from_block: "1",
        created_at: "2026-08-01T00:00:00Z",
      },
      language: "solidity" as const,
      compiler_version: "0.8.30",
      file_name: "Delegate.sol",
      contract_name: "Delegate",
      settings: {},
      sources: {},
      compilation_artifacts: {},
      creation_code_artifacts: {},
      runtime_code_artifacts: {},
      libraries: {},
      is_blueprint: false,
      abi: [{
        type: "function",
        name: "setValue",
        stateMutability: "nonpayable",
        inputs: [{ name: "value", type: "uint256" }],
        outputs: [],
      }],
    };
    const olderHistoryItem = {
      authority: delegatedAddress,
      kind: "delegated" as const,
      delegate: delegateAddress,
      block_number: "100",
      block_hash: delegationBlockHash,
      transaction_hash: delegationTransactionHash,
      transaction_index: "0",
      authorization_index: "0",
    };
    const newestHistoryItem = {
      ...olderHistoryItem,
      kind: "redelegated" as const,
      previous_delegate: delegateAddress,
      block_number: "101",
      block_hash: canonicalHash,
      transaction_hash: transactionHash,
    };

    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      requestedPaths.push(url.pathname);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/addresses/${delegatedAddress}`) {
        return envelope({
          address: delegatedAddress,
          type: "delegated_eoa",
          balance: "1000000000000000000",
          nonce: "4",
          at_block: delegationBlockHash,
          code_hash: delegateCodeHash,
          completeness: completeness(),
          has_delegation_history: true,
        });
      }
      if (url.pathname === `/api/v1/addresses/${delegatedAddress}/delegation`) {
        return envelope(delegation);
      }
      if (url.pathname === `/api/v1/addresses/${delegatedAddress}/delegations`) {
        return url.searchParams.get("cursor") === nextCursor
          ? envelope([olderHistoryItem])
          : envelope([newestHistoryItem], { next_cursor: nextCursor });
      }
      if (url.pathname === `/api/v1/contracts/${delegateAddress}/verification`) {
        return envelope(artifact);
      }
      return notFound();
    }));

    renderExplorer(`/address/${delegatedAddress}`);

    const addressTabs = await screen.findByRole("navigation", { name: "Address activity sections" });
    const delegationEntry = await within(addressTabs).findByRole("link", { name: "Delegation" });
    expect(delegationEntry).toHaveAttribute(
      "href",
      `/address/${delegatedAddress}?tab=delegation#code`,
    );
    expect(delegationEntry).toHaveClass("transaction-tab");
    expect(delegationEntry).not.toHaveClass("active", "contract-entry");
    const user = userEvent.setup();
    await user.click(delegationEntry);
    const activeDelegationEntry = within(addressTabs).getByRole("link", { name: "Delegation" });
    expect(activeDelegationEntry).toHaveClass("transaction-tab", "active");
    expect(activeDelegationEntry).not.toHaveClass("contract-entry");

    const bindingHeading = await screen.findByRole("heading", { name: "EIP-7702 delegation binding" });
    const delegateLink = await screen.findByRole("link", { name: delegateAddress });
    const bindingPanel = bindingHeading.closest("section");
    if (!bindingPanel) throw new Error("delegation binding panel is missing");
    expect(bindingPanel).toHaveClass("panel", "detail-card");
    expect(bindingPanel.querySelector(".detail-grid")).not.toBeNull();
    expect(bindingPanel.querySelectorAll(".detail-item")).toHaveLength(5);
    expect(delegateLink).toHaveAttribute(
      "href",
      `/address/${delegateAddress}?tab=transactions`,
    );

    const tabs = screen.getByRole("tablist", { name: "Delegated account sections" });
    expect(within(tabs).getByRole("tab", { name: "Code" })).toHaveAttribute("aria-selected", "true");
    expect(await within(tabs).findByRole("tab", { name: "Read contract" })).toBeVisible();
    expect(within(tabs).getByRole("tab", { name: "Write contract" })).toBeVisible();
    expect(within(tabs).getByRole("tab", { name: "Delegation history" })).toBeVisible();
    for (const tab of within(tabs).getAllByRole("tab")) {
      const panelID = tab.getAttribute("aria-controls");
      expect(panelID).toBeTruthy();
      const panel = document.getElementById(panelID!);
      expect(panel).toHaveAttribute("role", "tabpanel");
      expect(panel).toHaveAttribute("aria-labelledby", tab.id);
      if (tab.getAttribute("aria-selected") === "true") {
        expect(panel).not.toHaveAttribute("hidden");
      } else {
        expect(panel).toHaveAttribute("hidden");
      }
    }
    expect(requestedPaths).not.toContain(`/api/v1/addresses/${delegatedAddress}/delegations`);

    await user.click(within(tabs).getByRole("tab", { name: "Write contract" }));
    expect(await screen.findByText("setValue(uint256)")).toBeVisible();
    expect(requestedPaths).not.toContain(`/api/v1/addresses/${delegatedAddress}/delegations`);

    await user.click(within(tabs).getByRole("tab", { name: "Delegation history" }));
    const historyNavigation = await screen.findByRole("navigation", { name: "Delegation history" });
    expect(requestedPaths).toContain(`/api/v1/addresses/${delegatedAddress}/delegations`);
    expect(await screen.findByText("Re-delegated")).toBeVisible();
    expect(within(historyNavigation).getByRole("button", { name: "Previous page" })).toBeDisabled();
    await user.click(within(historyNavigation).getByRole("button", { name: "Next page" }));
    expect(await screen.findByText("Delegated", { exact: true })).toBeVisible();
  });

  it("keeps cleared delegation history discoverable without eagerly loading current binding", async () => {
    const requestedPaths: string[] = [];
    const clearedHistoryItem = {
      authority: clearedDelegationAddress,
      kind: "cleared" as const,
      delegate: "0x0000000000000000000000000000000000000000",
      previous_delegate: delegateAddress,
      block_number: "102",
      block_hash: canonicalHash,
      transaction_hash: transactionHash,
      transaction_index: "0",
      authorization_index: "0",
    };
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      requestedPaths.push(url.pathname);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/addresses/${clearedDelegationAddress}`) {
        return envelope({
          address: clearedDelegationAddress,
          type: "eoa",
          balance: "0",
          nonce: "5",
          at_block: canonicalHash,
          completeness: completeness(),
          has_delegation_history: true,
        });
      }
      if (url.pathname === `/api/v1/addresses/${clearedDelegationAddress}/transactions`) {
        return envelope([]);
      }
      if (url.pathname === `/api/v1/addresses/${clearedDelegationAddress}/delegations`) {
        return envelope([clearedHistoryItem]);
      }
      if (url.pathname === `/api/v1/addresses/${clearedDelegationAddress}/delegation`) {
        return envelope({
          authority: clearedDelegationAddress,
          status: "not_delegated",
          chain_id: "1",
          block_number: "102",
          block_hash: canonicalHash,
        });
      }
      return notFound();
    }));

    renderExplorer(`/address/${clearedDelegationAddress}`);

    const addressTabs = await screen.findByRole("navigation", { name: "Address activity sections" });
    const delegationEntry = await within(addressTabs).findByRole("link", { name: "Delegation" });
    expect(delegationEntry).toHaveAttribute(
      "href",
      `/address/${clearedDelegationAddress}?tab=delegation#history`,
    );
    expect(requestedPaths).not.toContain(`/api/v1/addresses/${clearedDelegationAddress}/delegations`);

    const user = userEvent.setup();
    await user.click(delegationEntry);

    expect(await screen.findByRole("heading", { name: "Delegation history" })).toBeVisible();
    expect(await screen.findByText("Cleared")).toBeVisible();
    expect(requestedPaths).toContain(`/api/v1/addresses/${clearedDelegationAddress}/delegations`);
    expect(requestedPaths).not.toContain(`/api/v1/addresses/${clearedDelegationAddress}/delegation`);
    expect(requestedPaths).not.toContain(`/api/v1/contracts/${delegateAddress}/verification`);
    const delegatedTabs = screen.getByRole("tablist", { name: "Delegated account sections" });
    expect(within(delegatedTabs).getByRole("tab", { name: "Delegation history" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    const statusTab = within(delegatedTabs).getByRole("tab", { name: "Status" });
    await user.click(statusTab);
    expect(await screen.findByRole("heading", { name: "Delegation status" })).toBeVisible();
    expect(await screen.findByText("Not delegated", { exact: true })).toBeVisible();
    expect(screen.getByText(/currently has no active EIP-7702 delegation/)).toBeVisible();
    expect(screen.getByRole("link", { name: "102" })).toHaveAttribute("href", `/blocks/${canonicalHash}`);
    expect(screen.queryByRole("heading", { name: "Verified artifact" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "View delegation history" })).not.toBeInTheDocument();
    expect(requestedPaths).toContain(`/api/v1/addresses/${clearedDelegationAddress}/delegation`);
    expect(requestedPaths).not.toContain(`/api/v1/contracts/${delegateAddress}/verification`);
    await user.click(screen.getByRole("button", { name: "切换到中文" }));
    expect(within(delegatedTabs).getByRole("tab", { name: "状态" })).toBeVisible();
  });

  it("does not report an unavailable delegation binding as cleared", async () => {
    const requestedPaths: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      requestedPaths.push(url.pathname);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/addresses/${clearedDelegationAddress}`) {
        return envelope({
          address: clearedDelegationAddress,
          type: "eoa",
          balance: "0",
          nonce: "5",
          at_block: canonicalHash,
          completeness: completeness(),
          has_delegation_history: true,
        });
      }
      if (url.pathname === `/api/v1/addresses/${clearedDelegationAddress}/delegation`) {
        return envelope({
          authority: clearedDelegationAddress,
          status: "unavailable",
          reason: "state_unavailable",
          chain_id: "1",
          block_number: "103",
          block_hash: canonicalHash,
        });
      }
      return notFound();
    }));

    renderExplorer(`/address/${clearedDelegationAddress}?tab=delegation#code`);

    const tabs = await screen.findByRole("tablist", { name: "Delegated account sections" });
    expect(await within(tabs).findByRole("tab", { name: "Status" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(await screen.findByText("Unavailable", { exact: true })).toBeVisible();
    expect(screen.getByText(/It is not treated as cleared/)).toBeVisible();
    expect(screen.queryByText(/currently has no active EIP-7702 delegation/)).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Verified artifact" })).not.toBeInTheDocument();
    expect(requestedPaths).not.toContain(`/api/v1/contracts/${delegateAddress}/verification`);
  });

  it("uses the latest binding to replace stale delegated tabs with Status", async () => {
    const requestedPaths: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      requestedPaths.push(url.pathname);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/addresses/${delegatedAddress}`) {
        return envelope({
          address: delegatedAddress,
          type: "delegated_eoa",
          balance: "0",
          nonce: "5",
          at_block: delegationBlockHash,
          code_hash: delegateCodeHash,
          completeness: completeness(),
          has_delegation_history: true,
        });
      }
      if (url.pathname === `/api/v1/addresses/${delegatedAddress}/delegation`) {
        return envelope({
          authority: delegatedAddress,
          status: "not_delegated",
          chain_id: "1",
          block_number: "104",
          block_hash: canonicalHash,
        });
      }
      return notFound();
    }));

    renderExplorer(`/address/${delegatedAddress}?tab=delegation#read-contract`);

    const tabs = await screen.findByRole("tablist", { name: "Delegated account sections" });
    expect(await within(tabs).findByRole("tab", { name: "Status" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(within(tabs).queryByRole("tab", { name: "Read contract" })).not.toBeInTheDocument();
    expect(within(tabs).queryByRole("tab", { name: "Write contract" })).not.toBeInTheDocument();
    expect(await screen.findByText("Not delegated", { exact: true })).toBeVisible();
    expect(requestedPaths).not.toContain(`/api/v1/contracts/${delegateAddress}/verification`);
  });

  it("renders ETH-formatted values on transactions list", async () => {
    const requestedPaths: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = requestURL(input).pathname;
      requestedPaths.push(path);
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
          input: "0xa9059cbb",
          method: "transferTokensWithAnIntentionallyLongMethodName",
          method_signature: "transferTokensWithAnIntentionallyLongMethodName(address,uint256)",
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
    expect(screen.getByRole("columnheader", { name: "Method" })).toBeVisible();
    const method = within(txRow).getByLabelText(
      "transferTokensWithAnIntentionallyLongMethodName(address,uint256)",
    );
    expect(method).toHaveTextContent("transferTokensWithAnIntentionallyLongMethodName");
    expect(method).toHaveClass("transaction-method");
    expect(method).toHaveAttribute(
      "title",
      "transferTokensWithAnIntentionallyLongMethodName(address,uint256)",
    );
    expect(requestedPaths).not.toContain(`/api/v1/transactions/${transactionHash}/calldata`);
  });

  it("renders localized Method fallbacks without deriving them in the browser", async () => {
    await i18n.changeLanguage("zh-CN");
    const requestedPaths: string[] = [];
    const hashes = [transactionHash, delegationTransactionHash, orphanHash, olderHash];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = requestURL(input).pathname;
      requestedPaths.push(path);
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
        const methods = ["Native Transfer", "Contract Creation", "0xdeadbeef", undefined];
        return envelope(hashes.map((hash, index) => ({
          hash,
          status: "success",
          block_hash: canonicalHash,
          block_number: "12",
          from: address,
          to: index === 1 ? null : address,
          transaction_index: index,
          nonce: String(index),
          value: "0",
          gas: "21000",
          gas_price: "1000000000",
          input: index === 0 ? "0x" : index === 1 ? "0x6000" : "0xdeadbeef",
          method: methods[index],
          completeness: completeness(),
          finality: "safe",
          canonical: true,
        })));
      }
      return notFound();
    }));

    renderExplorer("/transactions");

    expect(await screen.findByRole("columnheader", { name: "方法" })).toBeVisible();
    expect(screen.getByText("Native Transfer", { exact: true })).toBeVisible();
    expect(screen.getByText("Contract Creation", { exact: true })).toBeVisible();
    expect(screen.getByText("0xdeadbeef", { exact: true })).toBeVisible();
    const missingMethodRow = screen.getByText(shorten(olderHash)).closest("tr");
    if (!missingMethodRow) throw new Error("missing-method transaction row is absent");
    expect(within(missingMethodRow).getByText("—", { exact: true })).toBeVisible();
    expect(requestedPaths.some((path) => path.endsWith("/calldata"))).toBe(false);
  });

  it("clears a contract hash for an EOA and returns to transactions", async () => {
    const requestedPaths: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      requestedPaths.push(url.pathname);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/addresses/${address}`) {
        return envelope({
          address,
          type: "eoa",
          balance: "0",
          nonce: "0",
          at_block: canonicalHash,
          completeness: completeness(),
          has_delegation_history: false,
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
    expect(within(addressTabs).queryByRole("link", { name: "Delegation" })).not.toBeInTheDocument();
    expect(requestedPaths).not.toContain(`/api/v1/addresses/${address}/delegations`);
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

function mempoolTransaction(hash: string, overrides: Record<string, unknown> = {}) {
  return {
    hash,
    from: address,
    nonce: "7",
    value: "1000000000000000000",
    gas: "100000",
    max_fee_per_gas: "30000000000",
    max_priority_fee_per_gas: "1000000000",
    type: "2",
    input: "0x6000",
    first_seen_at: "2026-08-13T00:00:00Z",
    last_seen_at: "2026-08-13T00:00:00Z",
    expires_at: "2026-08-13T00:00:10Z",
    endpoint: "pending-primary",
    ...overrides,
  };
}

function envelope(data: unknown, meta: Record<string, unknown> = {}) {
  const responseData = includedTransactionFixture(data)
    ? { kind: "included", transaction: data }
    : data;
  return Response.json({
    data: responseData,
    meta: { request_id: "core-pages-test", chain_id: "1", ...meta },
  });
}

function includedTransactionFixture(data: unknown): data is Record<string, unknown> {
  if (!data || Array.isArray(data) || typeof data !== "object") return false;
  const candidate = data as Record<string, unknown>;
  return typeof candidate.hash === "string"
    && typeof candidate.from === "string"
    && typeof candidate.nonce === "string"
    && typeof candidate.gas === "string"
    && typeof candidate.input === "string"
    && typeof candidate.canonical === "boolean"
    && typeof candidate.finality === "string";
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
