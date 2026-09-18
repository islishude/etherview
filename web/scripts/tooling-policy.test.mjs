import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const web = fileURLToPath(new URL("../", import.meta.url));

function fixture(t, files) {
  const root = mkdtempSync(join(tmpdir(), "etherview-tooling-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  for (const config of [".oxlintrc.json", ".oxfmtrc.json"]) {
    copyFileSync(join(web, config), join(root, config));
  }
  for (const [name, content] of Object.entries(files)) {
    mkdirSync(dirname(join(root, name)), { recursive: true });
    writeFileSync(join(root, name), content);
  }
  return root;
}

function run(root, tool, args) {
  const result = spawnSync(
    process.execPath,
    [join(web, "node_modules", tool, "bin", tool), ...args],
    {
      cwd: root,
      encoding: "utf8",
      timeout: 20_000,
    },
  );
  assert.ifError(result.error);
  assert.equal(result.signal, null);
  return { status: result.status, output: result.stdout + result.stderr };
}

function lint(root) {
  return run(root, "oxlint", ["--format", "json", "src"]);
}

function rejects(result, rule) {
  assert.equal(result.status, 1, result.output);
  assert.match(result.output, new RegExp(`\\(${rule}\\)`));
}

const sizedFunction = (lines) =>
  `export function example() {\n${"  // counted comment\n".repeat(lines - 2)}}\n`;
const sizedFile = (lines) =>
  Array.from({ length: lines }, (_, i) => `export const value${i} = ${i};\n`).join("");
const complexFunction = (branches) =>
  `export function example(value: number) {\n${Array.from({ length: branches }, (_, i) => `  if (value === ${i}) return ${i};\n`).join("")}  return -1;\n}\n`;

test("lint rejects unused imports/variables and both hook violations", (t) => {
  const root = fixture(t, {
    "src/unused.ts": 'import { missing } from "./other";\nconst unused = 1;\nexport {};\n',
    "src/hooks.tsx":
      'import { useEffect } from "react";\nexport function Example({ value }: { value: number }) {\nif (value) useEffect(() => { console.log(value); }, []);\nreturn null;\n}\n',
  });
  const result = lint(root);
  for (const rule of ["no-unused-vars", "rules-of-hooks", "exhaustive-deps"]) rejects(result, rule);
  assert.match(result.output, /missing/);
  assert.match(result.output, /unused/);
});

test("classic complexity accepts 150 paths, rejects 151, and excludes tests", (t) => {
  const root = fixture(t, { "src/example.ts": complexFunction(149) });
  assert.equal(lint(root).status, 0);
  writeFileSync(join(root, "src/example.ts"), complexFunction(150));
  rejects(lint(root), "complexity");
  writeFileSync(join(root, "src/example.ts"), "export {};\n");
  for (const name of [
    "src/example.test.ts",
    "src/example.test.tsx",
    "src/test/helper.ts",
    "src/test/helper.tsx",
  ]) {
    mkdirSync(dirname(join(root, name)), { recursive: true });
    writeFileSync(join(root, name), complexFunction(150));
  }
  assert.equal(lint(root).status, 0);
});

test("function size retains production/test limits and counts comments but not blanks", (t) => {
  const root = fixture(t, { "src/example.ts": sizedFunction(400) + "\n".repeat(50) });
  assert.equal(lint(root).status, 0);
  writeFileSync(join(root, "src/example.ts"), sizedFunction(401));
  rejects(lint(root), "max-lines-per-function");
  writeFileSync(join(root, "src/example.ts"), "export {};\n");
  for (const name of [
    "src/example.test.ts",
    "src/example.test.tsx",
    "src/test/helper.ts",
    "src/test/helper.tsx",
  ]) {
    mkdirSync(dirname(join(root, name)), { recursive: true });
    writeFileSync(join(root, name), sizedFunction(1000));
    assert.equal(lint(root).status, 0);
    writeFileSync(join(root, name), sizedFunction(1001));
    rejects(lint(root), "max-lines-per-function");
    writeFileSync(join(root, name), "export {};\n");
  }
  writeFileSync(join(root, "src/example.ts"), `(() => {\n${"// comment\n".repeat(400)}})();\n`);
  assert.equal(lint(root).status, 0);
});

test("file size enforces production/test limits and excludes generated API types", (t) => {
  const root = fixture(t, {
    "src/example.ts": sizedFile(1400),
    "src/api/schema.gen.ts": sizedFile(2600),
  });
  assert.equal(lint(root).status, 0);
  writeFileSync(join(root, "src/example.ts"), sizedFile(1401));
  rejects(lint(root), "max-lines");
  writeFileSync(join(root, "src/example.ts"), "export {};\n");
  for (const name of [
    "src/example.test.ts",
    "src/example.test.tsx",
    "src/test/helper.ts",
    "src/test/helper.tsx",
  ]) {
    mkdirSync(dirname(join(root, name)), { recursive: true });
    writeFileSync(join(root, name), sizedFile(2500));
    assert.equal(lint(root).status, 0);
    writeFileSync(join(root, name), sizedFile(2501));
    rejects(lint(root), "max-lines");
    writeFileSync(join(root, name), "export {};\n");
  }
});

test("format gate covers hand-written frontend files, preserves exclusions, and is idempotent", (t) => {
  const ignored = {
    "src/api/schema.gen.ts": "export type Generated={value:string}",
    "package-lock.json": '{"lockfileVersion":3}',
    "dist/output.js": "const x=1",
    "e2e/server/main.go": "package main",
  };
  const root = fixture(t, {
    ...ignored,
    "src/example.ts": "export const value={name:'value'}",
    "src/example.css": "body{color:red}",
    "scripts/example.mjs": "export const value=1",
    "e2e/example.spec.ts": "export const value=1",
    "vite.config.ts": "export default {}",
    "index.html": "<html><body><main>Example</main></body></html>",
    "package.json": '{"z":1,"a":2}',
  });
  assert.equal(run(root, "oxfmt", ["--check", "."]).status, 1);
  const formatted = run(root, "oxfmt", ["--write", "."]);
  assert.equal(formatted.status, 0, formatted.output);
  assert.equal(run(root, "oxfmt", ["--check", "."]).status, 0);
  const before = readFileSync(join(root, "src/example.ts"), "utf8");
  assert.equal(before, 'export const value = { name: "value" };\n');
  assert.equal(run(root, "oxfmt", ["--write", "."]).status, 0);
  assert.equal(readFileSync(join(root, "src/example.ts"), "utf8"), before);
  const packageJSON = readFileSync(join(root, "package.json"), "utf8");
  assert.ok(packageJSON.indexOf('"z"') < packageJSON.indexOf('"a"'));
  for (const [name, content] of Object.entries(ignored))
    assert.equal(readFileSync(join(root, name), "utf8"), content);
});
