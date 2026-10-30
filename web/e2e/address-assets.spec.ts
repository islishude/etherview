import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { appearanceCases, collectBrowserErrors, configureAppearance } from "./appearance";

const address = `0x${"1".repeat(40)}`;
const longID = (2n ** 256n - 1n).toString();

for (const appearance of appearanceCases) {
  const { width, language, theme } = appearance;
  test(`address assets ${width}px ${language} ${theme}`, async ({ page }, testInfo) => {
    await configureAppearance(page, appearance);
    const errors = collectBrowserErrors(page);
    const external: string[] = [];
    const assetRequests: string[] = [];
    page.on("request", (request) => {
      const url = new URL(request.url());
      if (url.origin !== "http://127.0.0.1:4173") external.push(request.url());
      if (/\/(erc20-balances|nfts)$/.test(url.pathname)) assetRequests.push(url.pathname);
    });
    await page.route(`**/api/v1/addresses/${address}/erc20-balances?*`, async (route) => {
      const response = await route.fetch();
      const body = await response.json();
      const item = body.data[0];
      body.data = [
        item,
        {
          ...item,
          token_address: `0x${"3".repeat(40)}`,
          name: "A token with a very long untrusted display name ".repeat(4),
          balance: "1000000000000000000000001",
          decimals: 24,
        },
        {
          ...item,
          token_address: `0x${"4".repeat(40)}`,
          name: undefined,
          symbol: undefined,
          decimals: undefined,
          balance: longID,
        },
      ];
      await route.fulfill({ response, json: body });
    });
    await page.route(`**/api/v1/addresses/${address}/nfts?*`, async (route) => {
      const response = await route.fetch();
      const body = await response.json();
      const item = body.data[0];
      body.data = [
        item,
        { ...item, token_id: "42", balance: "12" },
        { ...item, token_id: longID },
        { ...item, token_id: "2026" },
      ];
      await route.fulfill({ response, json: body });
    });

    await page.goto(`/address/${address}?tab=assets`);
    await expect(page.locator(".address-token-row")).toHaveCount(3);
    expect(assetRequests.some((path) => path.endsWith("/nfts"))).toBe(false);
    await expect(page.getByText("1.000000000000000000000001", { exact: true })).toBeVisible();
    const tabs = page.getByRole("navigation", {
      name: language === "zh" ? "资产类型" : "Asset types",
    });

    for (const asset of ["tokens", "nfts"] as const) {
      if (asset === "nfts") {
        const nft = tabs.getByRole("link", { name: "NFT", exact: true });
        await nft.focus();
        await expect(nft).toBeFocused();
        await nft.press("Enter");
        await expect(nft).toHaveAttribute("aria-current", "page");
        await expect(page).toHaveURL(/asset=nfts/);
        await expect(page.locator(".address-nft-card")).toHaveCount(4);
        await expect(page.getByTitle(longID, { exact: true })).toHaveAttribute(
          "href",
          `/nft/${address}/${longID}`,
        );
        const columns = await page
          .locator(".address-nft-grid")
          .evaluate((grid) => getComputedStyle(grid).gridTemplateColumns.split(" ").length);
        expect(columns).toBe(width >= 1024 ? 3 : width >= 768 ? 2 : 1);
      }
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
      ).toBeLessThanOrEqual(1);
      const scan = await new AxeBuilder({ page })
        .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
        .analyze();
      expect(scan.violations).toEqual([]);
      const screenshot = testInfo.outputPath(`${asset}-${width}-${language}-${theme}.png`);
      await page.screenshot({ path: screenshot, fullPage: true });
      await testInfo.attach(asset, { path: screenshot, contentType: "image/png" });
    }
    await page.goBack();
    await expect(page.locator(".address-token-row")).toHaveCount(3);
    await page.goForward();
    await expect(page.locator(".address-nft-card")).toHaveCount(4);
    await page.reload();
    await expect(tabs.getByRole("link", { name: "NFT", exact: true })).toHaveAttribute(
      "aria-current",
      "page",
    );
    await expect(page.locator(".address-nft-card")).toHaveCount(4);
    await expect(page.locator(".address-assets img")).toHaveCount(0);
    expect(errors).toEqual([]);
    expect(external).toEqual([]);
  });
}
