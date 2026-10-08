import { expect, test } from "@playwright/test";

test("Blocks list aligns identity and finality with opposite table edges", async ({ page }) => {
  await page.goto("/blocks");
  const table = page.locator("main table");
  const row = table.locator("tbody tr").first();
  await expect(row).toBeVisible();
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    for (const cells of [table.locator("thead th"), row.locator("td")]) {
      await expect(cells.nth(0)).toHaveCSS("text-align", "left");
      await expect(cells.nth(1)).toHaveCSS("text-align", "left");
      for (const index of [2, 3, 4]) {
        await expect(cells.nth(index)).toHaveCSS("text-align", "right");
      }
    }
    const edges = await row.evaluate((element) => {
      if (!(element instanceof HTMLTableRowElement)) {
        throw new Error("Expected a table row");
      }
      const first = element.cells[0]!;
      const last = element.cells[4]!;
      return {
        identity:
          first.querySelector("a")!.getBoundingClientRect().left -
          first.getBoundingClientRect().left,
        hash:
          first.querySelector("code")!.getBoundingClientRect().left -
          first.getBoundingClientRect().left,
        finality:
          last.getBoundingClientRect().right -
          last.firstElementChild!.getBoundingClientRect().right,
        leftPadding: parseFloat(getComputedStyle(first).paddingLeft),
        rightPadding: parseFloat(getComputedStyle(last).paddingRight),
      };
    });
    expect(Math.abs(edges.identity - edges.leftPadding)).toBeLessThanOrEqual(1);
    expect(Math.abs(edges.hash - edges.leftPadding)).toBeLessThanOrEqual(1);
    expect(Math.abs(edges.finality - edges.rightPadding)).toBeLessThanOrEqual(1);
    await expect(page.locator("body")).toHaveJSProperty("scrollWidth", width);
    await page.screenshot({ path: test.info().outputPath(`blocks-${width}.png`), fullPage: true });
  }
});
