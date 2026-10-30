import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { appearanceCases, collectBrowserErrors, configureAppearance } from "./appearance";
import { installVerificationSession } from "./verification-session";

const address = "0x1111111111111111111111111111111111111111";
test("login errors coalesce inside the native dialog and remain keyboard reachable", async ({
  page,
}, info) => {
  await configureAppearance(page, { width: 390, language: "en", theme: "dark" });
  await installVerificationSession(page, false);
  await page.route("**/api/v1/auth/session", (route) =>
    route.fulfill({
      json: { data: { authenticated: false }, meta: { chain_id: "1", request_id: "anonymous" } },
    }),
  );
  await page.addInitScript(() => {
    const detail = {
      info: {
        uuid: "00000000-0000-4000-8000-000000000065",
        name: "Message test wallet",
        icon: "data:image/png;base64,",
        rdns: "org.etherview.messages",
      },
      provider: {
        request: async ({ method }: { method: string }) => {
          if (method === "eth_requestAccounts") throw { code: 4001 };
          if (method === "eth_accounts") return [];
          if (method === "eth_chainId") return "0x1";
          throw new Error("Unexpected test wallet request");
        },
        on() {},
        removeListener() {},
      },
    };
    window.addEventListener("eip6963:requestProvider", () =>
      window.dispatchEvent(new CustomEvent("eip6963:announceProvider", { detail })),
    );
  });
  await page.goto(`/address/${address}`);
  const watch = page.getByRole("button", { name: "Watch address", exact: true });
  await watch.click();
  const dialog = page.getByRole("dialog");
  await dialog
    .getByRole("button", { name: "Sign in to manage this address watch.", exact: true })
    .click();
  const message = dialog.locator(".message-error");
  await expect(page.locator(".message-error")).toHaveCount(1);
  await expect(message.getByRole("alert")).toHaveText("The wallet request was rejected.");
  await message.hover();
  await page.screenshot({ path: info.outputPath("message-dialog.png") });
  expect(
    (await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze())
      .violations,
  ).toEqual([]);
  const close = message.getByRole("button", { name: "Close", exact: true });
  await close.focus();
  await page.keyboard.press("Tab");
  await expect(dialog.getByRole("button", { name: "Close", exact: true }).first()).toBeFocused();
  await close.focus();
  await page.keyboard.press("Enter");
  await expect(message).toHaveCount(0);
  await expect(dialog).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(watch).toBeFocused();
});

for (const appearance of appearanceCases.filter(({ width }) => width !== 768)) {
  test(`operation Messages ${appearance.width}px ${appearance.language} ${appearance.theme}`, async ({
    page,
  }, info) => {
    await configureAppearance(page, appearance);
    await page.emulateMedia({ reducedMotion: "reduce" });
    await installVerificationSession(page, false);
    const errors = collectBrowserErrors(page);
    let watching = false;
    const watch = {
      id: "watch-message",
      address,
      label: "",
      kinds: ["transaction"],
      direction: "both",
      enabled: true,
      start_number: "100",
      start_hash: `0x${"a".repeat(64)}`,
    };
    const envelope = (data: unknown) => ({ data, meta: { chain_id: "1", request_id: "messages" } });
    await page.route("**/api/v1/users/me/watchlist**", (route) => {
      const method = route.request().method();
      if (method === "DELETE")
        return route.fulfill({
          status: 503,
          json: {
            error: { code: "activity_unavailable", message: "Unavailable", request_id: "messages" },
          },
        });
      if (method === "POST") watching = true;
      return route.fulfill({
        status: method === "POST" ? 201 : 200,
        json: envelope(method === "POST" ? watch : watching ? [watch] : []),
      });
    });
    await page.goto(`/address/${address}`);
    const zh = appearance.language === "zh";
    const button = page.locator(".address-watch-button");
    await expect(button).toHaveAttribute("aria-busy", "false");
    await button.click();
    const success = page.locator(".message-success");
    await expect(success).toContainText(zh ? "已关注地址。" : "Address watched.");
    await expect(button.locator(".lucide-bell-ring")).toBeVisible();
    await success.hover();
    const bounds = (await success.boundingBox())!;
    expect(Math.abs(bounds.x + bounds.width / 2 - appearance.width / 2)).toBeLessThan(2);
    expect(bounds.y).toBeGreaterThanOrEqual(20);
    expect(bounds.x).toBeGreaterThanOrEqual(16);
    expect(await success.evaluate((node) => getComputedStyle(node).animationName)).toBe("none");
    await page.screenshot({ path: info.outputPath("message-success.png") });
    expect(
      (await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze())
        .violations,
    ).toEqual([]);
    await success.getByRole("button", { name: zh ? "关闭" : "Close" }).click();
    await expect(success).toHaveCount(0);
    await expect(button).toHaveAttribute("aria-pressed", "true");
    await button.click();
    const failure = page.locator(".message-error");
    await expect(failure.getByRole("alert")).toBeVisible();
    await expect(button.locator(".lucide-bell-ring")).toBeVisible();
    await failure.hover();
    await page.screenshot({ path: info.outputPath("message-error.png") });
    expect(
      (await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze())
        .violations,
    ).toEqual([]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await page.mouse.move(0, 500);
    await expect(failure).toHaveCount(0, { timeout: 5000 });
    // A repeated identical failure is a new message after the previous one expired.
    await button.click();
    await expect(failure.getByRole("alert")).toBeVisible();
    await failure.getByRole("button", { name: zh ? "关闭" : "Close" }).focus();
    await page.keyboard.press("Enter");
    await expect(failure).toHaveCount(0);
    expect(errors).toEqual([]);
  });
}
