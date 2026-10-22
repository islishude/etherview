import { expect, test } from "@playwright/test";
import { loadBranding } from "../scripts/branding";

const { brand, assets } = loadBranding();

test("embedded branding preserves metadata, image delivery and bilingual mobile layout", async ({
  page,
}, testInfo) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error" && /Content Security Policy|Refused to/iu.test(message.text())) {
      errors.push(message.text());
    }
  });
  await page.goto("/");
  await expect(page).toHaveTitle(brand.title);
  await expect(page.locator('meta[name="description"]')).toHaveAttribute(
    "content",
    brand.description,
  );
  await expect(page.locator('link[rel="icon"]')).toHaveAttribute("href", brand.favicon);
  await expect(page.locator('link[rel="icon"]')).toHaveAttribute("type", brand.faviconType);
  await expect(page.locator(".brand strong")).toHaveText(brand.name);
  await expect(page.locator(".footer-brand")).toHaveText(brand.name);
  await expect(page.locator(".brand-mark")).toHaveAttribute("src", brand.logo);
  await expect
    .poll(() =>
      page.locator(".brand-mark").evaluate((image: HTMLImageElement) => image.naturalWidth),
    )
    .toBeGreaterThan(0);
  for (const asset of assets) {
    const response = await page.request.get(asset.url);
    expect(response.status()).toBe(200);
    expect(response.headers()["content-type"]).toBe(asset.type);
    expect(response.headers()["cache-control"]).toBe("public, max-age=31536000, immutable");
    expect(response.headers()["x-content-type-options"]).toBe("nosniff");
    expect(await response.body()).toEqual(asset.bytes);
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("button", { name: "切换到中文" }).click();
  await expect(page.locator("a.brand")).toHaveAccessibleName(`${brand.name} 首页`);
  await expect(page.locator(".brand strong")).toHaveText(brand.name);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await page.screenshot({ path: testInfo.outputPath("branding-mobile.png") });
  await page.locator(".language-control").click();
  await expect(page.locator("a.brand")).toHaveAccessibleName(`${brand.name} home`);
  await page.goto("/blocks");
  await expect(page).toHaveTitle(brand.title);
  await expect(page.locator(".brand-mark")).toBeVisible();
  expect(errors).toEqual([]);
});
