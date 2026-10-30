import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { configureAppearance } from "./appearance";

const address = `0x${"1".repeat(40)}`;
for (const language of ["en", "zh"] as const) {
  for (const width of [390, 1440]) {
    test(`transaction ENS links replace addresses in ${language} at ${width}px`, async ({
      page,
    }, testInfo) => {
      await configureAppearance(page, { language, width, theme: "light" });
      await page.route("**/api/v1/config", async (route) => {
        const response = await route.fetch();
        const body = await response.json();
        body.data.features.ens = true;
        await route.fulfill({ response, json: body });
      });
      await page.route("**/api/v1/address-names?*", async (route) => {
        const addresses = new URL(route.request().url()).searchParams.get("addresses")!.split(",");
        await route.fulfill({
          json: {
            data: {
              snapshot: "names",
              publication: "1",
              items: addresses.map((address) => ({
                address,
                state: "resolved",
                primary_name: {
                  name: "alice.test",
                  publication: "1",
                  valid_until: new Date(Date.now() + 60_000).toISOString(),
                  observed_block_number: "100",
                  observed_block_hash: `0x${"1".repeat(64)}`,
                },
              })),
            },
            meta: { request_id: "names", chain_id: "1" },
          },
        });
      });
      await page.goto("/transactions");
      const transactionURL = await page
        .locator('table a[href^="/tx/"]')
        .first()
        .getAttribute("href");
      for (const [path, selector, key] of [
        ["/transactions", "table", "transactions"],
        [`/address/${address}?tab=transactions`, ".address-activity-table", "address"],
        [transactionURL!, ".transaction-detail-card", "detail"],
      ]) {
        await page.goto(path!);
        const content = page.locator(selector!).first();
        const names = content.locator(".address-name-link");
        await expect(names.first()).toBeVisible();
        await expect(content.locator('a[href^="/name/"]')).toHaveCount(0);
        await expect(content.locator('a[href^="/address/"] .address-identity-value')).toHaveCount(
          0,
        );
        const links = content.getByRole("link", { name: "alice.test", exact: true });
        expect(await links.count()).toBeGreaterThanOrEqual(2);
        for (const link of await links.all())
          await expect(link).toHaveAttribute(
            "href",
            /^\/address\/0x[0-9a-fA-F]{40}(\?tab=transactions)?$/,
          );
        expect((await new AxeBuilder({ page }).analyze()).violations).toEqual([]);
        expect(await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBe(
          0,
        );
        await page.screenshot({ path: testInfo.outputPath(`${key}.png`), fullPage: true });
        const destination = await links.first().getAttribute("href");
        await links.first().click();
        await expect(page).toHaveURL(new URL(destination!, page.url()).toString());
      }
    });
  }
}
