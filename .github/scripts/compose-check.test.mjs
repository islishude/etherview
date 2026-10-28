import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

const applicationKeys = [
  "ETHERVIEW_DATABASE_READ_MAX_CONNECTIONS",
  "ETHERVIEW_DATABASE_READ_MIN_CONNECTIONS",
  "ETHERVIEW_LOG_LEVEL",
  "ETHERVIEW_LOG_FORMAT",
  "ETHERVIEW_SYNC_PROGRESS_LOG_INTERVAL",
  "ETHERVIEW_USER_AUTH_API_KEY_RATE",
  "ETHERVIEW_USER_AUTH_API_KEY_BURST",
  "ETHERVIEW_USER_AUTH_MAX_ACTIVE_API_KEYS",
];
const observedKeys = [
  ...applicationKeys,
  "ETHERVIEW_TURNSTILE_ENABLED",
  "ETHERVIEW_TURNSTILE_SITE_KEY",
  "ETHERVIEW_TURNSTILE_SECRET_KEY",
  "ETHERVIEW_TURNSTILE_SECRET_KEY_FILE",
  "DOCKER",
  "AWS_ACCESS_KEY_ID",
  "ETHERVIEW_VERIFICATION_EXECUTOR_PATH",
  "ETHERVIEW_METADATA_IPFS_GATEWAY",
  "IPFS_API_PORT",
  "ETHERVIEW_COMPOSE_TOPOLOGY",
  "ETHERVIEW_EXPECT_COMPILER_RUNTIME_PATH_OVERRIDE",
  "ETHERVIEW_HARDHAT3_IMAGE",
  "ETHERVIEW_HARDHAT3_API_SERVICE",
  "ETHERVIEW_HARDHAT3_TOPOLOGY",
  "ETHERVIEW_VERIFICATION_SUITE",
  "ETHERVIEW_FOUNDRY_IMAGE",
  "ETHERVIEW_FOUNDRY_API_SERVICE",
  "ETHERVIEW_FOUNDRY_TOPOLOGY",
];

function fixture(t) {
  const root = mkdtempSync(join(tmpdir(), "compose checks "));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const scripts = join(root, ".github/scripts");
  mkdirSync(scripts, { recursive: true });
  for (const file of ["compose-check.mjs", "preview-compose.mjs"])
    copyFileSync(new URL(file, import.meta.url), join(scripts, file));
  const log = join(root, "commands.jsonl");
  const stubs = {};
  for (const command of ["compose", "node"]) {
    stubs[command] = join(root, `fake ${command}.mjs`);
    writeFileSync(
      stubs[command],
      `#!${process.execPath}
import { appendFileSync, readFileSync } from "node:fs";
const command = ${JSON.stringify(command)};
const rawArgs = process.argv.slice(2);
const args = rawArgs[0] === "prefix" ? rawArgs.slice(1) : rawArgs;
const keys = ${JSON.stringify(observedKeys)};
const env = Object.fromEntries(keys.map((key) => [key, process.env[key] ?? null]));
const input = command === "node" && !args.includes("--test") ? readFileSync(0, "utf8") : null;
appendFileSync(process.env.COMMAND_LOG, JSON.stringify({ command, args, rawArgs, env, input }) + "\\n");
if (command === "node") {
  if (args.some((arg) => arg.endsWith(process.env.FAIL_VALIDATOR || "!"))) {
    console.error("validator diagnostic"); process.exit(19);
  }
  if (input) {
    const document = JSON.parse(input);
    if (!document.fixture) process.exit(20);
  }
} else {
  const events = readFileSync(process.env.COMMAND_LOG, "utf8").trim().split("\\n").map(JSON.parse);
  const count = events.filter((event) => event.command === "compose").length;
  const environment = Object.fromEntries(${JSON.stringify(applicationKeys)}.map((key) => [key, process.env[key] ?? null]));
  environment.ETHERVIEW_METADATA_IPFS_GATEWAY = process.env.BAD_GATEWAY ? "wrong" : process.env.ETHERVIEW_METADATA_IPFS_GATEWAY;
  if (process.env.BAD_OVERRIDE && environment.ETHERVIEW_LOG_LEVEL === "debug") environment.ETHERVIEW_LOG_LEVEL = "wrong";
  const services = Object.fromEntries(["etherview", "api", "sync", "enrich", "trace", "metadata", "maintenance"].map((role) => [role, { environment }]));
  if (process.env.ETHERVIEW_TURNSTILE_SITE_KEY) {
    for (const role of ["etherview", "api"]) services[role].environment = { ...environment,
      ETHERVIEW_TURNSTILE_ENABLED: process.env.ETHERVIEW_TURNSTILE_ENABLED,
      ETHERVIEW_TURNSTILE_SITE_KEY: process.env.ETHERVIEW_TURNSTILE_SITE_KEY,
      ETHERVIEW_TURNSTILE_SECRET_KEY: process.env.ETHERVIEW_TURNSTILE_SECRET_KEY,
      ETHERVIEW_TURNSTILE_SECRET_KEY_FILE: process.env.ETHERVIEW_TURNSTILE_SECRET_KEY_FILE,
    };
  }
  if (args.includes("--format")) console.log(process.env.BAD_JSON ? "invalid JSON" : JSON.stringify({ fixture: true, services }));
  if (count === Number(process.env.FAIL_COMPOSE_AT)) {
    console.error("render diagnostic"); process.exit(23);
  }
}
`,
      { mode: 0o755 },
    );
  }
  return {
    root,
    run(extra = {}, commandPrefix = false) {
      writeFileSync(log, "");
      const env = { ...process.env };
      for (const key of observedKeys) delete env[key];
      const result = spawnSync(process.execPath, [join(scripts, "compose-check.mjs")], {
        cwd: root,
        encoding: "utf8",
        timeout: 30_000,
        env: {
          ...env,
          COMMAND_LOG: log,
          DOCKER: "custom-docker",
          NODE: commandPrefix ? `${process.execPath} '${stubs.node}' prefix` : stubs.node,
          COMPOSE: commandPrefix ? `${process.execPath} '${stubs.compose}' prefix` : stubs.compose,
          HARDHAT3_IMAGE: "custom-hardhat:local",
          FOUNDRY_IMAGE: "custom-foundry:local",
          AWS_ACCESS_KEY_ID: "host-access",
          ETHERVIEW_VERIFICATION_EXECUTOR_PATH: "/host/runtime",
          ETHERVIEW_LOG_LEVEL: "host-level",
          ...extra,
        },
      });
      const events = readFileSync(log, "utf8").trim().split("\n").filter(Boolean).map(JSON.parse);
      return { ...result, events };
    },
  };
}

test("Compose matrix retains every layout and isolates renderer and validator environments", (t) => {
  const result = fixture(t).run();
  assert.equal(result.status, 0, result.stdout + result.stderr);
  const compose = result.events.filter((e) => e.command === "compose");
  assert.equal(compose.length, 27); // Version probe, 23 original renders and 3 Turnstile layouts.
  const renders = compose
    .slice(1)
    .filter((event) => event.env.ETHERVIEW_TURNSTILE_SITE_KEY !== "public-widget");
  assert.equal(
    compose.filter((event) => event.env.ETHERVIEW_TURNSTILE_SITE_KEY === "public-widget").length,
    3,
  );
  for (const event of compose.filter(
    (e) => e.env.ETHERVIEW_TURNSTILE_SITE_KEY === "public-widget",
  )) {
    assert.equal(event.env.ETHERVIEW_TURNSTILE_ENABLED, "true");
  }
  const profiles = (e) => e.args.flatMap((arg, i) => (arg === "--profile" ? [e.args[i + 1]] : []));
  assert.deepEqual(renders.map(profiles), [
    ["monolith"],
    ["distributed"],
    ["monolith"],
    ["distributed"],
    ["monolith", "accelerators"],
    ["distributed", "accelerators"],
    ["accelerators"],
    ["monolith"],
    ["distributed"],
    [],
    ["archive-init", "x402-init"],
    [],
    [],
    ["monolith"],
    ["distributed"],
    ["monolith", "hardhat-client"],
    ["distributed", "hardhat-client"],
    ["monolith"],
    ["distributed"],
    ["monolith", "foundry-client"],
    ["distributed", "foundry-client"],
    ["monolith"],
    ["monolith"],
  ]);
  for (const e of renders) {
    assert.equal(e.env.DOCKER, "custom-docker");
    if (e.args.includes("deploy/compose.yaml"))
      assert.deepEqual(e.args.slice(0, 2), ["--project-directory", "."]);
  }
  assert.equal(renders[4].env.AWS_ACCESS_KEY_ID, "aws-access");
  assert.equal(renders[6].env.AWS_ACCESS_KEY_ID, "host-access");
  assert.equal(
    renders[7].env.ETHERVIEW_VERIFICATION_EXECUTOR_PATH,
    "/custom/runtime/etherview-solcjs",
  );
  assert.equal(renders[9].env.ETHERVIEW_VERIFICATION_EXECUTOR_PATH, "/host/runtime");
  assert.equal(renders[11].env.ETHERVIEW_METADATA_IPFS_GATEWAY, "https://gateway.example.com");
  assert.equal(renders[12].env.ETHERVIEW_METADATA_IPFS_GATEWAY, null);
  assert.equal(renders[12].env.IPFS_API_PORT, "0");
  assert.equal(renders[13].env.IPFS_API_PORT, null);
  const nodes = result.events.filter((e) => e.command === "node");
  assert.equal(nodes.length, 13);
  assert.deepEqual(nodes[0].args, ["--test", ".github/scripts/compose-layout.test.mjs"]);
  for (const e of nodes.slice(1)) {
    assert.equal(JSON.parse(e.input).fixture, true);
    assert.equal(e.env.AWS_ACCESS_KEY_ID, "host-access");
    assert.equal(e.env.ETHERVIEW_VERIFICATION_EXECUTOR_PATH, "/host/runtime");
  }
  for (const [index, topology] of ["monolith", "distributed"].entries()) {
    assert.equal(renders[15 + index].env.ETHERVIEW_HARDHAT3_IMAGE, "custom-hardhat:local");
    assert.equal(
      renders[15 + index].env.ETHERVIEW_HARDHAT3_API_SERVICE,
      index === 0 ? "etherview" : "api",
    );
    assert.equal(nodes[7 + index].env.ETHERVIEW_HARDHAT3_TOPOLOGY, topology);
    assert.equal(nodes[7 + index].env.ETHERVIEW_HARDHAT3_IMAGE, "custom-hardhat:local");
    assert.equal(nodes[9 + index].env.ETHERVIEW_VERIFICATION_SUITE, "vyper");
    assert.equal(renders[19 + index].env.ETHERVIEW_FOUNDRY_IMAGE, "custom-foundry:local");
    assert.equal(nodes[11 + index].env.ETHERVIEW_FOUNDRY_TOPOLOGY, topology);
  }
  for (const key of applicationKeys) assert.equal(renders.at(-2).env[key], null);
  assert.equal(renders.at(-1).env.ETHERVIEW_LOG_LEVEL, "debug");
  assert.equal(renders[0].env.ETHERVIEW_LOG_LEVEL, "host-level");
});

test("custom command prefixes and literal image arguments survive the extracted runner", (t) => {
  const literal = "literal $(exit 99); 'quoted'";
  const result = fixture(t).run({ HARDHAT3_IMAGE: literal, FOUNDRY_IMAGE: literal }, true);
  assert.equal(result.status, 0, result.stdout + result.stderr);
  assert(result.events.every((event) => event.rawArgs[0] === "prefix"));
  assert(result.events.some((event) => event.env.ETHERVIEW_HARDHAT3_IMAGE === literal));
  assert(result.events.some((event) => event.env.ETHERVIEW_FOUNDRY_IMAGE === literal));
});

test("a failed render cannot feed a validator even when it prints valid JSON", (t) => {
  const result = fixture(t).run({ FAIL_COMPOSE_AT: "6" });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /render diagnostic/);
  assert.match(result.stderr, /S3 monolith: Compose failed \(23\)/);
  assert.equal(result.events.at(-1).command, "compose");
  assert.equal(result.events.filter((e) => e.command === "node").length, 1);
});

test("validator failure preserves diagnostics and stops subsequent scenarios", (t) => {
  const result = fixture(t).run({ FAIL_VALIDATOR: "s3-compose-check.mjs" });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /validator diagnostic/);
  assert.match(result.stderr, /S3 monolith: Node .*failed \(19\)/);
  assert.equal(result.events.at(-1).args[0], ".github/scripts/s3-compose-check.mjs");
  assert.equal(result.events.filter((e) => e.command === "compose").length, 6);
});

for (const [extra, message] of [
  [{ FAIL_COMPOSE_AT: "1" }, /Docker Compose availability/],
  [{ FAIL_VALIDATOR: "compose-layout.test.mjs" }, /Compose layout regressions/],
  [{ BAD_JSON: "1" }, /S3 monolith/],
  [{ BAD_GATEWAY: "1" }, /Preview metadata gateway/],
  [{ BAD_OVERRIDE: "1" }, /application overrides/],
]) {
  test(`Compose checks reject ${Object.keys(extra)[0]}`, (t) => {
    const result = fixture(t).run(extra);
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, message);
  });
}
