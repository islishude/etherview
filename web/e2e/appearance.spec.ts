import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

import { appearanceCases, configureAppearance } from "./appearance";

const previewAddress = "0x1111111111111111111111111111111111111111";
const previewHash = `0x${"a".repeat(64)}`;
const visualRoutes = [
  { name: "home", path: "/", ready: ".activity-row" },
  { name: "transactions", path: "/transactions", ready: "tbody tr" },
  { name: "transaction", path: `/tx/${previewHash}`, ready: ".transaction-action-card" },
  { name: "address", path: `/address/${previewAddress}`, ready: ".address-activity" },
  {
    name: "contract",
    path: `/address/${previewAddress}#code`,
    ready: ".source-editor .cm-content",
  },
  { name: "charts", path: "/charts", ready: ".chart-preview-card" },
  { name: "account", path: "/account", ready: ".auth-gate" },
];

for (const { width, language, theme } of appearanceCases) {
  test(`explorer appearance ${width}px ${language} ${theme}`, async ({ page }, testInfo) => {
    test.setTimeout(120_000);
    await configureAppearance(page, { width, language, theme });
    await page.emulateMedia({ reducedMotion: "reduce" });
    const pageErrors: string[] = [];
    const externalRequests: string[] = [];
    const cspErrors: string[] = [];
    page.on("pageerror", (error) => pageErrors.push(error.message));
    page.on("console", (message) => {
      if (/content security policy|violates.*directive/i.test(message.text()))
        cspErrors.push(message.text());
    });
    page.on("request", (request) => {
      if (new URL(request.url()).origin !== "http://127.0.0.1:4173")
        externalRequests.push(request.url());
    });
    for (const route of visualRoutes) {
      await page.goto(route.path);
      await expect(page.locator("main h1")).toBeVisible();
      await expect(page.locator(route.ready).first()).toBeVisible();
      const invalidIcons = await page.locator("svg.ui-icon").evaluateAll((icons) =>
        icons.flatMap((icon) => {
          const size = Number(icon.getAttribute("width"));
          const style = getComputedStyle(icon);
          return [14, 16, 20, 24].includes(size) &&
            icon.getAttribute("height") === String(size) &&
            style.width === `${size}px` &&
            style.height === `${size}px` &&
            (icon.classList.contains("dropdown-chevron")
              ? size === 14 && style.strokeWidth === "1.5px"
              : style.strokeWidth === "2px") &&
            icon.getAttribute("aria-hidden") === "true" &&
            icon.getAttribute("focusable") === "false"
            ? []
            : [icon.outerHTML];
        }),
      );
      expect(invalidIcons, route.name).toEqual([]);
      const walletButton = page.locator(".wallet-summary");
      const walletBox = await walletButton.boundingBox();
      const walletIcon = await walletButton.locator("svg.ui-icon").boundingBox();
      expect(walletIcon!.x, route.name).toBeGreaterThan(walletBox!.x + 4);
      expect(walletIcon!.x + walletIcon!.width, route.name).toBeLessThan(
        walletBox!.x + walletBox!.width - 4,
      );
      await expect(page.locator(".query-notice .live-icon")).toHaveCount(0);
      await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
      await expect(page.locator("html")).toHaveAttribute(
        "lang",
        language === "zh" ? "zh-CN" : "en",
      );
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
        route.name,
      ).toBeLessThanOrEqual(1);
      const scan = await new AxeBuilder({ page })
        .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
        .analyze();
      expect(scan.violations, `${route.name}: ${JSON.stringify(scan.violations)}`).toEqual([]);
      const screenshot = testInfo.outputPath(`${route.name}-${width}-${language}-${theme}.png`);
      await page.screenshot({ path: screenshot, fullPage: route.name === "home" });
      await testInfo.attach(route.name, { path: screenshot, contentType: "image/png" });
    }
    expect(pageErrors).toEqual([]);
    expect(cspErrors).toEqual([]);
    expect(externalRequests).toEqual([]);
  });
}
