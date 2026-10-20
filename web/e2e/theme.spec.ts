import { expect, test } from "@playwright/test";

test("system theme follows live changes, manual overrides persist, and automatic mode can be restored", async ({
  page,
}) => {
  await page.emulateMedia({ colorScheme: "light" });
  await page.goto("/blocks");
  const root = page.locator("html");
  const automatic = page.getByRole("button", { name: "Follow system theme" });
  await expect(root).toHaveAttribute("data-theme", "light");
  await expect(automatic).toHaveAttribute("aria-pressed", "true");
  const lightBackground = await page
    .locator("body")
    .evaluate((body) => getComputedStyle(body).backgroundColor);
  await page.emulateMedia({ colorScheme: "dark" });
  await expect(root).toHaveAttribute("data-theme", "dark");
  await expect
    .poll(() => page.locator("body").evaluate((body) => getComputedStyle(body).backgroundColor))
    .not.toBe(lightBackground);
  await page.emulateMedia({ colorScheme: "light" });
  await expect(root).toHaveAttribute("data-theme", "light");
  await expect.poll(() => page.evaluate(() => localStorage.getItem("etherview.theme"))).toBeNull();
  await page.reload();
  await page.emulateMedia({ colorScheme: "dark" });
  await expect(root).toHaveAttribute("data-theme", "dark");

  await page.getByRole("button", { name: "Switch color theme" }).click();
  await expect(root).toHaveAttribute("data-theme", "light");
  await expect(automatic).toHaveAttribute("aria-pressed", "false");
  await page.reload();
  for (const colorScheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme });
    await expect(root).toHaveAttribute("data-theme", "light");
  }
  await automatic.focus();
  await page.keyboard.press("Enter");
  await expect(root).toHaveAttribute("data-theme", "dark");
  await expect(automatic).toHaveAttribute("aria-pressed", "true");
  await page.reload();
  await page.emulateMedia({ colorScheme: "light" });
  await expect(root).toHaveAttribute("data-theme", "light");
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("button", { name: "切换到中文" }).click();
  await expect(page.getByRole("button", { name: "跟随系统主题" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await expect(page.locator("main tbody tr").first()).toBeVisible();
  for (const width of [360, 390, 440, 768]) {
    await page.setViewportSize({ width, height: 844 });
    await expect(page.locator("body")).toHaveJSProperty("scrollWidth", width);
  }
  await expect(page.locator(".site-header")).toHaveCount(1);
});
