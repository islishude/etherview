import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AuthProvider } from "@/auth/AuthProvider";
import i18n from "@/i18n";
import { makeRouter } from "@/router";
import { ThemeProvider } from "@/theme/ThemeProvider";
import { WalletProvider } from "@/wallet/WalletProvider";

const userOpHash = `0x${"12".repeat(32)}`;
const transactionHash = `0x${"34".repeat(32)}`;
const blockHash = `0x${"56".repeat(32)}`;
const sender = "0x1000000000000000000000000000000000000001";
const entryPoint = "0x433709009B8330FDa32311DF1C2AFA402eD8D009";
const bundler = "0x2000000000000000000000000000000000000002";
const beneficiary = "0x3000000000000000000000000000000000000003";
const paymaster = "0x4000000000000000000000000000000000000004";

describe("ERC-4337 UserOperation pages", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("renders the continuous list and exact failure detail through generated API routes", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), "http://etherview.test");
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === "/api/v1/user-operations") {
        return Response.json({
          data: [summary()],
          meta: {
            request_id: "userop-list-test", chain_id: "1",
            coverage_start: "10", coverage_end: "42",
          },
        });
      }
      if (url.pathname === `/api/v1/user-operations/${userOpHash}`) {
        return Response.json({ data: detail(), meta: { request_id: "userop-detail-test", chain_id: "1" } });
      }
      return notFound();
    }));

    renderExplorer("/user-operations");
    expect(await screen.findByRole("heading", { name: "UserOperations" })).toBeVisible();
    expect(await screen.findByText("UserOperation coverage 10–42")).toBeVisible();
    const table = screen.getByRole("table", {
      name: "Canonical ERC-4337 operations from one continuous indexed snapshot.",
    });
    expect(within(table).getByText("Succeeded")).toBeVisible();
    expect(within(table).getByText("v0.9 · #0 · event #2")).toBeVisible();
    expect(within(table).getByText("0.000000000000001")).toBeVisible();

    await userEvent.setup().click(within(table).getByRole("link", { name: /0x121212/u }));
    expect(await screen.findAllByRole("heading", { name: "Paymaster postOp reverted" })).toHaveLength(2);
    expect(screen.getAllByText("paymaster rejected")).toHaveLength(2);
    expect(screen.getByText("0xdeadbeef", { exact: true })).toBeVisible();
    expect(screen.getByText("v0.9", { exact: true })).toBeVisible();
    expect(screen.getByRole("link", { name: /0x343434/u })).toHaveAttribute(
      "href",
      `/tx/${transactionHash}?tab=user-operations`,
    );
  });
});

function summary() {
  return {
    hash: userOpHash,
    entry_point: entryPoint,
    entry_point_version: "0.9",
    sender,
    nonce: "18446744073709551617",
    nonce_key: "1",
    nonce_sequence: "1",
    success: true,
    actual_gas_cost: "1000",
    actual_gas_used: "25000",
    transaction_hash: transactionHash,
    transaction_index: 0,
    operation_index: 0,
    event_log_index: 2,
    block_number: "42",
    block_hash: blockHash,
    block_timestamp: "2026-09-02T00:00:00Z",
    canonical: true,
    finality: "safe",
    bundler,
    beneficiary,
    init_kind: "factory",
    paymaster,
  };
}

function detail() {
  return {
    ...summary(),
    success: false,
    request: {
      call_gas_limit: "100000",
      verification_gas_limit: "200000",
      pre_verification_gas: "30000",
      max_fee_per_gas: "100",
      max_priority_fee_per_gas: "2",
      init_code: "0x1234",
      factory_data: "0x34",
      call_data: "0xdeadbeef",
      paymaster_and_data: "0xabcd",
      paymaster_data: "0xcd",
      paymaster_signature: "0x99",
      signature: "0x88",
      aggregated_signature: "0x",
      account_gas_limits: `0x${"00".repeat(32)}`,
      gas_fees: `0x${"01".repeat(32)}`,
    },
    events: [{
      kind: "post_op_revert",
      log_index: 3,
      sender,
      nonce: "18446744073709551617",
      paymaster,
      raw_data: "0x08c379a0",
      reason: "paymaster rejected",
    }],
  };
}

function configResponse() {
  return Response.json({
    data: {
      chain_id: "1", chain_name: "Ethereum", native_symbol: "ETH",
      native_name: "Ether", native_decimals: 18,
      features: { user_operations: true, user_auth: false },
    },
    meta: { request_id: "userop-config-test", chain_id: "1" },
  });
}

function notFound() {
  return Response.json({
    error: { code: "not_found", message: "not found", request_id: "userop-pages-test" },
  }, { status: 404 });
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
