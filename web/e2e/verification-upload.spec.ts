import { installVerificationSession } from "./verification-session";
import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

for (const language of ["en", "zh"] as const) {
  for (const width of [390, 768, 1440]) {
    test(`Geas form layout in ${language} at ${width}px`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 900 });
      await installVerificationSession(page);
      await page.route("**/api/v1/verifier/compilers?**", (route) =>
        route.fulfill({
          json: {
            data: { language: "geas", versions: ["0.3.3"] },
            meta: { request_id: "geas-layout", chain_id: "1" },
          },
        }),
      );
      await page.goto("/verify");
      if (language === "zh")
        await page.getByRole("button", { name: "切换到中文", exact: true }).click();
      await page.locator("#verification-language").selectOption("geas");
      const name = page.locator("#verification-contract-name");
      const runtime = page.locator("#verification-runtime-entrypoint");
      const upload = page.locator(".verification-form .verification-upload");
      const grid = page.locator(".verification-form .form-grid");
      const assertLayout = async () => {
        const nameBox = (await name.boundingBox())!;
        const runtimeBox = (await runtime.boundingBox())!;
        const uploadBox = (await upload.boundingBox())!;
        const gridBox = (await grid.boundingBox())!;
        expect(Math.abs(nameBox.height - runtimeBox.height)).toBeLessThanOrEqual(1);
        expect(nameBox.height).toBeLessThan(60);
        expect(Math.abs(nameBox.width - runtimeBox.width)).toBeLessThanOrEqual(1);
        expect(uploadBox.y).toBeGreaterThan(nameBox.y + nameBox.height);
        expect(Math.abs(uploadBox.x - gridBox.x)).toBeLessThanOrEqual(1);
        expect(Math.abs(uploadBox.width - gridBox.width)).toBeLessThanOrEqual(1);
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
      };
      await expect(name).toBeVisible();
      await assertLayout();
      await page.locator("#verification-input").setInputFiles({
        name: "main.eas",
        mimeType: "text/plain",
        buffer: Buffer.from("stop"),
      });
      await expect(page.locator(".verification-upload-list")).toContainText("main.eas");
      await assertLayout();
      await page.screenshot({
        path: testInfo.outputPath(`geas-layout-${language}-${width}.png`),
        fullPage: true,
      });
    });
  }
}

for (const language of ["en", "zh"] as const) {
  test(`local verification file uploads in ${language} at 390px`, async ({ page }, testInfo) => {
    const address = `0x${"12".repeat(20)}`;
    const posts: unknown[] = [];
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    const envelope = (data: unknown) => ({
      data,
      meta: { request_id: "upload-browser", chain_id: "1" },
    });
    await page.route("**/api/v1/config", async (route) => {
      const response = await route.fetch();
      const body = await response.json();
      body.data.features.verification = true;
      await route.fulfill({ response, json: body });
    });
    await page.route("**/api/v1/verifier/compilers?**", (route) =>
      route.fulfill({
        json: envelope({ language: "solidity", versions: ["0.8.30"] }),
      }),
    );
    await page.route("**/api/v1/verification-jobs/local", async (route) => {
      posts.push(route.request().postDataJSON());
      await route.fulfill({
        status: 503,
        json: { error: { code: "verification_unavailable", message: "Unavailable" } },
      });
    });
    await installVerificationSession(page);
    await page.goto(`/verify?address=${address}`);
    if (language === "zh")
      await page.getByRole("button", { name: "切换到中文", exact: true }).click();
    const zh = language === "zh";
    const input = page.getByLabel(zh ? "Standard JSON 输入" : "Standard JSON input", {
      exact: true,
    });
    await expect(input).toHaveAttribute("accept", ".json");
    const submit = page.getByRole("button", {
      name: zh ? "提交验证" : "Submit verification",
      exact: true,
    });
    await input.setInputFiles({
      name: "bad.txt",
      mimeType: "application/json",
      buffer: Buffer.from("{}"),
    });
    await expect(page.locator(".verification-form").getByRole("alert")).toContainText(
      zh ? "小写后缀" : "lowercase extensions",
    );
    await submit.click();
    expect(posts).toHaveLength(0);
    const standard = {
      language: "Solidity",
      sources: { "A.sol": { content: "contract A {}" } },
      settings: {},
    };
    const chooser = page.waitForEvent("filechooser");
    await input.focus();
    await page.keyboard.press("Space");
    await (
      await chooser
    ).setFiles({
      name: "input.json",
      mimeType: "application/json",
      buffer: Buffer.from(JSON.stringify(standard)),
    });
    await submit.click();
    await expect.poll(() => posts.length).toBe(1);
    await expect(page.locator(".message-error").getByRole("alert")).toBeVisible();
    expect(posts[0]).toEqual({
      address,
      compiler_version: "0.8.30",
      language: "solidity",
      input_kind: "standard_json",
      input: standard,
    });
    await page
      .getByRole("combobox", { name: zh ? "输入格式" : "Input format", exact: true })
      .selectOption("multipart");
    await expect(
      page.getByRole("button", { name: zh ? "移除 input.json" : "Remove input.json" }),
    ).toHaveCount(0);
    const sources = page.getByLabel(zh ? "源码文件" : "Source files", { exact: true });
    await sources.setInputFiles({
      name: "A.sol",
      mimeType: "text/plain",
      buffer: Buffer.from("contract A {}"),
    });
    const path = page.getByLabel(zh ? "源码路径：A.sol" : "Source path: A.sol", { exact: true });
    await path.fill("src/A.exe");
    await expect(page.locator(".verification-form").getByRole("alert")).toContainText("src/A.exe");
    await submit.click();
    expect(posts).toHaveLength(1);
    await path.fill("src/A.sol");
    await submit.click();
    await expect.poll(() => posts.length).toBe(2);
    expect(posts[1]).toEqual({
      address,
      compiler_version: "0.8.30",
      language: "solidity",
      input_kind: "multipart",
      sources: { "src/A.sol": "contract A {}" },
    });
    await page.setViewportSize({ width: 390, height: 844 });
    expect(
      (
        await new AxeBuilder({ page })
          .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
          .analyze()
      ).violations,
    ).toEqual([]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await page.screenshot({
      path: testInfo.outputPath(`verification-upload-${language}.png`),
      fullPage: true,
    });
    await page
      .getByRole("button", { name: zh ? "移除 src/A.sol" : "Remove src/A.sol", exact: true })
      .click();
    await expect(path).toHaveCount(0);
    expect(errors).toEqual([]);
  });
}
