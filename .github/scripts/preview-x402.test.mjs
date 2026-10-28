import assert from "node:assert/strict";
import fs from "node:fs";
import test from "node:test";
test("Preview lifecycle has no transaction-based x402 initializer", () => {
  for (const file of ["compose.yaml", "Makefile"])
    assert.doesNotMatch(fs.readFileSync(file, "utf8"), /x402-init/);
  assert.equal(fs.existsSync("preview/x402-init.sh"), false);
});
