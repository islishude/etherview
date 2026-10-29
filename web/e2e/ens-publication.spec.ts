import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { configureAppearance, collectBrowserErrors } from "./appearance";

for (const language of ["en", "zh"] as const) {
  test(`ENS publications update and expire without upstream requests at mobile width in ${language}`, async ({
    page,
  }, testInfo) => {
    await configureAppearance(page, { width: 390, language, theme: "light" });
    const errors = collectBrowserErrors(page);
    await page.addInitScript(() => {
      const sources: EventTarget[] = [];
      class LocalEvents extends EventTarget {
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
        EventSource: LocalEvents,
        ensSources: sources,
        publishNames: () =>
          sources.forEach((source) =>
            source.dispatchEvent(
              new MessageEvent("names", { data: '{"publication":"2"}', lastEventId: "2" }),
            ),
          ),
      });
    });
    await page.route("**/api/v1/config", async (route) => {
      const response = await route.fetch();
      const body = await response.json();
      body.data.features.ens = true;
      await route.fulfill({ response, json: body });
    });
    await page.route("**/api/v1/status", async (route) => {
      const response = await route.fetch();
      const body = await response.json();
      body.data.names = [
        {
          state: "sync_pending",
          coverage: "incomplete",
          coverage_start: "1",
          target: "100",
          scanned: "20",
          last_published_block: "100",
          protocols: ["v1", "v2"],
          pending: "2",
        },
      ];
      await route.fulfill({ response, json: body });
    });
    let version = "1";
    let requests = 0;
    let expiry = Date.now() + 60000;
    await page.route("**/api/v1/address-names?*", async (route) => {
      requests++;
      const url = new URL(route.request().url());
      await route.fulfill({
        json: {
          data: {
            publication: version,
            snapshot: `snapshot-${version}`,
            items: (url.searchParams.get("addresses") ?? "").split(",").map((address) => ({
              address,
              state: "resolved",
              primary_name: {
                name: `published-${version}.test`,
                publication: version,
                observed_block_number: "100",
                observed_block_hash: `0x${"1".repeat(64)}`,
                valid_until: new Date(expiry).toISOString(),
              },
            })),
          },
          meta: { request_id: "names", chain_id: "1" },
        },
      });
    });
    await page.goto("/transactions");
    await expect(page.locator(".address-primary-name").first()).toHaveText("published-1.test");
    await expect(
      page.getByText(
        language === "en"
          ? "Name history syncing; verified names available."
          : "名称历史同步中，已验证名称可用。",
      ),
    ).toBeVisible();
    await expect(page.locator(".address-identity-value").first()).toHaveAttribute(
      "title",
      /^0x[0-9a-fA-F]{40}$/,
    );
    expect(
      await page.evaluate(() => (window as unknown as { ensSources: unknown[] }).ensSources.length),
    ).toBe(1);
    version = "2";
    expiry = Date.now() + 4000;
    await page.evaluate(() => (window as unknown as { publishNames: () => void }).publishNames());
    await expect(page.locator(".address-primary-name").first()).toHaveText("published-2.test");
    const settled = requests;
    expect((await new AxeBuilder({ page }).include("main").analyze()).violations).toEqual([]);
    await page.screenshot({ path: testInfo.outputPath(`ens-${language}.png`) });
    await expect(page.locator(".address-primary-name")).toHaveCount(0, { timeout: 6000 });
    expect(requests).toBe(settled);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      ),
    ).toBeLessThanOrEqual(1);
    expect(errors).toEqual([]);
  });
}
