import { expect, test } from "@playwright/test";
import { configureAppearance } from "./appearance";

for (const language of ["en", "zh"] as const) {
  for (const theme of ["light", "dark"] as const) {
    test(`first request cancellation supports keyboard recovery ${language} ${theme}`, async ({
      page,
    }, testInfo) => {
      await configureAppearance(page, { language, theme, width: 390 });
      await page.addInitScript(() => {
        const sources: EventTarget[] = [];
        class Events extends EventTarget {
          static CLOSED = 2;
          readyState = 1;
          constructor(_url: string) {
            super();
            sources.push(this);
          }
          close() {
            this.readyState = 2;
          }
        }
        Object.assign(window, {
          EventSource: Events,
          cancelFirstRead: () =>
            sources.forEach((source) =>
              source.dispatchEvent(
                new MessageEvent("status", {
                  data: JSON.stringify({ reason: "browsing_publication", block_number: "2" }),
                  lastEventId: "2",
                }),
              ),
            ),
        });
      });
      const address = `0x${"1".repeat(40)}`;
      let reads = 0;
      const release = Promise.withResolvers<void>();
      await page.route(`**/api/v1/addresses/${address}/native-transfers?*`, async (route) => {
        reads++;
        if (reads === 1) await release.promise;
        await route.continue().catch(() => {});
      });
      try {
        await page.goto(`/address/${address}?tab=native-transfers`);
        await expect.poll(() => reads).toBe(1);
        const panel = page.getByRole("region", {
          name: language === "en" ? "Native transfers" : "原生币转账",
          exact: true,
        });
        await expect(panel.locator(".query-loading-spinner")).toBeVisible();
        await page.evaluate(() =>
          (window as unknown as { cancelFirstRead: () => void }).cancelFirstRead(),
        );
        await expect(page.locator(".query-loading-spinner")).toHaveCount(0);
        await expect(page.locator(".browsing-refresh-status")).toContainText(
          language === "en" ? "snapshot may be out of date" : "当前快照可能已失效",
        );
        release.resolve();
        expect(reads).toBe(1);
        const refresh = page
          .locator(".browsing-refresh")
          .getByRole("button", { name: language === "en" ? "Refresh" : "刷新", exact: true });
        await refresh.focus();
        await page.screenshot({
          path: testInfo.outputPath("cancelled-initial-read.png"),
          fullPage: true,
        });
        await refresh.press("Enter");
        await expect.poll(() => reads).toBe(2);
        await expect(page.getByRole("table")).toBeVisible();
        await expect(refresh).toBeEnabled();
      } finally {
        release.resolve();
      }
    });
  }
}
