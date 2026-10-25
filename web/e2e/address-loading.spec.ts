import { expect, test } from "@playwright/test";

const address = "0x1111111111111111111111111111111111111111";
const wallet = "0x2222222222222222222222222222222222222222";

for (const [tab, endpoint, region] of [
  ["transactions", "transactions", "Transactions"],
  ["native-transfers", "native-transfers", "Native transfers"],
  ["internal-transactions", "internal-transactions", "Internal Transactions"],
  ["withdrawals", "withdrawals", "Withdrawals"],
  ["erc20-transfers", "erc20-transfers", "ERC-20 Transfers"],
  ["nft-transfers", "nft-transfers", "NFT Transfers"],
  ["assets", "erc20-balances", "ERC-20 holdings"],
  ["assets", "nfts", "Canonical NFT balances"],
  ["user-operations", "user-operations", "User Ops"],
] as const) {
  test(`${region} shows a loading indicator until its data arrives`, async ({ page }) => {
    const target = tab === "user-operations" ? wallet : address;
    const response = Promise.withResolvers<void>();
    await page.route(`**/api/v1/addresses/${target}/${endpoint}?*`, async (route) => {
      await response.promise;
      await route.continue();
    });
    try {
      await page.goto(`/address/${target}?tab=${tab}`);
      const panel = page.getByRole("region", { name: region, exact: true });
      await expect(panel.getByRole("status")).toHaveText("Loading indexed data…");
      await expect(panel.locator(".query-loading-spinner")).toBeVisible();
      await expect(panel.locator(".empty-result")).toHaveCount(0);
      response.resolve();
      await expect(panel.locator(".query-loading-spinner")).toHaveCount(0);
      await expect(panel.getByRole("table")).toBeVisible();
    } finally {
      response.resolve();
    }
  });
}
