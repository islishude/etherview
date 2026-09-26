import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { readFile } from "node:fs/promises";
import type { components } from "../src/api/schema.gen";

test("private watches, notification reads and CSV download use the embedded generated client", async ({
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
  let watches: components["schemas"]["AddressWatch"][] = [];
  let read = false;
  let mutationCount = 0;
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
      if (request.method() === "POST")
        watches = [
          { ...request.postDataJSON(), id: "watch-1", start_number: "100", start_hash: hash },
        ];
      await route.fulfill({
        status: request.method() === "POST" ? 201 : 200,
        json: envelope(request.method() === "POST" ? watches[0] : watches),
      });
      return;
    }
    if (url.pathname.endsWith("/notifications")) {
      await route.fulfill({
        json: envelope({
          items: watches.length
            ? [
                {
                  id: "1",
                  watch_id: "watch-1",
                  address,
                  label: "",
                  canonical: true,
                  published: true,
                  read,
                  activity: {
                    kind: "transaction",
                    block_number: "101",
                    block_hash: hash,
                    timestamp: "1790385000",
                    transaction_hash: hash,
                    transaction_index: "0",
                    direction: "in",
                    from: address,
                    to: address,
                    value: "12345678901234567890",
                    status: "success",
                  },
                },
              ]
            : [],
          unread_count: watches.length && !read ? "1" : "0",
          watermark: "1",
          next_cursor: "",
        }),
      });
      return;
    }
    if (url.pathname.endsWith("/read-through")) {
      expect(request.postDataJSON()).toEqual({ through_id: "1" });
      read = true;
      await route.fulfill({ status: 204 });
      return;
    }
    if (url.pathname.endsWith("/address-activity")) {
      expect(request.postDataJSON().address).toBe(address);
      await route.fulfill({ contentType: "text/csv", body: "amount\r\n12345678901234567890\r\n" });
      return;
    }
    await route.fulfill({ status: 404 });
  });
  await page.goto(`/address/${address}`);
  await page.getByRole("button", { name: "Watch address", exact: true }).click();
  await expect(page.getByRole("button", { name: "Remove watch", exact: true })).toBeVisible();
  await page.goto("/account?tab=watchlist");
  await expect(page.getByRole("heading", { name: "Watchlist", exact: true })).toBeVisible();
  await page
    .locator(".account-tabs")
    .getByRole("link", { name: "Notifications", exact: true })
    .click();
  await expect(page.getByText("Successful transaction", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Mark all as read", exact: true }).click();
  await expect(page.getByRole("button", { name: "Mark all as read", exact: true })).toBeDisabled();
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  expect(
    (await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze())
      .violations,
  ).toEqual([]);
  await page.getByRole("button", { name: "切换到中文" }).click();
  await expect(page.getByRole("heading", { name: "通知", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Switch to English" }).click();
  await page.goto(`/address/${address}`);
  await page.getByRole("button", { name: "Export CSV", exact: true }).click();
  const downloadPromise = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download CSV", exact: true }).click();
  const download = await downloadPromise;
  expect(await readFile((await download.path())!, "utf8")).toContain("12345678901234567890");
  expect(mutationCount).toBe(3);
});
