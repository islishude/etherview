import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { readFile } from "node:fs/promises";
import type * as schemas from "../src/api/heyapi/types.gen";

test("private watches, browser notification settings and CSV download use the embedded generated client", async ({
  page,
}) => {
  const address = "0x1111111111111111111111111111111111111111";
  const hash = `0x${"a".repeat(64)}`;
  const csrf = "c".repeat(43);
  const user = {
    id: "123e4567-e89b-42d3-a456-426614174000",
    chain_id: "1",
    address,
    role: "user",
    status: "active",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
  const envelope = (data: unknown) => ({
    data,
    meta: { chain_id: "1", request_id: "watchlist-browser" },
  });
  let watches: schemas.AddressWatch[] = [];
  let mutationCount = 0;
  let releaseWatch: (() => void) | undefined;
  await page.route("**/api/v1/config", async (route) => {
    const upstream = await route.fetch();
    const body = await upstream.json();
    body.data.features.user_auth = true;
    await route.fulfill({ json: body });
  });
  await page.route("**/api/v1/auth/session", (route) =>
    route.fulfill({
      json: envelope({
        authenticated: true,
        user,
        csrf_token: csrf,
        expires_at: "2099-01-01T00:00:00Z",
      }),
    }),
  );
  await page.route("**/api/v1/users/me/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (request.method() !== "GET") {
      expect(request.headers()["x-csrf-token"]).toBe(csrf);
      mutationCount++;
    }
    if (url.pathname.endsWith("/watchlist")) {
      if (request.method() === "POST" && mutationCount === 1)
        await new Promise<void>((resolve) => {
          releaseWatch = resolve;
        });
      if (request.method() === "POST")
        watches = [
          ...watches,
          {
            ...request.postDataJSON(),
            id: `watch-${watches.length + 1}`,
            start_number: "100",
            start_hash: hash,
          },
        ];
      await route.fulfill({
        status: request.method() === "POST" ? 201 : 200,
        json: envelope(request.method() === "POST" ? watches.at(-1) : watches),
      });
      return;
    }
    if (url.pathname.endsWith("/push-subscriptions")) {
      await route.fulfill({ json: envelope({ enabled: false, public_key: "", items: [] }) });
      return;
    }
    if (url.pathname.endsWith("/address-activity")) {
      expect(request.postDataJSON().address).toBe(address);
      expect(request.postDataJSON().kind).toBe("all");
      await route.fulfill({ contentType: "text/csv", body: "amount\r\n12345678901234567890\r\n" });
      return;
    }
    await route.fulfill({ status: 404 });
  });
  await page.goto(`/address/${address}`);
  expect(mutationCount).toBe(0);
  await page.getByRole("button", { name: "Watch address", exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/address/${address}$`));
  const busyWatch = page.locator(".address-watch-button");
  await expect(busyWatch).toBeDisabled();
  await expect(busyWatch).toHaveAttribute("aria-busy", "true");
  await expect(busyWatch.locator(".lucide-loader-circle")).toBeVisible();
  expect(
    await busyWatch.locator("svg").evaluate((icon) => getComputedStyle(icon).animationName),
  ).toBe("query-loading-spin");
  await expect.poll(() => Boolean(releaseWatch)).toBe(true);
  releaseWatch!();
  await expect(page.getByRole("button", { name: "Remove watch", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await expect(busyWatch.locator(".lucide-bell-ring")).toBeVisible();
  await expect(busyWatch.locator(".lucide-loader-circle")).toHaveCount(0);
  await expect(page.locator(".address-watch-feedback")).toHaveCount(0);
  await page.reload();
  await expect(page.getByRole("button", { name: "Remove watch", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await page.goto("/account?tab=watchlist");
  await expect(page.getByRole("heading", { name: "Watchlist", exact: true })).toBeVisible();
  await expect(page.locator(".account-tabs a")).toHaveText(["Overview", "Watchlist", "API Keys"]);
  await expect(page.getByRole("textbox", { name: "Address", exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Watch address", exact: true }).click();
  await expect(page.getByRole("textbox", { name: "Address", exact: true })).toBeVisible();
  await expect(page.locator(".watchlist-items")).toHaveCount(0);
  await expect(page.getByRole("radio", { name: "Both directions" })).toBeChecked();
  await page
    .locator(".account-create-form")
    .screenshot({ path: test.info().outputPath("watchlist-create-desktop.png") });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page
    .locator(".account-create-form")
    .screenshot({ path: test.info().outputPath("watchlist-create-mobile.png") });
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(page.locator(".watchlist-items li")).toHaveCount(1);
  await page.getByRole("button", { name: "Watch address", exact: true }).click();
  await page.getByRole("textbox", { name: "Address", exact: true }).fill(`0x${"2".repeat(40)}`);
  await page.getByRole("textbox", { name: "Private label", exact: true }).fill("Second watch");
  await page.locator(".watch-direction-options").getByText("Incoming", { exact: true }).click();
  await page.getByRole("radio", { name: "Incoming", exact: true }).press("ArrowRight");
  await expect(page.getByRole("radio", { name: "Outgoing", exact: true })).toBeChecked();
  await page.getByRole("button", { name: "Watch address", exact: true }).click();
  await expect(page.locator(".watchlist-items li")).toHaveCount(2);
  expect(watches[1].direction).toBe("out");
  await expect(page.getByRole("textbox", { name: "Address", exact: true })).toHaveCount(0);
  watches = [
    ...watches,
    ...[3, 4, 5].map((n) => ({
      ...watches[0],
      id: `watch-${n}`,
      address: `0x${String(n).repeat(40)}`,
      label: `Watch ${n}`,
    })),
  ];
  await page.reload();
  await expect(page.locator(".watchlist-items li")).toHaveCount(5);
  await expect(page.getByRole("button", { name: "Watch address", exact: true })).toBeDisabled();
  await expect(
    page.getByRole("heading", { name: "Browser notifications", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Allow notifications", exact: true }),
  ).toBeDisabled();
  await page.evaluate(() => window.scrollTo({ top: 0, behavior: "instant" }));
  await page.screenshot({
    path: test.info().outputPath("notifications-desktop.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  expect(
    (await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze())
      .violations,
  ).toEqual([]);
  await page.evaluate(() => window.scrollTo({ top: 0, behavior: "instant" }));
  await page.screenshot({
    path: test.info().outputPath("notifications-mobile.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "切换到中文" }).click();
  await expect(page.getByRole("heading", { name: "浏览器通知", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Switch to English" }).click();
  await page.goto(`/address/${address}`);
  await page.getByRole("link", { name: "Export CSV", exact: true }).click();
  const downloadPromise = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download CSV", exact: true }).click();
  const download = await downloadPromise;
  expect(await readFile((await download.path())!, "utf8")).toContain("12345678901234567890");
  expect(download.suggestedFilename()).toMatch(/-all\.csv$/);
  expect(mutationCount).toBe(3);
});

for (const language of ["en", "zh"] as const) {
  test(`anonymous address actions use a login dialog and compact export card in ${language}`, async ({
    page,
  }, info) => {
    const zh = language === "zh";
    const address = "0x1111111111111111111111111111111111111111";
    await page.addInitScript((lng) => localStorage.setItem("etherview.language", lng), language);
    await page.route("**/api/v1/config", async (route) => {
      const response = await route.fetch();
      const json = await response.json();
      json.data.features.user_auth = true;
      await route.fulfill({ response, json });
    });
    await page.route("**/api/v1/auth/session", (route) =>
      route.fulfill({
        json: {
          data: { authenticated: false },
          meta: { chain_id: "1", request_id: "anonymous-actions" },
        },
      }),
    );
    await page.route(`**/api/v1/addresses/${address}/transactions*`, async (route) => {
      const response = await route.fetch();
      const json = await response.json();
      json.data = [];
      delete json.meta.next_cursor;
      await route.fulfill({ response, json });
    });
    const privateRequests: string[] = [];
    page.on("request", (request) => {
      if (
        request.url().includes("/users/me/") ||
        request.url().includes("challenges.cloudflare.com")
      )
        privateRequests.push(request.url());
    });
    await page.goto(`/address/${address}`);
    const watch = page.getByRole("button", {
      name: zh ? "关注地址" : "Watch address",
      exact: true,
    });
    const exportLink = page.getByRole("link", {
      name: zh ? "导出 CSV" : "Export CSV",
      exact: true,
    });
    await expect(watch).toBeVisible();
    await expect(watch).toHaveText("");
    expect(
      await watch.evaluate((element) =>
        element.previousElementSibling?.classList.contains("address-qr-button"),
      ),
    ).toBe(true);
    await expect(
      page.getByText("Sign in to watch this address or export CSV.", { exact: true }),
    ).toHaveCount(0);
    await expect(page.locator(".address-activity table")).toHaveCount(0);
    for (const theme of ["light", "dark"]) {
      await page.evaluate((value) => {
        document.documentElement.dataset.theme = value;
      }, theme);
      for (const width of [1280, 390]) {
        await page.setViewportSize({ width, height: 900 });
        await expect(exportLink).toBeVisible();
        const linkBox = await exportLink.boundingBox();
        const sectionBox = await page.locator(".address-activity").boundingBox();
        expect(
          Math.abs(linkBox!.x + linkBox!.width - sectionBox!.x - sectionBox!.width),
        ).toBeLessThan(2);
        expect(linkBox!.y).toBeGreaterThan(sectionBox!.y);
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
        await page.screenshot({
          path: info.outputPath(`address-actions-${theme}-${width}.png`),
          fullPage: true,
        });
      }
    }
    await watch.focus();
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(new RegExp(`/address/${address}$`));
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    const close = dialog.getByRole("button", { name: zh ? "关闭" : "Close", exact: true });
    await expect(close).toBeFocused();
    await page.keyboard.press("Shift+Tab");
    await expect(close).toBeFocused(); // No wallet is installed; the login button is disabled.
    expect(
      (await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze())
        .violations,
    ).toEqual([]);
    for (const theme of ["light", "dark"]) {
      await page.evaluate((value) => {
        document.documentElement.dataset.theme = value;
      }, theme);
      for (const width of [1280, 390]) {
        await page.setViewportSize({ width, height: 900 });
        await page.screenshot({ path: info.outputPath(`watch-login-${theme}-${width}.png`) });
      }
    }
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
    await expect(watch).toBeFocused();
    await watch.click();
    await page.mouse.click(4, 4);
    await expect(dialog).toHaveCount(0);
    await expect(watch).toBeFocused();
    await exportLink.click();
    await expect(page).toHaveURL(new RegExp(`/address/${address}/export$`));
    await expect(page.locator(".address-action-login")).toBeVisible();
    await expect(page.locator("#address-csv-export")).toHaveCount(0);
    const login = page.locator(".address-action-login");
    const signIn = login.getByRole("button", { name: zh ? "登录" : "Sign in", exact: true });
    for (const theme of ["light", "dark"]) {
      await page.evaluate((value) => {
        document.documentElement.dataset.theme = value;
      }, theme);
      for (const width of [1280, 390]) {
        await page.setViewportSize({ width, height: 900 });
        const buttonBox = (await signIn.boundingBox())!;
        const textBox = (await login.locator("p").first().boundingBox())!;
        expect(Math.abs(buttonBox.x - textBox.x)).toBeLessThan(2);
        expect(buttonBox.width).toBeLessThan(160);
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
        await page.screenshot({
          path: info.outputPath(`export-login-${theme}-${width}.png`),
          fullPage: true,
        });
      }
    }
    expect(
      (await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze())
        .violations,
    ).toEqual([]);

    await page.reload();
    await expect(page.locator(".address-action-login")).toBeVisible();
    expect(privateRequests).toEqual([]);
    await page.goto(`/address/${address}?tab=erc20-transfers`);
    await expect(page.locator(".address-activity")).toBeVisible();
    await expect(exportLink).toHaveCount(0);
  });
}
