import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";
import { checkLicenses } from "../../.github/scripts/check-workspace-licenses.mjs";

function fixture(t, license = "MIT") {
  const root = mkdtempSync(join(tmpdir(), "etherview-workspace-licenses-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const manifests = {
    "package.json": { name: "fixture", private: true, workspaces: ["web"] },
    "web/package.json": {
      name: "@etherview/web",
      version: "0.0.0",
      private: true,
      dependencies: { direct: "1.0.0" },
      devDependencies: { development: "1.0.0" },
    },
    "node_modules/direct/package.json": {
      name: "direct",
      version: "1.0.0",
      license: "MIT",
      dependencies: { transitive: "1.0.0" },
    },
    "node_modules/transitive/package.json": { name: "transitive", version: "1.0.0", license },
    "node_modules/development/package.json": {
      name: "development",
      version: "1.0.0",
      license: "GPL-3.0-only",
    },
  };
  for (const [name, value] of Object.entries(manifests)) {
    const path = join(root, name);
    mkdirSync(dirname(path), { recursive: true });
    writeFileSync(path, JSON.stringify(value));
  }
  mkdirSync(join(root, "node_modules/@etherview"));
  symlinkSync("../../web", join(root, "node_modules/@etherview/web"));
  return root;
}

test("license gate covers hoisted production dependencies and excludes development", async (t) => {
  const result = await checkLicenses(fixture(t));
  assert.deepEqual(Object.keys(result).sort(), ["direct@1.0.0", "transitive@1.0.0"]);
});

test("license gate rejects a disallowed hoisted transitive license", (t) => {
  const root = fixture(t, "GPL-3.0-only");
  const moduleURL = new URL("../../.github/scripts/check-workspace-licenses.mjs", import.meta.url)
    .href;
  // The pinned checker exits on a disallowed license, so exercise it in a child.
  const result = spawnSync(
    process.execPath,
    [
      "--input-type=module",
      "-e",
      `import { checkLicenses } from ${JSON.stringify(moduleURL)}; await checkLicenses(${JSON.stringify(root)});`,
    ],
    { encoding: "utf8", timeout: 20_000 },
  );
  assert.ifError(result.error);
  assert.equal(result.status, 1);
  assert.match(result.stderr, /transitive@1\.0\.0.*GPL-3\.0-only/);
});

test("license gate fails when a required hoisted dependency is missing", async (t) => {
  const root = fixture(t);
  rmSync(join(root, "node_modules/transitive"), { recursive: true });
  await assert.rejects(checkLicenses(root), /Command failed/);
});

test("license gate follows npm aliases to the original package identity", async (t) => {
  const root = fixture(t);
  const manifest = join(root, "web/package.json");
  writeFileSync(
    manifest,
    JSON.stringify({
      name: "@etherview/web",
      version: "0.0.0",
      private: true,
      dependencies: { direct: "npm:original@1.0.0" },
    }),
  );
  writeFileSync(
    join(root, "node_modules/direct/package.json"),
    JSON.stringify({
      name: "original",
      version: "1.0.0",
      license: "MIT",
      dependencies: { transitive: "1.0.0" },
    }),
  );
  const result = await checkLicenses(root);
  assert.deepEqual(Object.keys(result).sort(), ["original@1.0.0", "transitive@1.0.0"]);
});
