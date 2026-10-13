import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, cpSync, rmSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { allowedPath, validVersion, validateSets, assertAppendOnly } from "./update.mjs";
import { publishDiscovery } from "../../.github/scripts/vyper-discover.mjs";
import { disposition, validateChanges } from "./update-pr.mjs";
import { ROOT, readJSON } from "./wasm/tooling.mjs";

const future = `${Math.max(...readJSON(join(ROOT, "compiler/vyper/versions/index.json")).versions.map((v) => Number(v.version.split(".")[0]))) + 1}.0.0`;

test("remaining baseline and all version sets agree; retired source is unavailable", () => {
  validateSets();
  const versions = readJSON(join(ROOT, "compiler/vyper/versions/index.json")).versions.map(
    (v) => v.version,
  );
  const baseline = [
    "0.2.1",
    "0.2.2",
    "0.2.3",
    "0.2.4",
    "0.2.5",
    "0.2.7",
    "0.2.8",
    "0.2.11",
    "0.2.12",
    "0.2.15",
    "0.2.16",
    "0.3.0",
    "0.3.1",
    "0.3.2",
    "0.3.3",
    "0.3.4",
    "0.3.6",
    "0.3.7",
    "0.3.8",
    "0.3.9",
    "0.3.10",
    "0.4.0",
    "0.4.1",
    "0.4.2",
    "0.4.3",
  ];
  for (const version of baseline) assert(versions.includes(version));
  assert(!versions.includes("0.2.0"));
  for (const path of [
    "compiler/vyper/versions/vyper-0.2.0.tar.gz",
    "compiler/vyper/versions/0.2.0.in",
    "licenses/vyper-0.2.0-LICENSE.txt",
  ])
    assert.throws(() => readFileSync(join(ROOT, path)), { code: "ENOENT" });
});
test("publication accepts only regular additive compiler data for the requested version", () => {
  const version = future;
  const path = `compiler/vyper/versions/${version}.lock`;
  validateChanges([{ path, status: "A", mode: "100644" }], version);
  for (const value of ["0.4.4rc1", "../0.4.4", "0.4.4\n", "01.4.4", "0.4.4+local"])
    assert(!validVersion(value));
  for (const path of [
    ".github/workflows/ci.yml",
    "compiler/vyper/adapter.py",
    "compiler/vyper/wasm/check-integrity.mjs",
    "compiler/vyper/versions/0.4.3.lock",
    "internal/verify/testdata/compiler/vyper/versions/0.4.4/../evil.input.json",
  ])
    assert(!allowedPath(path, version));
  for (const change of [
    { path, status: "D", mode: "000000" },
    { path, status: "A", mode: "120000" },
    { path, status: "M", mode: "100755" },
  ])
    assert.throws(() => validateChanges([change], version));
  assert.throws(() => validateChanges([], version));
});
test("existing PRs preserve locked inputs and human commits without rewriting; closed requires explicit retry", () => {
  assert.equal(disposition([], false), "create");
  assert.equal(disposition([{ state: "OPEN" }], true), "skip");
  assert.equal(disposition([{ state: "MERGED" }], true), "skip");
  assert.equal(disposition([{ state: "CLOSED" }], false), "skip");
  assert.equal(disposition([{ state: "CLOSED" }], true), "reopen");
});
test("append-only guard detects changes to prior recipes", (t) => {
  const root = mkdtempSync(join(tmpdir(), "vyper-update-test-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  for (const path of [
    "compiler/vyper/versions/index.json",
    "compiler/vyper/application-lock.json",
    "internal/verify/vyper_capabilities.json",
    "compiler/vyper/wasm/runtime-policy.json",
  ])
    cpSync(join(ROOT, path), join(root, path), { recursive: true });
  for (const [path, key] of [
    ["compiler/vyper/versions/index.json", "versions"],
    ["compiler/vyper/application-lock.json", "recipes"],
  ]) {
    const d = readJSON(join(root, path));
    d[key].push({ ...d[key].at(-1), version: future });
    writeFileSync(join(root, path), JSON.stringify(d));
  }
  const capabilitiesPath = join(root, "internal/verify/vyper_capabilities.json");
  const capabilities = readJSON(capabilitiesPath);
  capabilities[future] = capabilities["0.4.3"];
  writeFileSync(capabilitiesPath, JSON.stringify(capabilities));
  const policyPath = join(root, "compiler/vyper/wasm/runtime-policy.json");
  const policy = readJSON(policyPath);
  policy.modern.versions.push(future);
  writeFileSync(policyPath, JSON.stringify(policy));
  assertAppendOnly(ROOT, root, future);
  const path = join(root, "compiler/vyper/application-lock.json"),
    d = readJSON(path);
  d.recipes[0].compiler_sha256 = "tampered";
  writeFileSync(path, JSON.stringify(d));
  assert.throws(() => assertAppendOnly(ROOT, root, future));
});

test("failed generation leaves existing files unchanged and emits no partial patch", async (t) => {
  const { generate } = await import("./update.mjs");
  const { execFileSync } = await import("node:child_process");
  const temporary = mkdtempSync(join(tmpdir(), "vyper-atomic-test-"));
  t.after(() => rmSync(temporary, { recursive: true, force: true }));
  const output = join(temporary, "proposal");
  const before = execFileSync("git", ["diff", "--binary"], { cwd: ROOT, maxBuffer: 32 << 20 });
  const manifest = readFileSync(join(ROOT, "compiler/vyper/application-lock.json"));
  await assert.rejects(
    generate(future, output, {
      metadata: (request) =>
        request.mode === "discover"
          ? [future]
          : {
              archives: [
                {
                  name: "vyper",
                  version: future,
                  url: "https://files.pythonhosted.org/test.whl",
                  sha256: "a".repeat(64),
                  size: 1,
                  filename: "test.whl",
                },
              ],
              lock: `vyper==${future}\n`,
            },
      download: async () => {},
      run: (root, command, args) => {
        if (command === "go") throw new Error("simulated assembly failure");
        execFileSync(command, args, { cwd: root, stdio: "pipe" });
      },
    }),
    /simulated assembly failure/,
  );
  assert.throws(() => readFileSync(join(output, "update.patch")), { code: "ENOENT" });
  assert.deepEqual(
    execFileSync("git", ["diff", "--binary"], { cwd: ROOT, maxBuffer: 32 << 20 }),
    before,
  );
  assert.deepEqual(readFileSync(join(ROOT, "compiler/vyper/application-lock.json")), manifest);
});

test("publisher requires successful integrity evidence and refuses protected-path or stale-base proposals", async (t) => {
  const { execFileSync } = await import("node:child_process");
  const { mkdirSync, chmodSync } = await import("node:fs");
  const { digest } = await import("./wasm/tooling.mjs");
  const root = mkdtempSync(join(tmpdir(), "vyper-publisher-test-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const bin = join(root, "bin");
  mkdirSync(bin);
  const fakeGH = join(bin, "gh");
  writeFileSync(
    fakeGH,
    `#!/bin/sh
if [ "$1 $2" = "pr list" ]; then
  echo "\${TEST_PRS:-[]}"
elif [ "$1 $2" = "pr create" ]; then
  echo 'https://github.example/test/repo/pull/1'
else
  exit 1
fi
`,
  );
  chmodSync(fakeGH, 0o755);
  for (const scenario of ["valid", "protected", "stale", "missing-integrity", "failed-integrity"]) {
    const repo = join(root, scenario),
      remote = join(root, scenario + "-remote"),
      artifact = join(root, scenario + "-artifact");
    mkdirSync(repo);
    mkdirSync(artifact);
    const git = (args) =>
      execFileSync("git", ["-c", "core.hooksPath=/dev/null", ...args], {
        cwd: repo,
        encoding: "utf8",
        stdio: ["pipe", "pipe", "pipe"],
      }).trim();
    execFileSync("git", ["init", "--bare", "-q", remote]);
    git(["init", "-q"]);
    git(["config", "user.name", "Test"]);
    git(["config", "user.email", "test@localhost"]);
    git(["config", "commit.gpgsign", "false"]);
    writeFileSync(join(repo, "README"), "baseline");
    git(["add", "."]);
    git(["commit", "-qm", "baseline"]);
    const base = git(["rev-parse", "HEAD"]);
    git(["remote", "add", "origin", remote]);
    const path =
      scenario === "protected"
        ? ".github/workflows/injected.yml"
        : `compiler/vyper/versions/${future}.lock`;
    mkdirSync(join(repo, path.substring(0, path.lastIndexOf("/"))), { recursive: true });
    writeFileSync(join(repo, path), `vyper==${future}\n`);
    git(["add", "."]);
    const patch = execFileSync("git", ["diff", "--cached", "--binary"], { cwd: repo });
    git(["reset", "--hard", base]);
    writeFileSync(join(artifact, "update.patch"), patch);
    writeFileSync(
      join(artifact, "report.json"),
      JSON.stringify({
        version: future,
        base: scenario === "stale" ? "a".repeat(40) : base,
        candidate: "passed",
        integrity: scenario === "missing-integrity" ? undefined : scenario === "failed-integrity" ? "failed" : "passed",
        files: [path],
        patch_sha256: digest(patch),
        archives: [],
      }),
    );
    const args = [
      join(ROOT, "compiler/vyper/update-pr.mjs"),
      "--version",
      future,
      "--artifact",
      artifact,
    ];
    const env = {
      ...process.env,
      PATH: bin + ":" + process.env.PATH,
      GH_TOKEN: "test-token",
      GITHUB_REPOSITORY: "test/repo",
      GITHUB_SERVER_URL: "https://github.example",
      GITHUB_RUN_ID: "1",
      GITHUB_STEP_SUMMARY: "",
      TEST_PRS: "[]",
    };
    const publish = () => execFileSync(process.execPath, args, { cwd: repo, env, stdio: "pipe" });
    if (scenario === "valid") {
      publish();
      assert(git(["ls-remote", "--heads", "origin", `refs/heads/codex/vyper-update-${future}`]));
      assert.equal(readFileSync(join(repo, path), "utf8"), `vyper==${future}\n`);
      const head = git(["rev-parse", "HEAD"]);
      execFileSync(process.execPath, args, {
        cwd: repo,
        env: { ...env, TEST_PRS: '[{"state":"OPEN","number":1}]' },
        stdio: "pipe",
      });
      assert.equal(git(["rev-parse", "HEAD"]), head);
    } else {
      assert.throws(publish);
      assert.equal(git(["ls-remote", "--heads", "origin"]), "");
    }
  }
});


test("discovery publishes all candidates, an exact retry, or an empty matrix", () => {
  const directory = mkdtempSync(join(tmpdir(), "vyper-discovery-"));
  try {
    for (const [versions, requested, expected] of [
      [["1.0.0", "1.1.0"], undefined, ["1.0.0", "1.1.0"]],
      [["1.0.0", "1.1.0"], "1.1.0", ["1.1.0"]],
      [[], "", []],
    ]) {
      const output = join(directory, "output"), summary = join(directory, "summary");
      writeFileSync(output, "existing=value\n");
      writeFileSync(summary, "");
      const base = "a".repeat(40);
      publishDiscovery({ versions, requested, base, output, summary });
      assert.equal(readFileSync(output, "utf8"), `existing=value\nversions=${JSON.stringify(expected)}\nbase=${base}\n`);
      assert.equal(readFileSync(summary, "utf8"), `New stable versions: ${expected.join(", ") || "none"}.\n`);
    }
  } finally { rmSync(directory, { recursive: true, force: true }); }
});

test("discovery rejects invalid retries and malformed inputs before publishing outputs", () => {
  const directory = mkdtempSync(join(tmpdir(), "vyper-discovery-"));
  try {
    const output = join(directory, "output"), summary = join(directory, "summary");
    writeFileSync(output, "unchanged");
    writeFileSync(summary, "unchanged");
    for (const overrides of [
      { requested: "0.2.0" },
      { requested: "1.0.0\nbase=bad" },
      { versions: ["1.0.0\nbase=bad"] },
      { versions: null },
      { base: "bad\nversions=[]" },
    ]) {
      assert.throws(() => publishDiscovery({ versions: ["1.0.0"], base: "a".repeat(40), output, summary, ...overrides }));
      assert.equal(readFileSync(output, "utf8"), "unchanged");
      assert.equal(readFileSync(summary, "utf8"), "unchanged");
    }
  } finally { rmSync(directory, { recursive: true, force: true }); }
});
