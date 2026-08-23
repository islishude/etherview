import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { render, screen, within } from "@testing-library/react";
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
const _orphanHash = `0x${"33".repeat(32)}`;
const parentHash = `0x${"00".repeat(32)}`;
const address = `0x${"44".repeat(20)}`;
const transactionHash = `0x${"aa".repeat(32)}`;
const delegatedAddress = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266";
const _clearedDelegationAddress = `0x${"77".repeat(20)}`;
const _delegateAddress = "0x5FbDB2315678afecb367f032d93F642f64180aa3";
const _delegateCodeHash = `0x${"55".repeat(32)}`;
const _delegationBlockHash = `0x${"66".repeat(32)}`;
const _delegationTransactionHash = `0x${"bb".repeat(32)}`;

describe("core value and address pages", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
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
