import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

const address = `0x${"12".repeat(20)}`;
const jobID = "123e4567-e89b-42d3-a456-426614174000";
const externalID = "123e4567-e89b-42d3-a456-426614174001";
const apiKey = "ev_e2e_sourcify";

for (const language of ["en", "zh"] as const) {
  test(`Sourcify publication and external results in ${language} at 390px`, async ({
    page,
  }, testInfo) => {
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    page.on("console", (message) => {
      if (message.type() === "error") errors.push(message.text());
    });
    const posts: { path: string; body: unknown }[] = [];
    let kind = "sourcify";
    let polls = 0;
    const envelope = (data: unknown) => ({
      data,
      meta: { request_id: "sourcify-e2e", chain_id: "1" },
    });
    await page.route("**/api/v1/config", async (route) => {
      const response = await route.fetch();
      const payload = await response.json();
      payload.data.features.verification = true;
      payload.data.features.sourcify = true;
      await route.fulfill({ response, json: payload });
    });
    await page.route("**/api/v1/verifier/compilers?language=solidity", (route) =>
      route.fulfill({ json: envelope({ language: "solidity", versions: ["0.8.30"] }) }),
    );
    await page.route("**/api/v1/verifier/sourcify**", async (route) => {
      expect(route.request().headers()["x-api-key"]).toBe(apiKey);
      const path = new URL(route.request().url()).pathname;
      posts.push({ path, body: route.request().postDataJSON() });
      kind = path.endsWith("from-etherscan") ? "sourcify_from_etherscan" : "sourcify";
      polls = 0;
      await route.fulfill({ status: 202, json: envelope({ id: jobID, kind, status: "queued" }) });
    });
    await page.route(`**/api/v1/verifier/jobs/${jobID}`, async (route) => {
      expect(route.request().headers()["x-api-key"]).toBe(apiKey);
      polls++;
      await route.fulfill({
        json: envelope({
          id: jobID,
          kind,
          status: polls === 1 ? "running" : "succeeded",
          ...(polls > 1
            ? { outcome: { kind: "sourcify_success", verification_id: externalID } }
            : {}),
          updated_at: "2026-10-25T00:00:00Z",
        }),
      });
    });
    await page.goto(`/verify?address=${address}`);
    if (language === "zh")
      await page.getByRole("button", { name: "切换到中文", exact: true }).click();
    const zh = language === "zh";
    await page.getByLabel(zh ? "验证服务" : "Verification service").selectOption("sourcify");
    await expect(page.getByLabel(zh ? "地址" : "Address", { exact: true })).toHaveValue(address);
    await page.getByLabel(zh ? /^API Key/ : /^API key/).fill(apiKey);
    const files = {
      "A.sol": "contract A {}",
      "metadata.json": '{"compiler":{},"settings":{},"output":{}}',
    };
    await page
      .getByLabel(zh ? "选择源码文件和 metadata.json" : "Select source files and metadata.json")
      .setInputFiles(
        Object.entries(files).map(([name, content]) => ({
          name,
          mimeType: "text/plain",
          buffer: Buffer.from(content),
        })),
      );
    const sourceSubmit = page.getByRole("button", {
      name: zh ? "向 Sourcify 发布源码" : "Publish sources to Sourcify",
      exact: true,
    });
    await expect(sourceSubmit).toBeDisabled();
    await expect(
      page.getByText(zh ? /提交会将源码和 metadata/ : /Submitting sends the source files/),
    ).toBeVisible();
    await page.getByRole("checkbox").check();
    await sourceSubmit.click();
    await expect(
      page.getByText(
        zh ? "Sourcify 验证成功（外部结果）" : "Sourcify verification succeeded (external result)",
      ),
    ).toBeVisible();
    await expect(page.getByText(externalID, { exact: true })).toBeVisible();
    await expect(
      page.getByText(zh ? "运行时代码匹配" : "Runtime match", { exact: true }),
    ).toHaveCount(0);
    expect(posts).toEqual([
      { path: "/api/v1/verifier/sourcify", body: { chain_id: "1", address, files } },
    ]);
    await page.setViewportSize({ width: 390, height: 844 });
    expect(
      (
        await new AxeBuilder({ page })
          .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
          .analyze()
      ).violations,
    ).toEqual([]);
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true);
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.screenshot({
      path: testInfo.outputPath(`sourcify-${language}.png`),
      fullPage: true,
    });

    await page
      .getByLabel(zh ? "Sourcify 提交方式" : "Sourcify submission method")
      .selectOption("etherscan");
    const importSubmit = page.getByRole("button", {
      name: zh
        ? "从 Etherscan 导入并发布至 Sourcify"
        : "Import from Etherscan and publish to Sourcify",
      exact: true,
    });
    await expect(importSubmit).toBeDisabled();
    await page.getByRole("checkbox").check();
    await importSubmit.click();
    await expect.poll(() => posts.length).toBe(2);
    expect(posts[1]).toEqual({
      path: "/api/v1/verifier/sourcify/from-etherscan",
      body: { chain_id: "1", address },
    });
    await expect(page.getByText("sourcify_from_etherscan", { exact: true })).toBeVisible();
    await expect(
      page.getByText(
        zh ? "Sourcify 验证成功（外部结果）" : "Sourcify verification succeeded (external result)",
      ),
    ).toBeVisible();
    expect(
      (
        await new AxeBuilder({ page })
          .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
          .analyze()
      ).violations,
    ).toEqual([]);
    expect(
      await page.evaluate(() => JSON.stringify(localStorage) + JSON.stringify(sessionStorage)),
    ).not.toContain(apiKey);
    expect(page.url()).not.toContain(apiKey);
    expect(errors).toEqual([]);
  });
}
