import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { installAdminSession } from "./adminFixtures";
import { appearanceCases, configureAppearance, collectBrowserErrors } from "./appearance";

const timestamp = "2026-11-01T10:00:00Z";
const address = "0x2222222222222222222222222222222222222222";
const userID = "00000000-0000-4000-8000-000000000002";
const envelope = (data: unknown) => ({ data, meta: { chain_id: "1", request_id: "admin-design" } });

for (const appearance of appearanceCases.filter(({ width }) => width !== 768)) {
  test(`admin management ${appearance.width}px ${appearance.language} ${appearance.theme}`, async ({
    page,
  }, testInfo) => {
    await configureAppearance(page, appearance);
    await installAdminSession(page);
    const errors = collectBrowserErrors(page);
    const zh = appearance.language === "zh";
    let saved = false;
    let failSave = true;
    const searches: string[] = [];
    let user = {
      id: userID,
      chain_id: "1",
      address,
      role: "user",
      status: "active",
      created_at: timestamp,
      updated_at: timestamp,
      last_login_at: timestamp,
    };
    await page.route("**/api/v1/admin/users**", async (route) => {
      const request = route.request();
      if (request.method() === "PATCH") {
        if (failSave) {
          failSave = false;
          await route.fulfill({
            status: 503,
            json: {
              error: { code: "user_auth_unavailable", message: "Unavailable", request_id: "test" },
            },
          });
          return;
        }
        user = { ...user, ...request.postDataJSON() };
        saved = true;
        await route.fulfill({ json: envelope(user) });
        return;
      }
      const q = new URL(request.url()).searchParams.get("q") ?? "";
      searches.push(q);
      await route.fulfill({ json: envelope(q === "missing" ? [] : [user]) });
    });
    await page.goto("/account");
    const identity = page.locator(".account-identity");
    await expect(
      page.getByRole("heading", { name: zh ? "个人资料" : "Profile", exact: true }),
    ).toBeVisible();
    await expect(identity).toHaveCount(0);
    await expect(page.locator(".identity-card")).toHaveCount(0);
    await page.screenshot({ path: testInfo.outputPath("account-overview.png") });
    await page.getByRole("link", { name: zh ? "管理" : "Admin", exact: true }).click();
    await expect(page).toHaveURL(/\/account\?tab=admin&section=overview$/);
    await expect(identity).toHaveCount(0);
    await expect(page.getByRole("heading", { level: 1 })).toHaveCount(1);
    await expect(
      page.locator(".navigation-bar").getByRole("link", { name: /^(Account|账户|Admin|管理)$/ }),
    ).toHaveCount(0);
    await page.goto("/admin/users");
    await expect(page).toHaveURL(/\/account\?tab=admin&section=users$/);
    const adminNav = page.getByRole("navigation", { name: zh ? "管理" : "Admin", exact: true });
    await expect(
      adminNav.getByRole("link", { name: zh ? "用户" : "Users", exact: true }),
    ).toHaveAttribute("aria-current", "page");
    await page.locator(".account-tabs").getByRole("link").first().click();
    await expect(
      page.getByRole("heading", { name: zh ? "个人资料" : "Profile", exact: true }),
    ).toBeVisible();
    await expect(identity).toHaveCount(0);
    await page.goBack();
    await expect(page).toHaveURL(/\/account\?tab=admin&section=users$/);
    await expect(identity).toHaveCount(0);
    const search = page.getByRole("textbox", { name: zh ? "搜索用户" : "Search users" });
    await expect(page.locator(".admin-user-list")).toBeVisible();
    expect((await search.boundingBox())!.width).toBeLessThanOrEqual(320);
    await page.screenshot({ path: testInfo.outputPath("admin-users.png") });
    await search.fill("missing");
    expect(searches).not.toContain("missing");
    await search.press("Enter");
    await expect(
      page.getByText(zh ? "未找到匹配的用户。" : "No users match this search."),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: zh ? "上一页" : "Previous page", exact: true }),
    ).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: zh ? "下一页" : "Next page", exact: true }),
    ).toHaveCount(0);
    const clear = page.getByRole("button", { name: zh ? "清空" : "Clear", exact: true });
    await clear.click();
    await expect(search).toBeFocused();
    await expect(search).toHaveValue("");
    await expect(clear).toHaveCount(0);
    await expect(page.locator(".admin-user-list")).toBeVisible();
    await search.fill(address.slice(2, 14));
    await search.press("Enter");
    const settings = page.getByRole("button", { name: zh ? "设置" : "Settings", exact: true });
    await settings.click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toContainText(address);
    await page.screenshot({ path: testInfo.outputPath("user-settings.png") });
    const selects = dialog.getByRole("combobox");
    await selects.first().selectOption("admin");
    await page.keyboard.press("Escape");
    const confirmation = page.getByRole("alertdialog");
    await expect(confirmation).toBeVisible();
    await confirmation.getByRole("button", { name: zh ? "取消" : "Cancel", exact: true }).click();
    await expect(selects.first()).toHaveValue("admin");
    const save = dialog.getByRole("button", { name: zh ? "保存用户" : "Save user" });
    await save.click();
    await expect(dialog.getByRole("alert")).toBeVisible();
    await expect(selects.first()).toHaveValue("admin");
    await save.click();
    await expect(dialog).toHaveCount(0);
    expect(saved).toBe(true);
    await expect(settings).toBeFocused();
    await settings.click();
    await page.keyboard.press("Escape");
    await expect(settings).toBeFocused();
    expect((await new AxeBuilder({ page }).include("main").analyze()).violations).toEqual([]);

    const payment = {
      id: "00000000-0000-4000-8000-000000000003",
      operation: "getBlock",
      state: "settled",
      amount_atomic: "900719925474099312345",
      network: "eip155:1",
      asset: address,
      payer: address,
      recipient: address,
      user_id: userID,
      api_key_prefix: "ev_example",
      transaction_hash: `0x${"a".repeat(64)}`,
      created_at: timestamp,
      updated_at: timestamp,
    };
    const reads: string[] = [];
    await page.route("**/api/v1/admin/billing/**", async (route) => {
      const path = new URL(route.request().url()).pathname;
      reads.push(path);
      await route.fulfill({
        json: envelope(
          path.endsWith("summary")
            ? {
                payment_count: "1",
                amount_atomic: payment.amount_atomic,
                from_time: timestamp,
                to_time: timestamp,
                rows: [
                  {
                    operation: payment.operation,
                    state: payment.state,
                    network: payment.network,
                    asset: payment.asset,
                    payment_count: "1",
                    amount_atomic: payment.amount_atomic,
                  },
                ],
              }
            : [payment],
        ),
      });
    });
    await page.goto("/account?tab=admin");
    await expect(
      page.getByRole("heading", { name: zh ? "用户总数" : "Total users" }),
    ).toBeVisible();
    await expect(
      adminNav.getByRole("link", { name: zh ? "概览" : "Overview", exact: true }),
    ).toHaveAttribute("aria-current", "page");
    await expect(page.getByTitle(zh ? "原子金额" : "Atomic amount", { exact: true })).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath("admin-overview.png") });
    expect((await new AxeBuilder({ page }).include("main").analyze()).violations).toEqual([]);
    await page.goto("/admin/billing");
    await expect(page).toHaveURL(/\/account\?tab=admin&section=billing$/);
    await expect(page.locator(".account-identity")).toHaveCount(0);
    await expect(
      page.getByText(zh ? "按网络和资产汇总" : "Amounts by network and asset"),
    ).toBeVisible();
    expect(reads.some((path) => path.endsWith("payments"))).toBe(false);
    await page.screenshot({ path: testInfo.outputPath("billing-overview.png") });
    await page.getByRole("tab", { name: zh ? "流水" : "Payments", exact: true }).click();
    await page.getByRole("button", { name: zh ? "详情" : "Details", exact: true }).click();
    await expect(dialog).toContainText(userID);
    await page.screenshot({ path: testInfo.outputPath("billing-details.png") });
    expect((await new AxeBuilder({ page }).include("dialog").analyze()).violations).toEqual([]);
    await page.keyboard.press("Escape");
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      ),
    ).toBeLessThanOrEqual(1);
    expect(errors).toEqual([]);
  });
}

test("ordinary users cannot request detailed sync status or see admin navigation", async ({
  page,
}) => {
  await installAdminSession(page, "user");
  const requests: string[] = [];
  page.on("request", (request) => requests.push(new URL(request.url()).pathname));
  await page.goto("/status");
  await expect(page.getByText("Administrator access is required.")).toBeVisible();
  await expect(page.getByRole("link", { name: "Admin", exact: true })).toHaveCount(0);
  expect(requests).not.toContain("/api/v1/status");
});

test("disabled billing stays absent from Account management and direct aliases cannot fetch it", async ({
  page,
}) => {
  await installAdminSession(page, "admin", { api_billing: false });
  const requests: string[] = [];
  page.on("request", (request) => {
    if (request.url().includes("/api/v1/admin/billing/")) requests.push(request.url());
  });
  await page.goto("/admin/billing");
  await expect(page).toHaveURL(/\/account\?tab=admin&section=billing$/);
  const nav = page.getByRole("navigation", { name: "Admin", exact: true });
  await expect(nav.getByRole("link", { name: "Billing", exact: true })).toHaveCount(0);
  await expect(
    page.getByRole("heading", { name: "Prepaid API billing is disabled" }),
  ).toBeVisible();
  expect(requests).toEqual([]);
});
