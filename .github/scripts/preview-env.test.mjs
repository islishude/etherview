import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { parseEnv } from "node:util";

import { prepareEnvironment } from "./preview-env.mjs";

function fixture(t) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "preview-env-"));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  return path.join(directory, ".env");
}

test("fresh Preview gets a valid unique P-256 pair, reused without rewriting", (t) => {
  const file = fixture(t);
  prepareEnvironment(file);
  const source = fs.readFileSync(file, "utf8");
  const values = parseEnv(source);
  const key = crypto.createECDH("prime256v1");
  assert.equal(Buffer.from(values.ETHERVIEW_PUSH_PRIVATE_KEY, "base64url").length, 32);
  key.setPrivateKey(Buffer.from(values.ETHERVIEW_PUSH_PRIVATE_KEY, "base64url"));
  assert.equal(key.getPublicKey().toString("base64url"), values.ETHERVIEW_PUSH_PUBLIC_KEY);
  assert.equal(values.ETHERVIEW_PUSH_ENABLED, "true");
  assert.equal(values.ETHERVIEW_PUSH_UNSAFE_ALLOW_PRIVATE_NETWORKS, "false");
  assert.equal(fs.statSync(file).mode & 0o777, 0o600);
  const modified = fs.statSync(file).mtimeMs;
  prepareEnvironment(file);
  assert.equal(fs.readFileSync(file, "utf8"), source);
  assert.equal(fs.statSync(file).mtimeMs, modified);
  const other = fixture(t);
  prepareEnvironment(other);
  assert.notEqual(
    parseEnv(fs.readFileSync(other, "utf8")).ETHERVIEW_PUSH_PUBLIC_KEY,
    values.ETHERVIEW_PUSH_PUBLIC_KEY,
  );
});

for (const length of [1, 30, 31, 32]) {
  test(`generated ${length}-byte scalar retains its identity in a reusable 32-byte key`, (t) => {
    const scalar = Buffer.alloc(length, 1);
    const expected = Buffer.from("00".repeat(32 - length) + "01".repeat(length), "hex");
    const reference = crypto.createECDH("prime256v1");
    reference.setPrivateKey(scalar);
    assert.equal(reference.getPrivateKey().length, length);
    const generate = t.mock.method(crypto.ECDH.prototype, "generateKeys", function () {
      this.setPrivateKey(scalar);
      return this.getPublicKey();
    });
    const file = fixture(t);
    prepareEnvironment(file);
    const source = fs.readFileSync(file, "utf8");
    const values = parseEnv(source);
    assert.equal(values.ETHERVIEW_PUSH_PRIVATE_KEY, expected.toString("base64url"));
    assert.equal(values.ETHERVIEW_PUSH_PUBLIC_KEY, reference.getPublicKey().toString("base64url"));
    const modified = fs.statSync(file).mtimeMs;
    prepareEnvironment(file);
    assert.equal(generate.mock.callCount(), 1);
    assert.equal(fs.readFileSync(file, "utf8"), source);
    assert.equal(fs.statSync(file).mtimeMs, modified);
    assert.equal(fs.statSync(file).mode & 0o777, 0o600);
  });
}

test("operator settings and comments survive completion and permission tightening", (t) => {
  const file = fixture(t);
  const source =
    '# local settings\nETHERVIEW_PORT=9999\nETHERVIEW_PUSH_ENABLED=false\nETHERVIEW_PUSH_CONTACT="mailto:me@example.com"\nETHERVIEW_PUSH_UNSAFE_ALLOW_PRIVATE_NETWORKS=true';
  fs.writeFileSync(file, source, { mode: 0o644 });
  prepareEnvironment(file);
  const result = fs.readFileSync(file, "utf8");
  assert.ok(result.startsWith(source + "\n"));
  assert.equal(parseEnv(result).ETHERVIEW_PUSH_ENABLED, "false");
  assert.equal(parseEnv(result).ETHERVIEW_PUSH_CONTACT, "mailto:me@example.com");
  assert.equal(parseEnv(result).ETHERVIEW_PUSH_UNSAFE_ALLOW_PRIVATE_NETWORKS, "true");
  assert.equal(fs.statSync(file).mode & 0o777, 0o600);
});

test("existing private key derives the same public identity; invalid or partial pairs fail unchanged", (t) => {
  const file = fixture(t);
  const key = crypto.createECDH("prime256v1");
  key.setPrivateKey(Buffer.alloc(32, 1));
  const privateLine = `ETHERVIEW_PUSH_PRIVATE_KEY=${key.getPrivateKey().toString("base64url")}\n`;
  fs.writeFileSync(file, privateLine);
  prepareEnvironment(file);
  assert.equal(
    parseEnv(fs.readFileSync(file, "utf8")).ETHERVIEW_PUSH_PUBLIC_KEY,
    key.getPublicKey().toString("base64url"),
  );
  for (const source of [
    "ETHERVIEW_PUSH_PUBLIC_KEY=existing\n",
    "ETHERVIEW_PUSH_PRIVATE_KEY=invalid\n",
    `ETHERVIEW_PUSH_PRIVATE_KEY=${Buffer.alloc(31, 1).toString("base64url")}\n`,
    `ETHERVIEW_PUSH_PRIVATE_KEY=${Buffer.alloc(32).toString("base64url")}\n`,
    `ETHERVIEW_PUSH_PRIVATE_KEY=${Buffer.alloc(32, 255).toString("base64url")}\n`,
    privateLine.trimEnd() + "=\n",
    privateLine + "ETHERVIEW_PUSH_PUBLIC_KEY=mismatch\n",
    "ETHERVIEW_PUSH_PRIVATE_KEY_FILE=/run/push-key\n",
    privateLine + "ETHERVIEW_PUSH_PRIVATE_KEY_FILE=/run/push-key\n",
  ]) {
    fs.writeFileSync(file, source);
    assert.throws(() => prepareEnvironment(file));
    assert.equal(fs.readFileSync(file, "utf8"), source);
  }
});

test("explicit file-based secrets are preserved without generating an inline key", (t) => {
  const file = fixture(t);
  fs.writeFileSync(
    file,
    "ETHERVIEW_PUSH_PUBLIC_KEY=operator-public\nETHERVIEW_PUSH_PRIVATE_KEY_FILE=/run/push-key\n",
  );
  prepareEnvironment(file);
  assert.equal(parseEnv(fs.readFileSync(file, "utf8")).ETHERVIEW_PUSH_PRIVATE_KEY, undefined);
});

test("env initialization refuses symlinks", (t) => {
  const file = fixture(t);
  const target = file + ".target";
  fs.writeFileSync(target, "unchanged");
  fs.symlinkSync(target, file);
  assert.throws(() => prepareEnvironment(file), /regular file/);
  assert.equal(fs.readFileSync(target, "utf8"), "unchanged");
});

test("daily Preview startup and recreation initialize env before other prerequisites", () => {
  const makefile = fs.readFileSync(new URL("../../Makefile", import.meta.url), "utf8");
  for (const target of ["start-preview", "recreate-preview"]) {
    assert.ok(makefile.includes(`\n${target}: preview-env `));
  }
});

test("Make initializes a custom path with spaces and missing parent directories", (t) => {
  const file = path.join(fixture(t), "directory with spaces", "preview.env");
  const output = execFileSync(
    "make",
    ["--no-print-directory", "preview-env", `PREVIEW_ENV_FILE=${file}`],
    {
      cwd: path.resolve(import.meta.dirname, "../.."),
      encoding: "utf8",
    },
  );
  const values = parseEnv(fs.readFileSync(file, "utf8"));
  assert.equal(values.ETHERVIEW_PUSH_ENABLED, "true");
  assert.ok(!output.includes(values.ETHERVIEW_PUSH_PRIVATE_KEY));
  assert.equal(fs.statSync(file).mode & 0o777, 0o600);
});
