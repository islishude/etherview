import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { configureAppearance, collectBrowserErrors } from "./appearance";

const address = `0x${"1".repeat(40)}`;
const controller = `0x${"2".repeat(40)}`;
for (const language of ["en", "zh"] as const) {
  test(`name directory and independent profile fields in ${language} at 390px`, async ({
    page,
  }) => {
    await configureAppearance(page, { width: 390, language, theme: "light" });
    const errors = collectBrowserErrors(page);
    let detailRequests = 0;
    let expires = Date.now() + 60000;
    const detail = (name: string) => ({
      name,
      state: "indexed",
      registration_state: "registered",
      registrant: { state: "value", value: address },
      controller: { state: "value", value: controller, model: "registry_manager" },
      expiration_time: { state: "value", value: "2000000000" },
      resolved_address: { state: "not_set" },
      profile_observation: {
        publication: "9007199254740993",
        observed_block_number: "100",
        observed_block_hash: `0x${"1".repeat(64)}`,
        valid_until: new Date(expires).toISOString(),
      },
      resolution_observation: {
        publication: "9007199254740994",
        observed_block_number: "100",
        observed_block_hash: `0x${"1".repeat(64)}`,
        valid_until: new Date(Date.now() + 120000).toISOString(),
      },
    });
    await page.route("**/api/v1/config", async (route) => {
      const response = await route.fetch();
      const body = await response.json();
      body.data.features.ens = true;
      await route.fulfill({ response, json: body });
    });
    await page.route("**/api/v1/address-names?*", async (route) => {
      const addresses =
        new URL(route.request().url()).searchParams.get("addresses")?.split(",") ?? [];
      await route.fulfill({
        json: {
          data: {
            publication: "1",
            snapshot: "names",
            items: addresses.map((value) => ({
              address: value,
              state: "resolved",
              primary_name: {
                name: "alice.eth",
                publication: "1",
                observed_block_number: "100",
                observed_block_hash: `0x${"1".repeat(64)}`,
                valid_until: new Date(Date.now() + 120000).toISOString(),
              },
            })),
          },
          meta: { chain_id: "1", request_id: "test" },
        },
      });
    });
    await page.route("**/api/v1/search?*", async (route) => {
      await route.fulfill({
        json: {
          data: [
            {
              kind: "name",
              key: "alice.eth",
              label: "alice.eth",
              rank: 100,
              name_details: detail("alice.eth"),
            },
            {
              kind: "name",
              key: "old.eth",
              label: "old.eth",
              rank: 70,
              name_details: {
                ...detail("old.eth"),
                registration_state: "expired",
                registrant: { state: "not_applicable" },
                controller: { state: "not_applicable" },
              },
            },
          ],
          meta: { chain_id: "1", request_id: "test" },
        },
      });
    });
    await page.route("**/api/v1/names/*", async (route) => {
      detailRequests++;
      await route.fulfill({
        json: {
          data: detail(
            decodeURIComponent(new URL(route.request().url()).pathname.split("/").pop()!),
          ),
          meta: { chain_id: "1", request_id: "test" },
        },
      });
    });
    await page.route("**/api/v1/names/*/history?*", async (route) =>
      route.fulfill({
        json: {
          data: {
            name: "alice.eth",
            state: "indexed",
            snapshot: "",
            items: [],
            coverage: { state: "pending", start: "0", pending: "0" },
          },
          meta: { chain_id: "1", request_id: "test" },
        },
      }),
    );
    await page.goto("/search?q=alice");
    await expect(page.locator(".search-name-result")).toHaveCount(2);
    await expect(
      page.getByText(language === "en" ? "Expired" : "已到期", { exact: true }),
    ).toBeVisible();
    await page.locator('a[href="/name/alice.eth"]').first().focus();
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(/\/name\/alice.eth$/);
    await expect(
      page.getByText(language === "en" ? "Registrant" : "注册持有人", { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText(language === "en" ? "Controller" : "管理账户", { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText(language === "en" ? "Not set" : "未设置", { exact: true }),
    ).toBeVisible();
    await expect(page.locator("a").filter({ hasText: controller }).first()).toHaveAttribute(
      "href",
      `/address/${controller}`,
    );
    await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
    await page
      .locator(".name-overview")
      .getByRole("button", { name: language === "en" ? "Copy" : "复制", exact: true })
      .first()
      .click();
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(address);
    expect((await new AxeBuilder({ page }).analyze()).violations).toEqual([]);
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true);
    await page.locator(`a[href="/address/${address}"]`).first().click();
    await expect(page).toHaveURL(new RegExp(`/address/${address}`));
    await page.locator('a[href="/name/alice.eth"]').first().click();
    await expect(page).toHaveURL(/\/name\/alice.eth$/);
    // A fresh deep link gives the profile a short validity window. Expiry must
    // hide ownership without polling or discarding a still-valid negative addr.
    expires = Date.now() + 4000;
    await page.goto("/name/short-lived.eth");
    await expect(page.locator(`a[href="/address/${controller}"]`)).toBeVisible();
    const calls = detailRequests;
    await expect(page.locator(`a[href="/address/${controller}"]`)).toHaveCount(0, {
      timeout: 10000,
    });
    await expect(
      page.getByText(language === "en" ? "Not set" : "未设置", { exact: true }),
    ).toBeVisible();
    expect(detailRequests).toBe(calls);
    expect(errors).toEqual([]);
  });
}
