import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { appearanceCases, collectBrowserErrors, configureAppearance } from "./appearance";
import type { DeFiPositionsPage } from "../src/api/heyapi/types.gen";

const owner = `0x${"1".repeat(40)}`;
const token = `0x${"2".repeat(40)}`;
const hash = `0x${"ab".repeat(32)}`;
const fixture: DeFiPositionsPage = {
  deployments: [
    {
      protocol: "aave-v3",
      deployment: "aave",
      state: "complete",
      block_number: "99",
      block_hash: hash,
    },
    {
      protocol: "uniswap-v4",
      deployment: "uniswap",
      state: "partial",
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
    {
      id: "lp",
      protocol: "uniswap-v4",
      deployment: "uniswap",
      owner,
      kind: "liquidity-nft",
      contract: token,
      token_id: (2n ** 256n - 1n).toString(),
      pool_id: hash,
      hook: token,
      assets: [],
      debts: [],
      claimable: [],
      unsettled_fees: [],
      state: "unsupported",
      limitations: ["hook_accounting_not_supported"],
      block_number: "99",
      block_hash: hash,
    },
  ],
};

const receipt = {
  kind: "erc20" as const,
  address: token,
  amount: "12345000000000000000",
  decimals: 18,
  symbol: "UNI-V2",
};
const underlying = [
  {
    kind: "erc20" as const,
    address: `0x${"3".repeat(40)}`,
    amount: "250000000000000000",
    decimals: 18,
    symbol: "WETH",
  },
  {
    kind: "erc20" as const,
    address: `0x${"4".repeat(40)}`,
    amount: "800000000",
    decimals: 6,
    symbol: "USDC",
  },
];
for (const protocol of ["uniswap-v2", "uniswap-v3", "curve", "lido"] as const) {
  fixture.deployments.push({
    protocol,
    deployment: protocol,
    state: "complete",
    block_number: "99",
    block_hash: hash,
  });
  fixture.positions.push({
    ...fixture.positions[0]!,
    id: protocol,
    protocol,
    deployment: protocol,
    kind:
      protocol === "lido" ? "staking" : protocol === "uniswap-v3" ? "liquidity-nft" : "liquidity",
    receipt_asset:
      protocol === "uniswap-v3"
        ? undefined
        : {
            ...receipt,
            symbol: protocol === "lido" ? "wstETH" : protocol === "curve" ? "3CRV" : "UNI-V2",
          },
    token_id: protocol === "uniswap-v3" ? "42" : undefined,
    fee: protocol === "uniswap-v3" ? 3000 : undefined,
    range:
      protocol === "uniswap-v3"
        ? {
            lower_tick: -60,
            upper_tick: 60,
            current_tick: 0,
            sqrt_price_x96: "79228162514264337593543950336",
            in_range: true,
          }
        : undefined,
    assets: protocol === "lido" ? [{ ...underlying[0]!, symbol: "stETH" }] : underlying,
    debts: [],
    limitations: protocol === "curve" ? ["metapool_underlying_expanded"] : [],
  });
}
fixture.positions[0]!.receipt_asset = {
  ...receipt,
  amount: "9007199254740993123",
  symbol: "variableDebtUSDC",
};

for (const appearance of appearanceCases) {
  test(`address DeFi ${appearance.width}px ${appearance.language} ${appearance.theme}`, async ({
    page,
  }, testInfo) => {
    await configureAppearance(page, appearance);
    const errors = collectBrowserErrors(page);
    const requests: string[] = [];
    page.on("request", (request) => {
      const url = new URL(request.url());
      if (/\/(erc20-balances|nfts|defi-positions)$/.test(url.pathname)) requests.push(url.pathname);
    });
    await page.route(`**/api/v1/addresses/${owner}/defi-positions?*`, async (route) => {
      const protocol = new URL(route.request().url()).searchParams.get("protocol");
      await route.fulfill({
        json: {
          data: {
            deployments: fixture.deployments.filter((d) => !protocol || d.protocol === protocol),
            positions: fixture.positions.filter((p) => !protocol || p.protocol === protocol),
          },
          meta: { request_id: "defi-browser", chain_id: "1" },
        },
      });
    });
    await page.goto(`/address/${owner}?tab=assets&asset=defi`);
    await expect(page.locator(".defi-position")).toHaveCount(fixture.positions.length);
    await expect(page.getByText("9.007199254740993123", { exact: true }).first()).toBeVisible();
    expect(requests.some((path) => /\/(erc20-balances|nfts)$/.test(path))).toBe(false);
    const v2 = page
      .getByRole("article")
      .filter({ has: page.getByRole("link", { name: "UNI-V2", exact: true }) });
    await expect(v2.getByRole("heading", { name: "WETH / USDC" })).toBeVisible();
    await expect(v2.getByText("12.345", { exact: true })).toBeVisible();
    await expect(v2.getByText("0.25", { exact: true })).toBeVisible();
    await expect(v2.getByText("800", { exact: true })).toBeVisible();
    const selected = page.getByRole("link", { name: "DeFi", exact: true });
    await expect(selected).toHaveAttribute("aria-current", "page");
    await page.locator(".defi-position summary").last().click();
    await expect(page.locator(".defi-details").last()).toBeVisible();
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
    ).toBeLessThanOrEqual(1);
    expect(
      (
        await new AxeBuilder({ page })
          .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
          .analyze()
      ).violations,
    ).toEqual([]);
    await page.locator("#defi-protocol").focus();
    await page.evaluate(() => window.scrollTo(0, 0));
    const screenshot = testInfo.outputPath("defi.png");
    await page.screenshot({ path: screenshot, fullPage: true });
    await testInfo.attach("DeFi", { path: screenshot, contentType: "image/png" });
    await page.locator("#defi-protocol").selectOption("aave-v3");
    await expect(page.locator(".defi-position")).toHaveCount(1);
    await page.reload();
    await expect(selected).toHaveAttribute("aria-current", "page");
    await expect(page.locator(".defi-position")).toHaveCount(fixture.positions.length);
    expect(errors).toEqual([]);
  });
}
