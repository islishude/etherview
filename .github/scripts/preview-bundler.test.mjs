import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import { spawnSync } from "node:child_process";
import test from "node:test";
import { verifyGenesis } from "../../preview/bundler-start.mjs";
test("Bundler verifies chain and exact block-zero code before starting", async () => {
  const manifest = { contracts: { EntryPoint: { address: "0xAB" } } };
  const alloc = { "0xab": { code: "0x1234" } };
  for (const [chain, code, error] of [
    ["0x1", "0x1234", /chain mismatch/],
    ["0xbeaf", "0xabcd", /genesis mismatch/],
    ["0xbeaf", "0x1234", null],
  ]) {
    const rpc = async (method, params) => {
      if (method === "eth_chainId") return chain;
      assert.deepEqual(params, ["0xab", "0x0"]);
      return code;
    };
    if (error) await assert.rejects(verifyGenesis(rpc, manifest, alloc), error);
    else await verifyGenesis(rpc, manifest, alloc);
  }
});
test("Corrupt cached Rundler archives fail before extraction or download", (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "rundler-cache-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const cache = path.join(dir, ".local/rundler/arm64");
  fs.mkdirSync(cache, { recursive: true });
  fs.writeFileSync(path.join(cache, "release.tar.gz"), "corrupt");
  const result = spawnSync(
    process.execPath,
    [path.resolve("preview/rundler-download.mjs"), "arm64"],
    { cwd: dir, encoding: "utf8" },
  );
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /cached release checksum mismatch/);
  assert.equal(fs.existsSync(path.join(cache, "rundler")), false);
});
