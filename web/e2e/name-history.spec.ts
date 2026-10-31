import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { configureAppearance, collectBrowserErrors } from "./appearance";

const address = `0x${"1".repeat(40)}`;
const hash = (digit: string) => `0x${digit.repeat(64)}`;
for (const language of ["en", "zh"] as const) {
  for (const theme of ["light", "dark"] as const) {
    test(`name history groups, expands and refreshes in ${language} ${theme}`, async ({
      page,
    }, testInfo) => {
      await configureAppearance(page, { width: 390, language, theme });
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
          historySources: sources,
          publishHistory: (invalidated: boolean) =>
            sources.forEach((source) =>
              source.dispatchEvent(
                new MessageEvent("names", {
                  data: JSON.stringify({ history: true, invalidated }),
                  lastEventId: "2",
                }),
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
      await page.route("**/api/v1/names/alice.eth", async (route) =>
        route.fulfill({
          json: {
            data: {
              name: "alice.eth",
              state: "indexed",
              registration_state: "registered",
              registrant: { state: "value", value: address },
              controller: { state: "multi_account" },
              expiration_time: { state: "value", value: "2000000000" },
              resolved_address: { state: "not_set" },
              profile_observation: {
                publication: "1",
                observed_block_number: "100",
                observed_block_hash: hash("a"),
                valid_until: new Date(Date.now() + 60000).toISOString(),
              },
              resolution_observation: {
                publication: "2",
                observed_block_number: "100",
                observed_block_hash: hash("a"),
                valid_until: new Date(Date.now() + 60000).toISOString(),
              },
            },
            meta: { chain_id: "1", request_id: "test" },
          },
        }),
      );
      let eventRequests = 0;
      await page.route("**/api/v1/names/alice.eth/history?*", async (route) => {
        const older = new URL(route.request().url()).searchParams.has("cursor");
        await route.fulfill({
          json: {
            data: {
              name: "alice.eth",
              state: "indexed",
              snapshot: "frozen",
              coverage: {
                state: "partial",
                start: "0",
                scanned: "100",
                target: "110",
                pending: "1",
              },
              items: [
                {
                  transaction_hash: hash(older ? "2" : "1"),
                  block_hash: hash("a"),
                  block_number: older ? "99" : "100",
                  timestamp: "2026-10-29T00:00:00Z",
                  actions: older ? ["registration"] : ["text", "address"],
                  event_count: "2",
                },
              ],
            },
            meta: { chain_id: "1", request_id: "test", ...(!older && { next_cursor: "older" }) },
          },
        });
      });
      await page.route("**/api/v1/names/alice.eth/history/*/events?*", async (route) => {
        eventRequests++;
        const url = new URL(route.request().url());
        expect(url.searchParams.get("snapshot")).toBe("frozen");
        expect(url.searchParams.get("block_hash")).toBe(hash("a"));
        const second = url.searchParams.has("cursor");
        await route.fulfill({
          json: {
            data: {
              items: [
                {
                  log_index: second ? "2" : "1",
                  emitter: address,
                  action: second ? "address" : "text",
                  event_name: second ? "AddrChanged" : "TextChanged",
                  fields: [
                    {
                      key: second ? "a" : "value",
                      type: second ? "address" : "string",
                      value: second
                        ? address
                        : `<script>alert(1)</script>${"long-name-".repeat(30)}`,
                      truncated: false,
                    },
                  ],
                },
              ],
            },
            meta: {
              chain_id: "1",
              request_id: "test",
              ...(!second && { next_cursor: "more-events" }),
            },
          },
        });
      });
      await page.goto("/name/alice.eth");
      await expect(page.getByRole("heading", { name: "alice.eth", exact: true })).toBeVisible();
      await expect(page.locator(".name-history-row")).toHaveCount(1);
      expect(eventRequests).toBe(0);
      const toggle = page.locator(".name-history-toggle");
      await toggle.focus();
      await page.keyboard.press("Enter");
      await expect(page.getByText("TextChanged · #1")).toBeVisible();
      await expect(page.locator(".name-event-value").last()).toContainText("<script>");
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      expect((await new AxeBuilder({ page }).analyze()).violations).toEqual([]);
      await page.screenshot({
        path: testInfo.outputPath(`name-history-${language}-${theme}-mobile.png`),
        fullPage: true,
      });
      const nextLabel = language === "en" ? "Next page" : "下一页";
      await page
        .locator(".name-history-events")
        .getByRole("button", { name: nextLabel, exact: true })
        .click();
      await expect(page.getByText("AddrChanged · #2")).toBeVisible();
      await page
        .locator(".name-history > .cursor-pagination")
        .getByRole("button", { name: nextLabel, exact: true })
        .click();
      await expect(
        page.locator(`.name-history-transaction a[href^="/tx/${hash("2")}"]`),
      ).toBeVisible();
      await page.evaluate(() =>
        (window as unknown as { publishHistory: (invalidate: boolean) => void }).publishHistory(
          false,
        ),
      );
      await expect(page.locator(".browsing-refresh-dot")).toBeVisible();
      await expect(
        page.getByRole("button", {
          name: language === "en" ? "New records · Refresh" : "有新记录 · 刷新",
        }),
      ).toHaveCount(0);
      await page.evaluate(() =>
        (window as unknown as { publishHistory: (invalidate: boolean) => void }).publishHistory(
          true,
        ),
      );
      await expect(page.locator(".browsing-refresh-status")).toHaveText(
        language === "en" ? "Data changed" : "数据已变化",
      );
      await expect(page.locator(".browsing-refresh-dot")).toBeVisible();
      await page
        .getByRole("button", { name: language === "en" ? "Refresh" : "刷新", exact: true })
        .click();
      await expect(
        page.locator(`.name-history-transaction a[href^="/tx/${hash("1")}"]`),
      ).toBeVisible();
      expect(
        await page.evaluate(
          () => (window as unknown as { historySources: unknown[] }).historySources.length,
        ),
      ).toBe(1);
      await page.setViewportSize({ width: 1440, height: 1000 });
      await page.locator(".name-history-toggle").click();
      await expect(page.getByText("TextChanged · #1")).toBeVisible();
      await page.screenshot({
        path: testInfo.outputPath(`name-history-${language}-${theme}-desktop.png`),
        fullPage: true,
      });
      expect(errors).toEqual([]);
    });
  }
}
