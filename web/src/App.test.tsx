import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import i18n from "./i18n";
import { makeRouter } from "./router";
import { ThemeProvider } from "./theme/ThemeProvider";
import { WalletProvider } from "./wallet/WalletProvider";

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

describe("embedded explorer shell", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        Response.json(
          { error: { code: "NOT_READY", message: "API not ready" } },
          { status: 503 },
        ),
      ),
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("renders a deep-linked route and switches language and theme", async () => {
    renderExplorer("/tokens");

    expect(await screen.findByRole("heading", { name: "Tokens & NFTs", level: 1 })).toBeVisible();
    expect(screen.getByRole("link", { name: "Skip to content" })).toHaveAttribute(
      "href",
      "#main-content",
    );

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "切换到中文" }));
    expect(await screen.findByRole("heading", { name: "代币与 NFT", level: 1 })).toBeVisible();

    await user.click(screen.getByRole("button", { name: "切换颜色主题" }));
    expect(document.documentElement).toHaveAttribute("data-theme", "dark");
  });

  it("uses typed routing for global search", async () => {
    renderExplorer("/");
    const user = userEvent.setup();
    const input = await screen.findByRole("searchbox", {
      name: "Search",
    });
    await user.type(input, "0x1234");
    await user.click(screen.getByRole("button", { name: "Search" }));

    expect(await screen.findByRole("heading", { name: "Search results", level: 1 })).toBeVisible();
    expect(screen.getByText("0x1234")).toBeVisible();
  });

  it("renders the native OpenAPI response envelopes without shape adapters", async () => {
    const blockHash = `0x${"ab".repeat(32)}`;
    const transactionHash = `0x${"cd".repeat(32)}`;
    const address = `0x${"11".repeat(20)}`;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input);
        const meta = { request_id: "web-test", chain_id: "1" };
        if (path.startsWith("/api/v1/config")) {
          return Response.json({
            data: {
              chain_id: "1",
              chain_name: "Testnet",
              native_symbol: "ETH",
              native_name: "Ether",
              native_decimals: 18,
              features: {},
            },
            meta,
          });
        }
        if (path.startsWith("/api/v1/status")) {
          return Response.json({
            data: {
              chain_id: "1",
              core_ready: true,
              latest_block: "12",
              indexed_block: "12",
              finalized_block: "10",
              lag: "0",
              completeness: {
                core: "complete",
                trace: "unavailable",
                metadata: "pending",
                state: "complete",
              },
            },
            meta,
          });
        }
        if (path.startsWith("/api/v1/blocks")) {
          return Response.json({
            data: [{
              hash: blockHash,
              number: "12",
              parent_hash: `0x${"aa".repeat(32)}`,
              timestamp: "2026-01-01T00:00:00Z",
              transaction_count: 1,
              gas_used: "21000",
              canonical: true,
              finality: "latest",
              completeness: {
                core: "complete",
                trace: "unavailable",
                metadata: "pending",
                state: "complete",
              },
            }],
            meta,
          });
        }
        if (path.startsWith("/api/v1/transactions")) {
          return Response.json({
            data: [{
              hash: transactionHash,
              block_hash: blockHash,
              block_number: "12",
              transaction_index: 0,
              from: address,
              to: address,
              nonce: "0",
              value: "1",
              gas: "21000",
              input: "0x",
              status: "success",
              canonical: true,
              finality: "latest",
              completeness: {
                core: "complete",
                trace: "unavailable",
                metadata: "pending",
                state: "complete",
              },
            }],
            meta,
          });
        }
        return Response.json({ error: { code: "NOT_FOUND", message: "not found" } }, { status: 404 });
      }),
    );

    renderExplorer("/");
    expect(await screen.findByText("#12")).toBeVisible();
    expect(await screen.findByText("Testnet")).toBeVisible();
    expect(screen.getByText("0xcdcdcd…cdcdcd")).toBeVisible();
  });

  it("keeps contiguous coverage distinct from a higher live-head island", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input);
        const meta = { request_id: "coverage-web-test", chain_id: "1" };
        if (path === "/api/v1/config") {
          return Response.json({
            data: {
              chain_id: "1",
              chain_name: "Coverage Testnet",
              native_symbol: "ETH",
              native_name: "Ether",
              native_decimals: 18,
              features: {},
            },
            meta,
          });
        }
        if (path === "/api/v1/status") {
          return Response.json({
            data: {
              chain_id: "1",
              core_ready: false,
              latest_block: "121",
              indexed_block: "50",
              highest_covered_block: "120",
              backfill_complete: false,
              lag: "71",
              completeness: {
                core: "pending",
                trace: "unavailable",
                metadata: "pending",
                state: "complete",
              },
            },
            meta,
          });
        }
        return Response.json({ error: { code: "NOT_FOUND", message: "not found" } }, { status: 404 });
      }),
    );

    renderExplorer("/status");

    expect(await screen.findByText("Coverage Testnet")).toBeVisible();
    expect(screen.getByText("Highest covered block")).toBeVisible();
    expect(screen.getByText("120")).toBeVisible();
    expect(screen.getByText("Historical backfill")).toBeVisible();
    expect(screen.getByText("In progress")).toBeVisible();
    expect(screen.getByText("50")).toBeVisible();
  });

  it("renders generated token metadata and canonical transfer data on the detail route", async () => {
    const address = `0x${"12".repeat(20)}`;
    const peer = `0x${"34".repeat(20)}`;
    const blockHash = `0x${"56".repeat(32)}`;
    const transactionHash = `0x${"78".repeat(32)}`;
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      const meta = { request_id: "token-web-test", chain_id: "1" };
      if (path === `/api/v1/tokens/${address}/transfers?limit=25`) {
        return Response.json({
          data: [{
            chain_id: "1",
            block_number: "42",
            block_hash: blockHash,
            log_index: "3",
            sub_index: "0",
            transaction_hash: transactionHash,
            token_address: address,
            standard: "erc20",
            kind: "transfer",
            from: peer,
            to: address,
            amount: "1000000",
            confidence: "verified",
          }],
          meta,
        });
      }
      if (path === `/api/v1/tokens/${address}`) {
        return Response.json({
          data: {
            chain_id: "1",
            address,
            code_hash: blockHash,
            standard: "erc20",
            confidence: "verified",
            name: "Example Dollar",
            symbol: "EXD",
            decimals: 6,
            total_supply: "1000000000",
            metadata_state: "complete",
            observed_block_number: "42",
            observed_block_hash: blockHash,
            updated_at: "2026-01-01T00:00:00Z",
          },
          meta,
        });
      }
      return Response.json(
        { error: { code: "NOT_FOUND", message: "not found", request_id: "token-web-test" } },
        { status: 404 },
      );
    });
    vi.stubGlobal("fetch", fetcher);

    renderExplorer(`/token/${address}`);

    expect(await screen.findByRole("heading", { name: "Example Dollar", level: 1 })).toBeVisible();
    expect(screen.getByRole("heading", { name: "Token metadata", level: 2 })).toBeVisible();
    expect(screen.getByText("EXD")).toBeVisible();
    expect(await screen.findByRole("heading", { name: "Token transfers", level: 2 })).toBeVisible();
    expect(screen.getByRole("link", { name: "0x787878…787878" })).toHaveAttribute(
      "href",
      `/tx/${transactionHash}`,
    );
    expect(fetcher).toHaveBeenCalledWith(`/api/v1/tokens/${address}`, expect.anything());
    expect(fetcher).toHaveBeenCalledWith(
      `/api/v1/tokens/${address}/transfers?limit=25`,
      expect.anything(),
    );
  });

  it("shows stage_unavailable as explicit trace capability degradation", async () => {
    const hash = `0x${"90".repeat(32)}`;
    const blockHash = `0x${"ab".repeat(32)}`;
    const address = `0x${"cd".repeat(20)}`;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input);
        const meta = { request_id: "trace-web-test", chain_id: "1" };
        if (path === `/api/v1/transactions/${hash}/trace`) {
          return Response.json(
            {
              error: {
                code: "stage_unavailable",
                message: "required enrichment stage is unavailable",
                details: {
                  stage: "trace",
                  state: "unavailable",
                  block_number: "42",
                  block_hash: blockHash,
                },
                request_id: "trace-web-test",
              },
            },
            { status: 503 },
          );
        }
        if (path === `/api/v1/transactions/${hash}`) {
          return Response.json({
            data: {
              hash,
              block_hash: blockHash,
              block_number: "42",
              transaction_index: 0,
              from: address,
              to: address,
              nonce: "1",
              value: "2",
              gas: "21000",
              input: "0x",
              status: "success",
              canonical: true,
              finality: "safe",
              completeness: {
                core: "complete",
                trace: "unavailable",
                metadata: "pending",
                state: "complete",
              },
            },
            meta,
          });
        }
        return Response.json(
          { error: { code: "NOT_FOUND", message: "not found", request_id: "trace-web-test" } },
          { status: 404 },
        );
      }),
    );

    renderExplorer(`/tx/${hash}`);

    expect(await screen.findByRole("heading", { name: "Transaction summary", level: 2 })).toBeVisible();
    expect(await screen.findByText("Trace data is unavailable")).toBeVisible();
    expect(screen.getByText(/reported Unavailable at block 42/)).toBeVisible();
    expect(screen.getByText(/Core indexed data remains available\./)).toBeVisible();
  });

  it("renders chart trends with an accessible exact-value table", async () => {
    const blockHash = `0x${"44".repeat(32)}`;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input);
        const meta = { request_id: "chart-web-test", chain_id: "1" };
        if (path === "/api/v1/config") {
          return Response.json({
            data: {
              chain_id: "1",
              chain_name: "Chartnet",
              native_symbol: "ETH",
              native_name: "Ether",
              native_decimals: 18,
              features: {},
            },
            meta,
          });
        }
        if (path === "/api/v1/status") {
          return Response.json({
            data: {
              chain_id: "1",
              core_ready: true,
              latest_block: "12",
              indexed_block: "12",
              finalized_block: "10",
              lag: "0",
              completeness: { core: "complete", trace: "unavailable", metadata: "pending", state: "complete" },
            },
            meta,
          });
        }
        if (path === "/api/v1/stats/blocks?from_block=0&to_block=12") {
          return Response.json({
            data: [{
              chain_id: "1",
              block_number: "12",
              block_hash: blockHash,
              transaction_count: "17",
              gas_limit: "30000000",
              gas_used: "12345678",
              base_fee_per_gas: "25000000000",
              burned_wei: "123456789012345678901234567890",
              computed_at: "2026-01-01T00:00:00Z",
            }],
            meta,
          });
        }
        return Response.json({ error: { code: "NOT_FOUND", message: "not found" } }, { status: 404 });
      }),
    );

    renderExplorer("/charts");

    expect(await screen.findByRole("heading", { name: "Canonical block statistics", level: 2 })).toBeVisible();
    expect(screen.getByText("Accessible exact-value alternative to the trend chart")).toBeVisible();
    expect(screen.getByText("123456789012345678901234567890")).toBeVisible();
    expect(screen.getByRole("table")).toBeVisible();
  });

  it("shows statistics stage loss as an explicit unavailable state", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input);
        const meta = { request_id: "chart-stage-test", chain_id: "1" };
        if (path === "/api/v1/status") {
          return Response.json({
            data: {
              chain_id: "1",
              core_ready: true,
              latest_block: "8",
              indexed_block: "8",
              lag: "0",
              completeness: { core: "complete", trace: "unavailable", metadata: "pending", state: "complete" },
            },
            meta,
          });
        }
        if (path.startsWith("/api/v1/stats/blocks")) {
          return Response.json({
            error: {
              code: "stage_unavailable",
              message: "statistics are unavailable",
              details: { stage: "statistics", state: "unavailable", block_number: "8" },
              request_id: "chart-stage-test",
            },
          }, { status: 503 });
        }
        return Response.json({ error: { code: "NOT_READY", message: "not ready" } }, { status: 503 });
      }),
    );

    renderExplorer("/charts");

    expect(await screen.findByText("Statistics data is unavailable")).toBeVisible();
    expect(screen.getByText(/reported Unavailable at block 8/)).toBeVisible();
  });

  it("rejects invalid Standard JSON locally and explains disabled verification", async () => {
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === "/api/v1/config") {
        return Response.json({
          data: {
            chain_id: "1",
            chain_name: "Testnet",
            native_symbol: "ETH",
            native_name: "Ether",
            native_decimals: 18,
            features: { verification: true },
          },
          meta: { request_id: "verify-json-test", chain_id: "1" },
        });
      }
      return Response.json({ error: { code: "NOT_FOUND", message: "not found" } }, { status: 404 });
    });
    vi.stubGlobal("fetch", fetcher);

    renderExplorer("/verify");
    fireEvent.change(await screen.findByLabelText(/^Standard JSON input/), { target: { value: "{" } });
    await userEvent.setup().click(screen.getByRole("button", { name: "Submit verification" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Standard JSON input is not valid JSON.");
    expect(fetcher.mock.calls.every(([input]) => String(input) !== "/api/v1/verification/jobs")).toBe(true);

    vi.stubGlobal(
      "fetch",
      vi.fn(async () => Response.json({
        data: {
          chain_id: "1",
          chain_name: "Testnet",
          native_symbol: "ETH",
          native_name: "Ether",
          native_decimals: 18,
          features: { verification: false },
        },
        meta: { request_id: "verify-disabled-test", chain_id: "1" },
      })),
    );
    renderExplorer("/verify");
    expect(await screen.findByRole("heading", { name: "Public verification is unavailable", level: 2 })).toBeVisible();
  });

  it("submits and polls a verification job without persisting or routing the API key", async () => {
    const address = `0x${"12".repeat(20)}`;
    const secret = "ev_live_component-memory-only";
    const jobID = "018f3b52-0b3d-7bf1-b65f-6f214827cb41";
    let submittedBody: Record<string, unknown> | undefined;
    const storageSpy = vi.spyOn(window.localStorage, "setItem");
    const consoleSpy = vi.spyOn(console, "log").mockImplementation(() => undefined);
    const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      const meta = { request_id: "verification-web-test", chain_id: "1" };
      if (path === "/api/v1/config") {
        return Response.json({
          data: {
            chain_id: "1",
            chain_name: "Testnet",
            native_symbol: "ETH",
            native_name: "Ether",
            native_decimals: 18,
            features: { verification: true },
          },
          meta,
        });
      }
      if (path === "/api/v1/verification/jobs" && init?.method === "POST") {
        submittedBody = JSON.parse(String(init.body)) as Record<string, unknown>;
        return Response.json({
          data: { id: jobID, status: "queued", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" },
          meta,
        }, { status: 202 });
      }
      if (path === `/api/v1/verification/jobs/${jobID}`) {
        return Response.json({
          data: { id: jobID, status: "succeeded", result_kind: "exact", runtime_match: "exact", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:02Z" },
          meta,
        });
      }
      return Response.json({ error: { code: "NOT_FOUND", message: "not found" } }, { status: 404 });
    });
    vi.stubGlobal("fetch", fetcher);
    renderExplorer("/verify");

    fireEvent.change(await screen.findByLabelText("Address"), { target: { value: address } });
    fireEvent.change(screen.getByLabelText("Compiler version"), { target: { value: "0.8.30" } });
    fireEvent.change(screen.getByLabelText("Contract identifier"), { target: { value: "src/Test.sol:Test" } });
    fireEvent.change(screen.getByLabelText(/^API key/), { target: { value: secret } });
    await userEvent.setup().click(screen.getByRole("button", { name: "Submit verification" }));

    expect(await screen.findByText("succeeded")).toBeVisible();
    expect(screen.getAllByText("exact").length).toBeGreaterThan(0);
    expect(submittedBody).toMatchObject({
      address,
      compiler_version: "0.8.30",
      contract_identifier: "src/Test.sol:Test",
    });
    expect(submittedBody).not.toHaveProperty("code_hash");
    expect(submittedBody).not.toHaveProperty("at_block_hash");
    expect(submittedBody).not.toHaveProperty("creation_bytecode");
    expect(submittedBody).not.toHaveProperty("runtime_bytecode");
    const protectedCalls = fetcher.mock.calls.filter(([input]) => String(input).includes("/verification/jobs"));
    expect(protectedCalls).toHaveLength(2);
    for (const [url, init] of protectedCalls) {
      expect(String(url)).not.toContain(secret);
      expect(new Headers(init?.headers).get("X-API-Key")).toBe(secret);
    }
    expect(window.location.href).not.toContain(secret);
    expect(storageSpy.mock.calls.every(([, value]) => !String(value).includes(secret))).toBe(true);
    expect(consoleSpy.mock.calls.flat().every((value) => !String(value).includes(secret))).toBe(true);
  });

  it("renders verified source artifacts as inert preformatted text", async () => {
    const address = `0x${"77".repeat(20)}`;
    const codeHash = `0x${"88".repeat(32)}`;
    const secret = "ev_live_contract-query";
    const malicious = '<img src=x onerror="window.__etherviewPwned=true">';
    const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      const meta = { request_id: "contract-artifact-test", chain_id: "1" };
      if (path === "/api/v1/config") {
        return Response.json({
          data: {
            chain_id: "1",
            chain_name: "Testnet",
            native_symbol: "ETH",
            native_name: "Ether",
            native_decimals: 18,
            features: { verification: true },
          },
          meta,
        });
      }
      if (path === `/api/v1/contracts/${address}/verification?code_hash=${codeHash}`) {
        expect(new Headers(init?.headers).get("X-API-Key")).toBe(secret);
        return Response.json({
          data: {
            chain_id: "1",
            address,
            code_hash: codeHash,
            language: "solidity",
            compiler_version: "0.8.30",
            contract_name: "HostileText",
            match_kind: "exact",
            abi: [{ type: "function", name: malicious }],
            sources: { "src/Hostile.sol": { content: malicious } },
            settings: { metadata: { note: "<script>window.__etherviewPwned=true</script>" } },
            valid_from_block: "12",
            created_at: "2026-01-01T00:00:00Z",
          },
          meta,
        });
      }
      return Response.json({ error: { code: "NOT_FOUND", message: "not found" } }, { status: 404 });
    });
    vi.stubGlobal("fetch", fetcher);
    renderExplorer(`/contract/${address}?code_hash=${codeHash}`);

    fireEvent.change(await screen.findByLabelText("API key"), { target: { value: secret } });
    await userEvent.setup().click(screen.getByRole("button", { name: "Load verification" }));

    expect(await screen.findByText("HostileText")).toBeVisible();
    expect(screen.getAllByText(/<img src=x/).length).toBeGreaterThan(0);
    expect(document.querySelector(".artifact-panel img")).toBeNull();
    expect(document.querySelector(".artifact-panel script")).toBeNull();
    expect(String(fetcher.mock.calls.find(([input]) => String(input).includes("/contracts/"))?.[0])).not.toContain(secret);
  });
});

function renderExplorer(path: string) {
  const router = makeRouter(createMemoryHistory({ initialEntries: [path] }));
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
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
