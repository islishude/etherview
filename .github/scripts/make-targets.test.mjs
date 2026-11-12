import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  cpSync,
  existsSync,
  mkdtempSync,
  mkdirSync,
  writeFileSync,
  readFileSync,
  rmSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";

const previewHelperImage = "node:26.13.1-slim";
const previewVerifierImage = "etherview-preview-genesis-verify:local";

function fixture(t) {
  const dir = mkdtempSync(join(tmpdir(), "prebuilt targets "));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  writeFileSync(join(dir, "Makefile"), readFileSync(new URL("../../Makefile", import.meta.url)));
  mkdirSync(join(dir, "preview/scripts"), { recursive: true });
  writeFileSync(
    join(dir, "preview/scripts/env.mjs"),
    readFileSync(new URL("../../preview/scripts/env.mjs", import.meta.url)),
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
appendFileSync(process.env.COMMAND_LOG, JSON.stringify({ command: ${JSON.stringify(name)}, args, consensusFull: process.env.PREVIEW_CONSENSUS_FULL }) + "\\n");
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
          "PREVIEW_GATEWAY_IMAGE=caddy:2.11.9-alpine",
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

test("Preview tools install locked dependencies before running and stop if installation fails", (t) => {
  const f = fixture(t);
  const overrides = ["NODE=stub-go", "ARGS=run all"];
  const result = f.run("preview-tool", {}, overrides);
  assert.equal(result.status, 0, result.stdout + result.stderr);
  assert.deepEqual(
    result.commands.map(({ command, args }) => ({ command, args })),
    [
      { command: "pnpm", args: ["install", "--frozen-lockfile"] },
      { command: "go", args: ["preview/contracts/tools/cli.mjs", "run", "all"] },
    ],
  );
  const failed = f.run("preview-tool", { FAIL_COMMAND: "pnpm" }, overrides);
  assert.notEqual(failed.status, 0);
  assert.deepEqual(
    failed.commands.map(({ command }) => command),
    ["pnpm"],
  );
});

test("Preview startup reuses TLS files and generates missing files before starting services", (t) => {
  const f = fixture(t);
  const mkcert = join(f.dir, "bin", "mkcert");
  const caRoot = join(f.dir, "ca-root");
  mkdirSync(caRoot);
  writeFileSync(join(caRoot, "rootCA.pem"), "public CA");
  writeFileSync(
    mkcert,
    `#!${process.execPath}
const fs = require("node:fs");
const args = process.argv.slice(2);
fs.appendFileSync(process.env.COMMAND_LOG, JSON.stringify({ command: "mkcert", args }) + "\\n");
if (args[0] === "-CAROOT") console.log(${JSON.stringify(caRoot)});
if (args[0] === "-ecdsa") {
  fs.writeFileSync(args[args.indexOf("-cert-file") + 1], "certificate");
  fs.writeFileSync(args[args.indexOf("-key-file") + 1], "private key");
}
`,
    { mode: 0o755 },
  );
  const overrides = [
    "NODE=stub-go",
    `MKCERT=${mkcert}`,
    "-o",
    "docker-build",
    "-o",
    "preview-images",
  ];
  const existing = f.run("start-preview", {}, overrides);
  assert.equal(existing.status, 0, existing.stdout + existing.stderr);
  assert.equal(
    existing.commands.some(({ command }) => command === "mkcert"),
    false,
  );
  for (const file of ["gateway.crt", "gateway.key", "rootCA.pem"]) {
    rmSync(join(f.dir, ".local/preview-tls", file));
    const generated = f.run("start-preview", {}, overrides);
    assert.equal(generated.status, 0, generated.stdout + generated.stderr);
    assert.ok(existsSync(join(f.dir, ".local/preview-tls", file)));
    const installed = generated.commands.findIndex(
      ({ command, args }) => command === "mkcert" && args[0] === "-install",
    );
    const started = generated.commands.findIndex(({ command }) => command === "compose");
    assert.ok(installed >= 0 && started > installed);
  }
  rmSync(join(f.dir, ".local/preview-tls/gateway.crt"));
  rmSync(mkcert);
  const failed = f.run("start-preview", {}, overrides);
  assert.notEqual(failed.status, 0);
  assert.ok(failed.stderr.includes("https://github.com/filosottile/mkcert#installation"));
  assert.equal(
    failed.commands.some(({ command }) => command === "compose"),
    false,
  );
});

test("Preview certificate setup explains how to install missing mkcert before creating certificates", (t) => {
  const f = fixture(t);
  const tlsDir = join(f.dir, "new-tls");
  const result = f.run("preview-cert", {}, [
    `MKCERT=${join(f.dir, "missing-mkcert")}`,
    `PREVIEW_TLS_DIR=${tlsDir}`,
  ]);
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /mkcert is required/);
  assert.ok(result.stderr.includes("https://github.com/filosottile/mkcert#installation"));
  assert.ok(result.stderr.includes("rerun 'make preview-cert'"));
  assert.equal(existsSync(tlsDir), false);
});

test("Preview image preparation preserves a prebuilt application and fails before side effects when missing", (t) => {
  const f = fixture(t);
  const prepared = f.run("preview-images-prebuilt");
  assert.equal(prepared.status, 0, prepared.stdout + prepared.stderr);
  assert.deepEqual(prepared.commands[0].args, ["image", "inspect", "etherview:local"]);
  const builds = prepared.commands.filter((command) => command.command === "buildx");
  assert.equal(builds.length, 1);
  assert.ok(builds[0].args.includes("tools/genesis-verify/Dockerfile"));
  assert.ok(!builds[0].args.includes("production"));
  const pull = prepared.commands.find((command) => command.command === "compose");
  assert.ok(pull.args.includes("pull"));
  assert.ok(pull.args.includes("bundler"));
  assert.ok(!pull.args.includes("genesis-verify"));
  const ordinary = f.run("preview-images");
  assert.equal(ordinary.status, 0, ordinary.stdout + ordinary.stderr);
  assert.ok(ordinary.commands[0].args.includes("production"));
  assert.deepEqual(ordinary.commands.slice(1), prepared.commands);

  const missingFixture = fixture(t);
  const missing = missingFixture.run("preview-images-prebuilt", {
    MISSING_IMAGE: "etherview:local",
  });
  assert.notEqual(missing.status, 0);
  assert.deepEqual(missing.commands, [prepared.commands[0]]);
  assert.ok(!existsSync(join(missingFixture.dir, ".local/preview.env")));
  for (const command of ["buildx", "compose"]) {
    const failed = f.run("preview-images-prebuilt", { FAIL_COMMAND: command });
    assert.notEqual(failed.status, 0);
    assert.equal(failed.commands.at(-1).command, command);
  }
});

for (const suite of [
  "schema-e2e",
  "runtime-e2e",
  "preview-metadata",
  "preview-consensus",
  "preview-consensus-full",
  "preview-x402",
  "preview-contracts",
  "preview-tools-e2e",
  "preview-tools-e2e-full",
]) {
  const target = `test-${suite}`;
  const preview = suite.startsWith("preview-");
  test(`${target}: prepared entry point does not build; ordinary entry point prepares first`, (t) => {
    const f = fixture(t);
    const prebuilt = f.run(`${target}-prebuilt`);
    assert.equal(prebuilt.status, 0, prebuilt.stdout + prebuilt.stderr);
    assert.equal(existsSync(join(f.dir, ".local/preview.env")), false);
    assert.deepEqual(
      prebuilt.commands.map((c) => c.command),
      preview ? ["docker", "docker", "docker", "docker", "go"] : ["docker", "go"],
    );
    assert.deepEqual(prebuilt.commands[0].args, ["image", "inspect", "etherview:local"]);
    if (preview)
      assert.deepEqual(prebuilt.commands[1].args, ["image", "inspect", "caddy:2.11.9-alpine"]);
    if (suite.startsWith("preview-tools-e2e")) {
      const args = prebuilt.commands.at(-1).args;
      const full = suite.endsWith("-full");
      assert.equal(
        args[args.indexOf("-run") + 1],
        full ? "^TestPreviewToolsFull$" : "^TestPreviewTools$",
      );
      assert.ok(args.includes(full ? "-timeout=60m" : "-timeout=15m"));
      assert.equal(prebuilt.stdout.includes("PREVIEW_TOOLS_FULL=1"), full);
    }
    if (suite.startsWith("preview-consensus")) {
      const args = prebuilt.commands.at(-1).args;
      const full = suite.endsWith("-full");
      assert.equal(
        args[args.indexOf("-run") + 1],
        full ? "^TestPreviewConsensusFull$" : "^TestPreviewConsensus$",
      );
      assert.ok(args.includes(full ? "-timeout=25m" : "-timeout=10m"));
      assert.equal(prebuilt.commands.at(-1).consensusFull, full ? "1" : undefined);
    }
    const ordinary = f.run(target);
    assert.equal(ordinary.status, 0, ordinary.stdout + ordinary.stderr);
    assert.equal(existsSync(join(f.dir, ".local/preview.env")), preview);
    const builds = ordinary.commands.filter((c) => c.command === "buildx");
    assert.equal(builds.length, preview ? 2 : 1);
    assert(builds[0].args.includes("production"));
    assert.deepEqual(ordinary.commands.slice(-prebuilt.commands.length), prebuilt.commands);
    assert.equal(ordinary.commands.filter((c) => c.command === "compose").length, preview ? 1 : 0);
  });

  test(`${target}: missing images and failed tests stop the command`, (t) => {
    const f = fixture(t);
    for (const missing of preview
      ? ["etherview:local", "caddy:2.11.9-alpine", previewHelperImage, previewVerifierImage]
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

const makefile = resolve(import.meta.dirname, "../../Makefile");

test("ordinary Web builds consume the committed SDK without regeneration", () => {
  const result = spawnSync("make", ["-n", "web-build"], {
    cwd: resolve(import.meta.dirname, "../.."),
    encoding: "utf8",
  });
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /--dir web run build/);
  assert.doesNotMatch(result.stdout, /generate:api|openapi-ts/);
});

test("full generation regenerates the SDK before building the Web app", () => {
  const result = spawnSync("make", ["-n", "-j4", "generate"], {
    cwd: resolve(import.meta.dirname, "../.."),
    encoding: "utf8",
  });
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /--dir api run generate:api[\s\S]*--dir web run build/);
  assert.equal(result.stdout.match(/--dir api run generate:api/g)?.length, 1);
});

for (const change of ["none", "added", "removed", "modified"]) {
  test(`generate-check recursively detects ${change} generated files`, (t) => {
    const root = mkdtempSync(join(tmpdir(), "etherview-generation-"));
    t.after(() => rmSync(root, { recursive: true, force: true }));
    const generated = join(root, "generated");
    mkdirSync(join(generated, "client"), { recursive: true });
    writeFileSync(join(generated, "client/index.ts"), "export {};\n");
    cpSync(generated, join(root, "expected"), { recursive: true });
    if (change === "added") writeFileSync(join(generated, "stale.ts"), "export {};\n");
    if (change === "removed") rmSync(join(generated, "client/index.ts"));
    if (change === "modified")
      writeFileSync(join(generated, "client/index.ts"), "export const drift = true;\n");
    const generator = join(root, "generate.mjs");
    writeFileSync(
      generator,
      `import { cpSync, rmSync } from "node:fs";
rmSync("generated", { recursive: true, force: true });
cpSync("expected", "generated", { recursive: true });
`,
    );
    // Use the real snapshot/recursive-diff recipe, replacing only its generator.
    const result = spawnSync(
      "make",
      [
        "-f",
        makefile,
        "generate-check",
        "GENERATED_PATHS=generated",
        `MAKE=${process.execPath} ${generator}`,
      ],
      { cwd: root, encoding: "utf8" },
    );
    const output = result.stdout + result.stderr;
    if (change === "none") assert.equal(result.status, 0, output);
    else {
      assert.notEqual(result.status, 0, output);
      assert.match(output, /Only in|diff -ru/);
    }
  });
}

test("Preview network checks install locked dependencies before tests and stop on install failure", (t) => {
  const f = fixture(t);
  const result = f.run("preview-network-check", {}, ["NODE=stub-go"]);
  assert.equal(result.status, 0, result.stdout + result.stderr);
  assert.deepEqual(result.commands[0], {
    command: "pnpm",
    args: ["install", "--frozen-lockfile"],
  });
  assert.deepEqual(result.commands[1].args, ["test", "./tools/genesis-verify"]);
  assert.ok(result.commands[2].args.includes("preview/contracts/admin.test.mjs"));
  const failed = f.run("preview-network-check", { FAIL_COMMAND: "pnpm" }, ["NODE=stub-go"]);
  assert.notEqual(failed.status, 0);
  assert.deepEqual(
    failed.commands.map(({ command }) => command),
    ["pnpm"],
  );
});

test("deployment CI sets up the repository toolchains before invoking Make", () => {
  const workflow = readFileSync(new URL("../workflows/ci-checks.yml", import.meta.url), "utf8");
  const deployment = workflow.slice(workflow.indexOf("  deployment:"));
  const setup = deployment.indexOf("uses: ./.github/actions/setup-toolchain");
  assert.ok(setup >= 0);
  assert.ok(setup < deployment.indexOf("run: make deployment-check"));
});
