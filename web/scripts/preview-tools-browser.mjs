import assert from "node:assert/strict";
import { createHash, X509Certificate } from "node:crypto";
import fs from "node:fs";
import { chromium, expect } from "@playwright/test";
const [binding, certificate, reportPath, artifacts, phase] = process.argv.slice(2);
const report = JSON.parse(fs.readFileSync(reportPath));
const fingerprint = createHash("sha256")
  .update(
    new X509Certificate(fs.readFileSync(certificate)).publicKey.export({
      type: "spki",
      format: "der",
    }),
  )
  .digest("base64");
const browser = await chromium.launch({
  channel: process.env.PLAYWRIGHT_USE_BUNDLED === "1" ? undefined : "chrome",
  args: [
    "--host-resolver-rules=MAP *.etherview.localhost " + binding + ", EXCLUDE localhost",
    "--ignore-certificate-errors-spki-list=" + fingerprint,
    "--no-proxy-server",
  ],
});
try {
  for (const mobile of [false, true]) {
    const context = await browser.newContext({
      viewport: mobile ? { width: 390, height: 844 } : { width: 1440, height: 1000 },
    });
    await context.addInitScript(
      (language) => localStorage.setItem("etherview.language", language),
      mobile ? "zh" : "en",
    );
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    for (const scenario of report.scenarios) {
      assert.equal(scenario.status, "api_published", scenario.name);
      const preferred =
        scenario.name === "blob"
          ? scenario.links.find((l) => l.label.endsWith(" blobs"))
          : scenario.name === "aa"
            ? scenario.links.find((l) => l.url.includes("/user-op/"))
            : scenario.name === "contracts"
              ? scenario.links.find((l) => l.label === "verified source")
              : undefined;
      const link =
        preferred ??
        scenario.links.find(
          (l) =>
            l.label === "proxy" ||
            l.label === "authority" ||
            l.label === "ENS" ||
            l.label === "NFT",
        ) ??
        scenario.links.find((l) => l.url.includes("/tx/") || l.url.includes("/user-op/"));
      if (!link) continue;
      const url = new URL(link.url);
      url.port = "";
      if (scenario.name.startsWith("proxy.")) url.hash = "code";
      if (scenario.name === "eip7702") url.search = "?tab=delegation";
      // Respect the same anonymous request budget as a person browsing Preview.
      await new Promise((resolve) => setTimeout(resolve, 4000));
      await page.goto(url.href);
      await expect(page.locator("h1")).toBeVisible({ timeout: 30000 });
      const refresh = page.getByRole("button", { name: /^(Refresh|刷新)$/ });
      await expect(page.locator("main")).toBeVisible();
      await expect(page.locator("html")).toHaveAttribute("lang", mobile ? "zh-CN" : "en");
      // Publications can cancel even the initial read. Ordinary explorer pages
      // require explicit refresh; retry that user action within a bounded window.
      // Keep the anonymous request budget and all scenario assertions intact.
      await expect(async () => {
        if (await refresh.count()) {
          await new Promise((resolve) => setTimeout(resolve, 4000));
          await refresh.first().click();
        }
        const implementation = scenario.checks.find((c) => c.label.startsWith("proxy "))?.expected[
          "data.implementation.address"
        ];
        if (implementation)
          await expect(
            page.locator('a[href^="/address/' + implementation + '" i]').first(),
          ).toBeVisible({ timeout: 2000 });
        if (scenario.name === "ens")
          await expect(page.locator("h1")).toContainText(scenario.entities.name);
        if (scenario.name === "aa")
          await expect(
            page.locator('a[href^="/address/' + scenario.entities.smartAccount + '" i]').first(),
          ).toBeVisible({ timeout: 2000 });
        if (scenario.name === "blob") {
          const blob = scenario.transactions.find((t) => t.blobs?.length)?.blobs[0];
          await expect(page.locator('code[title="' + blob.versionedHash + '"]')).toBeVisible({
            timeout: 2000,
          });
        }
        if (scenario.entities.erc721 && link.label === "NFT")
          await expect(page.getByText(scenario.entities.tokenID, { exact: true })).toBeVisible({
            timeout: 2000,
          });
        if (scenario.name === "eip7702") {
          const cleared = /^0x0{40}$/.test(scenario.entities.currentDelegate);
          const bindingPanel = page.locator('section[aria-labelledby="delegation-binding-title"]');
          await expect(
            bindingPanel.getByText(
              cleared ? (mobile ? "未委托" : "Not delegated") : mobile ? "已委托" : "Delegated",
              { exact: true },
            ),
          ).toBeVisible({ timeout: 2000 });
          if (!cleared)
            await expect(
              bindingPanel.locator(
                'a[href^="/address/' + scenario.entities.currentDelegate + '" i]',
              ),
            ).toBeVisible();
        }
      }).toPass({ timeout: 60000, intervals: [4000] });
      assert.equal(
        await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1),
        false,
        "horizontal page overflow: " + scenario.name,
      );
      await page.screenshot({
        path:
          artifacts +
          "/tools-" +
          phase +
          "-" +
          scenario.name +
          "-" +
          (mobile ? "mobile" : "desktop") +
          ".png",
        fullPage: true,
      });
    }
    assert.deepEqual(errors, []);
    await context.close();
  }
} finally {
  await browser.close();
}
