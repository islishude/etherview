import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

const address = "0x1212121212121212121212121212121212121212";
const proxyURL = `**/api/v1/contracts/${address}/proxy`;

test("Code shows loading while proxy classification is delayed and then the unverified state", async ({
  page,
}, testInfo) => {
  const response = Promise.withResolvers<void>();
  await page.route(proxyURL, async (route) => {
    await response.promise;
    await route.continue();
  });
  try {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(`/address/${address}#code`);
    const panel = page.locator("#contract-panel-code");
    await expect(panel.getByRole("status")).toHaveText("Loading indexed data…");
    const spinner = panel.locator(".query-loading-spinner");
    await expect(spinner).toBeVisible();
    await expect(spinner).toHaveCSS("animation-name", "query-loading-spin");
    await page.emulateMedia({ reducedMotion: "reduce" });
    await expect(spinner).toHaveCSS("animation-iteration-count", "1");
    await page.emulateMedia({ reducedMotion: "no-preference" });
    await panel.screenshot({ path: testInfo.outputPath("code-loading.png") });
    await expect(panel.getByRole("link", { name: "Submit a verification request" })).toHaveCount(0);
    await page.getByRole("button", { name: "切换到中文" }).click();
    await expect(panel.getByRole("status")).toHaveText("正在加载索引数据…");
    response.resolve();
    await expect(panel.getByRole("link", { name: "提交合约验证请求" })).toBeVisible();
    await expect(panel.getByText("正在加载索引数据…")).toHaveCount(0);
    expect(
      (
        await new AxeBuilder({ page })
          .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
          .analyze()
      ).violations,
    ).toEqual([]);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      ),
    ).toBeLessThanOrEqual(1);
  } finally {
    response.resolve();
  }
});

for (const status of ["unavailable", "failed"] as const) {
  test(`Code explains a ${status} proxy result without showing unchecked artifacts`, async ({
    page,
  }) => {
    await page.route(proxyURL, async (route) => {
      await route.fulfill({
        status: 200,
        json: {
          data: {
            address,
            status,
            snapshot: { chain_id: "1", block_number: "42", block_hash: `0x${"11".repeat(32)}` },
            evidence: [],
          },
          meta: { chain_id: "1", request_id: "code-state-e2e" },
        },
      });
    });
    await page.goto(`/address/${address}#code`);
    const panel = page.getByRole("tabpanel", { name: "Code", exact: true });
    await expect(
      panel.getByText(status === "failed" ? "Failed" : "Unavailable", { exact: true }),
    ).toBeVisible();
    await expect(panel.getByRole("heading", { name: "Verified artifact" })).toHaveCount(0);
    await expect(panel.getByRole("link", { name: "Submit a verification request" })).toHaveCount(0);
  });
}

test("Code refreshes manually after proxy publication", async ({ page }) => {
  const verifiedAddress = "0x1111111111111111111111111111111111111111";
  // A publication must not start a classification recovery poll.
  await page.addInitScript(() => {
    window.EventSource = class {
      static CLOSED = 2;
      readyState = 1;
      addEventListener() {}
      close() {}
    } as unknown as typeof EventSource;
  });
  let published = false;
  let requests = 0;
  await page.route(`**/api/v1/contracts/${verifiedAddress}/proxy`, async (route) => {
    requests++;
    await route.fulfill({
      status: 200,
      json: {
        data: {
          address: verifiedAddress,
          status: published ? "not_detected" : "unavailable",
          snapshot: { chain_id: "1", block_number: "42", block_hash: `0x${"11".repeat(32)}` },
          evidence: [],
        },
        meta: { chain_id: "1", request_id: "code-recovery-e2e" },
      },
    });
  });
  await page.goto(`/address/${verifiedAddress}#code`);
  const panel = page.getByRole("tabpanel", { name: "Code", exact: true });
  await expect(panel.getByText("Unavailable", { exact: true })).toBeVisible();
  await expect(panel.getByText("Loading indexed data…")).toHaveCount(0);
  await expect(panel.getByRole("heading", { name: "Verified artifact" })).toHaveCount(0);
  await page.waitForTimeout(1200);
  expect(requests).toBe(1);
  published = true;
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(panel.getByRole("heading", { name: "Verified artifact" })).toBeVisible();
  await expect(panel.getByText("Loading indexed data…")).toHaveCount(0);
  const completedRequests = requests;
  await page.waitForTimeout(1200);
  expect(requests).toBe(completedRequests);
});
