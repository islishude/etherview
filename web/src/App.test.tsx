import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import i18n from "./i18n";
import { makeRouter } from "./router";
import { AuthProvider } from "./auth/AuthProvider";
import { ThemeProvider } from "./theme/ThemeProvider";
import { WalletProvider } from "./wallet/WalletProvider";

vi.mock("echarts/core", () => ({
  use: vi.fn(),
  init: vi.fn(() => ({ setOption: vi.fn(), resize: vi.fn(), dispose: vi.fn() })),
}));
vi.mock("echarts/charts", () => ({ LineChart: {} }));
vi.mock("echarts/components", () => ({
  DataZoomComponent: {},
  GridComponent: {},
  LegendComponent: {},
  TooltipComponent: {},
}));
vi.mock("echarts/renderers", () => ({ CanvasRenderer: {} }));

class AppEventSource {
  static latest?: AppEventSource;

  static current() {
    return AppEventSource.latest;
  }

  private readonly listeners = new Map<string, Set<EventListener>>();

  constructor(readonly url: string | URL) {
    AppEventSource.latest = this;
  }

  addEventListener(type: string, listener: EventListenerOrEventListenerObject | null) {
    if (typeof listener !== "function") return;
    const listeners = this.listeners.get(type) ?? new Set<EventListener>();
    listeners.add(listener);
    this.listeners.set(type, listeners);
  }

  removeEventListener(type: string, listener: EventListenerOrEventListenerObject | null) {
    if (typeof listener === "function") this.listeners.get(type)?.delete(listener);
  }

  close() {}

  snapshot(data: unknown) {
    for (const listener of this.listeners.get("snapshot") ?? []) {
      listener(new MessageEvent("snapshot", { data: JSON.stringify(data) }));
    }
  }
}

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
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("returns 404 for the removed standalone contract route", async () => {
    renderExplorer(`/contract/0x${"11".repeat(20)}`);

    expect(await screen.findByRole("heading", { name: "404 ·" })).toBeVisible();
  });

  it("removes the standalone Contracts navigation and lookup route", async () => {
    renderExplorer("/contracts");

    expect(await screen.findByRole("heading", { name: "404 ·" })).toBeVisible();
    expect(screen.queryByRole("link", { name: "Contracts" })).toBeNull();

    await userEvent.setup().click(screen.getByRole("button", { name: "切换到中文" }));
    expect(screen.queryByRole("link", { name: "合约" })).toBeNull();
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
    vi.useFakeTimers({ toFake: ["Date", "setInterval", "clearInterval"] });
    vi.setSystemTime(new Date("2026-01-01T00:01:59Z"));
    const blockHash = `0x${"ab".repeat(32)}`;
    const transactionHash = `0x${"cd".repeat(32)}`;
    const address = `0x${"11".repeat(20)}`;
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
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
      return Response.json(
        { error: { code: "NOT_FOUND", message: "not found" } },
        { status: 404 },
      );
    });
    vi.stubGlobal("fetch", fetchMock);
    vi.stubGlobal(
      "EventSource",
      AppEventSource as unknown as typeof EventSource,
    );
    const completeness = {
      core: "complete",
      trace: "unavailable",
      metadata: "pending",
      state: "complete",
    };

    AppEventSource.latest = undefined;
    renderExplorer("/");
    await act(async () => {
      await Promise.resolve();
    });
    const homeSource = AppEventSource.current();
    expect(homeSource?.url).toBe("/api/v1/home/stream");
    await act(async () => {
      homeSource?.snapshot({
        data: {
          status: {
            chain_id: "1",
            core_ready: true,
            latest_block: "12",
            indexed_block: "12",
            finalized_block: "10",
            backfill_complete: true,
            lag: "0",
            completeness,
          },
          blocks: [{
            hash: blockHash,
            number: "12",
            parent_hash: `0x${"aa".repeat(32)}`,
            timestamp: "2026-01-01T00:00:00Z",
            transaction_count: 1,
            gas_used: "21000",
            canonical: true,
            finality: "latest",
            completeness,
          }],
          transactions: [{
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
            completeness,
          }],
        },
        meta: {
          request_id: "web-test",
          chain_id: "1",
          coverage_start: "0",
          coverage_end: "12",
        },
      });
    });
    expect(await screen.findByText("#12")).toBeVisible();
    expect(await screen.findByText("Testnet")).toBeVisible();
    expect(screen.getByText("0xcdcdcd…cdcdcd")).toBeVisible();
    expect(screen.getByText("1 minute ago")).toBeVisible();
    expect(document.querySelector(".hero")).not.toBeInTheDocument();
    expect(screen.queryByText("Follow every block, call and asset movement."))
      .not.toBeInTheDocument();

    const brand = screen.getByRole("link", { name: "Etherview home" });
    const brandMark = brand.querySelector("img.brand-mark");
    expect(brandMark).toHaveAttribute("alt", "");
    expect(brandMark).toHaveAttribute("aria-hidden", "true");
    expect(brandMark).toHaveAttribute(
      "src",
      expect.stringMatching(/etherview-mark.*\.svg$/),
    );
    expect(fetchMock.mock.calls.some(([input]) =>
      /^\/api\/v1\/(status|blocks|transactions)(?:\?|$)/.test(String(input)),
    )).toBe(false);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    expect(screen.getByText("2 minutes ago")).toBeVisible();
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
            decimals: 6,
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
    expect(await screen.findByRole("heading", { name: "Token events", level: 2 })).toBeVisible();
    expect(screen.getByRole("link", { name: "0x787878…787878" })).toHaveAttribute(
      "href",
      `/tx/${transactionHash}?tab=overview`,
    );
    expect(screen.getByText("1", { exact: true })).toBeVisible();
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
            data: { kind: "included", transaction: {
              hash,
              block_hash: blockHash,
              block_number: "42",
              transaction_index: 0,
              from: address,
              to: address,
              nonce: "1",
              value: "2",
              gas: "21000",
              effective_gas_price: "2000000000",
              tx_fee_wei: "42000000000000",
              burned_wei: "21000000000000",
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
            } },
            meta,
          });
        }
        return Response.json(
          { error: { code: "NOT_FOUND", message: "not found", request_id: "trace-web-test" } },
          { status: 404 },
        );
      }),
    );

    renderExplorer(`/tx/${hash}?tab=trace`);

    expect(await screen.findByText("Trace data is unavailable")).toBeVisible();
    expect(screen.getByText(/reported Unavailable at block 42/)).toBeVisible();
    expect(screen.getByText(/Core indexed data remains available\./)).toBeVisible();
  });

  it("renders a deep-linked metric chart with an accessible exact-value table", async () => {
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
        if (path === "/api/v1/stats/charts/overview") {
          return Response.json({
            data: {
              generated_at: "2026-01-08T00:00:00Z",
              snapshot: { chain_id: "1", block_number: "12", block_hash: blockHash },
              coverage: {
                available_from: "2026-01-01T00:00:00Z",
                available_to: "2026-01-08T00:00:00Z",
                complete: true,
                dirty_hours: "0",
                backfill_state: "complete",
                backfill_progress: "100",
              },
              metrics: [],
              pending: false,
            },
            meta,
          });
        }
        if (path.includes("/api/v1/stats/charts/execution-fees")) {
          return Response.json({
            data: {
              metric: "execution-fees",
              interval: "day",
              from_time: "2026-01-01T00:00:00Z",
              to_time: "2026-01-08T00:00:00Z",
              points: [{
                bucket_start: "2026-01-07T00:00:00Z",
                bucket_end: "2026-01-08T00:00:00Z",
                value: "123456789012345678901234567890",
                partial: false,
                from_block: "1",
                to_block: "12",
              }],
              summary: {
                current: "123456789012345678901234567890",
                highest: "123456789012345678901234567890",
                lowest: "123456789012345678901234567890",
                total: "123456789012345678901234567890",
                average: "123456789012345678901234567890",
              },
              snapshot: { chain_id: "1", block_number: "12", block_hash: blockHash },
              coverage: {
                available_from: "2026-01-01T00:00:00Z",
                available_to: "2026-01-08T00:00:00Z",
                complete: true,
                dirty_hours: "0",
                backfill_state: "complete",
                backfill_progress: "100",
              },
            },
            meta,
          });
        }
        return Response.json({ error: { code: "NOT_FOUND", message: "not found" } }, { status: 404 });
      }),
    );

    renderExplorer("/charts/execution-fees?range=7d&interval=day");

    expect(await screen.findByRole("heading", { name: "Execution gas fees", level: 1 })).toBeVisible();
    expect(await screen.findByText("Accessible exact values from the same response used by the chart")).toBeVisible();
    expect(screen.getAllByText("123456789012345678901234567890").length).toBeGreaterThan(0);
    expect(screen.getByRole("table")).toBeVisible();
  });

  it("routes unknown chart metrics through the existing not-found page", async () => {
    renderExplorer("/charts/not-a-metric");
    expect(await screen.findByText("Page not found", { exact: true })).toBeVisible();
    expect(screen.queryByText("Chart range and interval controls")).not.toBeInTheDocument();
  });

  it("shows statistics stage loss as an explicit unavailable state", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input);
        const meta = { request_id: "chart-stage-test", chain_id: "1" };
        if (path === "/api/v1/stats/charts/overview") {
          return Response.json({
            error: {
              code: "analytics_pending",
              message: "historical analytics are still being rebuilt",
              request_id: "chart-stage-test",
            },
          }, { status: 503 });
        }
        return Response.json({ error: { code: "NOT_READY", message: "not ready" } }, { status: 503 });
      }),
    );

    renderExplorer("/charts");

    expect(await screen.findByText("Historical analytics are being rebuilt")).toBeVisible();
    expect(screen.getByText(/Available history appears newest first/)).toBeVisible();
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
      if (path === "/api/v1/verifier/compilers?language=solidity") {
        return Response.json({ data: { language: "solidity", versions: ["0.8.30"] }, meta });
      }
      if (path === `/api/v1/contracts/${address}/verification` && init?.method === "POST") {
        submittedBody = JSON.parse(String(init.body)) as Record<string, unknown>;
        return Response.json({
          data: { id: jobID, kind: "address", status: "queued", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" },
          meta,
        }, { status: 202 });
      }
      if (path === `/api/v1/verifier/jobs/${jobID}`) {
        return Response.json({
          data: {
            id: jobID,
            kind: "address",
            status: "succeeded",
            outcome: {
              kind: "verification_success",
              file_name: "src/Test.sol",
              contract_name: "Test",
              language: "solidity",
              compiler_version: "0.8.30",
              settings: {},
              sources: {},
              compilation_artifacts: {},
              creation_code_artifacts: {},
              runtime_code_artifacts: {},
              libraries: {},
              is_blueprint: false,
              runtime_match: { match_type: "full", transformations: [], values: {} },
            },
            created_at: "2026-01-01T00:00:00Z",
            updated_at: "2026-01-01T00:00:02Z",
          },
          meta,
        });
      }
      return Response.json({ error: { code: "NOT_FOUND", message: "not found" } }, { status: 404 });
    });
    vi.stubGlobal("fetch", fetcher);
    renderExplorer(`/verify?address=${address}`);

    expect(await screen.findByLabelText("Address")).toHaveValue(address);
    expect(await screen.findByLabelText("Compiler version")).toHaveValue("0.8.30");
    fireEvent.change(screen.getByLabelText(/^API key/), { target: { value: secret } });
    await userEvent.setup().click(screen.getByRole("button", { name: "Submit verification" }));

    expect(await screen.findByText("succeeded")).toBeVisible();
    expect(screen.getAllByText("full").length).toBeGreaterThan(0);
    expect(submittedBody).toMatchObject({
      compiler_version: "0.8.30",
      input_kind: "standard_json",
    });
    expect(submittedBody).not.toHaveProperty("address");
    expect(submittedBody).not.toHaveProperty("code_hash");
    expect(submittedBody).not.toHaveProperty("at_block_hash");
    expect(submittedBody).not.toHaveProperty("creation_bytecode");
    expect(submittedBody).not.toHaveProperty("runtime_bytecode");
    const protectedCalls = fetcher.mock.calls.filter(([input]) =>
      String(input).includes("/verification") || String(input).includes("/verifier/jobs/"),
    );
    expect(protectedCalls).toHaveLength(2);
    for (const [url, init] of protectedCalls) {
      expect(String(url)).not.toContain(secret);
      expect(new Headers(init?.headers).get("X-API-Key")).toBe(secret);
    }
    expect(window.location.href).not.toContain(secret);
    expect(storageSpy.mock.calls.every(([, value]) => !String(value).includes(secret))).toBe(true);
    expect(consoleSpy.mock.calls.flat().every((value) => !String(value).includes(secret))).toBe(true);
  });

  it("submits a multi-file Geas address verification with explicit entrypoints", async () => {
    const address = `0x${"34".repeat(20)}`;
    const jobID = "018f3b52-0b3d-7bf1-b65f-6f214827cb63";
    let submittedBody: Record<string, unknown> | undefined;
    const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      const meta = { request_id: "geas-verification-web-test", chain_id: "1" };
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
      if (path === "/api/v1/verifier/compilers?language=solidity") {
        return Response.json({ data: { language: "solidity", versions: ["0.8.30"] }, meta });
      }
      if (path === "/api/v1/verifier/compilers?language=geas") {
        return Response.json({ data: { language: "geas", versions: ["0.3.3"] }, meta });
      }
      if (path === `/api/v1/contracts/${address}/verification` && init?.method === "POST") {
        submittedBody = JSON.parse(String(init.body)) as Record<string, unknown>;
        return Response.json({
          data: { id: jobID, kind: "address", status: "queued", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" },
          meta,
        }, { status: 202 });
      }
      if (path === `/api/v1/verifier/jobs/${jobID}`) {
        return Response.json({
          data: {
            id: jobID,
            kind: "address",
            status: "succeeded",
            outcome: {
              kind: "verification_success",
              file_name: "system/main.eas",
              contract_name: "Withdrawals",
              language: "geas",
              compiler_version: "0.3.3",
              settings: {
                runtime_entrypoint: "system/main.eas",
                creation_entrypoint: "system/ctor.eas",
                stack_check: true,
              },
              sources: {},
              abi: [],
              compilation_artifacts: {},
              creation_code_artifacts: {},
              runtime_code_artifacts: {},
              libraries: {},
              is_blueprint: false,
              creation_match: { match_type: "full", transformations: [], values: {} },
              runtime_match: { match_type: "full", transformations: [], values: {} },
            },
            created_at: "2026-01-01T00:00:00Z",
            updated_at: "2026-01-01T00:00:02Z",
          },
          meta,
        });
      }
      return Response.json({ error: { code: "NOT_FOUND", message: "not found" } }, { status: 404 });
    });
    vi.stubGlobal("fetch", fetcher);
    renderExplorer(`/verify?address=${address}`);

    const user = userEvent.setup();
    await user.selectOptions(await screen.findByLabelText("Language"), "geas");
    expect(screen.getByLabelText("Input format")).toBeDisabled();
    expect(screen.getByLabelText("Input format")).toHaveValue("geas_sources");
    expect(await screen.findByLabelText("Compiler version")).toHaveValue("0.3.3");
    fireEvent.change(screen.getByLabelText("Runtime entrypoint"), { target: { value: "system/main.eas" } });
    fireEvent.change(screen.getByLabelText("Creation entrypoint (optional)"), { target: { value: "system/ctor.eas" } });
    fireEvent.change(screen.getByLabelText("Contract name (optional)"), { target: { value: "Withdrawals" } });
    fireEvent.change(screen.getByLabelText(/^Geas source files/), {
      target: {
        value: JSON.stringify({
          "system/main.eas": "#include \"../common/value.eas\"\npush VALUE\n",
          "system/ctor.eas": "#bytes code: assemble(\"main.eas\")\n",
          "common/value.eas": "#define VALUE = 1\n",
        }),
      },
    });
    fireEvent.change(screen.getByLabelText(/^API key/), { target: { value: "ev_geas_test" } });
    await user.click(screen.getByRole("button", { name: "Submit verification" }));

    expect(await screen.findByText("succeeded")).toBeVisible();
    expect(submittedBody).toEqual({
      compiler_version: "0.3.3",
      contract_name_hint: "Withdrawals",
      creation_entrypoint: "system/ctor.eas",
      input_kind: "geas_sources",
      language: "geas",
      runtime_entrypoint: "system/main.eas",
      sources: {
        "system/main.eas": "#include \"../common/value.eas\"\npush VALUE\n",
        "system/ctor.eas": "#bytes code: assemble(\"main.eas\")\n",
        "common/value.eas": "#define VALUE = 1\n",
      },
    });
    expect(submittedBody).not.toHaveProperty("input");
  });

  it("renders verified source artifacts as inert preformatted text", async () => {
    const address = `0x${"77".repeat(20)}`;
    const codeHash = `0x${"88".repeat(32)}`;
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
      if (path === `/api/v1/addresses/${address}`) {
        return Response.json({
          data: {
            address,
            type: "contract",
            balance: "0",
            nonce: "0",
            code_hash: codeHash,
            at_block: codeHash,
            completeness: {},
          },
          meta,
        });
      }
      if (path === `/api/v1/contracts/${address}/verification`) {
        expect(new Headers(init?.headers).get("X-API-Key")).toBeNull();
        return Response.json({
          data: {
            resolution: "exact_address",
            target: { chain_id: "1", address, code_hash: codeHash, block_number: "12", block_hash: codeHash },
            source: { address, code_hash: codeHash, valid_from_block: "12", created_at: "2026-01-01T00:00:00Z" },
            language: "solidity",
            compiler_version: "0.8.30",
            file_name: "src/Hostile.sol",
            contract_name: "HostileText",
            kind: "verification_success",
            runtime_match: { match_type: "full", transformations: [], values: {} },
            abi: [{ type: "function", name: malicious }],
            sources: { "src/Hostile.sol": { content: malicious } },
            settings: { metadata: { note: "<script>window.__etherviewPwned=true</script>" } },
            compilation_artifacts: {},
            creation_code_artifacts: {},
            runtime_code_artifacts: {},
            libraries: {},
            is_blueprint: false,
          },
          meta,
        });
      }
      if (path === `/api/v1/contracts/${address}/proxy`) {
        return Response.json({
          data: {
            address,
            status: "not_detected",
            snapshot: { chain_id: "1", block_number: "12", block_hash: codeHash },
            evidence: [],
          },
          meta,
        });
      }
      return Response.json({ error: { code: "NOT_FOUND", message: "not found" } }, { status: 404 });
    });
    vi.stubGlobal("fetch", fetcher);
    renderExplorer(`/address/${address}#code`);

    expect(await screen.findByRole("heading", { name: "HostileText", level: 2 })).toBeVisible();
    const editor = screen.getByRole("textbox", {
      name: "Read-only source editor for src/Hostile.sol",
    });
    expect(editor).toHaveAttribute("contenteditable", "false");
    expect(editor).toHaveTextContent(malicious);
    expect(document.querySelector(".contract-code-view img")).toBeNull();
    expect(document.querySelector(".contract-code-view script")).toBeNull();
    expect(screen.queryByLabelText("API key")).not.toBeInTheDocument();
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
          <AuthProvider>
            <RouterProvider router={router} />
          </AuthProvider>
        </WalletProvider>
      </ThemeProvider>
    </QueryClientProvider>,
  );
}
