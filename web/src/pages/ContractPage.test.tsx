import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AuthProvider } from "@/auth/AuthProvider";
import i18n from "@/i18n";
import { makeRouter } from "@/router";
import { ThemeProvider } from "@/theme/ThemeProvider";
import { WalletProvider } from "@/wallet/WalletProvider";

vi.mock("echarts/core", () => ({
  use: vi.fn(),
  init: vi.fn(() => ({
    setOption: vi.fn(),
    resize: vi.fn(),
    dispose: vi.fn(),
  })),
}));
vi.mock("echarts/charts", () => ({ LineChart: {} }));
vi.mock("echarts/components", () => ({
  DataZoomComponent: {},
  GridComponent: {},
  LegendComponent: {},
  TooltipComponent: {},
}));
vi.mock("echarts/renderers", () => ({ CanvasRenderer: {} }));

const proxyAddress = "0x1111111111111111111111111111111111111111";
const implementationAddress = "0x2222222222222222222222222222222222222222";
const managementAddress = "0x3333333333333333333333333333333333333333";
const oldImplementationAddress = "0x4444444444444444444444444444444444444444";
const hash = `0x${"ab".repeat(32)}`;
const oldHash = `0x${"cd".repeat(32)}`;
const bindingId = "018f3b52-0b3d-7bf1-b65f-6f214827cb41";
const nextCursor = "proxy/snapshot + page=2?fork=canonical/#";

type ExactPattern = "uups" | "transparent" | "beacon";
type ContractPattern = ExactPattern | "clone" | "none";
type HistoryKind = "upgrades" | "initializations";

beforeEach(async () => {
  await i18n.changeLanguage("en");
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("contract proxy route", () => {
  it("explains when the contract has no verified artifact", async () => {
    installContractAPI({ pattern: "none", verificationStatus: 404 });
    renderContractRoute();

    expect(await screen.findByText(
      "This contract has not been verified yet. Its source code and ABI-based read/write forms are unavailable until verification is complete.",
    )).toBeVisible();
    expect(screen.queryByText("Indexed entity not found")).not.toBeInTheDocument();

    await userEvent.setup().click(screen.getByRole("button", { name: "切换到中文" }));
    expect(await screen.findByText(
      "该合约尚未完成验证。完成验证后才能查看源码并使用基于 ABI 的合约读写功能。",
    )).toBeVisible();
  });

  it("restores contract subpages from hashes and navigates between them", async () => {
    installContractAPI({ pattern: "uups" });
    renderContractRoute("read-contract");

    const tabs = await screen.findByRole("tablist", { name: "Contract interaction sections" });
    const read = within(tabs).getByRole("tab", { name: "Read contract" });
    expect(read).toHaveAttribute("aria-selected", "true");
    await userEvent.setup().click(await within(tabs).findByRole("tab", { name: "Write contract" }));
    expect(within(tabs).getByRole("tab", { name: "Write contract" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });

  it("falls back to Code when a requested subpage is unavailable", async () => {
    installContractAPI({ pattern: "none" });
    renderContractRoute("management");

    const tabs = await screen.findByRole("tablist", { name: "Contract interaction sections" });
    await waitFor(() => expect(within(tabs).getByRole("tab", { name: "Code" })).toHaveAttribute(
      "aria-selected",
      "true",
    ));
    expect(within(tabs).queryByRole("tab", { name: "Management" })).toBeNull();
  });

  it("preserves a requested subpage while a contract API request is temporarily unavailable", async () => {
    installContractAPI({ pattern: "none", proxyStatus: 503 });
    renderContractRoute("management");

    const tabs = await screen.findByRole("tablist", { name: "Contract interaction sections" });
    await waitFor(() => expect(within(tabs).getByRole("tab", { name: "Proxy management" })).toHaveAttribute(
      "aria-selected",
      "true",
    ));
  });

  it.each([
    ["code", "Code"],
    ["read-contract", "Read contract"],
    ["write-contract", "Write contract"],
    ["read-implementation", "Read implementation (as proxy)"],
    ["write-implementation", "Write implementation (as proxy)"],
    ["management", "Proxy management"],
    ["upgrades", "Upgrade history"],
    ["initializations", "Initialization history"],
  ] as const)("restores the %s deep link", async (hashID, label) => {
    installContractAPI({ pattern: "transparent" });
    renderContractRoute(hashID);

    const tabs = await screen.findByRole("tablist", { name: "Contract interaction sections" });
    await waitFor(() => expect(within(tabs).getByRole("tab", { name: label })).toHaveAttribute(
      "aria-selected",
      "true",
    ));
  });

  it("shows a confirmed Safe shell and singleton without enabling legacy proxy writes", async () => {
    installContractAPI({ pattern: "none", safeDetection: true });
    renderContractRoute();

    await screen.findByRole("heading", { name: "Proxy identity" });
    const summary = document.querySelector("details.proxy-summary");
    expect(summary).not.toBeNull();
    summary!.setAttribute("open", "");
    expect(await screen.findByText("Safe Proxy")).toBeVisible();
    expect(screen.getAllByText("Confirmed").length).toBeGreaterThan(0);
    expect(screen.getByText(implementationAddress)).toBeVisible();
    expect(screen.queryByRole("tab", { name: "Read implementation (as proxy)" })).not.toBeInTheDocument();
  });

  it.each([
    { pattern: "uups", managementKind: undefined },
    { pattern: "transparent", managementKind: "proxy_admin" },
    { pattern: "beacon", managementKind: "upgradeable_beacon" },
  ] as const)(
    "loads anonymous artifacts and exact $pattern interaction targets",
    async ({ pattern, managementKind }) => {
      const fetcher = installContractAPI({ pattern });
      const user = userEvent.setup();

      renderContractRoute();

      expect(
        await screen.findByRole("heading", { name: "Verified artifact" }),
      ).toBeVisible();
      const codePanel = document.getElementById("contract-panel-code");
      expect(codePanel).not.toBeNull();
      await within(codePanel!).findByRole("heading", { name: "Proxy identity" });
      expect(codePanel!.querySelector("details.proxy-summary")).not.toHaveAttribute("open");
      expect(
        within(codePanel!).getByRole("heading", { name: "Proxy identity" }),
      ).toBeVisible();
      const tabs = await screen.findByRole("tablist", {
        name: "Contract interaction sections",
      });
      expect(within(tabs).getByRole("tab", { name: "Read contract" })).toBeVisible();
      expect(within(tabs).getByRole("tab", { name: "Write contract" })).toBeVisible();
      expect(
        await within(tabs).findByRole("tab", {
          name: "Read implementation (as proxy)",
        }),
      ).toBeVisible();
      expect(
        within(tabs).getByRole("tab", {
          name: "Write implementation (as proxy)",
        }),
      ).toBeVisible();
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

      await user.click(
        within(tabs).getByRole("tab", {
          name: "Read implementation (as proxy)",
        }),
      );
      expect(
        within(screen.getByRole("tabpanel")).getByText("value()", { exact: true }),
      ).toBeVisible();

      const managementTab = within(tabs).queryByRole("tab", {
        name: "Proxy management",
      });
      if (managementKind === undefined) {
        expect(managementTab).toBeNull();
      } else {
        expect(managementTab).toBeVisible();
        await user.click(managementTab!);
        expect(
          within(screen.getByRole("tabpanel")).getByText("value()", { exact: true }),
        ).toBeVisible();
      }

      await waitFor(() => {
        const artifactAddresses = contractRequests(fetcher)
          .filter(({ url }) => url.pathname.endsWith("/verification"))
          .map(({ url }) => url.pathname.split("/").at(-2));
        expect(artifactAddresses).toContain(proxyAddress);
        expect(artifactAddresses).toContain(implementationAddress);
        if (managementKind !== undefined) {
          expect(artifactAddresses).toContain(managementAddress);
        }
      });
      expectAnonymousContractRequests(fetcher);
      expect(screen.queryByRole("textbox", { name: /api key/iu })).toBeNull();
      expect(screen.queryByRole("textbox", { name: /abi json/iu })).toBeNull();
      expect(screen.queryByRole("textbox", { name: /calldata/iu })).toBeNull();
      expect(screen.queryByRole("button", { name: "Load verification" })).toBeNull();
    },
  );

  it("keeps mismatched implementation and management artifact code identities closed", async () => {
    const fetcher = installContractAPI({
      pattern: "transparent",
      implementationArtifactCodeHash: oldHash,
      managementArtifactCodeHash: oldHash,
    });

    renderContractRoute();

    const tabs = await screen.findByRole("tablist", {
      name: "Contract interaction sections",
    });
    await waitFor(() => {
      const artifactAddresses = contractRequests(fetcher)
        .filter(({ url }) => url.pathname.endsWith("/verification"))
        .map(({ url }) => url.pathname.split("/").at(-2));
      expect(artifactAddresses).toContain(implementationAddress);
      expect(artifactAddresses).toContain(managementAddress);
    });
    expect(within(tabs).getByRole("tab", { name: "Read contract" })).toBeVisible();
    expect(
      within(tabs).queryByRole("tab", { name: "Read implementation (as proxy)" }),
    ).toBeNull();
    expect(within(tabs).queryByRole("tab", { name: "Proxy management" })).toBeNull();
    expectAnonymousContractRequests(fetcher);
  });

  it("shows an immutable Clone without an upgrade tab or upgrade request", async () => {
    const fetcher = installContractAPI({ pattern: "clone" });

    renderContractRoute();

    const user = userEvent.setup();
    await user.click(await screen.findByRole("heading", { name: "Proxy identity" }));
    expect(
      await screen.findByText(
        "This EIP-1167 Clone is immutable. No upgrade controls or fabricated upgrade history are exposed.",
      ),
    ).toBeVisible();
    const tabs = screen.getByRole("tablist", {
      name: "Contract interaction sections",
    });
    expect(within(tabs).queryByRole("tab", { name: "Upgrade history" })).toBeNull();
    expect(
      within(tabs).getByRole("tab", { name: "Initialization history" }),
    ).toBeVisible();
    expect(
      await within(tabs).findByRole("tab", {
        name: "Read implementation (as proxy)",
      }),
    ).toBeVisible();
    expect(
      within(tabs).getByRole("tab", {
        name: "Write implementation (as proxy)",
      }),
    ).toBeVisible();
    await waitFor(() => {
      expect(
        contractRequests(fetcher).some(({ url }) =>
          url.pathname.endsWith("/proxy/upgrades"),
        ),
      ).toBe(false);
    });
  });

  it("hides proxy identity and proxy histories for a non-proxy contract", async () => {
    const fetcher = installContractAPI({ pattern: "none" });

    renderContractRoute();

    expect(
      await screen.findByRole("heading", { name: "Verified artifact" }),
    ).toBeVisible();
    expect(screen.queryByRole("heading", { name: "Proxy identity" })).toBeNull();
    const tabs = screen.getByRole("tablist", {
      name: "Contract interaction sections",
    });
    expect(within(tabs).queryByRole("tab", { name: "Upgrade history" })).toBeNull();
    expect(
      within(tabs).queryByRole("tab", { name: "Initialization history" }),
    ).toBeNull();
    await waitFor(() => {
      expect(
        contractRequests(fetcher).some(({ url }) =>
          url.pathname.endsWith("/proxy/upgrades") ||
          url.pathname.endsWith("/proxy/initializations"),
        ),
      ).toBe(false);
    });
  });

  it.each([
    { kind: "upgrades", tab: "Upgrade history" },
    { kind: "initializations", tab: "Initialization history" },
  ] as const)(
    "resets a stale $kind snapshot cursor to the first page",
    async ({ kind, tab }) => {
      const fetcher = installContractAPI({ pattern: "transparent", staleHistory: kind });
      const user = userEvent.setup();

      renderContractRoute();

      const historyTab = await screen.findByRole("tab", { name: tab });
      await user.click(historyTab);
      expect(await screen.findByText("Page 1")).toBeVisible();
      const next = screen.getByRole("button", { name: "Next page" });
      await waitFor(() => expect(next).toBeEnabled());
      await user.click(next);

      expect(
        await screen.findByText("This page cursor is no longer valid"),
      ).toBeVisible();
      expect(screen.getByText("Page 2")).toBeVisible();
      await user.click(
        screen.getByRole("button", { name: "Restart from the first page" }),
      );

      expect(await screen.findByText("Page 1")).toBeVisible();
      await waitFor(() => {
        expect(historyRequests(fetcher, kind, false)).toHaveLength(2);
      });
      expect(historyRequests(fetcher, kind, true)).toHaveLength(1);
      expectAnonymousContractRequests(fetcher);
    },
  );

  it.each([
    {
      language: "en",
      tab: "Upgrade history",
      pattern: "Beacon proxy",
      mechanism: "Beacon delegation",
      evidenceState: "Exact",
      confidence: "Verified",
      evidenceSource: "Runtime immutable",
      evidenceResult: "Authoritative",
      coverage: "Complete coverage",
      range: "Covered blocks 1–42",
      event: "Event",
      logIndex: "Log index",
      emitter: "Event emitter",
      beacon: "Beacon evidence",
      management: "UpgradeableBeacon evidence",
    },
    {
      language: "zh",
      tab: "升级历史",
      pattern: "Beacon 代理",
      mechanism: "Beacon 委托",
      evidenceState: "精确",
      confidence: "已验证",
      evidenceSource: "运行时 immutable 值",
      evidenceResult: "权威证据",
      coverage: "覆盖完整",
      range: "覆盖区块 1–42",
      event: "事件",
      logIndex: "日志索引",
      emitter: "事件发出地址",
      beacon: "Beacon 证据",
      management: "UpgradeableBeacon 证据",
    },
  ] as const)(
    "renders localized proxy enums and complete Beacon history evidence in $language",
    async (labels) => {
      await i18n.changeLanguage(labels.language);
      installContractAPI({ pattern: "beacon" });
      const user = userEvent.setup();

      renderContractRoute();

      const proxyTitle = labels.language === "en" ? "Proxy identity" : "代理身份";
      await user.click(await screen.findByRole("heading", { name: proxyTitle }));
      expect(await screen.findByText(labels.pattern)).toBeVisible();
      expect(screen.getByText(labels.mechanism)).toBeVisible();
      expect(screen.getByText(labels.evidenceState)).toBeVisible();
      expect(screen.getAllByText(labels.confidence).length).toBeGreaterThan(0);
      await user.click(screen.getByText(/Recognition evidence|识别证据/u));
      expect(screen.getByText(labels.evidenceSource, { exact: false })).toBeVisible();
      expect(screen.getByText(labels.evidenceResult, { exact: false })).toBeVisible();

      await user.click(screen.getByRole("tab", { name: labels.tab }));
      expect(await screen.findByText(labels.coverage)).toBeVisible();
      expect(screen.getByText(labels.range)).toBeVisible();
      expect(screen.getByText(labels.event)).toBeVisible();
      expect(screen.getByText(labels.logIndex, { exact: false })).toBeVisible();
      expect(screen.getByText(labels.emitter, { exact: false })).toBeVisible();
      expect(screen.getAllByText(labels.beacon, { exact: false }).length).toBeGreaterThan(0);
      expect(screen.getByText(labels.management, { exact: false })).toBeVisible();
      expect(screen.getAllByText(managementAddress).length).toBeGreaterThan(0);
    },
  );
});

function renderContractRoute(hash = "code") {
  const router = makeRouter(
    createMemoryHistory({ initialEntries: [`/address/${proxyAddress}#${hash}`] }),
  );
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, refetchOnWindowFocus: false },
    },
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

function installContractAPI({
  pattern,
  staleHistory,
  implementationArtifactCodeHash,
  managementArtifactCodeHash,
  safeDetection,
  proxyStatus,
  verificationStatus,
}: {
  pattern: ContractPattern;
  staleHistory?: HistoryKind;
  implementationArtifactCodeHash?: string;
  managementArtifactCodeHash?: string;
  safeDetection?: boolean;
  proxyStatus?: number;
  verificationStatus?: number;
}) {
  const fetcher = vi.fn<typeof fetch>().mockImplementation(async (input) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/config") {
      return envelope({
        chain_id: "1",
        chain_name: "Proxy Testnet",
        native_symbol: "ETH",
        native_name: "Ether",
        native_decimals: 18,
        features: { user_auth: false },
      });
    }
    if (url.pathname === `/api/v1/addresses/${proxyAddress}`) {
      return envelope({
        address: proxyAddress,
        type: "contract",
        balance: "0",
        nonce: "0",
        code_hash: hash,
        at_block: hash,
        completeness: {},
      });
    }
    if (url.pathname === `/api/v1/contracts/${proxyAddress}/proxy`) {
      if (proxyStatus !== undefined) {
        return Response.json(
          { error: { code: "temporary_failure", message: "temporarily unavailable" } },
          { status: proxyStatus },
        );
      }
      const detail = proxyDetail(pattern);
      return envelope(safeDetection ? {
        ...detail,
        proxy_detection_v2: safeProxyDetection(),
      } : detail);
    }
    if (url.pathname.endsWith("/verification")) {
      if (verificationStatus !== undefined) {
        return Response.json(
          {
            error: {
              code: "not_found",
              message: "no verified artifact",
              request_id: "contract-page-test",
            },
          },
          { status: verificationStatus },
        );
      }
      const address = url.pathname.split("/").at(-2) ?? "";
      const codeHash = address === implementationAddress
        ? implementationArtifactCodeHash
        : address === managementAddress
          ? managementArtifactCodeHash
          : undefined;
      return envelope(verifiedArtifact(address, codeHash));
    }
    if (url.pathname === `/api/v1/contracts/${proxyAddress}/proxy/upgrades`) {
      if (staleHistory === "upgrades" && url.searchParams.has("cursor")) {
        return staleCursor();
      }
      return envelope(upgradeHistory(pattern), { next_cursor: nextCursor });
    }
    if (
      url.pathname ===
      `/api/v1/contracts/${proxyAddress}/proxy/initializations`
    ) {
      if (
        staleHistory === "initializations" &&
        url.searchParams.has("cursor")
      ) {
        return staleCursor();
      }
      return envelope(initializationHistory(), { next_cursor: nextCursor });
    }
    return Response.json(
      {
        error: {
          code: "not_found",
          message: "not found",
          request_id: "contract-page-test",
        },
      },
      { status: 404 },
    );
  });
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}

function safeProxyDetection() {
  const primary = {
    detector: "safe",
    detector_version: "1.0.0+manifest.1",
    priority: 200,
    family: "safe" as const,
    variant: "safe-proxy",
    status: "confirmed" as const,
    confidence: "high" as const,
    proxy: proxyAddress,
    implementation: implementationAddress,
    implementation_role: "singleton" as const,
    implementation_path: [proxyAddress, implementationAddress],
    canonical_proxy_shell: true,
    implementation_has_code: true,
    official_singleton: false,
    singleton_changed: false,
    evidence: [],
    warnings: [],
    chain_id: "1",
    block_number: "42",
    block_hash: hash,
  };
  return {
    status: "confirmed" as const,
    primary,
    outcomes: [primary],
    conflicts: [],
    shadow_diff: { different: true, reasons: ["v2_positive_legacy_not_detected"] },
  };
}

function contractRequests(fetcher: ReturnType<typeof installContractAPI>) {
  return fetcher.mock.calls
    .map(([input, init]) => ({
      url: new URL(String(input), "http://localhost"),
      init,
    }))
    .filter(({ url }) => url.pathname.startsWith("/api/v1/contracts/"));
}

function historyRequests(
  fetcher: ReturnType<typeof installContractAPI>,
  kind: HistoryKind,
  withCursor: boolean,
) {
  return contractRequests(fetcher).filter(
    ({ url }) =>
      url.pathname.endsWith(`/proxy/${kind}`) === true &&
      url.searchParams.has("cursor") === withCursor,
  );
}

function expectAnonymousContractRequests(
  fetcher: ReturnType<typeof installContractAPI>,
) {
  const requests = contractRequests(fetcher);
  expect(requests.length).toBeGreaterThan(0);
  for (const { url, init } of requests) {
    expect(url.origin).toBe("http://localhost");
    expect(url.pathname.startsWith("/api/v1/")).toBe(true);
    const headers = new Headers(init?.headers);
    expect(headers.has("X-API-Key")).toBe(false);
    expect(headers.has("PAYMENT-SIGNATURE")).toBe(false);
    expect(headers.has("X-CSRF-Token")).toBe(false);
  }
}

function envelope(data: unknown, meta: Record<string, unknown> = {}) {
  return Response.json({
    data,
    meta: {
      request_id: "contract-page-test",
      chain_id: "1",
      ...meta,
    },
  });
}

function staleCursor() {
  return Response.json(
    {
      error: {
        code: "invalid_cursor",
        message: "cursor is stale after a canonical change",
        request_id: "contract-page-test",
      },
    },
    { status: 400 },
  );
}

function snapshot() {
  return {
    chain_id: "1",
    block_number: "42",
    block_hash: hash,
  };
}

function proxyDetail(pattern: ContractPattern) {
  if (pattern === "none") {
    return {
      address: proxyAddress,
      status: "not_detected",
      snapshot: snapshot(),
      evidence: [],
    };
  }
  const management = pattern === "transparent"
    ? {
        kind: "proxy_admin" as const,
        target: currentIdentity(managementAddress, "proxy_admin"),
        affected_proxy_count: "1",
      }
    : pattern === "beacon"
      ? {
          kind: "upgradeable_beacon" as const,
          target: currentIdentity(managementAddress, "upgradeable_beacon"),
          affected_proxy_count: "2",
        }
      : undefined;
  const mechanism = pattern === "clone"
    ? "eip1167"
    : pattern === "beacon"
      ? "beacon"
      : "eip1967";
  return {
    address: proxyAddress,
    status: "verified",
    snapshot: snapshot(),
    mechanism,
    pattern,
    ...(pattern === "clone" ? {} : { standard_version: "5.6.1" }),
    evidence_state: "exact",
    confidence: "verified",
    binding_id: bindingId,
    proxy: pattern === "clone"
      ? {
          address: proxyAddress,
          code_hash: hash,
          verification_state: "unverified" as const,
        }
      : currentIdentity(
          proxyAddress,
          pattern === "transparent"
            ? "transparent_proxy"
            : pattern === "beacon"
              ? "beacon_proxy"
              : "erc1967_proxy",
        ),
    implementation: currentIdentity(
      implementationAddress,
      pattern === "uups" ? "uups_implementation" : undefined,
    ),
	implementation_interaction: {
		mechanism,
		pattern,
		proxy: pattern === "clone"
			? {
				address: proxyAddress,
				code_hash: hash,
				verification_state: "unverified" as const,
			}
			: currentIdentity(
				proxyAddress,
				pattern === "transparent"
					? "transparent_proxy"
					: pattern === "beacon"
						? "beacon_proxy"
						: "erc1967_proxy",
			),
		implementation: currentIdentity(
			implementationAddress,
			pattern === "uups" ? "uups_implementation" : undefined,
		),
		...(pattern === "beacon"
			? { beacon: currentIdentity(managementAddress, "upgradeable_beacon") }
			: {}),
	},
    ...(pattern === "transparent"
      ? { admin: currentIdentity(managementAddress, "proxy_admin") }
      : {}),
    ...(pattern === "beacon"
      ? { beacon: currentIdentity(managementAddress, "upgradeable_beacon") }
      : {}),
    ...(management ? { management } : {}),
    ...(pattern === "clone" ? { immutable_args: "0x1234" } : {}),
    evidence: pattern === "beacon"
      ? [{
          source: "runtime_immutable",
          subject: "beacon",
          result: "authoritative",
          address: managementAddress,
          block_number: "42",
          block_hash: hash,
        }]
      : [],
  };
}

function currentIdentity(
  address: string,
  artifactKind?:
    | "erc1967_proxy"
    | "transparent_proxy"
    | "beacon_proxy"
    | "uups_implementation"
    | "proxy_admin"
    | "upgradeable_beacon",
) {
  return {
    address,
    code_hash: hash,
    verification_state: "verified",
    ...(artifactKind ? { artifact_kind: artifactKind } : {}),
    ...(artifactKind && artifactKind !== "erc1967_proxy"
      ? { standard_version: "5.6.1" }
      : {}),
  };
}

function verifiedArtifact(address: string, codeHash = hash) {
  return {
    kind: "verification_success",
    file_name: "Fixture.sol",
    contract_name: address === proxyAddress ? "ProxyFixture" : "TargetFixture",
    language: "solidity",
    compiler_version: "0.8.30",
    settings: {},
    sources: {},
    abi: [
      {
        type: "function",
        name: "value",
        stateMutability: "view",
        inputs: [],
        outputs: [{ name: "", type: "uint256" }],
      },
      {
        type: "function",
        name: "setValue",
        stateMutability: "nonpayable",
        inputs: [{ name: "value", type: "uint256" }],
        outputs: [],
      },
    ],
    compilation_artifacts: {},
    creation_code_artifacts: {},
    runtime_code_artifacts: {},
    libraries: {},
    is_blueprint: false,
		resolution: "exact_address",
		target: {
			chain_id: "1", address, code_hash: codeHash,
			block_number: "42", block_hash: hash,
		},
		source: {
			address, code_hash: codeHash, valid_from_block: "1",
			created_at: "2026-08-02T00:00:00Z",
		},
	};
}

function upgradeHistory(pattern: ContractPattern) {
  const beaconEvidence = pattern === "beacon"
    ? {
        beacon: historicalIdentity(managementAddress, hash),
        emitter_address: managementAddress,
        management: {
          kind: "upgradeable_beacon" as const,
          target: historicalIdentity(managementAddress, hash),
        },
      }
    : {};
  return {
    proxy_address: proxyAddress,
    snapshot: snapshot(),
    coverage: { state: "complete", from_block: "1", to_block: "42" },
    items: [
      {
        change_type: pattern === "beacon" ? "beacon_implementation" : "implementation",
        evidence_type: "event",
        old_implementation: historicalIdentity(oldImplementationAddress, oldHash),
        new_implementation: historicalIdentity(implementationAddress, hash),
        block_number: "40",
        block_hash: hash,
        block_timestamp: "2026-08-02T00:00:00Z",
        transaction_hash: hash,
        log_index: "7",
        ...beaconEvidence,
      },
    ],
  };
}

function initializationHistory() {
  return {
    contract_address: proxyAddress,
    snapshot: snapshot(),
    coverage: { state: "partial", from_block: "1", to_block: "42" },
    items: [
      {
        version: "2",
        block_number: "41",
        block_hash: hash,
        block_timestamp: "2026-08-02T00:00:00Z",
        transaction_hash: hash,
        log_index: "1",
        implementation: historicalIdentity(implementationAddress, hash),
      },
    ],
  };
}

function historicalIdentity(address: string, codeHash: string) {
  return {
    address,
    code_hash: codeHash,
    verification_state: "verified",
  };
}
