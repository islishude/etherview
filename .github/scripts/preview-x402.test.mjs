import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";

function fixture(t) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "preview-x402-"));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  fs.writeFileSync(path.join(directory, "forge"), "#!/bin/sh\nprintf '0x1234\\n'\n", {
    mode: 0o700,
  });
  fs.writeFileSync(
    path.join(directory, "cast"),
    `#!/bin/sh
set -eu
echo "$1" >> "$STATE/calls"
case "$1" in
  chain-id) echo "\${CHAIN:-48815}";;
  code) if [ -f "$STATE/code" ]; then cat "$STATE/code"; else echo 0x; fi;;
  nonce) echo "\${NONCE:-0}";;
  balance) echo 0;;
  abi-encode) echo 0xabc;;
  send) case "$*" in *--create*) echo 0x1234 > "$STATE/code";; esac;;
  *) exit 91;;
esac
`,
    { mode: 0o700 },
  );
  return {
    directory,
    run(overrides = {}) {
      return spawnSync("sh", ["deploy/preview/x402-init.sh"], {
        encoding: "utf8",
        env: {
          ...process.env,
          PATH: `${directory}:${process.env.PATH}`,
          STATE: directory,
          RPC_URL: "http://geth:8545",
          ...overrides,
        },
      });
    },
    calls() {
      return fs.readFileSync(path.join(directory, "calls"), "utf8");
    },
  };
}

test("Preview initialization deploys once and preserves spent state on reuse", (t) => {
  const f = fixture(t);
  const first = f.run();
  assert.equal(first.status, 0, first.stderr);
  assert.equal(
    f
      .calls()
      .split("\n")
      .filter((c) => c === "send").length,
    2,
  );
  const second = f.run({ NONCE: "15" });
  assert.equal(second.status, 0, second.stderr);
  assert.equal(
    f
      .calls()
      .split("\n")
      .filter((c) => c === "send").length,
    2,
  );
});

for (const [name, overrides, code] of [
  ["wrong chain", { CHAIN: "1" }],
  ["used deployer without token", { NONCE: "2" }],
  ["unexpected deployed code", {}, "0x9876"],
]) {
  test(`Preview initialization rejects ${name} without sending a transaction`, (t) => {
    const f = fixture(t);
    if (code) fs.writeFileSync(path.join(f.directory, "code"), code);
    assert.notEqual(f.run(overrides).status, 0);
    assert.ok(!f.calls().split("\n").includes("send"));
  });
}

test("Preview lifecycle propagates x402 init failure after PoS readiness", () => {
  const makefile = fs.readFileSync("Makefile", "utf8");
  for (const target of ["start-preview", "recreate-preview"]) {
    const recipe = makefile.split(`\n${target}:`)[1].split(/\n\S/)[0];
    assert.ok(recipe.indexOf("run --rm x402-init") > recipe.indexOf("preview-ready.mjs"));
    assert.match(recipe, /\n\tDOCKER=.*run --rm x402-init\n/);
  }
  assert.doesNotMatch(makefile.match(/^PREVIEW_RUNTIME_SERVICES :=.*$/m)[0], /x402-init/);
});
