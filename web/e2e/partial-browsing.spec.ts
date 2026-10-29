import { expect, test } from "@playwright/test";
import { configureAppearance } from "./appearance";

const address = "0x1111111111111111111111111111111111111111";
for (const locale of ["en", "zh"] as const) {
  test(`published browsing distinguishes partial, pending and empty in ${locale}`, async ({
    page,
  }) => {
    await configureAppearance(page, { width: 390, language: locale, theme: "light" });
    let state = "partial";
    await page.route(`**/api/v1/addresses/${address}/nft-transfers?*`, async (route) => {
      const response = await route.fetch();
      const envelope = await response.json();
      envelope.meta.browsing = {
        state,
        from_block: "0",
        to_block: "548",
        revision: "1",
        snapshot: { chain_id: "1", block_number: "548", block_hash: `0x${"ab".repeat(32)}` },
      };
      delete envelope.meta.coverage_start;
      delete envelope.meta.coverage_end;
      if (state !== "partial") envelope.data = [];
      await route.fulfill({ response, json: envelope });
    });
    await page.goto(`/address/${address}?tab=nft-transfers`);
    await expect(page.locator(".address-activity-table tbody tr").first()).toBeVisible();
    await expect(
      page.getByText(
        locale === "en"
          ? "Showing synced data; some data is not ready yet."
          : "展示已同步数据，部分数据尚未就绪。",
      ),
    ).toBeVisible();
    await expect(page.getByText("stage_unavailable", { exact: true })).toHaveCount(0);
    state = "pending";
    await page.reload();
    await expect(
      page.getByText(locale === "en" ? "No synced data available yet." : "暂无已同步数据。"),
    ).toBeVisible();
    await expect(page.locator(".address-activity-table")).toHaveCount(0);
    state = "complete";
    await page.reload();
    await expect(
      page.getByText(
        locale === "en"
          ? "No matching address activity is available in this snapshot."
          : "当前快照中没有匹配的地址活动。",
      ),
    ).toBeVisible();
    await expect(
      page.getByText(locale === "en" ? "No synced data available yet." : "暂无已同步数据。"),
    ).toHaveCount(0);
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
    ).toBeLessThanOrEqual(1);
  });
}
