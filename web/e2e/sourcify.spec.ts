import { installVerificationSession, verificationCSRF } from "./verification-session";
import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

const address = `0x${"12".repeat(20)}`;
const jobID = "123e4567-e89b-42d3-a456-426614174000";
const externalID = "123e4567-e89b-42d3-a456-426614174001";
const apiKey = "ev_e2e_sourcify";
const etherscanKey = "etherscan_e2e_private";

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
    await page.route("**/api/v1/verification-jobs/sourcify**", async (route) => {
      expect(route.request().headers()["x-api-key"]).toBeUndefined();
      if (route.request().method() === "POST")
        expect(route.request().headers()["x-csrf-token"]).toBe(verificationCSRF);
      const path = new URL(route.request().url()).pathname;
      posts.push({ path, body: route.request().postDataJSON() });
      kind = path.endsWith("from-etherscan") ? "sourcify_from_etherscan" : "sourcify";
      polls = 0;
      await route.fulfill({ status: 202, json: envelope({ id: jobID, kind, status: "queued" }) });
    });
    await page.route(`**/api/v1/verification-jobs/${jobID}`, async (route) => {
      expect(route.request().headers()["x-api-key"]).toBeUndefined();
      if (route.request().method() === "POST")
        expect(route.request().headers()["x-csrf-token"]).toBe(verificationCSRF);
      polls++;
      await route.fulfill({
        json: envelope({
          address,
          chain_id: "1",
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
    await installVerificationSession(page);
    await page.goto(`/verify?address=${address}`);
    if (language === "zh")
      await page.getByRole("button", { name: "切换到中文", exact: true }).click();
    const zh = language === "zh";
    await page
      .getByRole("radio", { name: zh ? /Sourcify 源码库/ : /Sourcify source library/ })
      .check();
    await page
      .getByRole("button", { name: zh ? "发布至 Sourcify" : "Publish to Sourcify", exact: true })
      .first()
      .click();
    await expect(page.getByLabel(zh ? "地址" : "Address", { exact: true })).toHaveValue(address);
    const files = {
      "A.sol": "contract A {}",
      "metadata.json": '{"compiler":{},"settings":{},"output":{}}',
    };
    await page.getByLabel(zh ? "源码文件" : "Source files", { exact: true }).setInputFiles({
      name: "bad.txt",
      mimeType: "text/plain",
      buffer: Buffer.from("contract A {}"),
    });
    await expect(page.getByRole("alert")).toContainText(zh ? "小写后缀" : "lowercase extensions");
    expect(posts).toHaveLength(0);
    const fileChooser = page.waitForEvent("filechooser");
    await page.getByLabel(zh ? "源码文件" : "Source files", { exact: true }).focus();
    await page.keyboard.press("Space");
    await (
      await fileChooser
    ).setFiles({
      name: "A.sol",
      mimeType: "text/plain",
      buffer: Buffer.from(files["A.sol"]),
    });
    await page.getByLabel("metadata.json", { exact: true }).setInputFiles({
      name: "metadata.json",
      mimeType: "application/json",
      buffer: Buffer.from(files["metadata.json"]),
    });
    const sourceSubmit = page.getByRole("button", {
      name: zh ? "向 Sourcify 发布源码" : "Publish sources to Sourcify",
      exact: true,
    });
    await expect(sourceSubmit).toBeDisabled();
    await expect(
      page.getByText(zh ? /提交会将源码和 metadata/ : /Submitting sends the source files/),
    ).toBeVisible();
    await page.getByRole("checkbox").check();
    await page
      .getByLabel(zh ? "源码路径：A.sol" : "Source path: A.sol", { exact: true })
      .fill("src/A.sol");
    await expect(page.getByRole("checkbox")).not.toBeChecked();
    await page
      .getByLabel(zh ? "源码路径：A.sol" : "Source path: A.sol", { exact: true })
      .fill("A.sol");
    await page.getByRole("checkbox").check();
    await sourceSubmit.click();
    await expect(
      page.getByText(
        zh ? "Sourcify 验证成功（外部结果）" : "Sourcify verification succeeded (external result)",
      ),
    ).toBeVisible();
    await page.getByText(zh ? "技术详情" : "Technical details", { exact: true }).click();
    await expect(page.getByText(externalID, { exact: true })).toBeVisible();
    await expect(page.getByText(zh ? "运行时匹配" : "Runtime match", { exact: true })).toHaveCount(
      0,
    );
    expect(posts).toEqual([
      { path: "/api/v1/verification-jobs/sourcify", body: { chain_id: "1", address, files } },
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

    await page.getByRole("button", { name: zh ? "返回修改" : "Back to form", exact: true }).click();
    for (const colorScheme of ["light", "dark"] as const) {
      await page.emulateMedia({ colorScheme });
      await expect(page.locator("html")).toHaveAttribute("data-theme", colorScheme);
      for (const width of [390, 1440]) {
        await page.setViewportSize({ width, height: 900 });
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
        expect(
          (
            await new AxeBuilder({ page })
              .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
              .analyze()
          ).violations,
        ).toEqual([]);
        await page.locator(".sourcify-uploads").screenshot({
          path: testInfo.outputPath(`uploads-${language}-${width}-${colorScheme}.png`),
        });
      }
    }
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
    const etherscanInput = page.getByLabel(/^Etherscan API Key/);
    await expect(etherscanInput).toHaveAttribute("type", "password");
    await etherscanInput.fill(etherscanKey);
    await expect(page.getByRole("checkbox")).not.toBeChecked();
    await page.getByRole("checkbox").check();
    await importSubmit.click();
    await expect.poll(() => posts.length).toBe(2);
    expect(posts[1]).toEqual({
      path: "/api/v1/verification-jobs/sourcify-from-etherscan",
      body: { chain_id: "1", address, etherscan_api_key: etherscanKey },
    });
    await expect(etherscanInput).toHaveValue("");
    await page.getByText(zh ? "技术详情" : "Technical details", { exact: true }).click();
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
    expect(
      await page.evaluate(() => JSON.stringify(localStorage) + JSON.stringify(sessionStorage)),
    ).not.toContain(etherscanKey);
    expect(page.url()).not.toContain(etherscanKey);
    expect(errors).toEqual([]);
  });
}

for (const language of ["en", "zh"] as const) {
  test(`Sourcify direct local import and service cards in ${language}`, async ({
    page,
  }, testInfo) => {
    const zh = language === "zh";
    const posts: string[] = [];
    const envelope = (data: unknown) => ({
      data,
      meta: { chain_id: "1", request_id: "import-e2e" },
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
    await page.route("**/api/v1/verification-jobs/sourcify**", (route) => {
      posts.push("unexpected upload");
      return route.fulfill({ status: 500, json: {} });
    });
    await page.route("**/api/v1/verification-jobs/sourcify-import", (route) => {
      expect(route.request().method()).toBe("POST");
      expect(route.request().headers()["x-api-key"]).toBeUndefined();
      if (route.request().method() === "POST")
        expect(route.request().headers()["x-csrf-token"]).toBe(verificationCSRF);
      expect(route.request().postDataJSON()).toEqual({ address, chain_id: "11155111" });
      posts.push("local import");
      return route.fulfill({
        status: 202,
        json: envelope({ id: jobID, kind: "address", status: "queued" }),
      });
    });
    await page.route(`**/api/v1/verification-jobs/${jobID}`, (route) =>
      route.fulfill({
        json: envelope({
          address,
          chain_id: "1",
          id: jobID,
          kind: "address",
          status: "succeeded",
          outcome: { kind: "verification_success", runtime_match: { match_type: "full" } },
        }),
      }),
    );
    await installVerificationSession(page);
    await page.goto(`/verify?address=${address}`);
    if (zh) await page.getByRole("button", { name: "切换到中文", exact: true }).click();
    const provider = page.getByRole("radio", {
      name: zh ? /Sourcify 源码库/ : /Sourcify source library/,
    });
    await provider.focus();
    await page.keyboard.press("Space");
    await expect(provider).toBeChecked();
    await expect(
      page.getByRole("button", {
        name: zh ? "导入 Etherview" : "Import into Etherview",
        exact: true,
      }),
    ).toHaveAttribute("aria-pressed", "true");
    await expect(page.getByLabel(zh ? /^Sourcify 文件/ : /^Sourcify files/)).toHaveCount(0);
    await expect(page.getByLabel(/^Etherscan API Key/)).toHaveCount(0);
    await expect(
      page.getByLabel(zh ? "源码链 ID" : "Source Chain ID", { exact: true }),
    ).toHaveValue("1");
    await page.getByLabel(zh ? "源码链 ID" : "Source Chain ID", { exact: true }).fill("11155111");
    for (const colorScheme of ["light", "dark"] as const) {
      await page.emulateMedia({ colorScheme });
      await expect(page.locator("html")).toHaveAttribute("data-theme", colorScheme);
      for (const width of [390, 1440]) {
        await page.setViewportSize({ width, height: 900 });
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
        expect(
          (
            await new AxeBuilder({ page })
              .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
              .analyze()
          ).violations,
        ).toEqual([]);
        await page.screenshot({
          path: testInfo.outputPath(`import-${language}-${width}-${colorScheme}.png`),
          fullPage: true,
        });
      }
    }
    await page
      .getByRole("button", {
        name: zh ? "从 Sourcify 导入 Etherview" : "Import from Sourcify into Etherview",
        exact: true,
      })
      .click();
    await page.getByText(zh ? "技术详情" : "Technical details", { exact: true }).click();
    await expect(page.getByText("verification_success", { exact: true })).toBeVisible();
    await expect(
      page.getByText(zh ? "运行时匹配" : "Runtime match", { exact: true }),
    ).toBeVisible();
    expect(posts).toEqual(["local import"]);
    await expect(page.getByText(externalID, { exact: true })).toHaveCount(0);
    await page.getByRole("button", { name: zh ? "返回修改" : "Back to form", exact: true }).click();
    await page
      .getByRole("radio", { name: zh ? /Etherview 本地验证/ : /Etherview local verification/ })
      .check();
    const select = page.getByRole("combobox", { name: zh ? "语言" : "Language", exact: true });
    await expect(select).toHaveCSS("appearance", "none");
    const control = await select.evaluate((element) => {
      const style = getComputedStyle(element);
      const icon = element.parentElement!.querySelector("svg.ui-icon")!;
      return {
        size: icon.getBoundingClientRect().width,
        right: parseFloat(style.paddingRight),
        height: element.getBoundingClientRect().height,
      };
    });
    expect(control.size).toBe(14);
    expect(control.right).toBeGreaterThan(control.size + 12);
    expect(control.size / control.height).toBeGreaterThan(0.3);
    await page.screenshot({
      path: testInfo.outputPath(`local-controls-${language}.png`),
      fullPage: true,
    });
  });
}
