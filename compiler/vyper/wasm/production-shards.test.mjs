import assert from "node:assert/strict";
import test from "node:test";
import {
  assignments,
  mergeEvidence,
  partition,
  shardFile,
  shardOptions,
  versions,
} from "./production-shards.mjs";
import { fixtureIdentity, sha256, runtimeFor, sharedRuntimes } from "./release.mjs";
import { versionEvidence } from "./production-shards.mjs";

const groups = assignments(4);
const identity = {
  native_linux: true,
  image_id: "sha256:image",
  host_sha256: "host",
  shared_runtimes: sharedRuntimes,
};
const expected = Object.fromEntries(
  versions.map(({ version }) => [
    version,
    {
      name: `vyper-${version}-emscripten-wasm32.json`,
      digest: `descriptor-${version}`,
      matrix: {
        ...fixtureIdentity(version),
        package_sha256: `package-${version}`,
        shared_sha256: runtimeFor(version).shared_sha256,
        executor_sha256: `executor-${version}`,
      },
    },
  ]),
);
function records() {
  return groups.map((selected, index) => ({
    ...structuredClone(identity),
    shard: { index, count: 4, architecture: "amd64" },
    descriptors: Object.fromEntries(
      selected.map((version) => [expected[version].name, expected[version].digest]),
    ),
    matrix: Object.fromEntries(
      selected.map((version) => [version, structuredClone(expected[version].matrix)]),
    ),
  }));
}
const merge = (rows) => mergeEvidence(rows, groups, "amd64", identity, expected);

test("CLI preserves unsharded default and rejects invalid/incomplete options", () => {
  assert.equal(shardOptions([]), null);
  assert.deepEqual(shardOptions(["--shard-index", "0", "--shard-count", "4"]), {
    index: 0,
    count: 4,
  });
  assert.deepEqual(shardOptions(["--shard-count", "4"], true), { count: 4 });
  for (const args of [
    ["--shard-index", "0"],
    ["--shard-count", "4"],
    ["--shard-index", "4", "--shard-count", "4"],
    ["--shard-index", "-1", "--shard-count", "4"],
    ["--shard-index", "1.5", "--shard-count", "4"],
    ["--shard-index", "0", "--shard-count", "0"],
    ["--shard-index", "0", "--shard-count", "27"],
    ["--shard-index", "0", "--shard-count", "NaN"],
    ["--other", "1"],
  ])
    assert.throws(() => shardOptions(args));
  assert.throws(() => shardOptions([], true));
});

test("partition is deterministic, balanced and covers all current fixtures exactly once", () => {
  const items = versions.map(({ version }) => ({ version, ...fixtureIdentity(version) }));
  assert.deepEqual(partition(items.reverse(), 4), groups);
  assert.deepEqual(groups.flat().sort(), versions.map((item) => item.version).sort());
  const loads = groups.map((group) =>
    group.reduce((sum, version) => sum + fixtureIdentity(version).cases, 0),
  );
  assert(Math.max(...loads) - Math.min(...loads) <= Math.max(...items.map((item) => item.cases)));
  assert.equal(
    loads.reduce((a, b) => a + b),
    items.reduce((sum, item) => sum + item.cases, 0),
  );
  assert.deepEqual(
    partition(
      [
        { version: "0.10.0", cases: 2 },
        { version: "0.2.0", cases: 2 },
      ],
      2,
    ),
    [["0.2.0"], ["0.10.0"]],
  );
  assert.throws(() => partition(items, 0));
  assert.throws(() => partition([items[0], items[0]], 2));
  assert.equal(new Set(groups.map((_, index) => shardFile("amd64", index, 4))).size, 4);
  assert.notEqual(shardFile("amd64", 0, 4), shardFile("arm64", 0, 4));
});

test("merge is order independent and preserves the complete dual-runtime evidence shape", () => {
  const full = { ...identity, descriptors: {}, matrix: {} };
  for (const [version, entry] of Object.entries(expected)) {
    full.descriptors[entry.name] = entry.digest;
    full.matrix[version] = entry.matrix;
  }
  assert.deepEqual(merge(records().reverse()), full);
});

test("merge rejects missing, extra, duplicate and misassigned shards or versions", () => {
  assert.throws(() => merge(records().slice(1)));
  assert.throws(() => merge([...records(), records()[0]]));
  for (const mutate of [
    (rows) => {
      rows[1] = rows[0];
    },
    (rows) => {
      rows[0].shard.index = -1;
    },
    (rows) => {
      rows[0].shard.count = 3;
    },
    (rows) => {
      rows[0].shard.architecture = "arm64";
    },
    (rows) => {
      delete rows[0].matrix[groups[0][0]];
    },
    (rows) => {
      rows[0].matrix.extra = {};
    },
    (rows) => {
      rows[0].matrix[groups[1][0]] = rows[1].matrix[groups[1][0]];
    },
    (rows) => {
      rows[0].descriptors.extra = "digest";
    },
    (rows) => {
      delete rows[0].descriptors[expected[groups[0][0]].name];
    },
  ]) {
    const rows = records();
    mutate(rows);
    assert.throws(() => merge(rows));
  }
});

test("merge rejects stale image, runtime, descriptor, package and fixture identities", () => {
  for (const key of Object.keys(identity)) {
    const rows = records();
    rows[0][key] = "stale";
    assert.throws(() => merge(rows));
  }
  for (const key of Object.keys(expected[groups[0][0]].matrix)) {
    const rows = records();
    rows[0].matrix[groups[0][0]][key] = "stale";
    assert.throws(() => merge(rows));
  }
  const rows = records();
  rows[0].descriptors[expected[groups[0][0]].name] = "stale";
  assert.throws(() => merge(rows));
});

// Exercise real subprocess timeout/cancellation and per-container cleanup without Docker.
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync, rmSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { compile } from "./production-process.mjs";

test("compiler subprocess preserves stdin/output and cleans only its container on failures", async () => {
  const directory = mkdtempSync(join(tmpdir(), "vyper-process-test-"));
  try {
    const docker = join(directory, "docker");
    const log = join(directory, "removed");
    const ready = join(directory, "ready");
    writeFileSync(
      docker,
      `#!/usr/bin/env node
import { appendFileSync, writeFileSync } from 'node:fs';
const args = process.argv.slice(2);
if (args[0] === 'rm') { appendFileSync(${JSON.stringify(log)}, args[2] + '\\n'); }
else if (args.includes('fail')) { process.exitCode = 9; }
else if (args.includes('wait')) { process.on('SIGTERM', () => {}); setTimeout(() => process.exit(0), 2000); writeFileSync(${JSON.stringify(ready)}, 'ready'); setInterval(() => {}, 1000); }
else { process.stdin.pipe(process.stdout); }
`,
      { mode: 0o755 },
    );
    // .mjs extension makes the fixture independent of the host's default module mode.
    const executable = docker + ".mjs";
    writeFileSync(executable, readFileSync(docker), { mode: 0o755 });
    const success = await compile(executable, ["run", "--name", "success"], "input", undefined);
    assert.equal(success.stdout, "input");
    assert(!existsSync(log));
    await assert.rejects(compile(executable, ["run", "--name", "failure", "fail"], "", undefined));
    await assert.rejects(
      compile(executable, ["run", "--name", "timeout", "wait"], "", undefined, 100),
      { signal: "SIGKILL" },
    );
    rmSync(ready, { force: true });
    const abort = new AbortController();
    const cancelled = compile(executable, ["run", "--name", "cancelled", "wait"], "", abort.signal);
    const rejected = assert.rejects(cancelled, { name: "AbortError" });
    const deadline = Date.now() + 5000;
    while (!existsSync(ready) && Date.now() < deadline)
      await new Promise((resolve) => setTimeout(resolve, 10));
    abort.abort();
    await rejected;
    assert(existsSync(ready), "child started before cancellation");
    assert.deepEqual(readFileSync(log, "utf8").trim().split("\n"), [
      "failure",
      "timeout",
      "cancelled",
    ]);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("evidence validation rereads archive, manifest and package bytes", () => {
  const directory = mkdtempSync(join(tmpdir(), "vyper-evidence-test-"));
  try {
    const item = versions[0];
    const pkg = join(directory, item.version);
    mkdirSync(pkg);
    const archive = Buffer.from("archive");
    const bundle = Buffer.from("bundle");
    const manifest = Buffer.from(
      JSON.stringify({ files: [{ path: "packages.zip", sha256: sha256(bundle) }] }),
    );
    const descriptor = {
      compiler_sha256: item.compiler_sha256,
      shared_sha256: runtimeFor(item.version).shared_sha256,
      archive: "compiler.tar.gz",
      sha256: sha256(archive),
      manifest_sha256: sha256(manifest),
    };
    writeFileSync(join(directory, "compiler.tar.gz"), archive);
    writeFileSync(join(pkg, "packages.zip"), bundle);
    writeFileSync(join(pkg, "package-manifest.json"), manifest);
    writeFileSync(
      join(directory, `vyper-${item.version}-emscripten-wasm32.json`),
      JSON.stringify(descriptor),
    );
    const validate = () => versionEvidence(item, directory, directory, "a".repeat(64));
    assert.equal(validate().matrix.package_sha256, sha256(manifest));
    for (const [path, original] of [
      [join(directory, "compiler.tar.gz"), archive],
      [join(pkg, "packages.zip"), bundle],
      [join(pkg, "package-manifest.json"), manifest],
    ]) {
      writeFileSync(path, "tampered");
      assert.throws(validate);
      writeFileSync(path, original);
    }
    assert.deepEqual(validate().matrix.cases, fixtureIdentity(item.version).cases);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("merge rejects missing or exchanged runtime-map members", () => {
  for (const mutate of [
    (map) => {
      delete map.legacy;
    },
    (map) => {
      map.extra = map.modern;
    },
    (map) => {
      [map.modern, map.legacy] = [map.legacy, map.modern];
    },
  ]) {
    const rows = records();
    mutate(rows[0].shared_runtimes);
    assert.throws(() => merge(rows));
  }
});
