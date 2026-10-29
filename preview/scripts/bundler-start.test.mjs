import assert from "node:assert/strict";
import test from "node:test";

import { verifyGenesis } from "../bundler-start.mjs";
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
