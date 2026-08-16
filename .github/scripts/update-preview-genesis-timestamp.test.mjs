import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { writeGenesisWithTimestamp } from "./update-preview-genesis-timestamp.mjs";

function withTempGenesis(source, callback) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "etherview-preview-genesis-test-"));
  const filePath = path.join(directory, "genesis.json");
  fs.writeFileSync(filePath, source, "utf8");
  try {
    return callback(filePath);
  } finally {
    fs.rmSync(directory, { recursive: true, force: true });
  }
}

test("updates only the timestamp field and preserves the Genesis source", () => {
  const sourcePath = path.resolve("deploy/preview.genesis.json");
  const before = fs.readFileSync(sourcePath, "utf8");
  const nextSeconds = 1700000000n;
  const nextTimestamp = `0x${nextSeconds.toString(16)}`;

  withTempGenesis(before, (temporaryPath) => {
    const destinationPath = path.join(path.dirname(temporaryPath), "nested", "runtime.json");
    const result = writeGenesisWithTimestamp(temporaryPath, destinationPath, nextSeconds);
    const after = fs.readFileSync(destinationPath, "utf8");
    assert.deepEqual(result, { changed: true, timestamp: nextTimestamp });
    assert.equal(JSON.parse(after).timestamp, nextTimestamp);
    assert.equal(fs.readFileSync(temporaryPath, "utf8"), before);
    assert.equal(
      after.replace(nextTimestamp, "<timestamp>"),
      before.replace(JSON.parse(before).timestamp, "<timestamp>"),
    );
  });
});

test("uses the current Unix seconds by default", () => {
  const before = BigInt(Math.floor(Date.now() / 1000));
  withTempGenesis('{\n  "timestamp": "0x1"\n}\n', (temporaryPath) => {
    const destinationPath = path.join(path.dirname(temporaryPath), "runtime.json");
    const result = writeGenesisWithTimestamp(temporaryPath, destinationPath);
    const timestamp = BigInt(JSON.parse(fs.readFileSync(destinationPath, "utf8")).timestamp);
    const after = BigInt(Math.floor(Date.now() / 1000));
    assert.equal(result.changed, true);
    assert.ok(timestamp >= before && timestamp <= after);
    assert.equal(JSON.parse(fs.readFileSync(temporaryPath, "utf8")).timestamp, "0x1");
  });
});

test("does not write malformed or invalid Genesis timestamps", () => {
  const invalidSources = [
    '{"config": {},',
    '{"config": {}}',
    '{"timestamp": "1"}',
    '{"timestamp": "0x10000000000000000"}',
    '{"timestamp": "0x1", "nested": {"timestamp": "0x2"}}',
  ];

  for (const source of invalidSources) {
    withTempGenesis(source, (temporaryPath) => {
      const destinationPath = path.join(path.dirname(temporaryPath), "runtime.json");
      assert.throws(() => writeGenesisWithTimestamp(temporaryPath, destinationPath, 1700000000n));
      assert.equal(fs.readFileSync(temporaryPath, "utf8"), source);
      assert.equal(fs.existsSync(destinationPath), false);
    });
  }
});

test("reuses an unchanged runtime copy without rewriting it", () => {
  withTempGenesis('{\n  "timestamp": "0x1"\n}\n', (temporaryPath) => {
    const destinationPath = path.join(path.dirname(temporaryPath), "runtime.json");
    const first = writeGenesisWithTimestamp(temporaryPath, destinationPath, 1700000000n);
    const writtenAt = fs.statSync(destinationPath).mtimeMs;
    const second = writeGenesisWithTimestamp(temporaryPath, destinationPath, 1700000000n);
    assert.equal(first.changed, true);
    assert.equal(second.changed, false);
    assert.equal(fs.statSync(destinationPath).mtimeMs, writtenAt);
  });
});
