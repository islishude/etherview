import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

import { appearanceCases, configureAppearance, collectBrowserErrors } from "./appearance";

const blockHash = `0x${"1".repeat(64)}`;
const hash = `0x${"2".repeat(64)}`;

test("consensus slots and blob content remain exact, optional and on demand", async ({ page }) => {
  let reads = 0;
  await page.route("**/api/v1/config", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.data.features.consensus = true;
    body.data.features.blobs = true;
    await route.fulfill({ response, json: body });
  });
  await page.route("**/api/v1/blocks/1", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.data.slot_number = "9007199254740993";
    body.data.slot_number_source = "consensus";
    await route.fulfill({ response, json: body });
  });
  await page.route("**/api/v1/blocks/*/consensus", (route) =>
    route.fulfill({
      json: {
        data: {
          block_hash: blockHash,
          state: "complete",
          slot: "9007199254740993",
          epoch: "281474976710656",
          proposer_index: "9007199254740994",
          root: hash,
          parent_root: hash,
          state_root: hash,
          body_root: hash,
          fork: "fulu",
          graffiti: `0x${"00".repeat(32)}`,
          slot_conflict: true,
          operations: { attestations: "2" },
        },
        meta: { chain_id: "1", request_id: "consensus-e2e" },
      },
    }),
  );
  await page.route("**/api/v1/blocks/*/blobs", (route) =>
    route.fulfill({
      json: {
        data: {
          block_hash: blockHash,
          state: "complete",
          items: [
            {
              index: "0",
              transaction_hash: hash,
              transaction_index: "0",
              versioned_hash: hash,
              commitment: `0x${"ab".repeat(48)}`,
              size: "131072",
              sha256: hash,
            },
          ],
        },
        meta: { chain_id: "1", request_id: "consensus-e2e" },
      },
    }),
  );
  await page.route("**/api/v1/blocks/*/blobs/0/content", async (route) => {
    reads++;
    expect(route.request().url()).toContain(`/blocks/${blockHash}/`);
    await route.fulfill({
      contentType: "application/octet-stream",
      headers: { "Content-Disposition": "attachment; filename=blob-1-0.bin" },
      body: Buffer.alloc(131072, 7),
    });
  });
  await page.goto("/blocks/1");
  await expect(page.getByText("9,007,199,254,740,993")).toBeVisible();
  await page.getByRole("tab", { name: "Consensus", exact: true }).click();
  await expect(page.getByRole("tabpanel")).toContainText("9,007,199,254,740,994");
  await expect(page.getByRole("tabpanel")).toContainText("Consensus and execution slots differ");
  await page.getByRole("tab", { name: "Blobs", exact: true }).click();
  await page.getByRole("link", { name: "View Blob 0" }).click();
  const preview = page.getByRole("button", { name: "Preview first 1 KiB" });
  await expect(preview).toBeVisible();
  expect(reads).toBe(0);
  await preview.click();
  await expect(page.locator("pre")).toHaveText("07".repeat(1024));
  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download blob", exact: true }).click();
  expect((await download).suggestedFilename()).toBe("blob-1-0.bin");
  expect(reads).toBe(2);
  await page.setViewportSize({ width: 390, height: 900 });
  await expect(page.locator("body")).toHaveJSProperty("scrollWidth", 390);
});

test("archive history and capability failures retain metadata without downloads", async ({
  page,
}) => {
  await page.route("**/api/v1/config", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.data.features.consensus = true;
    body.data.features.blobs = true;
    await route.fulfill({ response, json: body });
  });
  await page.route("**/api/v1/blocks/*/blobs", (route) =>
    route.fulfill({
      json: {
        data: {
          block_hash: blockHash,
          state: "unavailable",
          code: "beacon_not_found",
          items: [
            { index: "0", transaction_hash: hash, transaction_index: "0", versioned_hash: hash },
          ],
        },
        meta: { chain_id: "1", request_id: "consensus-e2e" },
      },
    }),
  );
  await page.goto("/blocks/1?tab=blobs");
  await expect(page.getByRole("tabpanel")).toContainText(
    "Historical data is unavailable at this node.",
  );
  await expect(page.getByRole("button", { name: "Download blob" })).toHaveCount(0);
});

test("consensus freshness expires while the last successful snapshot stays visible", async ({
  page,
}) => {
  await page.clock.install({ time: new Date("2026-10-20T00:00:00Z") });
  await page.route("**/api/v1/home?*", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.data.status.consensus = {
      state: "complete",
      blob_state: "disabled",
      blobs_enabled: false,
      stale: false,
      stale_at: "2026-10-20T00:01:00Z",
      observed_at: "2026-10-20T00:00:00Z",
      slot: "9007199254740993",
    };
    await route.fulfill({ response, json: body });
  });
  await page.goto("/");
  const panel = page.getByRole("region", { name: "Consensus & finality" });
  await expect(panel).toBeVisible();
  await expect(panel.getByRole("status")).toHaveCount(0);
  await page.clock.fastForward(61_000);
  await expect(panel.getByRole("status")).toHaveText("Data is out of date");
  await expect(panel.getByText("9,007,199,254,740,993", { exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "#2" })).toBeVisible();
});

for (const { width, language, theme } of appearanceCases) {
  test(`consensus summary ${width}px ${language} ${theme}`, async ({ page }, testInfo) => {
    await configureAppearance(page, { width, language, theme });
    const errors = collectBrowserErrors(page);
    let warning = false;
    await page.route("**/api/v1/home?*", async (route) => {
      const response = await route.fetch();
      const body = await response.json();
      body.data.status.consensus = {
        state: "complete",
        stale: warning,
        blobs_enabled: true,
        blob_state: "complete",
        slot: "9007199254740993",
        epoch: "281474976710656",
        finalized: { epoch: "281474976710654", root: hash },
        root: hash,
        syncing: warning,
        execution_optimistic: warning,
      };
      await route.fulfill({ response, json: body });
    });
    await page.goto("/");
    const panel = page.getByRole("region", {
      name: language === "en" ? "Consensus & finality" : "共识与最终性",
    });
    for (const state of ["normal", "warning"]) {
      if (state === "warning") {
        warning = true;
        await page.reload();
      }
      await expect(panel.getByText("9,007,199,254,740,993", { exact: true })).toBeVisible();
      await expect(panel.locator("dt")).toHaveCount(5);
      await expect(
        page.getByText(language === "en" ? "Finalized block" : "已最终确认区块", {
          exact: true,
        }),
      ).toHaveCount(1);
      await expect(page.locator(".chain-context")).toHaveCount(0);
      await expect(panel.getByRole("status")).toHaveCount(warning ? 1 : 0);
      await expect(page.locator("body")).toHaveJSProperty("scrollWidth", width);
      const metrics = await panel.locator("dd").all();
      for (const metric of metrics) {
        expect(
          await metric.evaluate((element) => {
            const range = document.createRange();
            range.selectNodeContents(element);
            return range.getClientRects().length;
          }),
        ).toBe(1);
      }
      const boxes = await Promise.all(metrics.map((metric) => metric.boundingBox()));
      expect(boxes.every(Boolean)).toBe(true);
      const columns = width === 390 ? 1 : width === 768 ? 3 : 5;
      for (let index = 1; index < boxes.length; index++) {
        if (index % columns === 0) expect(boxes[index]!.y).toBeGreaterThan(boxes[index - 1]!.y);
        else expect(boxes[index]!.y).toBe(boxes[index - 1]!.y);
      }
      const scan = await new AxeBuilder({ page })
        .include(".consensus-overview")
        .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
        .analyze();
      expect(scan.violations).toEqual([]);
      const screenshot = testInfo.outputPath(`consensus-${state}.png`);
      await page.screenshot({ path: screenshot, fullPage: true });
      await testInfo.attach(`consensus-${state}`, {
        path: screenshot,
        contentType: "image/png",
      });
    }
    expect(errors).toEqual([]);
  });
}
