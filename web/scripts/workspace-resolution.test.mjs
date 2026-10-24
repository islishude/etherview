import assert from "node:assert/strict";
import { createRequire } from "node:module";
import test from "node:test";

const api = createRequire(new URL("../../api/package.json", import.meta.url));
const compiler = createRequire(new URL("../../compiler/package.json", import.meta.url));
const hardhat = createRequire(new URL("../../e2e/hardhat3/package.json", import.meta.url));
const web = createRequire(new URL("../package.json", import.meta.url));

test("workspaces retain distinct compiler and client runtime versions", () => {
  assert.match(compiler("solc").version(), /^0\.8\.37\+/);
  assert.match(hardhat("solc").version(), /^0\.8\.30\+/);
  assert.equal(compiler("pyodide/package.json").version, "314.0.7");
  assert.equal(compiler("pyodide-legacy/package.json").version, "0.29.5");
  assert.equal(hardhat("hardhat/package.json").version, "3.20.0");
  assert.equal(hardhat("@nomicfoundation/hardhat-verify/package.json").version, "3.1.3");
  assert.equal(hardhat("viem/package.json").version, "2.57.8");
  assert.equal(web("viem/package.json").version, "2.56.9");
});

test("explicit peers preserve API generation and browser testing under the root install policy", () => {
  assert.equal(api("typescript/package.json").version, "6.0.3");
  assert.equal(typeof api("typescript").factory.createKeywordTypeNode, "function");
  assert.equal(web("typescript/package.json").version, "7.0.2");
  assert.equal(web("@testing-library/dom/package.json").version, "10.4.1");
  assert.equal(typeof web("@testing-library/dom").screen.getByRole, "function");
});
