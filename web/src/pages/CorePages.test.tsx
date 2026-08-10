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
    expect(requested).toContain(`/api/v1/blocks/${canonicalHash}/transactions`);
    const withdrawalsTab = within(tabs).getByRole("tab", { name: "Withdrawals" });
    expect(withdrawalsTab).toHaveAttribute("aria-selected", "false");
    expect(screen.queryByRole("heading", { name: "Withdrawals" })).not.toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(withdrawalsTab);
    expect(await screen.findByRole("heading", { name: "Withdrawals" })).toBeVisible();
    expect(screen.getByText("3,200,000,000 Gwei")).toBeVisible();
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
              { name: "recipient", type: "address", value: `0x${"44".repeat(20)}` },
              { name: "amount", type: "uint256", value: "12" },
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
    const writeText = vi.spyOn(navigator.clipboard, "writeText");
    try {
      await user.click(await screen.findByText("More details"));

      const decoded = await screen.findByRole("region", { name: "Decoded calldata · transfer(address,uint256)" });
      const raw = screen.getByRole("region", { name: "Raw calldata" });
      expect(within(decoded).getAllByText("transfer(address,uint256)")).toHaveLength(1);
      expect(within(decoded).getByRole("columnheader", { name: "Params" })).toBeVisible();
      expect(within(decoded).getByRole("columnheader", { name: "Type" })).toBeVisible();
      expect(within(decoded).getByRole("columnheader", { name: "Data" })).toBeVisible();
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
      expect(requested).not.toContain(`/api/v1/contracts/${address}/verification`);
      expect(requested).not.toContain(`/api/v1/contracts/${address}/proxy`);
    } finally {
      writeText.mockRestore();
    }
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
            inputs: [{ name: "value", type: "uint256", value: "42" }], candidates: [],
            confidence: "verified",
            abi_source: { kind: "exact_address", address: delegateAddress, code_hash: canonicalHash },
          },
        });
      }
      return notFound();
    }));

    renderExplorer(`/tx/${transactionHash}`);
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

  it("reports an ordinary EOA transfer trace as having no executable code", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = requestURL(input).pathname;
      const meta = { request_id: "tx-transfer-trace-web-test", chain_id: "1" };
      if (path === "/api/v1/config") return configResponse();
      if (path === `/api/v1/transactions/${transactionHash}`) {
        return Response.json({
          data: {
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
          },
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

    await user.click(screen.getByRole("link", { name: "ERC-20 Transfers" }));
    expect(await screen.findByText("1.2345", { exact: true })).toBeVisible();
    expect(requestedPaths).toContain(`/api/v1/addresses/${address}/erc20-transfers`);

    await user.click(screen.getByRole("link", { name: "Assets" }));
    expect(await screen.findByText(/No positive NFT balances were observed/)).toBeVisible();
    expect(await screen.findByText("Asset Token")).toBeVisible();
    expect(screen.getByText("123.45 AST")).toBeVisible();
    expect(requestedPaths).toContain(`/api/v1/addresses/${address}/nfts`);
    expect(requestedPaths).toContain(`/api/v1/addresses/${address}/erc20-balances`);

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
    const firstHistoryItem = {
      authority: delegatedAddress,
      kind: "delegated" as const,
      delegate: delegateAddress,
      block_number: "100",
      block_hash: delegationBlockHash,
      transaction_hash: delegationTransactionHash,
      transaction_index: "0",
      authorization_index: "0",
    };
    const secondHistoryItem = {
      ...firstHistoryItem,
      kind: "redelegated" as const,
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
          ? envelope([secondHistoryItem])
          : envelope([firstHistoryItem], { next_cursor: nextCursor });
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
    expect(within(historyNavigation).getByRole("button", { name: "Previous page" })).toBeDisabled();
    await user.click(within(historyNavigation).getByRole("button", { name: "Next page" }));
    expect(await screen.findByText("Re-delegated")).toBeVisible();
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

    await userEvent.setup().click(delegationEntry);

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
