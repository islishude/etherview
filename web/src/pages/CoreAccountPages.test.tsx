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

describe("core account and list pages", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
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


function _block(number: string, hash: string, canonical = true) {
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

function _mempoolTransaction(hash: string, overrides: Record<string, unknown> = {}) {
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
