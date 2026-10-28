import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import test from "node:test";

// Synthetic public values exercise scanner entropy checks without using credentials.
const publicHash = createHash("sha256").update("etherview public scanner regression").digest("hex");

test("WebSocket sample nonce exception retains path, header and value boundaries", () => {
  const root = mkdtempSync(join(tmpdir(), "etherview-gitleaks-websocket-test-"));
  const canonical = "e2e/previewgateway/gateway_test.go";
  const sampleNonce = Buffer.from("the sample nonce").toString("base64");
  const handshake = (nonce) => `"GET / HTTP/1.1\\r\\nSec-WebSocket-Key: ${nonce}\\r\\n\\r\\n"`;
  const fixtures = {
    [canonical]: `${handshake(sampleNonce)}\n${handshake(publicHash)}\napi_key = "${sampleNonce}"\n`,
    "elsewhere.go": handshake(sampleNonce),
    "nested/e2e/previewgateway/gateway_test.go": handshake(sampleNonce),
    "e2e/previewgateway/gateway_test.go.bak": handshake(sampleNonce),
  };
  try {
    for (const [path, source] of Object.entries(fixtures)) {
      mkdirSync(dirname(join(root, path)), { recursive: true });
      writeFileSync(join(root, path), source);
    }
    const result = spawnSync(
      process.env.GITLEAKS ?? "gitleaks",
      [
        "dir",
        "--no-banner",
        "--redact",
        "--config",
        resolve(".gitleaks.toml"),
        "--report-format",
        "json",
        "--report-path",
        join(root, "report.json"),
        ".",
      ],
      { cwd: root, encoding: "utf8" },
    );
    assert.ifError(result.error);
    assert.equal(result.status, 1, "scanner must still reject other credential findings");
    const findings = JSON.parse(readFileSync(join(root, "report.json"), "utf8"));
    assert.deepEqual(
      findings.map(({ File, StartLine, RuleID }) => `${File}:${StartLine}:${RuleID}`).sort(),
      [
        `${canonical}:2:generic-api-key`,
        `${canonical}:3:generic-api-key`,
        "elsewhere.go:1:generic-api-key",
        "nested/e2e/previewgateway/gateway_test.go:1:generic-api-key",
        "e2e/previewgateway/gateway_test.go.bak:1:generic-api-key",
      ].sort(),
    );
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("root .local artifacts are ignored while source exceptions retain path and field boundaries", () => {
  const root = mkdtempSync(join(tmpdir(), "etherview-gitleaks-test-"));
  const config = resolve(".gitleaks.toml");
  const write = (path, value, pretty = false) => {
    mkdirSync(dirname(join(root, path)), { recursive: true });
    writeFileSync(join(root, path), JSON.stringify(value, null, pretty ? 2 : undefined));
  };
  try {
    for (const name of ["0x0", "0x1", "latest", "tx"]) {
      write(
        `.local/amsterdam-probe/block-${name}.json`,
        {
          blockAccessListHash: `0x${publicHash}`,
        },
        name === "tx",
      );
    }
    const canonical = "internal/chainbundle/testdata/amsterdam-block.json";
    write(canonical, { blockAccessListHash: `0x${publicHash}`, api_key: publicHash });
    write(".local/amsterdam-probe/block-latest.json", {
      blockAccessListHash: `0x${publicHash}`,
      api_key: publicHash,
    });
    write("elsewhere.json", { blockAccessListHash: `0x${publicHash}` });
    write(".local/preview-network/manifest.json", {
      "validator/keys/voting-keystore.json": publicHash,
      api_key: publicHash,
    });
    write(".local/preview-network/unrelated.json", { api_key: publicHash });
    write(".local/another-network/manifest.json", {
      "validator/keys/voting-keystore.json": publicHash,
    });
    write(".local/new-artifacts/credentials.json", { api_key: publicHash });
    write(".locality/credentials.json", { api_key: publicHash });
    write("nested/.local/credentials.json", { api_key: publicHash });
    const result = spawnSync(
      process.env.GITLEAKS ?? "gitleaks",
      [
        "dir",
        "--no-banner",
        "--redact",
        "--config",
        config,
        "--report-format",
        "json",
        "--report-path",
        join(root, "report.json"),
        ".",
      ],
      { cwd: root, encoding: "utf8" },
    );
    assert.ifError(result.error);
    assert.equal(result.status, 1, "scanner must still reject synthetic credential findings");
    const findings = JSON.parse(readFileSync(join(root, "report.json"), "utf8"));
    assert.deepEqual(
      findings.map(({ File, RuleID }) => `${File}:${RuleID}`).sort(),
      [
        `${canonical}:generic-api-key`,
        "elsewhere.json:generic-api-key",
        ".locality/credentials.json:generic-api-key",
        "nested/.local/credentials.json:generic-api-key",
      ].sort(),
    );
    assert.equal(findings.filter(({ Match }) => Match.startsWith("blockAccessListHash")).length, 1);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("Preview x402 exceptions permit only exact public identities at exact paths", () => {
  const root = mkdtempSync(join(tmpdir(), "etherview-gitleaks-x402-"));
  const compose = readFileSync("preview/compose/x402.yaml", "utf8");
  const identities = [
    compose.match(/X402_LOCAL_FACILITATOR_PRIVATE_KEY: (.+)/)[1],
    "0x8464135c8F25Da09e49BC8782676a84730C318bC",
  ];
  const paths = ["preview/compose/x402.yaml", "docs/operations.md"];
  try {
    for (const file of [...paths, "nested/compose.yaml"]) {
      mkdirSync(dirname(join(root, file)), { recursive: true });
      writeFileSync(
        join(root, file),
        [...identities, publicHash].map((value) => `private_key = "${value}"`).join("\n"),
      );
    }
    const result = spawnSync(
      process.env.GITLEAKS ?? "gitleaks",
      [
        "dir",
        "--no-banner",
        "--redact",
        "--config",
        resolve(".gitleaks.toml"),
        "--report-format",
        "json",
        "--report-path",
        join(root, "report.json"),
        ".",
      ],
      { cwd: root, encoding: "utf8" },
    );
    assert.ifError(result.error);
    assert.equal(result.status, 1);
    const findings = JSON.parse(readFileSync(join(root, "report.json"), "utf8"));
    assert.deepEqual(
      findings.map(({ File, StartLine }) => `${File}:${StartLine}`).sort(),
      [
        ...paths.map((file) => `${file}:3`),
        ...[1, 2, 3].map((line) => `nested/compose.yaml:${line}`),
      ].sort(),
    );
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
