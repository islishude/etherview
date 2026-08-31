import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { gunzipSync, brotliDecompressSync } from "node:zlib";

import {
  assetManifestName,
  assetManifestSchema,
  prepareDistribution,
} from "./prepare-assets.mjs";

test("prepares deterministic compressed hashed assets and a bounded manifest", () => {
  const distribution = mkdtempSync(join(tmpdir(), "etherview-web-assets-"));
  try {
    mkdirSync(join(distribution, "assets"));
    const javascript = Buffer.from("export const message = 'etherview';\n".repeat(512));
    writeFileSync(join(distribution, "index.html"), "<main>shell</main>");
    writeFileSync(join(distribution, "assets", "index-12345678.js"), javascript);
    writeFileSync(join(distribution, "assets", "mutable.js"), javascript);

    const manifest = prepareDistribution(distribution);
    assert.equal(manifest.schema, assetManifestSchema);
    const asset = manifest.assets["assets/index-12345678.js"];
    assert.ok(asset?.gzip && asset.br);
    assert.deepEqual(
      gunzipSync(readFileSync(join(distribution, asset.gzip.path))),
      javascript,
    );
    assert.deepEqual(
      brotliDecompressSync(readFileSync(join(distribution, asset.br.path))),
      javascript,
    );
    assert.equal(manifest.assets["index.html"]?.gzip, undefined);
    assert.equal(manifest.assets["assets/mutable.js"]?.br, undefined);
    const stored = JSON.parse(readFileSync(join(distribution, assetManifestName), "utf8"));
    assert.deepEqual(stored, manifest);
  } finally {
    rmSync(distribution, { recursive: true, force: true });
  }
});
