import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { manageCache } from "./compiler-cache.mjs";

function fixture(t) {
  const parent = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "preview-cache-test-")));
  const root = path.join(parent, "compilers");
  const cache = path.join(root, "cache");
  fs.mkdirSync(cache, { recursive: true });
  fs.chmodSync(root, 0o750);
  fs.chmodSync(cache, 0o750);
  t.after(() => fs.rmSync(parent, { recursive: true, force: true }));
  return { parent, root, cache };
}

function applicationView(t, uid = 65532, gid = 65532) {
  t.mock.method(process, "getuid", () => 65532);
  t.mock.method(process, "getgid", () => 65532);
  const lstat = fs.lstatSync;
  t.mock.method(fs, "lstatSync", (...args) => Object.assign(lstat(...args), { uid, gid }));
}

test("mapped root view permits repeat init; application view verifies without changing artifacts", (t) => {
  const { root, cache } = fixture(t);
  const artifact = path.join(cache, "artifact");
  fs.writeFileSync(artifact, "authenticated artifact", { mode: 0o400 });
  const before = fs.statSync(artifact);
  applicationView(t, 0, 0);
  const chown = t.mock.method(fs, "fchownSync", () =>
    assert.fail("must not repair existing cache"),
  );
  manageCache("init", root);
  manageCache("init", root);
  assert.throws(() => manageCache("verify", root), /ownership\/mode differs/);
  t.mock.restoreAll();
  applicationView(t);
  manageCache("verify", root);
  manageCache("verify", root);
  assert.equal(chown.mock.callCount(), 0);
  assert.deepEqual(fs.readdirSync(cache), ["artifact"]);
  assert.equal(fs.readFileSync(artifact, "utf8"), "authenticated artifact");
  assert.equal(fs.statSync(artifact).ino, before.ino);
  assert.equal(fs.statSync(artifact).mode, before.mode);
});

for (const [uid, gid] of [
  [0, 65532],
  [65532, 0],
]) {
  test(`application view rejects mismatched owner ${uid}:${gid}`, (t) => {
    const { root } = fixture(t);
    applicationView(t, uid, gid);
    assert.throws(() => manageCache("verify", root), /ownership\/mode differs/);
  });
}

for (const target of ["root", "cache"]) {
  test(`rejects unsafe ${target} mode without repairing it`, (t) => {
    const paths = fixture(t);
    fs.chmodSync(paths[target], 0o777);
    applicationView(t);
    for (const action of ["init", "verify"])
      assert.throws(() => manageCache(action, paths.root), /ownership\/mode differs/);
    assert.equal(fs.statSync(paths[target]).mode & 0o7777, 0o777);
  });
}

test("rejects redirected cache and unrelated parent entries", (t) => {
  const { parent, root, cache } = fixture(t);
  fs.rmdirSync(cache);
  fs.symlinkSync(parent, cache);
  assert.throws(() => manageCache("init", root), /ordinary directory/);
  applicationView(t);
  assert.throws(() => manageCache("verify", root), /ordinary directory/);
  fs.unlinkSync(cache);
  fs.writeFileSync(path.join(root, "unrelated"), "keep");
  assert.throws(() => manageCache("init", root), /unrelated entries/);
});

test("successful metadata checks cannot hide write failure; probe is removed", (t) => {
  const { root, cache } = fixture(t);
  applicationView(t);
  t.mock.method(fs, "writeFileSync", () => {
    throw new Error("EACCES probe write");
  });
  assert.throws(() => manageCache("verify", root), /EACCES/);
  assert.deepEqual(fs.readdirSync(cache), []);
});

test("root cannot run the application access check", (t) => {
  const { root } = fixture(t);
  t.mock.method(process, "getuid", () => 0);
  assert.throws(() => manageCache("verify", root), /requires application UID/);
});

for (const failure of ["", "init", "verify"]) {
  test(`host orchestration runs isolated unprivileged verification and propagates ${failure || "no"} failure`, (t) => {
    const { parent, root } = fixture(t);
    const docker = path.join(parent, "docker.mjs");
    const compose = path.join(parent, "compose.mjs");
    const log = path.join(parent, "calls.jsonl");
    fs.writeFileSync(
      compose,
      `console.log(${JSON.stringify(JSON.stringify({ services: { api: { volumes: [{ type: "bind", source: root, target: "/var/lib/etherview/compilers" }] } } }))});`,
    );
    fs.writeFileSync(
      docker,
      `import fs from 'node:fs'; const args=process.argv.slice(2); fs.appendFileSync(${JSON.stringify(log)}, JSON.stringify(args)+'\\n'); if(args.at(-1)===${JSON.stringify(failure)}) process.exit(1);`,
    );
    const result = spawnSync(
      process.execPath,
      [path.join(import.meta.dirname, "compiler-cache.mjs"), "init"],
      {
        encoding: "utf8",
        env: { ...process.env, DOCKER: `node '${docker}'`, COMPOSE: `node '${compose}'` },
      },
    );
    assert.equal(result.status, failure ? 1 : 0, result.stderr);
    const calls = fs.readFileSync(log, "utf8").trim().split("\n").map(JSON.parse);
    assert.equal(calls.length, failure === "init" ? 1 : 2);
    assert.ok(calls[0].includes("--user=0:0"));
    if (calls[1]) {
      assert.equal(calls[1].at(-1), "verify");
      assert.ok(calls[1].includes("--user=65532:65532"));
      assert.ok(!calls[1].some((arg) => arg.startsWith("--cap-add=")));
    }
    for (const args of calls) {
      for (const flag of [
        "--network=none",
        "--read-only",
        "--cap-drop=ALL",
        "--security-opt=no-new-privileges:true",
      ])
        assert.ok(args.includes(flag));
      assert.equal(args.filter((arg) => arg === "--mount").length, 2);
    }
  });
}
