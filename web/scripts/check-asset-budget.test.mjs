import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { measureInitialAssets } from "./check-asset-budget.mjs";
import { prepareDistribution } from "./prepare-assets.mjs";

function withFixture(callback) {
  const root = mkdtempSync(join(tmpdir(), "etherview-asset-budget-"));
  const distribution = join(root, "dist");
  const assets = join(distribution, "assets");
  const budget = join(root, "budget.json");
  mkdirSync(assets, { recursive: true });
  writeFileSync(
    join(distribution, "index.html"),
    '<script type="module" src="/assets/index-12345678.js"></script>\n',
  );
  writeFileSync(join(assets, "index-12345678.js"), "export const value = 1;\n".repeat(128));
  try {
    callback({ distribution, budget });
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
}

function writeBudget(path, overrides = {}) {
  writeFileSync(path, JSON.stringify({
    schema: "etherview-web-asset-budget-v1",
    maximumInitialRawBytes: 8192,
    maximumInitialGzipBytes: 8192,
    forbiddenInitialPatterns: [],
    ...overrides,
  }));
}

test("measures only the root asset graph", () => {
  withFixture(({ distribution, budget }) => {
    writeFileSync(join(distribution, "assets", "lazy-12345678.js"), "x".repeat(4096));
    writeBudget(budget);
    prepareDistribution(distribution);
    const result = measureInitialAssets(distribution, budget);
    assert.deepEqual(result.files, ["assets/index-12345678.js", "index.html"]);
    assert.ok(result.rawBytes > 0 && result.rawBytes < 8192);
  });
});

test("rejects size regressions and forbidden initial chunks", () => {
  withFixture(({ distribution, budget }) => {
    writeBudget(budget, { maximumInitialRawBytes: 1 });
    prepareDistribution(distribution);
    assert.throws(() => measureInitialAssets(distribution, budget), /exceed/u);
    writeBudget(budget, { forbiddenInitialPatterns: ["index-"] });
    assert.throws(() => measureInitialAssets(distribution, budget), /forbidden/u);
  });
});
