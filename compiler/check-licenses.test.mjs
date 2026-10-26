import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { cpSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { checkLicenses, compilerNotices } from "./check-licenses.mjs";

function inventory(t, name) {
  const root = mkdtempSync(join(tmpdir(), "etherview-license-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  cpSync(new URL(`../licenses/${name}`, import.meta.url), root, { recursive: true });
  return root;
}

test("compiler notice checks reject missing and modified installed dependency notices", (t) => {
  const root = inventory(t, "solcjs-runtime");
  checkLicenses(root, join(root, "node-LICENSE.txt"));
  const name = compilerNotices().keys().next().value;
  const path = join(root, "dependencies", name);
  const original = readFileSync(path);
  writeFileSync(path, "modified notice");
  assert.throws(() => checkLicenses(root), /checked-in compiler notice differs/);
  assert.equal(readFileSync(path, "utf8"), "modified notice");
  rmSync(path);
  assert.throws(() => checkLicenses(root), /ENOENT/);
  writeFileSync(path, original);
  const node = join(root, "builder-node-license");
  writeFileSync(node, "different Node release notice");
  assert.throws(() => checkLicenses(root, node), /Node notice differs/);
});

test("WASM notice checks fail offline without repairing missing or changed texts", (t) => {
  const root = inventory(t, "python-wasm");
  const script = new URL("./vyper/wasm/licenses.mjs", import.meta.url);
  const run = () => spawnSync(process.execPath, [script.pathname, root], { encoding: "utf8" });
  assert.equal(run().status, 0);
  const path = join(root, "Python-LICENSE.txt");
  writeFileSync(path, "modified notice");
  assert.match(run().stderr, /upstream notice identity mismatch/);
  assert.equal(readFileSync(path, "utf8"), "modified notice");
  rmSync(path);
  assert.match(run().stderr, /ENOENT/);
  assert.throws(() => readFileSync(path), /ENOENT/);
});
