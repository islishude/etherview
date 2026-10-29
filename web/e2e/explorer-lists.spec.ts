import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

for (const route of ["blocks", "transactions"]) {
  test(`${route} list renders without coverage context or chain status requests`, async ({
    page,
  }, testInfo) => {
    const statusRequests: string[] = [];
    page.on("request", (request) => {
      if (new URL(request.url()).pathname === "/api/v1/status") statusRequests.push(request.url());
    });
    await page.goto(`/${route}`);
    const table = page.getByRole("table");
    const row = table.locator("tbody tr").first();
    await expect(row).toBeVisible();
    for (const width of [390, 768, 1440]) {
      await page.setViewportSize({ width, height: 1000 });
      await expect(page.locator(".chain-context")).toHaveCount(0);
      await expect(page.getByRole("note")).toHaveCount(0);
      await expect(
        page.getByRole("heading", { name: "Coverage and finality context" }),
      ).toHaveCount(0);
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
      ).toBeLessThanOrEqual(1);
      if (route === "blocks") {
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
      }
      await page.screenshot({ path: testInfo.outputPath(`${route}-${width}.png`), fullPage: true });
    }
    expect(statusRequests).toEqual([]);
  });
}

test("long quantities and empty/error states stay readable in narrow explorer lists", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const quantity = "9007199254740993123456789";
  let mode: "large" | "empty" | "error" = "large";
  await page.route("**/api/v1/blocks?*", async (route) => {
    if (mode === "error") {
      await route.fulfill({
        status: 503,
        json: { error: { code: "NOT_READY", message: "not ready", request_id: "list-states" } },
      });
      return;
    }
    const response = await route.fetch();
    const body = await response.json();
    if (mode === "empty") body.data = [];
    else body.data[0].gas_used = quantity;
    await route.fulfill({ response, json: body });
  });
  await page.goto("/blocks");
  const number = page.getByRole("cell", {
    name: BigInt(quantity).toLocaleString("en"),
    exact: true,
  });
  await expect(number).toHaveCSS("text-align", "right");
  await expect(number).toHaveText(BigInt(quantity).toLocaleString("en"));
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
  ).toBeLessThanOrEqual(1);
  mode = "empty";
  await page.reload();
  await expect(
    page.getByText("No canonical blocks are available in this snapshot.", { exact: true }),
  ).toBeVisible();
  mode = "error";
  await page.reload();
  await expect(
    page.getByText("Canonical indexed data is not ready", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("No canonical blocks are available in this snapshot.", { exact: true }),
  ).toHaveCount(0);
  const scan = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
    .analyze();
  expect(scan.violations).toEqual([]);
});
