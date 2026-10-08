import { expect, test, type Locator } from "@playwright/test";

async function expectColumns(locator: Locator, count: number) {
  await expect(locator).toBeVisible();
  await expect
    .poll(() =>
      locator.evaluate(
        (element) => getComputedStyle(element).gridTemplateColumns.split(" ").length,
      ),
    )
    .toBe(count);
}

test("feature styles retain grid transitions on both sides of their breakpoints", async ({
  page,
}) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto("/");
  for (const width of [1024, 1023, 1022, 981, 980, 979, 768, 767, 766]) {
    await page.setViewportSize({ width, height: 1000 });
    await expectColumns(page.locator(".metrics-grid"), width > 980 ? 4 : width > 767 ? 2 : 1);
    await expectColumns(
      page.locator(".chain-context .chain-context-grid"),
      width > 1023 ? 6 : width > 767 ? 3 : 1,
    );
  }

  await page.goto("/blocks/1");
  for (const width of [981, 980, 979, 768, 767, 766]) {
    await page.setViewportSize({ width, height: 1000 });
    await expectColumns(page.locator(".detail-grid").first(), width > 980 ? 2 : 1);
    await expectColumns(page.locator(".detail-item").first(), width > 767 ? 2 : 1);
  }

  const transaction = `0x${"a".repeat(64)}`;
  await page.goto(`/tx/${transaction}`);
  for (const width of [768, 767, 766]) {
    await page.setViewportSize({ width, height: 1000 });
    const row = page.locator(".transaction-detail-row.detail-item").first();
    await expectColumns(row, width > 767 ? 2 : 1);
    await expect(row).toHaveCSS("gap", width > 767 ? "16px" : "4.8px");
  }
  await page.goto(`/tx/${transaction}?tab=trace`);
  for (const width of [768, 767, 766]) {
    await page.setViewportSize({ width, height: 1000 });
    await expectColumns(page.locator(".transaction-trace-summary").first(), width > 767 ? 4 : 1);
  }
  await page.route(`**/api/v1/transactions/${transaction}/logs*`, async (route) => {
    const response = await route.fetch();
    const envelope = await response.json();
    // Include an indexed value so the topic format selector participates in layout.
    const log = envelope.data.items[0];
    log.topics.push(`0x${"2a".padStart(64, "0")}`);
    log.decoding.arguments[0].indexed = true;
    log.data = "0x";
    await route.fulfill({ response, json: envelope });
  });
  await page.goto(`/tx/${transaction}?tab=logs`);
  await page.locator(".transaction-log-details > summary").first().click();
  for (const width of [768, 767, 766]) {
    await page.setViewportSize({ width, height: 1000 });
    await expectColumns(
      page.locator(".transaction-log-provenance-grid").first(),
      width > 767 ? 2 : 1,
    );
    await expectColumns(
      page.locator(".transaction-topic-convertible").first(),
      width > 767 ? 3 : 2,
    );
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
    ).toBeLessThanOrEqual(1);
  }
});
