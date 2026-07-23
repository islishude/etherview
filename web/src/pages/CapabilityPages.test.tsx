import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import axe from "axe-core";
import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import i18n from "@/i18n";
import { makeRouter } from "@/router";
import { ThemeProvider } from "@/theme/ThemeProvider";
import { WalletProvider } from "@/wallet/WalletProvider";

vi.mock("echarts/core", () => ({
  use: vi.fn(),
  init: vi.fn(() => ({ setOption: vi.fn(), resize: vi.fn(), dispose: vi.fn() })),
}));
vi.mock("echarts/charts", () => ({ LineChart: {} }));
vi.mock("echarts/components", () => ({
  GridComponent: {},
  LegendComponent: {},
  TooltipComponent: {},
}));
vi.mock("echarts/renderers", () => ({ CanvasRenderer: {} }));

const address = `0x${"12".repeat(20)}`;
const owner = `0x${"34".repeat(20)}`;
const blockHash = `0x${"56".repeat(32)}`;
const codeHash = `0x${"78".repeat(32)}`;
const meta = { request_id: "capability-pages-test", chain_id: "1" };

describe("P50 capability pages", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
    document.title = "Etherview";
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("round-trips token cursors and recovers an invalid canonical snapshot", async () => {
    const cursor = "tokens:snapshot+next/page?2&rank=1";
    let firstPageRequests = 0;
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === "/api/v1/config") return configResponse({});
      if (path === "/api/v1/tokens?limit=25") {
        firstPageRequests += 1;
        return Response.json({
          data: [tokenContract("Token One")],
          meta: { ...meta, next_cursor: cursor },
        });
      }
      if (path === "/api/v1/tokens?limit=25&cursor=tokens%3Asnapshot%2Bnext%2Fpage%3F2%26rank%3D1") {
        return apiError("invalid_cursor", 400);
      }
      return apiError("not_found", 404);
    });
    vi.stubGlobal("fetch", fetcher);
    renderExplorer("/tokens");

    expect(await screen.findByText("Token One")).toBeVisible();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Next page" }));
    expect(await screen.findByText("This page cursor is no longer valid")).toBeVisible();
    expect(fetcher).toHaveBeenCalledWith(
      "/api/v1/tokens?limit=25&cursor=tokens%3Asnapshot%2Bnext%2Fpage%3F2%26rank%3D1",
      expect.anything(),
    );

    await user.click(screen.getByRole("button", { name: "Restart from the first page" }));
    expect(await screen.findByText("Token One")).toBeVisible();
    expect(firstPageRequests).toBe(2);
  });

  it("discovers exact owner NFT balances without inventing an ERC-1155 owner route", async () => {
    const tokenID = "340282366920938463463374607431768211455";
    const balance = "115792089237316195423570985008687907853269984665640564039457584007913129639935";
    const cursor = "nft-owner:snapshot+next/page?2";
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === "/api/v1/config") return configResponse({});
      if (path === `/api/v1/addresses/${owner}`) {
        return Response.json({
          data: {
            address: owner,
            at_block: blockHash,
            balance: "0",
            nonce: "0",
            type: "eoa",
            completeness: completeness(),
          },
          meta,
        });
      }
      if (path === `/api/v1/addresses/${owner}/nfts?limit=25`) {
        return Response.json({
          data: [{
            chain_id: "1",
            owner,
            token_address: address,
            token_id: tokenID,
            balance,
            confidence: "rpc_exact",
          }],
          meta: { ...meta, coverage_end: "999", next_cursor: cursor },
        });
      }
      if (path === `/api/v1/addresses/${owner}/nfts?limit=25&cursor=nft-owner%3Asnapshot%2Bnext%2Fpage%3F2`) {
        return Response.json({ data: [], meta: { ...meta, coverage_end: "999" } });
      }
      return apiError("not_found", 404);
    });
    vi.stubGlobal("fetch", fetcher);
    renderExplorer(`/address/${owner}`);

    expect(await screen.findByRole("heading", { name: "Canonical NFT balances" })).toBeVisible();
    expect(await screen.findByText(tokenID)).toBeVisible();
    expect(screen.getByText(tokenID).closest("a")).toBeNull();
    expect(screen.getByText(balance)).toBeVisible();
    expect(screen.getByText("Exact RPC observation")).toBeVisible();
    expect(screen.getByText("Exact state reconciled against canonical block 999.")).toBeVisible();
    expect(screen.getByRole("link", { name: "0x121212…121212" })).toHaveAttribute(
      "href",
      `/token/${address}`,
    );

    await userEvent.setup().click(screen.getByRole("button", { name: "Next page" }));
    expect(await screen.findByText("No positive NFT balances were observed in this canonical snapshot.")).toBeVisible();
    expect(fetcher).toHaveBeenCalledWith(
      `/api/v1/addresses/${owner}/nfts?limit=25&cursor=nft-owner%3Asnapshot%2Bnext%2Fpage%3F2`,
      expect.anything(),
    );
  });

  it("keeps NFT stage loss distinct from an authoritative empty balance page", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input);
        if (path === "/api/v1/config") return configResponse({});
        if (path === `/api/v1/addresses/${owner}`) {
          return Response.json({
            data: {
              address: owner,
              at_block: blockHash,
              balance: "0",
              nonce: "0",
              type: "eoa",
              completeness: { ...completeness(), state: "unavailable" },
            },
            meta,
          });
        }
        if (path === `/api/v1/addresses/${owner}/nfts?limit=25`) {
          return Response.json({
            error: {
              code: "stage_unavailable",
              message: "token state unavailable",
              details: {
                stage: "token",
                state: "unavailable",
                block_number: "999",
                block_hash: blockHash,
              },
              request_id: "nft-stage-test",
            },
          }, { status: 503 });
        }
        return apiError("not_found", 404);
      }),
    );
    renderExplorer(`/address/${owner}`);

    expect(await screen.findByText("Token data is unavailable")).toBeVisible();
    expect(screen.getByText(/reported Unavailable at block 999/)).toBeVisible();
    expect(screen.getByText("Diagnostic code:").parentElement).toHaveTextContent(
      "stage_unavailable",
    );
    expect(
      screen.queryByText("No positive NFT balances were observed in this canonical snapshot."),
    ).toBeNull();
  });

  it("keeps durable verification reads available when new submissions are disabled", async () => {
    const jobID = "123e4567-e89b-42d3-a456-426614174000";
    const secret = "ev_live_read_only";
    const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === "/api/v1/config") {
        return configResponse({ verification: false, sourcify: false });
      }
      if (path === `/api/v1/verification/jobs/${jobID}`) {
        expect(new Headers(init?.headers).get("X-API-Key")).toBe(secret);
        return Response.json({
          data: {
            id: jobID,
            status: "succeeded",
            result_kind: "exact",
            runtime_match: "exact",
            published: true,
            created_at: "2026-07-20T10:00:00Z",
            updated_at: "2026-07-20T10:00:01Z",
          },
          meta,
        });
      }
      return apiError("not_found", 404);
    });
    vi.stubGlobal("fetch", fetcher);
    renderExplorer("/verify");

    expect(await screen.findByRole("heading", { name: "Public verification is unavailable" })).toBeVisible();
    expect(screen.queryByRole("heading", { name: "Verification request" })).toBeNull();
    expect(screen.getByRole("heading", { name: "Open a durable verification job" })).toBeVisible();
    fireEvent.change(screen.getByLabelText("Job ID"), { target: { value: jobID } });
    fireEvent.change(screen.getByLabelText("Job read API key"), { target: { value: secret } });
    await userEvent.setup().click(screen.getByRole("button", { name: "Load job" }));

    expect(await screen.findByText("succeeded")).toBeVisible();
    expect(screen.getByText("Yes")).toBeVisible();
    expect(fetcher.mock.calls.some(([input]) => String(input) === "/api/v1/verification/jobs")).toBe(false);
    expect(String(fetcher.mock.calls.find(([input]) => String(input).includes(jobID))?.[0])).not.toContain(secret);

    const scan = await axe.run(document, {
      runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"] },
      rules: { "color-contrast": { enabled: false } },
    });
    expect(scan.violations, JSON.stringify(scan.violations, null, 2)).toEqual([]);
  });

  it("loads published artifacts independently of the submission feature flag", async () => {
    const secret = "ev_live_artifact_read";
    const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === "/api/v1/config") return configResponse({ verification: false });
      if (path === `/api/v1/contracts/${address}/verification?code_hash=${codeHash}`) {
        expect(new Headers(init?.headers).get("X-API-Key")).toBe(secret);
        return Response.json({
          data: {
            chain_id: "1",
            address,
            code_hash: codeHash,
            valid_from_block: "500",
            language: "solidity",
            compiler_version: "0.8.30",
            match_kind: "exact",
            contract_name: "ReadOnlyArtifact",
            abi: [],
            sources: {},
            settings: {},
            created_at: "2026-07-20T10:00:00Z",
          },
          meta,
        });
      }
      if (path === "/api/v1/status") return statusResponse("520", "500");
      return apiError("not_found", 404);
    });
    vi.stubGlobal("fetch", fetcher);
    renderExplorer(`/contract/${address}?code_hash=${codeHash}`);

    expect(await screen.findByText(/published-artifact reads remain available/)).toBeVisible();
    fireEvent.change(screen.getByLabelText("API key"), { target: { value: secret } });
    await userEvent.setup().click(screen.getByRole("button", { name: "Load verification" }));
    expect(await screen.findByText("ReadOnlyArtifact")).toBeVisible();
    expect(screen.queryByRole("heading", { name: "Public verification is unavailable" })).toBeNull();
  });

  it("opens wallet-only contract tools without requiring a code hash", async () => {
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      if (String(input) === "/api/v1/config") return configResponse({});
      return apiError("not_found", 404);
    });
    vi.stubGlobal("fetch", fetcher);
    renderExplorer("/contracts");

    fireEvent.change(await screen.findByLabelText("Address"), {
      target: { value: address },
    });
    expect(screen.getByLabelText("Code hash (optional)")).toHaveValue("");
    await userEvent.setup().click(screen.getByRole("button", { name: "Open contract" }));

    expect(await screen.findByRole("heading", { name: "Contract", level: 1 })).toBeVisible();
    expect(screen.getByLabelText("Calldata")).toBeVisible();
    expect(screen.getByRole("button", { name: "Read contract" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Send transaction" })).toBeDisabled();
  });

  it("rejects nested duplicate Standard JSON keys before submission", async () => {
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      if (String(input) === "/api/v1/config") {
        return configResponse({ verification: true, sourcify: true });
      }
      return apiError("not_found", 404);
    });
    vi.stubGlobal("fetch", fetcher);
    renderExplorer("/verify");

    const duplicateInput =
      '{"language":"Solidity","sources":{},"settings":{"optimizer":true,"\\u006fptimizer":false}}';
    fireEvent.change(await screen.findByLabelText(/^Standard JSON input/), {
      target: { value: duplicateInput },
    });
    expect(
      screen.getByLabelText(
        "Allow a separate, explicitly confirmed later upload of these sources to Sourcify.",
      ),
    ).toBeVisible();
    await userEvent.setup().click(screen.getByRole("button", { name: "Submit verification" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Standard JSON input contains a duplicate key and was not submitted.",
    );
    expect(fetcher.mock.calls.some(([input]) => String(input) === "/api/v1/verification/jobs")).toBe(false);
    expect(fetcher.mock.calls.some(([input]) => String(input).includes("/sourcify"))).toBe(false);
  });

  it.each([
    ['{"settings":{"runs":9007199254740993}}', "an integer beyond JavaScript precision"],
    ['{"settings":{"runs":-0}}', "negative zero"],
  ])("rejects non-round-tripping Standard JSON numbers: %s (%s)", async (standardJSON) => {
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      if (String(input) === "/api/v1/config") {
        return configResponse({ verification: true });
      }
      return apiError("not_found", 404);
    });
    vi.stubGlobal("fetch", fetcher);
    renderExplorer("/verify");

    fireEvent.change(await screen.findByLabelText(/^Standard JSON input/), {
      target: { value: standardJSON },
    });
    await userEvent.setup().click(screen.getByRole("button", { name: "Submit verification" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Standard JSON numbers must be safe integers so the submitted compiler input is not changed.",
    );
    expect(fetcher.mock.calls.some(([input]) => String(input) === "/api/v1/verification/jobs")).toBe(false);
  });

  it("clamps charts to configured coverage and renders exact stats@2 aggregates", async () => {
    const hugeBurn = "115792089237316195423570985008687907853269984665640564039457584007913129639935";
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === "/api/v1/config") return configResponse({});
      if (path === "/api/v1/status") return statusResponse("520", "500");
      if (path === "/api/v1/stats/blocks?from_block=500&to_block=520") {
        return Response.json({
          data: [{
            chain_id: "1",
            block_number: "520",
            block_hash: blockHash,
            transaction_count: "17",
            gas_used: "12345678",
            gas_limit: "30000000",
            base_fee_per_gas: "25000000000",
            blob_gas_used: "393216",
            excess_blob_gas: "786432",
            blob_base_fee_per_gas: "7",
            burned_wei: hugeBurn,
            blob_burned_wei: "2752512",
            block_timestamp: "1784780000",
            block_interval_seconds: "12",
            transactions_per_second: "1.416666666666666667",
            token_event_count: "23",
            token_transfer_count: "19",
            nft_transfer_count: "5",
            computed_at: "2026-07-20T10:00:00Z",
          }],
          meta: { ...meta, coverage_start: "500", coverage_end: "520" },
        });
      }
      if (path === "/api/v1/stats/summary?from_block=500&to_block=520") {
        return Response.json({
          data: {
            chain_id: "1",
            from_block: "500",
            to_block: "520",
            snapshot: { chain_id: "1", block_number: "520", block_hash: blockHash },
            block_count: "21",
            transaction_count: "357",
            gas_used: "259259238",
            burned_wei: hugeBurn,
            blob_burned_wei: "2752512",
            token_event_count: "483",
            token_transfer_count: "399",
            nft_transfer_count: "105",
            average_tps: "1.416666666666666667",
            completeness: { core: true, stats: true, token: false },
          },
          meta,
        });
      }
      return apiError("not_found", 404);
    });
    vi.stubGlobal("fetch", fetcher);
    renderExplorer("/charts");

    expect(await screen.findByRole("heading", { name: "Range summary" })).toBeVisible();
    expect(screen.getAllByText(hugeBurn).length).toBeGreaterThanOrEqual(2);
    expect(screen.getAllByText("1.416666666666666667").length).toBeGreaterThanOrEqual(2);
    expect(screen.getByRole("columnheader", { name: "Parent interval (seconds)" })).toBeVisible();
    expect(screen.getByRole("columnheader", { name: "Blob base fee per gas" })).toBeVisible();
    expect(screen.getByRole("columnheader", { name: "NFT transfers" })).toBeVisible();
    expect(screen.getAllByRole("link", { name: "520" })[0]).toHaveAttribute(
      "href",
      `/blocks/${blockHash}`,
    );
    expect(fetcher.mock.calls.some(([input]) => String(input).includes("from_block=401"))).toBe(false);

    await userEvent.setup().click(screen.getByRole("button", { name: "切换到中文" }));
    expect(await screen.findByRole("heading", { name: "区间汇总" })).toBeVisible();
    expect(screen.getByText("统计")).toBeVisible();
    expect(screen.getByText("代币")).toBeVisible();
  });

  it("does not query statistics before configured coverage has started", async () => {
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === "/api/v1/config") return configResponse({});
      if (path === "/api/v1/status") return statusResponse("0", "500", false);
      return apiError("not_found", 404);
    });
    vi.stubGlobal("fetch", fetcher);
    renderExplorer("/charts");

    expect(await screen.findByText(
      "Contiguous coverage has not reached configured start block 500.",
    )).toBeVisible();
    expect(fetcher.mock.calls.some(([input]) => String(input).includes("/stats/"))).toBe(false);
  });

  it("localizes sync facts separately from configured feature availability", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input);
        if (path === "/api/v1/config") {
          return configResponse({
            historical_state: false,
            mempool: true,
            nft_metadata: false,
            pricing: true,
            sourcify: false,
            trace: true,
            verification: false,
          });
        }
        if (path === "/api/v1/status") return statusResponse("520", "500");
        return apiError("not_found", 404);
      }),
    );
    renderExplorer("/status");

    expect(await screen.findByRole("heading", { name: "Indexed data completeness" })).toBeVisible();
    expect(screen.getByRole("heading", { name: "Configured optional features" })).toBeVisible();
    expect(screen.getByText("Core readiness")).toBeVisible();
    expect(screen.getByText("Lag (blocks)")).toBeVisible();
    expect(screen.getByText("Historical exact state")).toBeVisible();
    expect(screen.getByText("New public verification submissions")).toBeVisible();

    await userEvent.setup().click(screen.getByRole("button", { name: "切换到中文" }));
    expect(await screen.findByRole("heading", { name: "索引数据完整度" })).toBeVisible();
    expect(screen.getByRole("heading", { name: "已配置的可选功能" })).toBeVisible();
    expect(screen.getByText("历史精确状态")).toBeVisible();
    expect(screen.getByText("新的公开验证提交")).toBeVisible();
  });
});

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

function configResponse(features: Record<string, boolean>): Response {
  return Response.json({
    data: {
      chain_id: "1",
      chain_name: "Capability Testnet",
      native_symbol: "ETH",
      native_name: "Ether",
      native_decimals: 18,
      features,
    },
    meta,
  });
}

function statusResponse(indexed: string, coverageStart: string, ready = true): Response {
  return Response.json({
    data: {
      chain_id: "1",
      core_ready: ready,
      latest_block: ready ? "521" : "500",
      indexed_block: indexed,
      highest_covered_block: indexed,
      backfill_complete: ready,
      safe_block: ready ? "519" : undefined,
      finalized_block: ready ? "518" : undefined,
      lag: ready ? "1" : "500",
      completeness: completeness(),
    },
    meta: {
      ...meta,
      coverage_start: coverageStart,
      coverage_end: indexed,
    },
  });
}

function tokenContract(name: string) {
  return {
    chain_id: "1",
    address,
    code_hash: codeHash,
    standard: "erc20",
    confidence: "verified",
    name,
    symbol: "TOK",
    decimals: 18,
    total_supply: "340282366920938463463374607431768211455",
    metadata_state: "complete",
    observed_block_number: "520",
    observed_block_hash: blockHash,
    updated_at: "2026-07-20T10:00:00Z",
  };
}

function completeness() {
  return {
    core: "complete",
    trace: "unavailable",
    metadata: "complete",
    state: "complete",
  };
}

function apiError(code: string, status: number): Response {
  return Response.json({
    error: {
      code,
      message: code,
      request_id: "capability-pages-error",
    },
  }, { status });
}
