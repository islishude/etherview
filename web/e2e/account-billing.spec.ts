import { testBillingConfig } from "../src/billing/testFixtures";
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
    await page.addInitScript((account) => {
      let chainID = "0x1";
      let rejectSwitch = true;
      const provider = {
        async request({ method, params }: { method: string; params?: { chainId: string }[] }) {
          if (method === "eth_accounts" || method === "eth_requestAccounts") return [account];
          if (method === "eth_chainId") return chainID;
          if (method === "wallet_switchEthereumChain") {
            if (rejectSwitch) {
              rejectSwitch = false;
              throw { code: 4001, message: "private provider details" };
            }
            chainID = params![0]!.chainId;
            return null;
          }
          if (method === "eth_signTypedData_v4") {
            await new Promise<void>((resolve) =>
              window.addEventListener("release-topup-signature", () => resolve(), { once: true }),
            );
            return `0x${"11".repeat(64)}1b`;
          }
          if (method === "eth_call") return `0x${25000000n.toString(16).padStart(64, "0")}`;
          throw new Error(`Unexpected authorization: ${method}`);
        },
        on() {},
        removeListener() {},
      };
      const detail = {
        info: {
          uuid: "00000000-0000-4000-8000-000000000065",
          name: "Billing Test Wallet",
          icon: "data:image/png;base64,",
          rdns: "org.etherview.billing-test",
        },
        provider,
      };
      window.addEventListener("eip6963:requestProvider", () =>
        window.dispatchEvent(new CustomEvent("eip6963:announceProvider", { detail })),
      );
    }, address);
    const walletChain = () =>
      page.evaluate(
        () =>
          new Promise<string>((resolve) => {
            window.addEventListener(
              "eip6963:announceProvider",
              ((event: CustomEvent) => {
                void event.detail.provider.request({ method: "eth_chainId" }).then(resolve);
              }) as EventListener,
              { once: true },
            );
            window.dispatchEvent(new Event("eip6963:requestProvider"));
          }),
      );
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
        credit_amount_atomic: largeAmount,
        payment_option: testBillingConfig({ network: "eip155:1", asset, recipient: address })
          .payment_chains[0]!.tokens[0],
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
          json: envelope(
            testBillingConfig({
              api_billing_enabled: true,
              x402_topups_enabled: true,
              x402_version: 2,
              scheme: "exact",
              network: "eip155:31337",
              asset,
              recipient: address,
              asset_transfer_methods: ["eip3009", "permit2"],
              minimum_topup_amount_atomic: "1",
              maximum_topup_amount_atomic: largeAmount,
              asset_decimals: 6,
              base_price_atomic: "1",
              operations: [
                {
                  operation: "etherview.account.balance",
                  complexity_weight: 2,
                  amount_atomic: "2",
                },
              ],
            }),
          ),
        });
      if (path.endsWith("/account"))
        return route.fulfill({
          json: envelope({
            user_id: "private-user",

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
    await expect(page.locator(".billing-connect")).toContainText(
      appearance.language === "zh" ? "请先连接" : "Connect the wallet",
    );
    await page.locator(".billing-connect button").click();
    await expect(page.locator(".billing-connect")).toHaveCount(0);
    await page.locator(".billing-payment h2").click();
    await expect(page.locator(".billing-wallet-balance")).not.toContainText("25");
    await checkView("payment-before-balance");
    await page
      .getByRole("button", { name: appearance.language === "zh" ? "刷新余额" : "Refresh balance" })
      .click();
    await expect(page.getByRole("alert")).toContainText(
      appearance.language === "zh" ? "已取消网络切换" : "Network switch cancelled",
    );
    await expect(page.getByRole("alert")).not.toContainText("private provider details");
    await expect(page.locator("#billing-payment-network")).toHaveAttribute("aria-invalid", "true");
    await expect(page.locator("#billing-network-error")).toContainText(
      appearance.language === "zh" ? "已取消网络切换" : "Network switch cancelled",
    );
    await expect(page.locator(".billing-wallet-balance")).toContainText(
      appearance.language === "zh" ? "不可用" : "Unavailable",
    );
    await expect(page.locator(".billing-wallet-balance")).not.toContainText(
      appearance.language === "zh" ? "无法检查" : "Unable to check",
    );
    expect(writes).toEqual([]);
    await checkView("payment-switch-rejected");
    await page
      .getByRole("button", {
        name: appearance.language === "zh" ? "刷新余额" : "Refresh balance",
      })
      .click();
    await expect(page.locator("#billing-network-error")).toHaveCount(0);
    await expect(page.locator(".billing-wallet-balance")).toContainText("25");
    await expect(
      page.getByRole("button", {
        name: appearance.language === "zh" ? "刷新余额" : "Refresh balance",
      }),
    ).toBeEnabled();
    expect(await walletChain()).toBe("0x7a69");
    await checkView("payment");
    await expect(page.locator(".billing-balance-values dd").first()).toHaveText("25");
    await expect(page.locator(".billing-prices")).toHaveCount(0);
    await page
      .getByRole("tab")
      .filter({ hasText: appearance.language === "zh" ? "查询价格" : "Query prices" })
      .click();
    await expect(page.getByRole("cell", { name: "0.000002", exact: true })).toBeVisible();
    await checkView("query-prices");
    await page.keyboard.press("Home");
    await expect.poll(walletChain).toBe("0x1");
    await page.locator(".billing-view-switch button").first().click();
    const amountInput = page.locator("#billing-topup-amount");
    await amountInput.fill("1.25");
    await expect(
      page.getByRole("button", {
        name: appearance.language === "zh" ? "充值并授权" : "Top up & authorize",
      }),
    ).toBeEnabled();
    await expect(page.locator(".billing-step.success")).toHaveCount(0);
    expect(writes).toEqual([]);
    const widths = await page.evaluate(() =>
      [".billing-steps", ".billing-topup-form"].map(
        (selector) => document.querySelector(selector)!.getBoundingClientRect().width,
      ),
    );
    expect(Math.abs(widths[0]! - widths[1]!)).toBeLessThan(1);
    await checkView("topup-ready");
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
    expect(writes).toEqual([]);
    await expect(page.locator("#billing-payment-network")).toContainText(
      "Test Network (eip155:31337)",
    );
    await expect(page.locator("#billing-payment-token option:checked")).toContainText(asset);
    await expect(page.locator(".billing-payment-destination")).toHaveCount(0);
    const paymentConfig = testBillingConfig({
      network: "eip155:31337",
      asset,
      recipient: address,
      asset_decimals: 6,
      minimum_topup_amount_atomic: "1",
      maximum_topup_amount_atomic: largeAmount,
    });
    const paymentIntent = {
      id: "00000000-0000-7000-8000-000000000067",
      user_id: "private-user",
      amount_atomic: "1250000",
      credit_amount_atomic: "1250000",
      network: paymentConfig.network,
      asset,
      recipient: address,
      payer: address,
      state: "open",
      created_at: timestamp,
      updated_at: timestamp,
      expires_at: "2099-01-01T00:00:00Z",
      payment_option: paymentConfig.payment_chains[0]!.tokens[0],
    };
    let releaseCreation!: () => void;
    const creationGate = new Promise<void>((resolve) => {
      releaseCreation = resolve;
    });
    await page.route("**/api/v1/billing/topup-intents", async (route) => {
      if (route.request().method() !== "POST") return route.fallback();
      await creationGate;
      return route.fulfill({
        json: envelope({
          ...paymentIntent,
          id: failPayment ? "00000000-0000-7000-8000-000000000068" : paymentIntent.id,
        }),
      });
    });
    let failPayment = false;
    let submissionProgress = "submitting";
    let releasePayment!: () => void;
    const paymentGate = new Promise<void>((resolve) => {
      releasePayment = resolve;
    });
    await page.route("**/api/v1/billing/topup-intents/*/pay", async (route) => {
      if (failPayment)
        return route.fulfill({ status: 500, json: { error: { code: "internal_error" } } });
      if (!route.request().headers()["payment-signature"])
        return route.fulfill({
          status: 402,
          headers: {
            "PAYMENT-REQUIRED": Buffer.from(
              JSON.stringify({
                x402Version: 2,
                resource: { url: route.request().url() },
                accepts: [
                  {
                    scheme: "exact",
                    network: paymentConfig.network,
                    asset,
                    payTo: address,
                    amount: "1250000",
                    maxTimeoutSeconds: 60,
                    extra: {
                      assetTransferMethod: "eip3009",
                      name: paymentConfig.asset_eip712_name,
                      version: paymentConfig.asset_eip712_version,
                    },
                  },
                ],
              }),
            ).toString("base64"),
          },
        });
      await paymentGate;
      return route.fulfill({
        json: envelope({
          intent: { ...paymentIntent, state: "credited", transaction_hash: hash },
          account: {},
        }),
      });
    });
    await page.route(`**/api/v1/billing/topup-intents/${paymentIntent.id}`, (route) =>
      route.fulfill({
        json: envelope({
          ...paymentIntent,
          state: "settling",
          submission_progress: submissionProgress,
          transaction_hash: submissionProgress === "confirming" ? hash : null,
        }),
      }),
    );
    const confirm = page.getByRole("button", {
      name: appearance.language === "zh" ? "充值并授权" : "Top up & authorize",
    });
    const continueButton = page.getByRole("button", {
      name: appearance.language === "zh" ? "继续充值" : "Continue topping up",
    });
    await confirm.click();
    await expect(page.locator(".billing-topup-status.processing")).toBeVisible();
    await expect(page.locator(".billing-steps, .billing-topup-form")).toHaveCount(0);
    await checkView("topup-processing");
    releaseCreation();
    await expect(page.locator(".billing-topup-steps [aria-current=step]")).toContainText(
      appearance.language === "zh" ? "等待用户签名" : "Waiting for your signature",
    );
    await checkView("topup-signing");
    await page.evaluate(() => window.dispatchEvent(new Event("release-topup-signature")));
    await expect(page.locator(".billing-topup-steps [aria-current=step]")).toContainText(
      appearance.language === "zh" ? "等待协调人提交交易" : "Waiting for the facilitator",
    );
    submissionProgress = "confirming";
    await expect(page.locator(".billing-topup-steps [aria-current=step]")).toContainText(
      appearance.language === "zh" ? "等待交易确认" : "Waiting for transaction confirmation",
    );
    await checkView("topup-confirming");
    releasePayment();
    await expect(page.locator(".billing-topup-status.success")).toBeVisible();
    await checkView("topup-success");
    await page
      .getByRole("button", {
        name: appearance.language === "zh" ? "查看充值历史" : "View top-up history",
      })
      .click();
    await expect(page.locator(".billing-history-controls select")).toHaveValue("topups");
    await page.locator(".billing-view-switch button").first().click();
    await expect(page.locator(".billing-topup-status.success")).toBeVisible();
    await continueButton.click();
    await expect(amountInput).toBeFocused();
    await expect(amountInput).toHaveValue("");
    failPayment = true;
    await amountInput.fill("1.25");
    await confirm.click();
    await expect(page.locator(".billing-topup-status.failure")).toBeVisible();
    await expect(page.locator(".billing-topup-form")).toHaveCount(0);
    await checkView("topup-failure");
    await continueButton.click();
    await expect(amountInput).toBeFocused();
    expect(writes).toHaveLength(5);
    await page.goto("/account");
    await expect(page.locator(".profile-details .account-address")).toHaveAttribute(
      "href",
      `/address/${address}`,
    );
    await page.locator(".profile-details .account-address").click();
    await expect(page).toHaveURL(new RegExp(`/address/${address}`));
    expect(external).toEqual([]);
    expect(errors).toEqual([]);
  });
}
