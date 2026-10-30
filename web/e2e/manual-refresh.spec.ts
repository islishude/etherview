import { expect, test } from "@playwright/test";

const address = `0x${"1".repeat(40)}`;
for (const tab of [
  "transactions",
  "withdrawals",
  "internal-transactions",
  "erc20-transfers",
  "nft-transfers",
  "native-transfers",
  "assets",
  "user-operations",
  "contract",
]) {
  test(`address ${tab} stays still on events and refreshes only on request`, async ({
    page,
  }, testInfo) => {
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
        emitRefreshTest: () => {
          for (const source of sources) {
            for (const [type, data] of [
              ["head", "{}"],
              ["status", '{"reason":"browsing_publication","block_number":"2"}'],
              ["names", '{"invalidated":true}'],
              ["reorg", "{}"],
            ])
              source.dispatchEvent(new MessageEvent(type!, { data, lastEventId: "2" }));
          }
        },
      });
    });
    const requests: string[] = [];
    page.on("request", (request) => {
      const url = new URL(request.url());
      if (/^\/api\/v1\/(addresses|contracts)\//.test(url.pathname)) requests.push(url.pathname);
    });
    await page.goto(`/address/${address}${tab === "contract" ? "#code" : `?tab=${tab}`}`);
    const suffixes =
      tab === "contract"
        ? ["/proxy", "/verification"]
        : tab === "assets"
          ? ["/nfts", "/erc20-balances"]
          : [`/${tab}`];
    for (const suffix of suffixes)
      await expect.poll(() => requests.some((path) => path.endsWith(suffix))).toBe(true);
    const refresh = page
      .locator(".browsing-refresh")
      .getByRole("button", { name: "Refresh", exact: true });
    await expect(refresh).toBeEnabled();
    await expect(page.locator(".page-header .browsing-refresh")).toHaveCount(1);
    const before = requests.length;
    const resources = [...new Set(requests)].sort();
    const url = page.url();
    await page.evaluate(() =>
      (window as unknown as { emitRefreshTest: () => void }).emitRefreshTest(),
    );
    await expect(page.getByText(/Data changed/)).toBeVisible();
    await page.waitForTimeout(350);
    expect(requests).toHaveLength(before);
    expect(page.url()).toBe(url);
    if (tab === "transactions") {
      await refresh.focus();
      const hint = page.locator(".browsing-refresh-hint");
      await expect(hint).toBeVisible();
      await expect(refresh).toHaveAccessibleDescription(/snapshot may be out of date/);
      await refresh.press("Escape");
      await expect(hint).toBeHidden();
      await page.screenshot({ path: testInfo.outputPath("refresh-desktop.png") });
      await page.setViewportSize({ width: 390, height: 844 });
      await expect(page.locator(".browsing-refresh-dot")).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(390);
      const title = await page.locator("h1").boundingBox();
      const control = await refresh.boundingBox();
      expect(
        Math.abs(title!.y + title!.height / 2 - control!.y - control!.height / 2),
      ).toBeLessThan(2);
      await page.screenshot({ path: testInfo.outputPath("refresh-mobile.png") });
      await refresh.press("Enter");
    } else await refresh.click();
    await expect(refresh).toBeEnabled();
    expect(requests.length).toBeGreaterThan(before);
    expect([...new Set(requests.slice(before))].sort()).toEqual(resources);
    expect(page.url()).toBe(url);
  });
}
