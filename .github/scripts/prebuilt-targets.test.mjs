import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

function fixture(t) {
  const dir = mkdtempSync(join(tmpdir(), "prebuilt targets "));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  writeFileSync(join(dir, "Makefile"), readFileSync(new URL("../../Makefile", import.meta.url)));
  mkdirSync(join(dir, ".github/scripts"), { recursive: true });
  writeFileSync(
    join(dir, ".github/scripts/preview-env.mjs"),
    readFileSync(new URL("./preview-env.mjs", import.meta.url)),
  );
  mkdirSync(join(dir, "bin"));
  mkdirSync(join(dir, ".local/preview-tls"), { recursive: true });
  for (const file of ["gateway.crt", "gateway.key", "rootCA.pem"])
    writeFileSync(join(dir, ".local/preview-tls", file), "fixture");
  const log = join(dir, "commands.jsonl");
  for (const name of ["docker", "go", "buildx", "compose", "pnpm"])
    writeFileSync(
      join(dir, "bin", `stub-${name}`),
      `#!${process.execPath}
import { appendFileSync } from "node:fs";
const args = process.argv.slice(2);
appendFileSync(process.env.COMMAND_LOG, JSON.stringify({ command: ${JSON.stringify(name)}, args }) + "\\n");
if (${JSON.stringify(name)} === process.env.FAIL_COMMAND) process.exit(9);
if (${JSON.stringify(name)} === "docker" && args[0] === "image" && args[1] === "inspect" && args[2] === process.env.MISSING_IMAGE) process.exit(1);
if (${JSON.stringify(name)} === "docker" && args[0] === "run" && process.env.FAIL_OFFLINE) process.exit(7);
`,
      { mode: 0o755 },
    );
  return {
    dir,
    run(target, extra = {}, overrides = []) {
      writeFileSync(log, "");
      const result = spawnSync(
        "make",
        [
          "--no-print-directory",
          target,
          "GO=stub-go",
          "PNPM=stub-pnpm",
          "DOCKER=stub-docker",
          "BUILDX=stub-buildx",
          "COMPOSE=stub-compose",
          "IMAGE=etherview:local",
          "PREVIEW_GATEWAY_IMAGE=caddy:2.11.7-alpine",
          "HARDHAT3_IMAGE=etherview-hardhat3:local",
          "FOUNDRY_IMAGE=etherview-foundry:local",
          ...overrides,
        ],
        {
          cwd: dir,
          env: {
            ...process.env,
            PATH: `${join(dir, "bin")}:${process.env.PATH}`,
            COMMAND_LOG: log,
            ...extra,
          },
          encoding: "utf8",
          timeout: 20_000,
        },
      );
      const commands = readFileSync(log, "utf8").trim().split("\n").filter(Boolean).map(JSON.parse);
      return { ...result, commands };
    },
  };
}

for (const suite of [
  "schema-e2e",
  "runtime-e2e",
  "preview-metadata",
  "preview-consensus",
  "preview-x402",
  "preview-contracts",
]) {
  const target = `test-${suite}`;
  const preview = suite.startsWith("preview-");
  test(`${target}: prepared entry point does not build; ordinary entry point prepares first`, (t) => {
    const f = fixture(t);
    const prebuilt = f.run(`${target}-prebuilt`);
    assert.equal(prebuilt.status, 0, prebuilt.stdout + prebuilt.stderr);
    assert.equal(existsSync(join(f.dir, ".local/preview-push.env")), false);
    assert.deepEqual(
      prebuilt.commands.map((c) => c.command),
      preview ? ["docker", "docker", "docker", "docker", "go"] : ["docker", "go"],
    );
    assert.deepEqual(prebuilt.commands[0].args, ["image", "inspect", "etherview:local"]);
    if (preview)
      assert.deepEqual(prebuilt.commands[1].args, ["image", "inspect", "caddy:2.11.7-alpine"]);
    const ordinary = f.run(target);
    assert.equal(ordinary.status, 0, ordinary.stdout + ordinary.stderr);
    assert.equal(existsSync(join(f.dir, ".local/preview-push.env")), preview);
    const builds = ordinary.commands.filter((c) => c.command === "buildx");
    assert.equal(builds.length, preview ? 2 : 1);
    assert(builds[0].args.includes("production"));
    assert.deepEqual(ordinary.commands.slice(-prebuilt.commands.length), prebuilt.commands);
    assert.equal(ordinary.commands.filter((c) => c.command === "compose").length, preview ? 2 : 0);
  });

  test(`${target}: missing images and failed tests stop the command`, (t) => {
    const f = fixture(t);
    for (const missing of preview
      ? [
          "etherview:local",
          "caddy:2.11.7-alpine",
          "etherview-preview-bundler:local",
          "etherview-x402-local-facilitator:local",
        ]
      : ["etherview:local"]) {
      const result = f.run(`${target}-prebuilt`, { MISSING_IMAGE: missing });
      assert.notEqual(result.status, 0);
      assert(!result.commands.some((c) => ["go", "buildx", "compose"].includes(c.command)));
      assert.match(result.stdout, /is not loaded/);
    }
    for (const entry of [target, `${target}-prebuilt`]) {
      const result = f.run(entry, { FAIL_COMMAND: "go" });
      assert.notEqual(result.status, 0);
      assert.equal(result.commands.at(-1).command, "go");
    }
    const buildFailure = f.run(target, { FAIL_COMMAND: "buildx" });
    assert.notEqual(buildFailure.status, 0);
    assert(!buildFailure.commands.some((c) => c.command === "go"));
  });

  if (preview)
    test(`${target}: missing certificates fail without preparation or test execution`, (t) => {
      const f = fixture(t);
      rmSync(join(f.dir, ".local/preview-tls/gateway.key"));
      const result = f.run(`${target}-prebuilt`);
      assert.notEqual(result.status, 0);
      assert.deepEqual(result.commands, []);
      assert.match(result.stdout, /certificate.*missing/);
    });
}

for (const suite of ["hardhat3", "foundry", "vyper"]) {
  const target = `test-${suite}-e2e`;
  const client = suite !== "vyper";
  const clientImage = `etherview-${suite}:local`;
  test(`${target}: build and offline compilation precede tests; prebuilt never builds`, (t) => {
    const f = fixture(t);
    const prebuilt = f.run(`${target}-prebuilt`);
    assert.equal(prebuilt.status, 0, prebuilt.stdout + prebuilt.stderr);
    assert.deepEqual(
      prebuilt.commands.map((c) => c.command),
      client ? ["docker", "docker", "docker", "docker", "go"] : ["docker", "go"],
    );
    if (client) {
      assert.deepEqual(prebuilt.commands[0].args, ["image", "inspect", clientImage]);
      assert.deepEqual(prebuilt.commands[1].args.slice(0, 5), [
        "run",
        "--rm",
        "--network",
        "none",
        clientImage,
      ]);
      assert.deepEqual(prebuilt.commands[2].args, ["image", "inspect", "etherview:local"]);
      assert.deepEqual(prebuilt.commands[3].args, ["image", "inspect", clientImage]);
    }
    const ordinary = f.run(target);
    assert.equal(ordinary.status, 0, ordinary.stdout + ordinary.stderr);
    assert.deepEqual(ordinary.commands.slice(-prebuilt.commands.length), prebuilt.commands);
    const builds = ordinary.commands.slice(0, -prebuilt.commands.length);
    assert.deepEqual(
      builds.map((c) => c.command),
      client ? ["buildx", "buildx"] : ["buildx"],
    );
    assert(builds[0].args.includes("production"));
    if (client) assert(builds[1].args.includes(clientImage));
  });

  test(`${target}: missing images, failed builds, offline compilation and tests fail closed`, (t) => {
    const f = fixture(t);
    for (const missing of client ? ["etherview:local", clientImage] : ["etherview:local"]) {
      const result = f.run(`${target}-prebuilt`, { MISSING_IMAGE: missing });
      assert.notEqual(result.status, 0);
      assert(!result.commands.some((c) => ["go", "buildx", "compose"].includes(c.command)));
      if (client) assert.match(result.stdout, /is not loaded/);
    }
    const buildFailure = f.run(target, { FAIL_COMMAND: "buildx" });
    assert.notEqual(buildFailure.status, 0);
    assert.deepEqual(
      buildFailure.commands.map((c) => c.command),
      ["buildx"],
    );
    for (const entry of [target, `${target}-prebuilt`]) {
      const failedTest = f.run(entry, { FAIL_COMMAND: "go" });
      assert.notEqual(failedTest.status, 0);
      assert.equal(failedTest.commands.at(-1).command, "go");
    }
    if (client) {
      const offlineFailure = f.run(`${target}-prebuilt`, { FAIL_OFFLINE: "1" });
      assert.notEqual(offlineFailure.status, 0);
      assert.deepEqual(
        offlineFailure.commands.map((c) => c.args[0]),
        ["image", "run"],
      );
    }
  });

  if (suite !== "foundry")
    test(`${target}: invalid verification parallelism stops before Go tests`, (t) => {
      const result = fixture(t).run(`${target}-prebuilt`, {}, ["VERIFICATION_PARALLEL=3"]);
      assert.notEqual(result.status, 0);
      assert.match(result.stdout, /VERIFICATION_PARALLEL must be 1 or 2/);
      assert(!result.commands.some((c) => c.command === "go"));
    });
}
