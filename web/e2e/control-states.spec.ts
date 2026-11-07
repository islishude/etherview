import { expect, test, type Page } from "@playwright/test";
import { configureAppearance } from "./appearance";

async function tokenColor(page: Page, token: string) {
  return page.evaluate((name) => {
    const probe = document.createElement("span");
    probe.style.color = `var(${name})`;
    document.body.append(probe);
    const color = getComputedStyle(probe).color;
    probe.remove();
    return color;
  }, token);
}

for (const theme of ["light", "dark"] as const) {
  test(`shared controls retain visible selection and success states in ${theme}`, async ({
    page,
    context,
  }) => {
    await configureAppearance(page, { width: 1440, language: "en", theme });
    await page.emulateMedia({ colorScheme: theme });
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    await page.goto("/charts/execution-fees?range=7d&interval=day");
    const preset = page.getByRole("button", { name: "7D", exact: true });
    await expect(preset).toHaveAttribute("aria-pressed", "true");
    await expect.soft(preset).toHaveCSS("background-color", await tokenColor(page, "--brand-soft"));
    await expect.soft(preset).toHaveCSS("border-top-color", await tokenColor(page, "--brand"));

    await page.getByRole("button", { name: "Follow system theme" }).click();
    const systemTheme = page.getByRole("button", { name: "Follow system theme" });
    await expect(systemTheme).toHaveAttribute("aria-pressed", "true");
    await expect.soft(systemTheme).toHaveCSS("border-top-color", await tokenColor(page, "--brand"));

    await page.goto("/charts");
    const more = page.getByRole("button", { name: "More", exact: true });
    await expect.soft(more).toHaveCSS("border-bottom-color", await tokenColor(page, "--brand"));
    await more.click();
    await expect.soft(more).toHaveCSS("color", await tokenColor(page, "--brand"));

    await page.goto("/address/0x1111111111111111111111111111111111111111");
    const copy = page.locator(".copyable-copy-button").first();
    await copy.click();
    await expect(copy).toHaveAttribute("aria-label", "Copied");
    await expect.soft(copy).toHaveCSS("color", await tokenColor(page, "--success"));
  });
}
