import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
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
  writeFileSync(
    path,
    JSON.stringify({
      schema: "etherview-web-asset-budget-v1",
      maximumInitialRawBytes: 8192,
      maximumInitialGzipBytes: 8192,
      forbiddenInitialPatterns: [],
      ...overrides,
    }),
  );
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

test("counts identity-only PNG and small SVG images without requiring compression", () => {
  withFixture(({ distribution, budget }) => {
    writeBudget(budget);
    prepareDistribution(distribution);
    const before = measureInitialAssets(distribution, budget);
    const indexPath = join(distribution, "index.html");
    writeFileSync(
      indexPath,
      readFileSync(indexPath, "utf8") +
        '<link rel="icon" href="/assets/tab-12345678.png"><link rel="preload" as="image" href="/assets/logo-12345678.svg">',
    );
    writeFileSync(join(distribution, "assets/tab-12345678.png"), Buffer.alloc(1024));
    writeFileSync(join(distribution, "assets/logo-12345678.svg"), "<svg/>");
    prepareDistribution(distribution);
    const after = measureInitialAssets(distribution, budget);
    assert.ok(after.rawBytes >= before.rawBytes + 1030);
    assert.ok(after.gzipBytes >= before.gzipBytes + 1030);
    writeBudget(budget, { maximumInitialGzipBytes: before.gzipBytes + 1029 });
    assert.throws(() => measureInitialAssets(distribution, budget), /exceed/u);
  });
});

test("still requires both compressed script representations and every image identity", () => {
  withFixture(({ distribution, budget }) => {
    writeBudget(budget);
    const manifest = prepareDistribution(distribution);
    delete manifest.assets["assets/index-12345678.js"].br;
    writeFileSync(join(distribution, "asset-manifest.json"), JSON.stringify(manifest));
    assert.throws(() => measureInitialAssets(distribution, budget), /lacks precompressed/u);
    writeFileSync(
      join(distribution, "index.html"),
      '<link rel="icon" href="/assets/tab-12345678.png">',
    );
    writeFileSync(join(distribution, "assets/tab-12345678.png"), Buffer.alloc(32));
    assert.throws(() => measureInitialAssets(distribution, budget), /lacks identity/u);
  });
});

test("detail budgets follow shared static imports but exclude lazy features", async () => {
  const { measureDetailAssets } = await import("./check-asset-budget.mjs");
  withFixture(({ distribution, budget }) => {
    mkdirSync(join(distribution, ".vite"));
    const manifestPath = join(distribution, ".vite", "manifest.json");
    const manifest = {
      block: {
        file: "assets/index-12345678.js",
        imports: ["shared"],
        dynamicImports: ["transaction"],
      },
      transaction: { file: "transaction.js" },
      shared: { file: "shared.js", imports: ["block"] },
    };
    writeFileSync(join(distribution, "shared.js"), "shared");
    writeFileSync(join(distribution, "transaction.js"), "transaction");
    const modulePath = join(distribution, ".vite", "module-graph.json");
    const modules = {
      "assets/index-12345678.js": ["block"],
      "shared.js": ["shared"],
      "transaction.js": ["transaction"],
    };
    writeFileSync(modulePath, JSON.stringify(modules));
    const detailRoutes = ["block", "transaction"].map((entry) => ({
      entry,
      maximumRawBytes: 8192,
      maximumGzipBytes: 8192,
    }));
    writeBudget(budget, { detailRoutes });
    writeFileSync(manifestPath, JSON.stringify(manifest));
    const [result] = measureDetailAssets(distribution, budget);
    assert.equal(result.files.length, 2);
    manifest.shared.imports.push("transaction");
    writeFileSync(manifestPath, JSON.stringify(manifest));
    assert.throws(() => measureDetailAssets(distribution, budget), /another entity/);
    manifest.shared.imports.pop();
    writeFileSync(manifestPath, JSON.stringify(manifest));
    modules["shared.js"].push("transaction");
    writeFileSync(modulePath, JSON.stringify(modules));
    assert.throws(() => measureDetailAssets(distribution, budget), /another entity module/);
    modules["shared.js"].pop();
    writeFileSync(modulePath, JSON.stringify(modules));
    detailRoutes[0].maximumRawBytes = 1;
    writeBudget(budget, { detailRoutes });
    assert.throws(() => measureDetailAssets(distribution, budget), /exceeds/);
  });
});
