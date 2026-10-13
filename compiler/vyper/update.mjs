// P30-T130: reviewed build-time updates only. Never imported by production.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import {
  mkdtempSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
  copyFileSync,
  rmSync,
  existsSync,
  readdirSync,
  lstatSync,
  renameSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { parseArgs } from "node:util";
import { ROOT, readJSON, digest } from "./wasm/tooling.mjs";

export const validVersion = (value) =>
  /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(value) && value.length < 64;
export function allowedPath(path, version) {
  if (!validVersion(version)) return false;
  return (
    [
      "compiler/vyper/versions/index.json",
      `compiler/vyper/versions/${version}.lock`,
      "compiler/vyper/application-lock.json",
      "compiler/vyper/wasm/runtime-policy.json",
      "internal/verify/vyper_capabilities.json",
      "internal/verify/vyper_runtime_policy_generated.go",
    ].includes(path) ||
    new RegExp(
      `^internal/verify/testdata/compiler/vyper/versions/${version.replaceAll(".", "\\.")}/[a-z_]+\\.(input|output|modified|modified_output)\\.json$`,
    ).test(path)
  );
}
export function validateSets(root = ROOT) {
  const read = (path) => readJSON(join(root, path));
  const index = read("compiler/vyper/versions/index.json").versions;
  const recipes = read("compiler/vyper/application-lock.json").recipes;
  const policy = read("compiler/vyper/wasm/runtime-policy.json");
  const expected = index.map((v) => v.version).sort();
  assert(expected.length > 0 && !expected.includes("0.2.0"));
  assert.equal(new Set(expected).size, expected.length);
  for (const actual of [
    recipes.map((r) => r.version),
    Object.values(policy).flatMap((p) => p.versions),
    Object.keys(read("internal/verify/vyper_capabilities.json")),
    readdirSync(join(root, "internal/verify/testdata/compiler/vyper/versions")),
  ]) {
    assert.deepEqual(actual.sort(), expected, "Vyper version sets differ");
  }
  for (const item of index) {
    assert(validVersion(item.version));
    const recipe = recipes.find((r) => r.version === item.version);
    const runtime = Object.values(policy).find((p) => p.versions.includes(item.version));
    assert.equal(recipe.compiler_sha256, item.compiler_sha256);
    assert.equal(recipe.python, runtime.python);
    assert.equal(recipe.pyodide, runtime.pyodide);
    assert(existsSync(join(root, `compiler/vyper/versions/${item.version}.lock`)));
    const fixtures = readdirSync(
      join(root, `internal/verify/testdata/compiler/vyper/versions/${item.version}`),
    );
    for (const stem of ["plain", "constructor", "invalid"])
      for (const suffix of ["input", "output", "modified", "modified_output"])
        assert(fixtures.includes(`${stem}.${suffix}.json`));
  }
}
export function metadata(request) {
  return JSON.parse(
    execFileSync(
      "uv",
      [
        "run",
        "--project",
        join(ROOT, "compiler/vyper"),
        "--locked",
        "python",
        join(ROOT, "compiler/vyper/update-metadata.py"),
      ],
      {
        input: JSON.stringify(request),
        encoding: "utf8",
        timeout: 600000,
        maxBuffer: 32 << 20,
        stdio: ["pipe", "pipe", "inherit"],
      },
    ),
  );
}
const run = (root, command, args, options = {}) =>
  execFileSync(command, args, { cwd: root, stdio: "inherit", timeout: 900000, ...options });
const save = (path, data) => writeFileSync(path, JSON.stringify(data, null, 2) + "\n");

export function assertAppendOnly(before, after, version) {
  for (const [path, key] of [
    ["compiler/vyper/versions/index.json", "versions"],
    ["compiler/vyper/application-lock.json", "recipes"],
  ]) {
    const old = readJSON(join(before, path))[key],
      next = readJSON(join(after, path))[key];
    assert.deepEqual(
      next.filter((v) => v.version !== version),
      old,
      "existing compiler inputs changed",
    );
    assert.equal(next.filter((v) => v.version === version).length, 1);
  }
  const old = readJSON(join(before, "internal/verify/vyper_capabilities.json"));
  const next = readJSON(join(after, "internal/verify/vyper_capabilities.json"));
  assert(Object.hasOwn(next, version), "new capabilities missing");
  delete next[version];
  assert.deepEqual(next, old, "existing capabilities changed");
  const policyPath = "compiler/vyper/wasm/runtime-policy.json";
  const policy = readJSON(join(after, policyPath));
  assert.equal(policy.modern.versions.filter((v) => v === version).length, 1);
  policy.modern.versions = policy.modern.versions.filter((v) => v !== version);
  assert.deepEqual(policy, readJSON(join(before, policyPath)), "existing runtime policy changed");
}

export async function generate(version, output, services = {}) {
  if (!validVersion(version) || version === "0.2.0") throw new Error("invalid update version");
  const { download } = await import("./wasm/packages.mjs");
  const root = services.root ?? ROOT;
  const readMetadata = services.metadata ?? metadata;
  const execute = services.run ?? run;
  const fetchArchive = services.download ?? download;
  validateSets(root);
  const index = readJSON(join(root, "compiler/vyper/versions/index.json"));
  const available = readMetadata({
    mode: "discover",
    current: index.versions.map((v) => v.version),
  });
  if (!available.includes(version)) throw new Error("not a new stable PyPI version");
  if (existsSync(output)) throw new Error("output directory must not exist");
  const temporary = mkdtempSync(join(tmpdir(), "etherview-vyper-update-"));
  const tree = join(temporary, "repo");
  mkdirSync(tree);
  try {
    // Copy the actual checked-out files, including local implementation changes.
    // No original tracked files are written, even if generation fails halfway.
    const files = execFileSync(
      "git",
      ["ls-files", "--cached", "--others", "--exclude-standard", "-z"],
      { cwd: root, encoding: "utf8" },
    )
      .split("\0")
      .filter(Boolean);
    for (const name of files) {
      const source = join(root, name);
      if (!existsSync(source)) continue;
      if (!lstatSync(source).isFile()) throw new Error("non-regular tracked input");
      mkdirSync(dirname(join(tree, name)), { recursive: true });
      copyFileSync(source, join(tree, name));
    }
    execute(tree, "git", ["init", "-q"]);
    execute(tree, "git", ["add", "."]);
    execute(tree, "git", [
      "-c",
      "core.hooksPath=/dev/null",
      "-c",
      "user.name=Vyper updater",
      "-c",
      "user.email=vyper-updater@localhost",
      "-c",
      "commit.gpgsign=false",
      "commit",
      "-qm",
      "Update baseline",
    ]);
    const policyPath = join(tree, "compiler/vyper/wasm/runtime-policy.json");
    const policy = readJSON(policyPath),
      runtime = policy.modern;
    const resolved = readMetadata({
      mode: "resolve",
      version,
      python: runtime.python,
      pycryptodome: runtime.pycryptodome,
    });
    const compiler = resolved.archives.find((a) => a.name === "vyper" && a.version === version);
    if (!compiler) throw new Error("missing exact compiler archive");
    for (const archive of resolved.archives)
      await fetchArchive(archive.url, archive.sha256, join(tree, ".local/vyper-wasm/downloads"));
    writeFileSync(join(tree, `compiler/vyper/versions/${version}.lock`), resolved.lock);
    index.versions.push({
      version,
      source_url: compiler.url,
      compiler_sha256: compiler.sha256,
      python_family: runtime.python,
      python: runtime.python,
    });
    save(join(tree, "compiler/vyper/versions/index.json"), index);
    policy.modern.versions.push(version);
    save(policyPath, policy);
    const lockPath = join(tree, "compiler/vyper/application-lock.json");
    const lock = readJSON(lockPath);
    lock.recipes.push({
      version,
      compiler_sha256: compiler.sha256,
      pyodide: runtime.pyodide,
      python: runtime.python,
      archives: resolved.archives,
      manifest: {},
    });
    save(lockPath, lock);
    execute(tree, "go", [
      "run",
      "./compiler/vyper/cmd/bundle",
      "--version",
      version,
      "--update-lock",
      "compiler/vyper/application-lock.json",
    ]);
    execute(tree, process.execPath, ["compiler/vyper/wasm/generate-policy.mjs"]);
    execute(tree, "npm", ["--prefix", "compiler", "ci", "--ignore-scripts"]);
    execute(tree, process.execPath, ["compiler/vyper/reference.mjs", "--version", version]);
    execute(tree, "uv", [
      "run",
      "--project",
      "compiler/vyper",
      "--locked",
      "python",
      "compiler/vyper/fixtures_versions.py",
      "--version",
      version,
    ]);
    validateSets(tree);
    assertAppendOnly(root, tree, version);
    execute(tree, process.execPath, ["compiler/vyper/wasm/check.mjs"], {
      env: { ...process.env, ETHERVIEW_VYPER_CHECK_VERSION: version },
    });
    execute(tree, process.execPath, ["compiler/vyper/wasm/packages.mjs"]);
    execute(tree, process.execPath, ["compiler/vyper/wasm/check-integrity.mjs"]);
    execute(tree, "git", ["add", "."]);
    const names = execFileSync("git", ["diff", "--cached", "--name-only"], {
      cwd: tree,
      encoding: "utf8",
    })
      .trim()
      .split("\n");
    if (!names.every((name) => allowedPath(name, version)))
      throw new Error("update touched unapproved files");
    const patch = execFileSync("git", ["diff", "--cached", "--binary"], {
      cwd: tree,
      maxBuffer: 32 << 20,
    });
    const proposal = join(temporary, "proposal");
    mkdirSync(proposal);
    writeFileSync(join(proposal, "update.patch"), patch);
    save(join(proposal, "report.json"), {
      version,
      patch_sha256: digest(patch),
      files: names,
      archives: resolved.archives,
      candidate: "passed",
      integrity: "passed",
      base: execFileSync("git", ["rev-parse", "HEAD"], { cwd: root, encoding: "utf8" }).trim(),
    });
    mkdirSync(dirname(output), { recursive: true });
    renameSync(proposal, output);
  } finally {
    rmSync(temporary, { recursive: true, force: true });
  }
}
if (import.meta.main) {
  const { values } = parseArgs({
    options: {
      discover: { type: "boolean" },
      check: { type: "boolean" },
      version: { type: "string" },
      output: { type: "string" },
    },
  });
  if (values.check) validateSets();
  else if (values.discover)
    console.log(
      JSON.stringify(
        metadata({
          mode: "discover",
          current: readJSON(join(ROOT, "compiler/vyper/versions/index.json")).versions.map(
            (v) => v.version,
          ),
        }),
      ),
    );
  else if (values.version && values.output) {
    try {
      await generate(values.version, resolve(values.output));
    } catch (error) {
      if (!existsSync(resolve(values.output))) {
        mkdirSync(resolve(values.output), { recursive: true });
        save(join(resolve(values.output), "failure.json"), {
          version: values.version,
          status: "failed",
          error: String(error.message).slice(0, 8192),
        });
      }
      throw error;
    }
  } else throw new Error("use --discover, --check, or --version X --output DIRECTORY");
}
