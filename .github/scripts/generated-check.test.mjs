import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { cpSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";

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
