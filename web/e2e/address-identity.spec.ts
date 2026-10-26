import { expect, test, type Locator, type Page } from "@playwright/test";

const system = "0xfffffffffffffffffffffffffffffffffffffffe";

async function expectHintAtMarker(page: Page, marker: Locator) {
  const tooltip = page.getByRole("tooltip");
  await expect(tooltip).toBeVisible();
  const hint = await tooltip.boundingBox();
  const icon = await marker.boundingBox();
  expect(hint).not.toBeNull();
  expect(icon).not.toBeNull();
  expect(hint!.x).toBeGreaterThanOrEqual(0);
  expect(hint!.x + hint!.width).toBeLessThanOrEqual(page.viewportSize()!.width);
  expect(hint!.y).toBeGreaterThanOrEqual(0);
  expect(hint!.y + hint!.height).toBeLessThanOrEqual(page.viewportSize()!.height);
  expect(Math.abs(hint!.y + hint!.height - icon!.y)).toBeLessThan(32);
}

for (const colorScheme of ["light", "dark"] as const) {
  for (const width of [1280, 390]) {
    test(`system markers and QR controls stay compact at ${width}px in ${colorScheme}`, async ({
      page,
    }, testInfo) => {
      await page.setViewportSize({ width, height: 900 });
      await page.emulateMedia({ colorScheme });
      await page.route("**/api/v1/transactions?*", async (route) => {
        const response = await route.fetch();
        const envelope = await response.json();
        const ordinary = envelope.data[0];
        envelope.data = [
          { ...ordinary, from: system },
          { ...ordinary, hash: `0x${"b".repeat(64)}` },
        ];
        await route.fulfill({ response, json: envelope });
      });
      await page.goto("/transactions");
      const rows = page.locator("main tbody tr");
      await expect(rows).toHaveCount(2);
      const marker = rows.first().getByRole("button", { name: "System Address" });
      await expect(marker).toBeVisible();
      await marker.scrollIntoViewIfNeeded();
      expect(await marker.locator("xpath=ancestor::a").count()).toBe(0);
      const systemRow = await rows.first().boundingBox();
      const ordinaryRow = await rows.nth(1).boundingBox();
      expect(Math.abs(systemRow!.height - ordinaryRow!.height)).toBeLessThanOrEqual(1);
      const identity = rows.first().locator(".address-system-identity");
      const code = await identity.locator("code").boundingBox();
      const icon = await marker.boundingBox();
      expect(Math.abs(code!.y + code!.height / 2 - icon!.y - icon!.height / 2)).toBeLessThan(2);
      await marker.hover();
      await expectHintAtMarker(page, marker);
      await page.screenshot({ path: testInfo.outputPath("table-marker.png") });
      await page.keyboard.press("Escape");
      await expect(page.getByRole("tooltip")).toHaveCount(0);
      await marker.click();
      await expectHintAtMarker(page, marker);
      await expect(page).toHaveURL(/\/transactions$/);
      await page.getByRole("heading", { level: 1 }).click();
      await expect(page.getByRole("tooltip")).toHaveCount(0);
      await identity.getByRole("link").click();
      await expect(page).toHaveURL(new RegExp(`/address/${system}`, "i"));

      const header = page.locator(".address-header-actions");
      const headerMarker = header.getByRole("button", { name: "System Address" });
      await headerMarker.focus();
      await expectHintAtMarker(page, headerMarker);
      await page.keyboard.press("Tab");
      await expect(page.getByRole("tooltip")).toHaveCount(0);
      const qrButton = page.getByRole("button", { name: "Show QR code" });
      await expect(qrButton.locator("svg")).toHaveAttribute("width", "16");
      const copyBox = await header.getByRole("button", { name: "Copy", exact: true }).boundingBox();
      const qrBox = await qrButton.boundingBox();
      expect(
        Math.abs(copyBox!.y + copyBox!.height / 2 - qrBox!.y - qrBox!.height / 2),
      ).toBeLessThan(2);
      await page.screenshot({ path: testInfo.outputPath("address-controls.png") });
      await qrButton.click();
      const dialog = page.getByRole("dialog", { name: "Address QR code" });
      await expect(dialog).toBeFocused();
      await expect(dialog.getByText(new RegExp(`^${system}$`, "i"))).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(dialog).toHaveCount(0);
      await expect(qrButton).toBeFocused();
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
        ),
      ).toBeLessThanOrEqual(1);
    });
  }
}
