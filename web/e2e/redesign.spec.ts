import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

test("grouped navigation supports keyboard, responsive disclosure and capability gating", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/blocks");
  const more = page.getByRole("button", { name: "More", exact: true });
  await expect(page.getByRole("link", { name: "Pending", exact: true })).toBeHidden();
  await more.focus();
  await page.keyboard.press("Enter");
  await page.keyboard.press("Tab");
  await expect(page.getByRole("link", { name: "User Ops", exact: true })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(more).toBeFocused();
  await expect(more).toHaveAttribute("aria-expanded", "false");
  await more.click();
  await page.getByRole("searchbox", { name: "Search", exact: true }).click();
  await expect(more).toHaveAttribute("aria-expanded", "false");

  for (const width of [768, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    const explore = page.getByRole("button", { name: "Explore", exact: true });
    await expect(explore).toBeVisible();
    await expect(more).toBeHidden();
    await explore.click();
    await more.click();
    await expect(page.getByRole("link", { name: "Sync status" })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(more).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(explore).toBeFocused();
    await expect(explore).toHaveAttribute("aria-expanded", "false");
    await explore.click();
    await more.click();
    await page.getByRole("link", { name: "Sync status" }).click();
    await expect(page).toHaveURL("/status");
    await expect(explore).toHaveAttribute("aria-expanded", "false");
    await expect(explore).toBeFocused();
    await page.getByRole("button", { name: "Switch color theme" }).click();
    await expect(page.getByRole("button", { name: "切换到中文" })).toBeVisible();
  }

  await page.route("**/api/v1/config", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.data.features.user_operations = false;
    body.data.features.user_auth = false;
    await route.fulfill({ response, json: body });
  });
  await page.reload();
  await page.getByRole("button", { name: "Explore", exact: true }).click();
  await more.click();
  await expect(page.getByRole("link", { name: "Pending", exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "User Ops", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Account", exact: true })).toHaveCount(0);
  const scan = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
    .analyze();
  expect(scan.violations).toEqual([]);
});

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

for (const width of [390, 768, 1440]) {
  for (const language of ["en", "zh"] as const) {
    for (const theme of ["light", "dark"] as const) {
      test(`visual acceptance ${width}px ${language} ${theme}`, async ({ page }, testInfo) => {
        test.setTimeout(120_000);
        await page.setViewportSize({ width, height: 1000 });
        await page.emulateMedia({ reducedMotion: "reduce" });
        await page.addInitScript(
          ({ language, theme }) => {
            localStorage.setItem("etherview.language", language);
            localStorage.setItem("etherview.theme", theme);
          },
          { language, theme },
        );
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
          await expect(page.locator(".query-notice .pulse-dot")).toHaveCount(0);
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
  }
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
        json: { error: { code: "NOT_READY", message: "not ready", request_id: "redesign" } },
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
