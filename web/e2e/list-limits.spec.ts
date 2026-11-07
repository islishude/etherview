import { expect, test } from "@playwright/test";
import { configureAppearance } from "./appearance";

for (const language of ["en", "zh"] as const) {
  for (const [resource, cap] of [
    ["blocks", 1000],
    ["transactions", 1000],
    ["user-operations", 1000],
    ["tokens", 150],
  ] as const) {
    test(`${resource} stops at ${cap} records in ${language}`, async ({ page }) => {
      test.setTimeout(90_000);
      await configureAppearance(page, { width: 1440, language, theme: "light" });
      const offsets: number[] = [];
      let template: Record<string, unknown> | undefined;
      let metadata: Record<string, unknown> = {};
      await page.route(`**/api/v1/${resource}?*`, async (route) => {
        const url = new URL(route.request().url());
        const offset = Number(url.searchParams.get("cursor") ?? 0);
        offsets.push(offset);
        if (!template) {
          const response = await route.fetch();
          const body = await response.json();
          template = body.data[0];
          metadata = body.meta;
        }
        const data = Array.from({ length: 25 }, (_, i) => ({
          ...template,
          hash: `0x${(offset + i + 1).toString(16).padStart(64, "0")}`,
          address: `0x${(offset + i + 1).toString(16).padStart(40, "0")}`,
        }));
        await route.fulfill({
          json: { data, meta: { ...metadata, next_cursor: String(offset + 25) } },
        });
      });
      await page.goto(`/${resource}`);
      const next = page.getByRole("button", {
        name: language === "en" ? "Next page" : "下一页",
        exact: true,
      });
      const previous = page.getByRole("button", {
        name: language === "en" ? "Previous page" : "上一页",
        exact: true,
      });
      await expect(
        page.getByText(
          language === "en"
            ? `Only the latest ${cap} records are shown.`
            : `仅展示最近 ${cap} 条记录。`,
          { exact: false },
        ),
      ).toBeVisible();
      for (let index = 1; index < cap / 25; index++) {
        await expect(next).toBeEnabled();
        await next.click();
        await expect(page.locator(".cursor-pagination")).toContainText(
          language === "en" ? `Page ${index + 1}` : `第 ${index + 1} 页`,
        );
      }
      await expect(next).toBeDisabled();
      await expect(page.locator("tbody tr")).toHaveCount(25);
      expect(Math.max(...offsets)).toBe(cap - 25);
      await previous.click();
      await expect(next).toBeEnabled();
      await next.click();
      await expect(next).toBeDisabled();
      expect(offsets).not.toContain(cap);
      await page.reload();
      await expect(previous).toBeDisabled();
      await expect(next).toBeEnabled();
    });
  }
}
