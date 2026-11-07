import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

test("explorer navigation switches between disclosure and links at 1024px", async ({ page }) => {
  await page.goto("/");
  const trigger = page.locator('button[aria-controls="explorer-navigation-links"]');
  const links = page.locator("#explorer-navigation-links");
  for (const width of [1440, 1024, 1023, 768, 390, 1440]) {
    await page.setViewportSize({ width, height: 1000 });
    if (width >= 1024) {
      await expect(trigger).toBeHidden();
      await expect(links).toBeVisible();
    } else {
      await expect(trigger).toBeVisible();
      await expect(links).toBeHidden();
      await trigger.click();
      await expect(links).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(trigger).toBeFocused();
      await expect(links).toBeHidden();
    }
  }
});

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
  await expect(page.getByRole("link", { name: "Verify contract", exact: true })).toBeFocused();
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
    await expect(page.getByRole("link", { name: "Verify contract", exact: true })).toBeVisible();
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
