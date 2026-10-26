import { expect, test, type Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { installVerificationSession } from "./verification-session";

const address = "0x1111111111111111111111111111111111111111";
const widgetScript = `
let next = 0;
const widgets = new Map();
window.turnstile = {
 render(container, options) {
  const id = String(++next);
  const pass = document.createElement('button'); pass.type = 'button'; pass.textContent = 'Pass test challenge';
  pass.dataset.widgetId = id;
  pass.onclick = () => options.callback('test-token-' + id);
  const expire = document.createElement('button'); expire.type = 'button'; expire.textContent = 'Expire test challenge';
  expire.onclick = () => options['expired-callback']();
  container.append(pass, expire); widgets.set(id, container); return id;
 },
 remove(id) { widgets.get(id)?.replaceChildren(); widgets.delete(id); }
};`;
async function protectedSession(page: Page) {
  await installVerificationSession(page);
  await page.route("**/api/v1/config", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    Object.assign(body.data.features, { user_auth: true, verification: true, sourcify: true });
    body.data.turnstile = {
      site_key: "test-public",
      enabled: true,
    };
    await route.fulfill({ response, json: body });
  });
}
for (const language of ["en", "zh"] as const) {
  test(`address toolbar and CSV challenge failure/retry in ${language}`, async ({ page }, info) => {
    const zh = language === "zh";
    await page.addInitScript((lng) => localStorage.setItem("etherview.language", lng), language);
    await protectedSession(page);
    let scripts = 0;
    await page.route("https://challenges.cloudflare.com/**", (route) => {
      scripts++;
      return scripts === 1
        ? route.abort()
        : route.fulfill({ contentType: "application/javascript", body: widgetScript });
    });
    const tokens: string[] = [];
    let releaseExport: (() => void) | undefined;
    await page.route("**/api/v1/users/me/watchlist", (route) =>
      route.fulfill({ json: { data: [], meta: { chain_id: "1", request_id: "test" } } }),
    );
    await page.route("**/api/v1/users/me/exports/address-activity", async (route) => {
      tokens.push(route.request().headers()["x-turnstile-token"]!);
      await new Promise<void>((resolve) => {
        releaseExport = resolve;
      });
      return tokens.length === 1
        ? route.fulfill({
            status: 503,
            json: {
              error: {
                code: "turnstile_unavailable",
                message: "Unavailable",
                request_id: "turnstile-test",
              },
            },
          })
        : route.fulfill({ contentType: "text/csv", body: "amount\r\n123\r\n" });
    });
    const response = await page.goto(`/address/${address}?turnstile=1`);
    expect(response?.headers()["content-security-policy"]).toContain(
      "frame-src https://challenges.cloudflare.com",
    );
    const toolbar = page.locator(".address-actions-toolbar");
    await expect(toolbar).toBeVisible();
    expect(scripts).toBe(0);
    for (const theme of ["light", "dark"]) {
      await page.evaluate((value) => {
        document.documentElement.dataset.theme = value;
      }, theme);
      for (const width of [1280, 390]) {
        await page.setViewportSize({ width, height: 900 });
        // Inspect settled theme colors, not an intermediate CSS transition frame.
        await toolbar.evaluate(async (element) => {
          await Promise.all(
            element.getAnimations({ subtree: true }).map((animation) => animation.finished),
          );
        });
        await toolbar.screenshot({
          path: info.outputPath(`address-toolbar-${theme}-${width}.png`),
        });
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
        expect(
          (
            await new AxeBuilder({ page })
              .include(".address-actions")
              .withTags(["wcag2a", "wcag2aa", "wcag21aa"])
              .analyze()
          ).violations,
        ).toEqual([]);
      }
    }
    const exportButton = page.getByRole("button", {
      name: zh ? "导出 CSV" : "Export CSV",
      exact: true,
    });
    await exportButton.focus();
    await page.keyboard.press("Enter");
    await expect(exportButton).toHaveAttribute("aria-expanded", "true");
    const download = page.getByRole("button", {
      name: zh ? "下载 CSV" : "Download CSV",
      exact: true,
    });
    await expect(download).toBeDisabled();
    await page.getByRole("button", { name: zh ? "重新验证" : "Retry verification" }).click();
    await page.getByRole("button", { name: "Pass test challenge" }).click();
    await expect(download).toBeEnabled();
    await page.screenshot({ path: info.outputPath("address-export-mobile.png"), fullPage: false });
    expect(
      (
        await new AxeBuilder({ page })
          .include(".address-actions")
          .withTags(["wcag2a", "wcag2aa", "wcag21aa"])
          .analyze()
      ).violations,
    ).toEqual([]);
    await page.getByRole("button", { name: "Expire test challenge" }).click();
    await expect(download).toBeDisabled();
    await page.getByRole("button", { name: zh ? "重新验证" : "Retry verification" }).click();
    await page.getByRole("button", { name: "Pass test challenge" }).click();
    const from = page.locator('input[type="datetime-local"]').first();
    const original = await from.inputValue();
    const challengeButton = page.getByRole("button", { name: "Pass test challenge" });
    const firstWidget = await challengeButton.getAttribute("data-widget-id");
    await download.click();
    await expect.poll(() => tokens.length).toBe(1);
    await expect(page.locator("#address-csv-export button.primary")).toBeDisabled();
    await expect(challengeButton).toHaveAttribute("data-widget-id", firstWidget!);
    releaseExport!();
    await expect(page.getByRole("alert")).toContainText(
      zh ? "暂时不可用" : "temporarily unavailable",
    );
    await expect(from).toHaveValue(original);
    await expect(download).toBeDisabled();
    await expect(challengeButton).not.toHaveAttribute("data-widget-id", firstWidget!);
    await page.getByRole("button", { name: "Pass test challenge" }).click();
    const secondWidget = await challengeButton.getAttribute("data-widget-id");
    const file = page.waitForEvent("download");
    await download.click();
    await expect.poll(() => tokens.length).toBe(2);
    await expect(page.locator("#address-csv-export button.primary")).toBeDisabled();
    await expect(challengeButton).toHaveAttribute("data-widget-id", secondWidget!);
    releaseExport!();
    const end = await page.locator('input[type="datetime-local"]').nth(1).inputValue();
    expect((await file).suggestedFilename()).toBe(
      `${address}-${original.replaceAll(":", "-")}-00Z-${end.replaceAll(":", "-")}-00Z-transaction.csv`,
    );
    await expect(challengeButton).not.toHaveAttribute("data-widget-id", secondWidget!);
    expect(tokens).toHaveLength(2);
    expect(tokens[0]).not.toBe(tokens[1]);
    await exportButton.click();
    await expect(page.getByRole("button", { name: "Pass test challenge" })).toHaveCount(0);
  });
}
test("local verification preserves uploaded source and needs a fresh challenge after rejection", async ({
  page,
}) => {
  await protectedSession(page);
  await page.route("https://challenges.cloudflare.com/**", (route) =>
    route.fulfill({ contentType: "application/javascript", body: widgetScript }),
  );
  await page.route("**/api/v1/verifier/compilers?**", (route) =>
    route.fulfill({
      json: {
        data: { language: "solidity", versions: ["0.8.30"] },
        meta: { chain_id: "1", request_id: "test" },
      },
    }),
  );
  const tokens: string[] = [];
  await page.route("**/api/v1/verification-jobs/local", (route) => {
    tokens.push(route.request().headers()["x-turnstile-token"]!);
    return route.fulfill({
      status: 403,
      json: {
        error: { code: "turnstile_invalid", message: "Rejected", request_id: "turnstile-test" },
      },
    });
  });
  await page.goto(`/verify?address=${address}&turnstile=1`);
  const submit = page.getByRole("button", { name: "Submit verification", exact: true });
  await expect(submit).toBeDisabled();
  await page.getByLabel("Standard JSON input", { exact: true }).setInputFiles({
    name: "source.json",
    mimeType: "application/json",
    buffer: Buffer.from('{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}}}'),
  });
  await page.getByRole("button", { name: "Pass test challenge" }).click();
  await submit.click();
  await expect(page.getByRole("alert")).toContainText("new human verification");
  await expect(page.getByRole("button", { name: "Remove source.json" })).toBeVisible();
  await expect(submit).toBeDisabled();
  await page.getByRole("button", { name: "Pass test challenge" }).click();
  await submit.click();
  await expect.poll(() => tokens.length).toBe(2);
  expect(tokens[0]).not.toBe(tokens[1]);
});
