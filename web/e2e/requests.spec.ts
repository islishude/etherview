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
  test(`requests deep link, fields and filtering at mobile width (${language})`, async ({
    page,
  }) => {
    await configureAppearance(page, { width: 390, language, theme: "light" });
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
    for (const summary of await panel.locator(".request-record summary").all())
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
    await panel.getByRole("combobox").selectOption("0x04");
    await expect(panel.locator(".request-record")).toHaveCount(1);
    expect(errors).toEqual([]);
  });
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
