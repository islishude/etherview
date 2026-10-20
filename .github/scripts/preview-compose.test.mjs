import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { runCompose, composeEndpoint } from "./preview-compose.mjs";
import { composeEnvironment } from "./preview-network.mjs";

function fixture(t) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "preview compose "));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  const script = path.join(directory, "compose wrapper");
  fs.writeFileSync(
    script,
    `#!/bin/sh
if [ "$1" = "prefix" ]; then shift; fi
case "$*" in
  *config*) printf 'PREVIEW_NETWORK_DIR=/bundle with spaces\\n';;
  *port*) printf '0.0.0.0:12345\\n';;
  fail) exit 23;;
  *) printf '%s\\n' "$@";;
esac
`,
    { mode: 0o700 },
  );
  return script;
}

test("Compose accepts executable paths with spaces and command plus arguments", (t) => {
  const script = fixture(t);
  const literal = ["two words", "$(exit 99)", "; false", "'quoted'"];
  for (const command of [script, `/bin/sh '${script}' prefix`]) {
    assert.equal(runCompose(literal, {}, command), literal.join("\n") + "\n");
    assert.equal(composeEnvironment(command).PREVIEW_NETWORK_DIR, "/bundle with spaces");
    assert.throws(() => runCompose(["fail"], {}, command), /failed \(23\)/);
  }
});

test("readiness resolves ports using the same Compose command contract", (t) => {
  const script = fixture(t);
  const previous = process.env.COMPOSE;
  t.after(() => {
    if (previous === undefined) delete process.env.COMPOSE;
    else process.env.COMPOSE = previous;
  });
  process.env.COMPOSE = `/bin/sh '${script}' prefix`;
  assert.equal(composeEndpoint("lighthouse", 5052), "http://127.0.0.1:12345");
  process.env.COMPOSE = "/missing/compose-command";
  assert.throws(() => composeEndpoint("geth", 8545), /Compose failed/);
});

test("start and recreate propagate explicit archive initialization failures", () => {
  const makefile = fs.readFileSync(new URL("../../Makefile", import.meta.url), "utf8");
  for (const target of ["start-preview", "recreate-preview"]) {
    const recipe = makefile.split(`\n${target}:`)[1].split(/\n\S/)[0];
    const up = recipe.indexOf("up ");
    const initialize = recipe.indexOf("$(COMPOSE) -f compose.yaml run --rm archive-init");
    assert.ok(up >= 0 && initialize > up, `${target}: initialize after core startup`);
    assert.match(recipe, /\n\tDOCKER=.*run --rm archive-init\n/);
    assert.ok(initialize < recipe.indexOf(".github/scripts/preview-ready.mjs"));
  }
  assert.doesNotMatch(makefile.match(/^PREVIEW_RUNTIME_SERVICES :=.*$/m)[0], /archive-init/);
  assert.match(
    makefile.split("\nstop-preview:")[1].split(/\n\S/)[0],
    /--profile archive-init down --volumes --remove-orphans/,
  );
});
