import test from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

function fixture(t) {
  const dir = mkdtempSync(join(tmpdir(), "prebuilt targets "));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  writeFileSync(join(dir, "Makefile"), readFileSync(new URL("../../Makefile", import.meta.url)));
  mkdirSync(join(dir, "bin"));
  mkdirSync(join(dir, ".local/preview-tls"), { recursive: true });
  for (const file of ["tls.crt", "tls.key", "rootCA.pem", "ipfs.crt", "ipfs.key"])
    writeFileSync(join(dir, ".local/preview-tls", file), "fixture");
  const log = join(dir, "commands.jsonl");
  for (const name of ["docker", "go", "buildx", "compose"])
    writeFileSync(
      join(dir, "bin", `stub-${name}`),
      `#!${process.execPath}
import { appendFileSync } from "node:fs";
const args = process.argv.slice(2);
appendFileSync(process.env.COMMAND_LOG, JSON.stringify({ command: ${JSON.stringify(name)}, args }) + "\\n");
if (${JSON.stringify(name)} === process.env.FAIL_COMMAND) process.exit(9);
if (${JSON.stringify(name)} === "docker" && args[0] === "image" && args[1] === "inspect" && args[2] === process.env.MISSING_IMAGE) process.exit(1);
`,
      { mode: 0o755 },
    );
  return {
    dir,
    run(target, extra = {}) {
      writeFileSync(log, "");
      const result = spawnSync(
        "make",
        [
          "--no-print-directory",
          target,
          "GO=stub-go",
          "DOCKER=stub-docker",
          "BUILDX=stub-buildx",
          "COMPOSE=stub-compose",
          "IMAGE=etherview:local",
          "IPFS_GATEWAY_IMAGE=etherview-ipfs-gateway:local",
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

for (const suite of ["schema-e2e", "preview-metadata", "preview-consensus"]) {
  const target = `test-${suite}`;
  const preview = suite.startsWith("preview-");
  test(`${target}: prepared entry point does not build; ordinary entry point prepares first`, (t) => {
    const f = fixture(t);
    const prebuilt = f.run(`${target}-prebuilt`);
    assert.equal(prebuilt.status, 0, prebuilt.stdout + prebuilt.stderr);
    assert.deepEqual(
      prebuilt.commands.map((c) => c.command),
      preview ? ["docker", "docker", "go"] : ["docker", "go"],
    );
    assert.deepEqual(prebuilt.commands[0].args, ["image", "inspect", "etherview:local"]);
    if (preview)
      assert.deepEqual(prebuilt.commands[1].args, [
        "image",
        "inspect",
        "etherview-ipfs-gateway:local",
      ]);
    const ordinary = f.run(target);
    assert.equal(ordinary.status, 0, ordinary.stdout + ordinary.stderr);
    const builds = ordinary.commands.filter((c) => c.command === "buildx");
    assert.equal(builds.length, preview ? 2 : 1);
    assert(builds[0].args.includes("production"));
    assert.deepEqual(ordinary.commands.slice(-prebuilt.commands.length), prebuilt.commands);
    assert.equal(ordinary.commands.filter((c) => c.command === "compose").length, preview ? 1 : 0);
  });

  test(`${target}: missing images and failed tests stop the command`, (t) => {
    const f = fixture(t);
    for (const missing of preview
      ? ["etherview:local", "etherview-ipfs-gateway:local"]
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
      rmSync(join(f.dir, ".local/preview-tls/ipfs.key"));
      const result = f.run(`${target}-prebuilt`);
      assert.notEqual(result.status, 0);
      assert.deepEqual(result.commands, []);
      assert.match(result.stdout, /certificate.*missing/);
    });
}
