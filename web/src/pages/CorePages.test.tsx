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
const _clearedDelegationAddress = `0x${"77".repeat(20)}`;
const _delegateAddress = "0x5FbDB2315678afecb367f032d93F642f64180aa3";
const _delegateCodeHash = `0x${"55".repeat(32)}`;
const _delegationBlockHash = `0x${"66".repeat(32)}`;
const _delegationTransactionHash = `0x${"bb".repeat(32)}`;

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
