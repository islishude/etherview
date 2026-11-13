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
    { protocol: "lido", deployment: "lido", state: "scanning" },
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
    await expect(page.locator(".defi-position")).toHaveCount(2);
    await expect(page.getByText("9.007199254740993123", { exact: true })).toBeVisible();
    expect(requests.some((path) => /\/(erc20-balances|nfts)$/.test(path))).toBe(false);
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
    await expect(page.locator(".defi-position")).toHaveCount(2);
    expect(errors).toEqual([]);
  });
}
