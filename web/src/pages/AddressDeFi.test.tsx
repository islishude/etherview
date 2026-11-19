import { screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, afterEach, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import {
  completeness,
  configResponse,
  envelope,
  notFound,
  renderExplorer,
  requestURL,
} from "@/test/corePageFixtures";
import type { DeFiPositionsPage } from "@/api/heyapi/types.gen";
const owner = `0x${"11".repeat(20)}`;
const token = `0x${"22".repeat(20)}`;
const hash = `0x${"ab".repeat(32)}`;
let data: DeFiPositionsPage;
let requests: URL[];
let failure: boolean;
beforeEach(async () => {
  await i18n.changeLanguage("en");
  requests = [];
  failure = false;
  data = {
    deployments: [
      {
        protocol: "aave-v3",
        deployment: "aave",
        state: "complete",
        block_number: "99",
        block_hash: hash,
      },
    ],
    positions: [
      {
        id: "loan",
        protocol: "aave-v3",
        deployment: "aave",
        owner,
        kind: "borrow",
        contract: token,
        assets: [],
        debts: [{ kind: "erc20", address: token, amount: "9007199254740993123", decimals: 18 }],
        claimable: [],
        unsettled_fees: [],
        state: "available",
        limitations: [],
        block_number: "99",
        block_hash: hash,
      },
    ],
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      requests.push(url);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/addresses/${owner}`)
        return envelope({
          address: owner,
          type: "eoa",
          balance: "0",
          nonce: "0",
          at_block: hash,
          completeness: completeness(),
        });
      if (url.pathname.endsWith("/defi-positions")) {
        if (failure)
          return Response.json(
            { error: { code: "query_failed", message: "Unavailable", request_id: "test" } },
            { status: 503 },
          );
        return envelope(data);
      }
      if (url.pathname.endsWith("/erc20-balances")) return envelope([]);
      return notFound();
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());
it("deep-links direct positions without fetching token or NFT balances", async () => {
  renderExplorer(`/address/${owner}?tab=assets&asset=defi`);
  expect(await screen.findByText("9.007199254740993123")).toBeVisible();
  const region = screen.getByRole("region", { name: "DeFi positions" });
  expect(within(region).getByRole("heading", { name: "Debt" })).toBeVisible();
  expect(
    within(region).queryByRole("heading", { name: "Underlying assets" }),
  ).not.toBeInTheDocument();
  expect(requests.some((url) => /\/(nfts|erc20-balances)$/.test(url.pathname))).toBe(false);
  expect(screen.getByRole("link", { name: "DeFi" })).toHaveAttribute("aria-current", "page");
});
it.each(["scanning", "partial", "unavailable", "not_configured"] as const)(
  "never claims no assets for %s coverage",
  async (state) => {
    data.positions = [];
    data.deployments[0]!.state = state;
    renderExplorer(`/address/${owner}?tab=assets&asset=defi`);
    await screen.findByRole("heading", { name: "Aave V3" });
    expect(
      screen.queryByText("No positions in the selected completed snapshots."),
    ).not.toBeInTheDocument();
  },
);
it("queries only after opening DeFi and filters through the generated client", async () => {
  const user = userEvent.setup();
  renderExplorer(`/address/${owner}?tab=assets`);
  await screen.findByRole("link", { name: "DeFi" });
  expect(requests.some((url) => url.pathname.endsWith("/defi-positions"))).toBe(false);
  await user.click(screen.getByRole("link", { name: "DeFi" }));
  await screen.findByRole("heading", { name: "Aave V3" });
  await user.selectOptions(screen.getByLabelText("Protocol"), "aave-v3");
  await waitFor(() =>
    expect(requests.some((url) => url.searchParams.get("protocol") === "aave-v3")).toBe(true),
  );
});
it("shows a typed query error without stale balances", async () => {
  failure = true;
  renderExplorer(`/address/${owner}?tab=assets&asset=defi`);
  await screen.findByText("Explorer API is not available yet");
  expect(screen.queryByText("9.007199254740993123")).not.toBeInTheDocument();
});
it("localizes the new tab and quantity groups", async () => {
  await i18n.changeLanguage("zh");
  renderExplorer(`/address/${owner}?tab=assets&asset=defi`);
  expect(await screen.findByRole("heading", { name: "债务" })).toBeVisible();
  expect(screen.getByLabelText("协议")).toBeVisible();
});

it.each([
  ["uniswap-v2", "liquidity", "UNI-V2", "LP tokens held"],
  ["curve", "liquidity", "3CRV", "LP tokens held"],
  ["aave-v3", "supply", "aUSDC", "Receipt tokens held"],
  ["aave-v3", "borrow", "variableDebtUSDC", "Debt tokens"],
  ["lido", "staking", "wstETH", "Tokens held"],
] as const)(
  "shows %s %s receipt holdings separately from underlying quantities",
  async (protocol, kind, symbol, label) => {
    data.deployments[0]!.protocol = protocol;
    data.positions[0] = {
      ...data.positions[0]!,
      protocol,
      kind,
      receipt_asset: {
        kind: "erc20",
        address: token,
        amount: "12345000000000000000",
        decimals: 18,
        symbol,
      },
      assets:
        kind === "borrow"
          ? []
          : [
              {
                kind: "erc20",
                address: owner,
                amount: "250000000000000000",
                decimals: 18,
                symbol: "WETH",
              },
              {
                kind: "erc20",
                address: `0x${"33".repeat(20)}`,
                amount: "800000000",
                decimals: 6,
                symbol: "USDC",
              },
            ],
      debts: kind === "borrow" ? data.positions[0]!.debts : [],
    };
    renderExplorer(`/address/${owner}?tab=assets&asset=defi`);
    expect(await screen.findByRole("heading", { name: label })).toBeVisible();
    expect(screen.getByText("12.345", { exact: true })).toBeVisible();
    const card = screen.getByRole("article");
    expect(within(card).getAllByRole("link", { name: symbol })[0]).toHaveAttribute(
      "href",
      `/token/${token}`,
    );
    if (protocol === "uniswap-v2") {
      expect(within(card).getByRole("heading", { name: "WETH / USDC" })).toBeVisible();
      expect(within(card).getByText("0.25", { exact: true })).toBeVisible();
      expect(within(card).getByText("800", { exact: true })).toBeVisible();
      expect(within(card).queryByText("ETH", { exact: true })).not.toBeInTheDocument();
    }
  },
);

it.each(["uniswap-v3", "uniswap-v4"] as const)(
  "shows %s NFT identity, fee and range without an LP balance",
  async (protocol) => {
    data.deployments[0]!.protocol = protocol;
    data.positions[0] = {
      ...data.positions[0]!,
      protocol,
      kind: "liquidity-nft",
      token_id: "9007199254740993123",
      fee: 3000,
      range: {
        lower_tick: -60,
        upper_tick: 60,
        current_tick: 0,
        sqrt_price_x96: "79228162514264337593543950336",
        in_range: true,
      },
      assets: [{ kind: "erc20", address: token, amount: "0", decimals: 6, symbol: "USDC" }],
      debts: [],
      claimable: [
        { kind: "erc20", address: token, amount: "1000000", decimals: 6, symbol: "USDC" },
      ],
      limitations: ["recorded_claimable_may_include_principal"],
    };
    renderExplorer(`/address/${owner}?tab=assets&asset=defi`);
    expect(await screen.findByText("Position NFT #9,007,199,254,740,993,123")).toBeVisible();
    expect(screen.getByText("Fee: 0.3%")).toBeVisible();
    expect(screen.getByText("In range", { exact: true })).toBeVisible();
    expect(screen.getByRole("heading", { name: "Recorded claimable" })).toBeVisible();
    expect(screen.queryByRole("heading", { name: "LP tokens held" })).not.toBeInTheDocument();
    expect(
      within(screen.getByRole("article")).getByText("0", {
        selector: ".defi-quantity",
        exact: true,
      }),
    ).toBeVisible();
  },
);

it.each(["pending", "claimable"] as const)(
  "shows Lido %s requests without ERC-20 receipts",
  async (state) => {
    data.deployments[0]!.protocol = "lido";
    data.positions[0] = {
      ...data.positions[0]!,
      protocol: "lido",
      kind: "withdrawal",
      token_id: "42",
      state,
      debts: [],
      assets:
        state === "pending"
          ? [
              {
                kind: "erc20",
                address: token,
                amount: "2000000000000000000",
                decimals: 18,
                symbol: "stETH",
              },
            ]
          : [],
      claimable: state === "claimable" ? [{ kind: "native", amount: "1800000000000000000" }] : [],
    };
    renderExplorer(`/address/${owner}?tab=assets&asset=defi`);
    expect(await screen.findByText("Position NFT #42")).toBeVisible();
    expect(
      screen.getByRole("heading", {
        name: state === "pending" ? "Requested amount" : "Recorded claimable",
      }),
    ).toBeVisible();
    if (state === "claimable") expect(screen.getByText("1.8", { exact: true })).toBeVisible();
    expect(screen.queryByRole("heading", { name: "Receipt tokens held" })).not.toBeInTheDocument();
  },
);

it("keeps unknown precision atomic and distinguishes same-symbol token addresses", async () => {
  data.positions[0]!.debts = [
    { kind: "erc20", address: token, amount: "9007199254740993123", symbol: "SAME" },
    { kind: "erc20", address: owner, amount: "0", symbol: "SAME" },
  ];
  renderExplorer(`/address/${owner}?tab=assets&asset=defi`);
  expect(await screen.findByText(/9,007,199,254,740,993,123/)).toBeVisible();
  const card = screen.getByRole("article");
  expect(within(card).getByText("0x222222…222222")).toBeVisible();
  expect(within(card).getByText("0x111111…111111")).toBeVisible();
  expect(
    within(card)
      .getAllByRole("link", { name: "SAME" })
      .map((link) => link.getAttribute("href")),
  ).toEqual(expect.arrayContaining([`/token/${token}`, `/token/${owner}`]));
});

it("does not invent pair assets for unsupported hooks", async () => {
  data.deployments[0]!.protocol = "uniswap-v4";
  data.positions[0] = {
    ...data.positions[0]!,
    protocol: "uniswap-v4",
    kind: "liquidity-nft",
    token_id: "1",
    state: "unsupported",
    assets: [],
    debts: [],
    limitations: ["hook_accounting_not_supported"],
  };
  renderExplorer(`/address/${owner}?tab=assets&asset=defi`);
  expect(await screen.findByText("Unsupported accounting")).toBeVisible();
  expect(screen.queryByRole("heading", { name: "Underlying assets" })).not.toBeInTheDocument();
  expect(
    within(screen.getByRole("article")).queryByText("0", { exact: true }),
  ).not.toBeInTheDocument();
});
