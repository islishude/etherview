import { expect, test } from "@playwright/test";
import { configureAppearance } from "./appearance";

const address = "0x1111111111111111111111111111111111111111";

test("chart coverage icons preserve complete and pending colors in both themes", async ({
  page,
}) => {
  let complete = true;
  await page.route("**/api/v1/stats/charts/overview", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.data.coverage.complete = complete;
    await route.fulfill({ response, json: body });
  });
  for (const theme of ["light", "dark"] as const) {
    await configureAppearance(page, { width: 390, language: "en", theme });
    for (const state of [true, false]) {
      complete = state;
      await page.goto("/charts");
      const badge = page.locator(`.coverage-badge.${complete ? "complete" : "pending"}`);
      await expect(badge).toBeVisible();
      await expect(badge.locator("svg.status-icon")).toHaveCSS(
        "color",
        theme === "light"
          ? complete
            ? "rgb(22, 112, 68)"
            : "rgb(148, 98, 0)"
          : complete
            ? "rgb(120, 215, 163)"
            : "rgb(242, 196, 94)",
      );
    }
  }
});

for (const language of ["en", "zh"] as const) {
  test(`source search keeps keyboard commands and a Lucide close control in ${language}`, async ({
    page,
  }) => {
    await configureAppearance(page, { width: 390, language, theme: "dark" });
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    page.on("console", (message) => {
      if (message.type() === "error") errors.push(message.text());
    });
    await page.goto(`/address/${address}#code`);
    const editor = page.locator(".source-editor .cm-content");
    await expect(editor).toBeVisible();
    const term = (await editor.innerText()).match(/[A-Za-z_]{3,}/)![0];
    const searchButton = page
      .locator(".source-editor-toolbar")
      .getByRole("button", { name: language === "en" ? "Search" : "搜索", exact: true });
    await searchButton.click();
    const search = page.locator(".cm-search input[name=search]");
    await expect(search).toBeFocused();
    await search.fill(term);
    await page.keyboard.press("Enter");
    await expect(page.locator(".source-editor .cm-searchMatch").first()).toBeVisible();
    await page.keyboard.press("Shift+Enter");
    const close = page.locator(".cm-search button[name=close]");
    await expect(close).toHaveAccessibleName(language === "en" ? "Close" : "关闭");
    await expect(close.locator("svg.lucide-x")).toBeVisible();
    await close.click();
    await expect(search).toHaveCount(0);
    await expect(editor).toBeFocused();
    await page.keyboard.press("ControlOrMeta+f");
    await expect(search).toBeFocused();
    await expect(search).toHaveValue(term);
    await page.keyboard.press("Escape");
    await expect(search).toHaveCount(0);
    await expect(editor).toBeFocused();
    await expect(editor).toHaveAttribute("contenteditable", "false");
    await page.keyboard.press("ControlOrMeta+Alt+g");
    const line = page.locator(".cm-goto-line input[name=line]");
    await expect(line).toBeFocused();
    const closeLine = page.locator(".cm-goto-line .cm-dialog-close");
    await expect(closeLine.locator("svg.lucide-x")).toBeVisible();
    await expect(closeLine).toHaveText("");
    await expect(closeLine).toHaveAccessibleName(language === "en" ? "Close" : "关闭");
    await line.fill("1");
    await page.keyboard.press("Enter");
    await expect(line).toHaveCount(0);
    await expect(editor).toBeFocused();
    expect(errors).toEqual([]);
  });
}

for (const width of [390, 1440]) {
  for (const language of ["en", "zh"] as const) {
    test(`icon controls keep labels and geometry at ${width}px in ${language}`, async ({
      page,
      context,
    }, testInfo) => {
      await configureAppearance(page, {
        width,
        language,
        theme: language === "en" ? "light" : "dark",
      });
      await page.clock.install();
      const expires = Date.now() + 60_000;
      const primaryName = "a-long-primary-name-for-address-layout-regression.test";
      let publishName!: () => void;
      const publication = new Promise<void>((resolve) => {
        publishName = resolve;
      });
      await page.route("**/api/v1/config", async (route) => {
        const response = await route.fetch();
        const body = await response.json();
        body.data.features.ens = true;
        await route.fulfill({ response, json: body });
      });
      await page.route("**/api/v1/address-names?*", async (route) => {
        await publication;
        const addresses = new URL(route.request().url()).searchParams.get("addresses") ?? "";
        await route.fulfill({
          json: {
            data: {
              publication: "1",
              snapshot: "layout-1",
              items: addresses.split(",").map((value) => ({
                address: value,
                state: "resolved",
                primary_name: {
                  name: primaryName,
                  publication: "1",
                  observed_block_number: "100",
                  observed_block_hash: `0x${"1".repeat(64)}`,
                  valid_until: new Date(expires).toISOString(),
                },
              })),
            },
            meta: { request_id: "layout", chain_id: "1" },
          },
        });
      });
      await context.grantPermissions(["clipboard-read", "clipboard-write"]);
      await page.goto(`/address/${address}`);
      const header = page.locator(".address-header-actions");
      const copy = header.getByRole("button", {
        name: language === "en" ? "Copy" : "复制",
        exact: true,
      });
      const qr = header.locator(".address-qr-button");
      const expectAlignment = async () => {
        const copyBox = await header.locator(".copyable-copy-button").boundingBox();
        const qrBox = await qr.boundingBox();
        const valueBox = await header.locator(".address-identity-value").boundingBox();
        expect(
          Math.abs(copyBox!.y + copyBox!.height / 2 - qrBox!.y - qrBox!.height / 2),
        ).toBeLessThan(2);
        expect(
          Math.abs(valueBox!.y + valueBox!.height / 2 - qrBox!.y - qrBox!.height / 2),
        ).toBeLessThan(2);
        expect(qrBox!.x).toBeGreaterThanOrEqual(copyBox!.x + copyBox!.width);
        expect(await qr.locator("xpath=ancestor::a").count()).toBe(0);
      };
      await expect(copy).toBeVisible();
      await expect(header.locator(".address-primary-name")).toHaveCount(0);
      await expectAlignment();
      publishName();
      await expect(header.locator(".address-primary-name")).toHaveText(primaryName);
      await expect(header.getByRole("link", { name: primaryName })).toHaveAttribute(
        "href",
        `/name/${primaryName}`,
      );
      await expectAlignment();
      const before = await copy.boundingBox();
      await copy.focus();
      expect(await copy.evaluate((element) => getComputedStyle(element).outlineStyle)).not.toBe(
        "none",
      );
      await page.keyboard.press("Enter");
      await expect(copy).toHaveCount(0);
      const success = header.getByRole("button", {
        name: language === "en" ? "Copied" : "已复制",
        exact: true,
      });
      await expect(success.locator("svg.lucide-check")).toBeVisible();
      const after = await success.boundingBox();
      expect(after).toEqual(before);
      expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(address);
      await expect(qr.locator("svg.lucide-qr-code")).toBeVisible();
      const qrBox = await qr.boundingBox();
      expect(Math.abs(after!.y + after!.height / 2 - qrBox!.y - qrBox!.height / 2)).toBeLessThan(2);
      await page.screenshot({ path: testInfo.outputPath("copy-success.png") });
      await expect(copy).toBeVisible();
      await qr.click();
      const dialog = page.getByRole("dialog");
      await expect(dialog).toHaveAccessibleDescription(
        language === "en" ? "Scan to get the full address." : "扫描二维码获取完整地址。",
      );
      await expect(dialog.locator("code")).toHaveText(address);
      const dialogCopy = dialog.getByRole("button", {
        name: language === "en" ? "Copy" : "复制",
        exact: true,
      });
      await dialogCopy.click();
      expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(address);
      const surface = dialog.locator(".qr-code-surface");
      expect(await surface.evaluate((element) => getComputedStyle(element).backgroundColor)).toBe(
        "rgb(255, 255, 255)",
      );
      const dialogBox = await dialog.boundingBox();
      expect(dialogBox!.x).toBeGreaterThanOrEqual(16);
      expect(dialogBox!.x + dialogBox!.width).toBeLessThanOrEqual(width - 16);
      expect(await dialog.evaluate((element) => element.scrollWidth - element.clientWidth)).toBe(0);
      await page.screenshot({ path: testInfo.outputPath("address-qr.png") });
      const close = dialog.locator(".dialog-close");
      await expect(close.locator("svg.lucide-x")).toBeVisible();
      await expect(close).toHaveAccessibleName(language === "en" ? "Close" : "关闭");
      await close.focus();
      await page.keyboard.press("Shift+Tab");
      await expect(dialog.locator(".copyable-copy-button")).toBeFocused();
      await page.keyboard.press("Tab");
      await expect(close).toBeFocused();
      await page.keyboard.press("Enter");
      await expect(dialog).toHaveCount(0);
      await expect(qr).toBeFocused();
      await page.keyboard.press("Enter");
      await expect(dialog).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(dialog).toHaveCount(0);
      await expect(qr).toBeFocused();
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
      ).toBeLessThanOrEqual(1);
      await page.clock.fastForward(61_000);
      await expect(header.locator(".address-primary-name")).toHaveCount(0);
      await expectAlignment();
      await page.screenshot({ path: testInfo.outputPath("name-expired.png") });
    });
  }
}
