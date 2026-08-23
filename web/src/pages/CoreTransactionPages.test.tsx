import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import i18n from "@/i18n";
import { makeRouter } from "@/router";
import { AuthProvider } from "@/auth/AuthProvider";
import { ThemeProvider } from "@/theme/ThemeProvider";
import { WalletProvider } from "@/wallet/WalletProvider";

const canonicalHash = `0x${"11".repeat(32)}`;
const _olderHash = `0x${"22".repeat(32)}`;
const orphanHash = `0x${"33".repeat(32)}`;
const parentHash = `0x${"00".repeat(32)}`;
const address = `0x${"44".repeat(20)}`;
const transactionHash = `0x${"aa".repeat(32)}`;
const delegatedAddress = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266";
const _clearedDelegationAddress = `0x${"77".repeat(20)}`;
const delegateAddress = "0x5FbDB2315678afecb367f032d93F642f64180aa3";
const delegateCodeHash = `0x${"55".repeat(32)}`;
const _delegationBlockHash = `0x${"66".repeat(32)}`;
const _delegationTransactionHash = `0x${"bb".repeat(32)}`;

describe("core transaction detail pages", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
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

function _statusResponse(overrides: Record<string, unknown>, meta: Record<string, unknown> = {}) {
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
