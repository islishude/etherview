import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";

// Synthetic public values exercise scanner entropy checks without using credentials.
const publicHash = createHash("sha256").update("etherview public scanner regression").digest("hex");

test("Amsterdam hash exceptions retain path and field boundaries", () => {
  const root = mkdtempSync(join(tmpdir(), "etherview-gitleaks-test-"));
  const config = resolve(".gitleaks.toml");
  const write = (path, value, pretty = false) => {
    mkdirSync(dirname(join(root, path)), { recursive: true });
    writeFileSync(join(root, path), JSON.stringify(value, null, pretty ? 2 : undefined));
  };
  try {
    for (const name of ["0x0", "0x1", "latest", "tx"]) {
      write(`.local/amsterdam-probe/block-${name}.json`, {
        blockAccessListHash: `0x${publicHash}`,
      }, name === "tx");
    }
    const canonical = "internal/chainbundle/testdata/amsterdam-block.json";
    write(canonical, { blockAccessListHash: `0x${publicHash}`, api_key: publicHash });
    write(".local/amsterdam-probe/block-latest.json", {
      blockAccessListHash: `0x${publicHash}`, api_key: publicHash,
    });
    write("elsewhere.json", { blockAccessListHash: `0x${publicHash}` });
    const result = spawnSync(process.env.GITLEAKS ?? "gitleaks", [
      "dir", "--no-banner", "--redact", "--config", config,
      "--report-format", "json", "--report-path", join(root, "report.json"), ".",
    ], { cwd: root, encoding: "utf8" });
    assert.ifError(result.error);
    assert.equal(result.status, 1, "scanner must still reject synthetic credential findings");
    const findings = JSON.parse(readFileSync(join(root, "report.json"), "utf8"));
    assert.deepEqual(findings.map(({ File, RuleID }) => `${File}:${RuleID}`).sort(), [
      ".local/amsterdam-probe/block-latest.json:generic-api-key",
      `${canonical}:generic-api-key`,
      "elsewhere.json:generic-api-key",
    ].sort());
    assert.equal(findings.filter(({ Match }) => Match.startsWith("blockAccessListHash")).length, 1);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
