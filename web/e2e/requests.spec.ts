import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { configureAppearance, collectBrowserErrors } from "./appearance";

const hex = (bytes: number) => `0x${"12".repeat(bytes)}`;
const hash = `0x${"1".repeat(64)}`;
const items = [
  {
    request_type: "0x00",
    sequence_index: "0",
    pubkey: hex(48),
    withdrawal_credentials: hex(32),
    amount: "18446744073709551615",
    signature: hex(96),
    index: "9007199254740993",
  },
  {
    request_type: "0x01",
    sequence_index: "0",
    source_address: hex(20),
    validator_pubkey: hex(48),
    amount: "0",
  },
  {
    request_type: "0x02",
    sequence_index: "0",
    source_address: hex(20),
    source_pubkey: hex(48),
    target_pubkey: hex(48),
  },
  {
    request_type: "0x03",
    sequence_index: "0",
    pubkey: hex(48),
    withdrawal_credentials: hex(32),
    amount: "32000000000",
    signature: hex(96),
  },
  { request_type: "0x04", sequence_index: "0", source_address: hex(20), pubkey: hex(48) },
];
for (const language of ["en", "zh"] as const) {
  for (const width of [390, 1440]) {
    for (const theme of ["light", "dark"] as const) {
      test(`requests cards, fields and filtering (${language}, ${width}, ${theme})`, async ({
        page,
      }, testInfo) => {
        await configureAppearance(page, { width, language, theme });
        const errors = collectBrowserErrors(page);
        await page.route("**/api/v1/config", async (route) => {
          const response = await route.fetch();
          const body = await response.json();
          body.data.features.consensus = true;
          await route.fulfill({ response, json: body });
        });
        await page.route("**/api/v1/blocks/*/requests*", (route) => {
          const url = new URL(route.request().url());
          expect(url.pathname).toBe(`/api/v1/blocks/${hash}/requests`);
          const type = url.searchParams.get("request_type");
          return route.fulfill({
            json: {
              data: {
                block_hash: hash,
                state: "complete",
                total: "5",
                counts: Object.fromEntries(items.map((item) => [item.request_type, "1"])),
                items: type ? items.filter((item) => item.request_type === type) : items,
                requests_hash: hex(32),
                beacon_root: hex(32),
              },
              meta: { chain_id: "1", request_id: "requests-browser" },
            },
          });
        });
        await page.goto("/blocks/1?tab=requests");
        const panel = page.getByRole("tabpanel");
        await expect(
          page.getByRole("tab", { name: language === "en" ? "Requests" : "请求", exact: true }),
        ).toHaveAttribute("aria-selected", "true");
        await expect(panel.locator(".request-record")).toHaveCount(5);
        const firstDetails = panel.locator(".request-record summary").first();
        await firstDetails.focus();
        await firstDetails.press("Enter");
        await expect(panel.locator(".request-record details").first()).toHaveAttribute("open", "");
        for (const summary of (await panel.locator(".request-record summary").all()).slice(1))
          await summary.click();
        await expect(panel.getByText("18,446,744,073,709,551,615 Gwei")).toBeVisible();
        await expect(panel.getByText("9,007,199,254,740,993")).toBeVisible();
        await expect(panel.getByText(hex(96)).first()).toBeVisible();
        expect(
          await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
        ).toBe(true);
        expect(
          (await new AxeBuilder({ page }).include(".transaction-tab-panel").analyze()).violations,
        ).toEqual([]);
        await page.evaluate(() => window.scrollTo({ top: 0, behavior: "instant" }));
        await page.screenshot({
          path: testInfo.outputPath("requests-expanded.png"),
          fullPage: true,
        });
        const filter = panel.getByRole("button", {
          name: language === "en" ? /^Request type:/ : /^请求类型:/,
        });
        await filter.click();
        const search = panel.getByRole("searchbox");
        await expect(search).toBeFocused();
        expect(
          (await new AxeBuilder({ page }).include(".transaction-tab-panel").analyze()).violations,
        ).toEqual([]);
        await page.screenshot({ path: testInfo.outputPath("requests-filter.png"), fullPage: true });
        await search.press("Escape");
        await expect(filter).toBeFocused();
        await filter.click();
        await search.fill("0x04");
        await panel
          .getByRole("button", {
            name: language === "en" ? "Builder exit 0x04 1" : "Builder 退出 0x04 1",
            exact: true,
          })
          .click();
        await expect(panel.locator(".request-record")).toHaveCount(1);
        await filter.click();
        await panel
          .getByRole("button", {
            name: language === "en" ? "All types 5" : "全部类型 5",
            exact: true,
          })
          .click();
        await expect(panel.locator(".request-record")).toHaveCount(5);
        await page.evaluate(() => window.scrollTo({ top: 0, behavior: "instant" }));
        await page.screenshot({
          path: testInfo.outputPath("requests-overview.png"),
          fullPage: true,
        });
        expect(errors).toEqual([]);
      });
    }
  }
}
test("disabled requests deep link performs no request collection read", async ({ page }) => {
  await page.route("**/api/v1/config", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.data.features.consensus = false;
    await route.fulfill({ response, json: body });
  });
  let reads = 0;
  page.on("request", (request) => {
    if (new URL(request.url()).pathname.endsWith("/requests")) reads++;
  });
  await page.goto("/blocks/1?tab=requests");
  await expect(page.getByRole("tab", { name: "Overview", exact: true })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect(page.getByRole("tab", { name: "Requests", exact: true })).toHaveCount(0);
  expect(reads).toBe(0);
});
