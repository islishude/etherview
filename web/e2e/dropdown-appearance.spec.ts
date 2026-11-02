import { expect, test } from "@playwright/test";
import { appearanceCases, configureAppearance } from "./appearance";
import { installVerificationSession } from "./verification-session";

for (const appearance of appearanceCases.filter(({ width }) => width !== 768)) {
  test(`dropdown indicators ${appearance.width}px ${appearance.language} ${appearance.theme}`, async ({
    page,
  }, testInfo) => {
    await configureAppearance(page, appearance);
    await page.emulateMedia({ reducedMotion: "reduce" });
    await installVerificationSession(page);
    await page.route("**/api/v1/verifier/compilers?**", (route) =>
      route.fulfill({
        json: {
          data: {
            language: "solidity",
            versions: ["0.8.30+commit.73712a01", "0.8.29+commit.ab55807c"],
          },
          meta: { request_id: "dropdown-appearance", chain_id: "1" },
        },
      }),
    );
    await page.goto("/verify");
    const native = page.locator("#verification-language");
    await expect(native).toBeVisible();
    const compiler = page.locator("#verification-compiler");
    await expect(compiler).toBeEnabled();
    const arrow = page.locator(".compiler-version-chevron");
    await expect(arrow).toHaveCSS("width", "14px");
    await expect(arrow).toHaveCSS("stroke-width", "1.5px");
    await expect(arrow).toHaveCSS("pointer-events", "none");
    await expect(arrow).toHaveCSS("transition-duration", "0s");
    await native.focus();
    await expect(page.locator(".select-control-chevron").first()).toHaveCSS("rotate", "none");
    const spacing = await page.locator(".select-control").evaluateAll((controls) =>
      controls.map((control) => {
        const select = control.querySelector("select")!;
        const icon = control.querySelector("svg")!;
        const box = select.getBoundingClientRect();
        const arrowBox = icon.getBoundingClientRect();
        return {
          padding: getComputedStyle(select).paddingRight,
          right: Math.round(box.right - arrowBox.right),
          centered: Math.abs(box.y + box.height / 2 - arrowBox.y - arrowBox.height / 2) < 1,
        };
      }),
    );
    for (const spacingCase of spacing)
      expect(spacingCase).toEqual({ padding: "36px", right: 12, centered: true });
    await compiler.click();
    await expect(arrow).toHaveCSS("rotate", "180deg");
    const inputBox = await compiler.boundingBox();
    const arrowBox = await arrow.boundingBox();
    expect(
      Math.abs(inputBox!.y + inputBox!.height / 2 - arrowBox!.y - arrowBox!.height / 2),
    ).toBeLessThan(1);
    await compiler.fill("0.8.29");
    await expect(page.locator(".compiler-version-popup").getByRole("option")).toHaveCount(1);
    await compiler.press("Enter");
    await expect(compiler).toHaveValue("0.8.29+commit.ab55807c");
    await expect(arrow).toHaveCSS("rotate", "none");
    await compiler.click();
    await compiler.press("Escape");
    await expect(compiler).toBeFocused();
    await expect(arrow).toHaveCSS("rotate", "none");
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
    ).toBeLessThanOrEqual(1);
    await page.screenshot({ path: testInfo.outputPath("dropdowns.png"), fullPage: true });
    await page.emulateMedia({ forcedColors: "active" });
    await expect(native).toHaveCSS("appearance", "auto");
    await expect(page.locator(".select-control-chevron").first()).toBeHidden();
  });
}
