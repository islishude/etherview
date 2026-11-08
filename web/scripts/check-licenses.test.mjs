import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, renameSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
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

test("license gate rejects a disallowed hoisted transitive license", async (t) => {
  const root = fixture(t, "GPL-3.0-only");
  await assert.rejects(checkLicenses(root), /transitive@1\.0\.0.*GPL-3\.0-only/);
});

test("license gate fails when a required hoisted dependency is missing", async (t) => {
  const root = fixture(t);
  rmSync(join(root, "node_modules/transitive"), { recursive: true });
  await assert.rejects(checkLicenses(root), /missing required dependency transitive/);
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

function virtualStoreFixture(t, license = "MIT") {
  const root = fixture(t, license);
  const store = join(root, "node_modules/.pnpm");
  mkdirSync(join(store, "direct@1.0.0/node_modules"), { recursive: true });
  mkdirSync(join(store, "transitive@1.0.0/node_modules"), { recursive: true });
  renameSync(join(root, "node_modules/direct"), join(store, "direct@1.0.0/node_modules/direct"));
  renameSync(
    join(root, "node_modules/transitive"),
    join(store, "transitive@1.0.0/node_modules/transitive"),
  );
  mkdirSync(join(root, "web/node_modules"));
  symlinkSync(
    "../../node_modules/.pnpm/direct@1.0.0/node_modules/direct",
    join(root, "web/node_modules/direct"),
  );
  symlinkSync(
    "../../transitive@1.0.0/node_modules/transitive",
    join(store, "direct@1.0.0/node_modules/transitive"),
  );
  return root;
}

test("license gate follows pnpm's isolated symlink graph and installed optional peers", async (t) => {
  const root = virtualStoreFixture(t);
  const direct = join(root, "node_modules/.pnpm/direct@1.0.0/node_modules/direct/package.json");
  writeFileSync(
    direct,
    JSON.stringify({
      name: "direct",
      version: "1.0.0",
      license: "MIT",
      peerDependencies: { transitive: "1.0.0" },
      peerDependenciesMeta: { transitive: { optional: true } },
      optionalDependencies: { unavailable: "1.0.0" },
    }),
  );
  const result = await checkLicenses(root);
  assert.deepEqual(Object.keys(result).sort(), ["direct@1.0.0", "transitive@1.0.0"]);
});

test("license gate rejects a missing required pnpm symlink target", async (t) => {
  const root = virtualStoreFixture(t);
  rmSync(join(root, "node_modules/.pnpm/transitive@1.0.0"), { recursive: true });
  await assert.rejects(checkLicenses(root), /missing required dependency transitive/);
});
