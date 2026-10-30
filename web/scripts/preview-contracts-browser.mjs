import assert from "node:assert/strict";
import { createHash, X509Certificate } from "node:crypto";
import fs from "node:fs";
import { chromium, expect } from "@playwright/test";
const [binding, certificate, reportPath, artifacts] = process.argv.slice(2);
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
    `--host-resolver-rules=MAP *.etherview.localhost ${binding}, EXCLUDE localhost`,
    `--ignore-certificate-errors-spki-list=${fingerprint}`,
    "--no-proxy-server",
  ],
});
try {
  const page = await browser.newPage();
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await new Promise((resolve) => setTimeout(resolve, 4000));
  await page.goto("https://app.etherview.localhost/user-operations");
  await expect(page.getByRole("heading", { name: "UserOperations", exact: true })).toBeVisible();
  await expect(page.locator(`a[href="/user-op/${report.userOperationHash}"]`).first()).toBeVisible({
    timeout: 30000,
  });
  await new Promise((resolve) => setTimeout(resolve, 4000));
  await page.goto(`https://app.etherview.localhost/user-op/${report.userOperationHash}`);
  await expect(
    page.getByRole("heading", { name: "UserOperation", exact: true, level: 1 }),
  ).toBeVisible();
  for (const route of [
    `/tx/${report.transactionHash}?tab=user-operations`,
    `/address/${report.smartAccount}?tab=user-operations`,
  ]) {
    await new Promise((resolve) => setTimeout(resolve, 4000));
    await page.goto(`https://app.etherview.localhost${route}`);
    await expect(
      page.locator(`a[href="/user-op/${report.userOperationHash}"]`).first(),
    ).toBeVisible({ timeout: 30000 });
  }
  await new Promise((resolve) => setTimeout(resolve, 4000));
  await page.goto(`https://app.etherview.localhost/address/${report.nameOwner}`);
  // New publications and invalidations no longer trigger background name reads.
  // Exercise the same explicit refresh a user needs after a snapshot expires.
  await expect(async () => {
    await page.getByRole("button", { name: "Refresh", exact: true }).click();
    await expect(page.getByRole("link", { name: report.name, exact: true }).first()).toBeVisible({
      timeout: 2000,
    });
  }).toPass({ timeout: 30000, intervals: [1000, 2000, 4000] });
  assert.deepEqual(errors, []);
  await page.screenshot({ path: `${artifacts}/contracts-${report.phase}.png`, fullPage: true });
} finally {
  await browser.close();
}
