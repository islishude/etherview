import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { appearanceCases, collectBrowserErrors, configureAppearance } from "./appearance";

const address = "0x1111111111111111111111111111111111111111";
const asset = "0x3333333333333333333333333333333333333333";
const hash = `0x${"6".repeat(64)}`;
const timestamp = "2026-10-30T00:00:00Z";
const largeAmount = "340282366920938463463374607431768211455";
const envelope = (data: unknown) => ({
  data,
  meta: { request_id: "account-design", chain_id: "1" },
});

for (const appearance of appearanceCases) {
  test(`account billing layout ${appearance.width}px ${appearance.language} ${appearance.theme}`, async ({
    page,
  }, testInfo) => {
    await configureAppearance(page, appearance);
    const errors = collectBrowserErrors(page);
    const external: string[] = [];
    const writes: string[] = [];
    page.on("request", (request) => {
      if (new URL(request.url()).origin !== "http://127.0.0.1:4173") external.push(request.url());
      if (request.url().includes("/billing/") && request.method() !== "GET")
        writes.push(request.url());
    });
    await page.route("**/api/v1/config", async (route) => {
      const response = await route.fetch();
      const body = await response.json();
      body.data.features = {
        ...body.data.features,
        user_auth: true,
        api_billing: true,
        x402_topups: true,
      };
      await route.fulfill({ json: body });
    });
    await page.route("**/api/v1/auth/session", (route) =>
      route.fulfill({
        json: envelope({
          authenticated: true,
          csrf_token: "c".repeat(43),
          expires_at: "2099-01-01T00:00:00Z",
          user: {
            id: "00000000-0000-7000-8000-000000000061",
            chain_id: "1",
            address,
            role: "user",
            status: "active",
            display_name: "Alex",
            created_at: timestamp,
            updated_at: timestamp,
          },
        }),
      }),
    );
    await page.route("**/api/v1/billing/**", (route) => {
      const path = new URL(route.request().url()).pathname;
      const common = {
        id: "00000000-0000-7000-8000-000000000066",
        user_id: "private-user",
        amount_atomic: largeAmount,
        network: "eip155:1",
        asset,
        recipient: address,
        payer: address,
        transaction_hash: hash,
        created_at: timestamp,
        updated_at: timestamp,
      };
      if (path.endsWith("/config"))
        return route.fulfill({
          json: envelope({
            api_billing_enabled: true,
            x402_topups_enabled: true,
            x402_version: 2,
            scheme: "exact",
            network: "eip155:1",
            asset,
            recipient: address,
            asset_transfer_methods: ["eip3009", "permit2"],
            minimum_topup_amount_atomic: "1",
            maximum_topup_amount_atomic: largeAmount,
            asset_decimals: 6,
            base_price_atomic: "1",
            operations: [
              { operation: "etherscan.account.balance", complexity_weight: 2, amount_atomic: "2" },
            ],
          }),
        });
      if (path.endsWith("/account"))
        return route.fulfill({
          json: envelope({
            user_id: "private-user",
            network: "eip155:1",
            asset,
            available_atomic: "25000000",
            reserved_atomic: "12000",
            total_debit_atomic: "368000",
            total_credit_atomic: "25380000",
            created_at: timestamp,
            updated_at: timestamp,
          }),
        });
      if (path.endsWith("/topup-intents"))
        return route.fulfill({
          json: envelope([
            { ...common, state: "credited", expires_at: timestamp, credited_at: timestamp },
          ]),
        });
      if (path.endsWith("/payments"))
        return route.fulfill({
          json: envelope([
            {
              ...common,
              state: "settled",
              purpose: "account_topup",
              asset_transfer_method: "eip3009",
              payment_flow: "authorization",
              operation: "account_topup",
              method: "POST",
            },
          ]),
        });
      return route.abort();
    });
    await page.goto("/account?tab=billing");
    await expect(page.locator(".billing-topup-form")).toBeVisible();
    await expect(page.locator(".identity-card .account-address")).toHaveAttribute(
      "href",
      `/address/${address}`,
    );
    const checkView = async (name: string) => {
      const scan = await new AxeBuilder({ page })
        .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
        .analyze();
      expect(scan.violations, name).toEqual([]);
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
        name,
      ).toBe(true);
      // Full-page captures must start at the top so sticky navigation does not
      // obscure the account content after keyboard navigation or an axe scan.
      await page.evaluate(() => {
        if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
        window.scrollTo({ top: 0, behavior: "instant" });
      });
      await page.screenshot({
        path: testInfo.outputPath(`${name}.png`),
        fullPage: true,
        animations: "disabled",
      });
    };
    await checkView("payment");
    await expect(page.locator(".billing-balance-values dd").first()).toHaveText("25");
    await expect(page.getByRole("cell", { name: "0.000002", exact: true })).toBeVisible();
    const amountInput = page.locator("#billing-topup-amount");
    await amountInput.fill("1.25");
    const historyButton = page.locator(".billing-view-switch button").nth(1);
    await historyButton.focus();
    await page.keyboard.press("Enter");
    await expect(page.locator(".billing-records li")).toHaveCount(1);
    await expect(page.locator(".billing-topup-form")).toHaveCount(0);
    const details = page.locator(".billing-record-details summary");
    await details.focus();
    await page.keyboard.press("Enter");
    await expect(page.locator(".billing-record-details[open]")).toBeVisible();
    await checkView("topup-history");
    await page.locator(".billing-history-controls select").selectOption("payments");
    await expect(page.locator(".billing-records li")).toHaveCount(1);
    await checkView("payment-history");
    await page.locator(".billing-view-switch button").first().click();
    await expect(amountInput).toHaveValue("1.25");
    await page.goto("/account");
    await expect(page.locator(".profile-details .account-address")).toHaveAttribute(
      "href",
      `/address/${address}`,
    );
    await page.locator(".profile-details .account-address").click();
    await expect(page).toHaveURL(new RegExp(`/address/${address}`));
    expect(writes).toEqual([]);
    expect(external).toEqual([]);
    expect(errors).toEqual([]);
  });
}
