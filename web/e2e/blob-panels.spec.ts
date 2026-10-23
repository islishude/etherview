import { expect, test, type Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

import { appearanceCases, configureAppearance, collectBrowserErrors } from "./appearance";

const blockHash = `0x${"1".repeat(64)}`;
const transactionHash = `0x${"a".repeat(64)}`;
const versionedHash = `0x01${"bc".repeat(31)}`;
const commitment = `0x${"cd".repeat(48)}`;

async function setupBlobs(page: Page, enabled = true, count = 1) {
  const hashes = Array.from({ length: count }, (_, index) =>
    index === 0 ? versionedHash : `0x01${String(index).repeat(62)}`,
  );
  const reads: string[] = [];
  await page.route("**/api/v1/config", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.data.features.consensus = enabled;
    body.data.features.blobs = enabled;
    await route.fulfill({ response, json: body });
  });
  await page.route(`**/api/v1/transactions/${transactionHash}`, async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    Object.assign(body.data.transaction, {
      type: "3",
      block_hash: blockHash,
      blob_versioned_hashes: hashes,
      blob_base_fee_per_gas: "1000000",
      max_fee_per_blob_gas: "1000000000",
    });
    await route.fulfill({ response, json: body });
  });
  await page.route("**/api/v1/blocks/*/blobs", (route) =>
    route.fulfill({
      json: {
        data: {
          block_hash: blockHash,
          state: "complete",
          items: hashes.map((hash, index) => ({
            index: String(42 + index),
            transaction_hash: transactionHash,
            transaction_index: "0",
            versioned_hash: hash,
            commitment,
            sha256: blockHash,
            size: "131072",
          })),
        },
        meta: { chain_id: "1", request_id: "blob-layout" },
      },
    }),
  );
  await page.route("**/api/v1/blocks/*/blobs/*/content", async (route) => {
    reads.push(new URL(route.request().url()).pathname);
    await route.fulfill({
      contentType: "application/octet-stream",
      headers: { "Content-Disposition": "attachment; filename=blob-9007199254740993-42.bin" },
      body: Buffer.alloc(131072, 7),
    });
  });
  return reads;
}

for (const { width, language, theme } of appearanceCases) {
  test(`blob panels ${width}px ${language} ${theme}`, async ({ page }, testInfo) => {
    await configureAppearance(page, { width, language, theme });
    const reads = await setupBlobs(page, true, 3);
    const errors = collectBrowserErrors(page);
    for (const [kind, url] of [
      ["block", "/blocks/1?tab=blobs"],
      ["transaction", `/tx/${transactionHash}?tab=blob`],
    ]) {
      await page.goto(url!);
      const panel = page.getByRole("tabpanel");
      const details = panel.getByRole("link", {
        name: language === "en" ? "View Blob 42" : "查看 Blob 42",
      });
      await expect(details).toBeVisible();
      await expect(panel.locator(".blob-list-row")).toHaveCount(3);
      await expect(details).toHaveAttribute("href", `/blocks/${blockHash}/blobs/42`);
      await expect(panel.getByRole("button")).toHaveCount(0);
      await expect(panel.getByText(versionedHash, { exact: true })).toHaveCount(0);
      await expect(panel.getByText(commitment, { exact: true })).toHaveCount(0);
      await expect(page.locator("body")).toHaveJSProperty("scrollWidth", width);
      const scan = await new AxeBuilder({ page })
        .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
        .analyze();
      expect(scan.violations).toEqual([]);
      const screenshot = testInfo.outputPath(`${kind}-blobs.png`);
      await page.screenshot({ path: screenshot, fullPage: true });
      await testInfo.attach(`${kind} blobs`, { path: screenshot, contentType: "image/png" });
    }
    await page
      .getByRole("link", { name: language === "en" ? "View Blob 42" : "查看 Blob 42" })
      .click();
    await expect(page).toHaveURL(`/blocks/${blockHash}/blobs/42`);
    await expect(
      page.getByRole("heading", {
        level: 1,
        name: language === "en" ? "Blob details" : "Blob 详情",
        exact: true,
      }),
    ).toBeVisible();
    await expect(page.getByText(versionedHash, { exact: true })).toBeVisible();
    await expect(page.getByText(commitment, { exact: true })).toBeVisible();
    await expect(
      page.getByRole("button", { name: language === "en" ? "Download blob" : "下载 Blob" }),
    ).toBeVisible();
    await expect(page.locator("body")).toHaveJSProperty("scrollWidth", width);
    const detailScan = await new AxeBuilder({ page })
      .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
      .analyze();
    expect(detailScan.violations).toEqual([]);
    await page.screenshot({ path: testInfo.outputPath("blob-detail.png"), fullPage: true });
    expect(reads).toEqual([]);
    expect(errors).toEqual([]);
  });
}

test("transaction preview and download use the exact block blob index and remain on demand", async ({
  page,
}) => {
  const reads = await setupBlobs(page);
  await page.goto(`/tx/${transactionHash}?tab=blob`);
  await page.getByRole("link", { name: "View Blob 42" }).click();
  await expect(page).toHaveURL(`/blocks/${blockHash}/blobs/42`);
  await page.reload();
  const preview = page.getByRole("button", { name: "Preview first 1 KiB" });
  await expect(preview).toBeVisible();
  expect(reads).toEqual([]);
  await preview.focus();
  await page.keyboard.press("Enter");
  await expect(page.locator(".blob-preview pre")).toHaveText("07".repeat(1024));
  await page.getByRole("button", { name: "Hide preview" }).click();
  await expect(page.locator(".blob-preview")).toBeHidden();
  expect(reads).toHaveLength(1);
  const downloadPromise = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download blob" }).click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toBe("blob-9007199254740993-42.bin");
  const downloadedBytes: Buffer[] = [];
  for await (const chunk of await download.createReadStream()) downloadedBytes.push(chunk);
  expect(Buffer.concat(downloadedBytes).equals(Buffer.alloc(131072, 7))).toBe(true);
  expect(reads).toEqual(Array(2).fill(`/api/v1/blocks/${blockHash}/blobs/42/content`));
  await expect(page.getByRole("link", { name: "View block blobs" })).toHaveAttribute(
    "href",
    `/blocks/${blockHash}?tab=blobs`,
  );
});

test("disabled archives leave transaction hashes and fees visible and hide the block blob tab", async ({
  page,
}) => {
  await setupBlobs(page, false);
  const archiveRequests: string[] = [];
  page.on("request", (request) => {
    if (/\/blobs(?:\/|$)/.test(new URL(request.url()).pathname))
      archiveRequests.push(request.url());
  });
  await page.goto(`/tx/${transactionHash}?tab=blob`);
  await expect(page.getByRole("tabpanel").getByText(versionedHash, { exact: true })).toBeVisible();
  await expect(page.getByRole("tabpanel")).toContainText("Blob Base Fee: 0.001 Gwei");
  await expect(page.getByRole("button", { name: "Download blob" })).toHaveCount(0);
  await page.goto("/blocks/1?tab=blobs");
  await expect(page.getByRole("tab", { name: "Overview", exact: true })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect(page.getByRole("tab", { name: "Blobs", exact: true })).toHaveCount(0);
  expect(archiveRequests).toEqual([]);
});

test("block links open the same detail page and browser back restores the blob tab", async ({
  page,
}) => {
  await setupBlobs(page);
  await page.goto("/blocks/1?tab=blobs");
  await page.getByRole("link", { name: "View Blob 42" }).click();
  await expect(page).toHaveURL(`/blocks/${blockHash}/blobs/42`);
  await expect(page.getByText(commitment, { exact: true })).toBeVisible();
  await page.goBack();
  await expect(page.getByRole("tab", { name: "Blobs", exact: true })).toHaveAttribute(
    "aria-selected",
    "true",
  );
});

test("invalid detail routes never request metadata or bytes", async ({ page }) => {
  const reads = await setupBlobs(page);
  const metadata: string[] = [];
  page.on("request", (request) => {
    if (/\/blobs$/.test(new URL(request.url()).pathname)) metadata.push(request.url());
  });
  for (const url of ["/blocks/1/blobs/42", `/blocks/${blockHash}/blobs/-1`]) {
    await page.goto(url);
    await expect(page.getByRole("heading", { name: "404 ·" })).toBeVisible();
  }
  expect(metadata).toEqual([]);
  expect(reads).toEqual([]);
});
