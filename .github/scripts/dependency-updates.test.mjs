import assert from "node:assert/strict";
import {
  mkdtempSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
  rmSync,
  cpSync,
  existsSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve, dirname } from "node:path";
import test from "node:test";

import {
  publish,
  branchName,
  disposition,
  validateReport,
  validateChanges,
  assertBotCommits,
} from "./dependency-update-pr.mjs";
import {
  audit,
  currentVersion,
  readRegistry,
  replacements,
  compare,
  compatible,
  selectVersions,
  discover,
  releases,
  fetchData,
  prepare,
  git,
  digest,
  run,
} from "./dependency-updates.mjs";

const root = resolve(import.meta.dirname, "../..");
function sandbox(fn) {
  const dir = mkdtempSync(join(tmpdir(), "dependency-test-"));
  try {
    return fn(dir);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}
function fixture(dir, commands = []) {
  mkdirSync(join(dir, ".github"), { recursive: true });
  const dep = {
    id: "example",
    source: "npm",
    package: "example",
    policy: "auto",
    reason: "",
    pins: [
      { path: "Makefile", template: "EXAMPLE_VERSION ?= v{{version}}", count: 1 },
      { path: "linked.txt", template: "example {{version}} / {{version}}", count: 1 },
    ],
    commands,
    generated: ["lock.json"],
    ignores: { npm: ["example"] },
  };
  writeFileSync(
    join(dir, ".github/dependency-registry.json"),
    JSON.stringify({ schema: 1, dependencies: [dep] }),
  );
  writeFileSync(
    join(dir, ".github/dependabot.yml"),
    'updates:\n  - package-ecosystem: npm\n    ignore:\n      - dependency-name: "example"\n  - package-ecosystem: docker\n    directories:\n      - /e2e/*\n  - package-ecosystem: docker-compose\n    directories:\n      - /e2e/*\n      - /deploy\n',
  );
  writeFileSync(
    join(dir, "Makefile"),
    "EXAMPLE_VERSION ?= v1.2.3\n.PHONY: test-workflow-scripts docs-check\ntest-workflow-scripts docs-check:\n\t@true\n",
  );
  writeFileSync(join(dir, "linked.txt"), "example 1.2.3 / 1.2.3\n");
  writeFileSync(join(dir, "lock.json"), "{}\n");
  git(["init", "-q"], dir);
  git(["add", "."], dir);
  git(
    [
      "-c",
      "user.name=Test",
      "-c",
      "user.email=test@localhost",
      "-c",
      "commit.gpgsign=false",
      "commit",
      "-qm",
      "base",
    ],
    dir,
  );
  return dep;
}

test("numeric stable versions, major and zero-major boundaries", () => {
  assert.ok(compare("1.10.0", "1.9.9") > 0);
  assert.equal(compare("26.3", "26.3.0"), 0);
  assert.equal(compatible("0.13.4", "0.13.5"), true);
  assert.equal(compatible("0.13.4", "0.14.0"), false);
  assert.deepEqual(
    selectVersions(
      "1.2.3",
      ["1.2.2", "1.2.3", "1.2.4", "1.10.0", "2.0.0", "1.11.0-rc1", "latest", "01.4.0"],
      "auto",
    ),
    { latest: "2.0.0", candidate: "1.10.0" },
  );
  assert.deepEqual(selectVersions("1.2.3", ["1.2.4"], "report"), {
    latest: "1.2.4",
    candidate: null,
  });
});
test("registry coverage and every linked position are valid", () => {
  const registry = audit(root);
  assert.equal(
    currentVersion(
      registry.dependencies.find((d) => d.id === "govulncheck"),
      root,
    ),
    readFileSync(join(root, "Makefile"), "utf8").match(/^GOVULNCHECK_VERSION \?= v(.*)$/m)[1],
  );
  for (const id of [
    "govulncheck",
    "gitleaks",
    "go-licenses",
    "golangci-lint",
    "license-checker",
    "mkcert",
    "go",
    "node",
    "pnpm",
    "uv",
    "packaging",
    "foundry",
    "caddy",
    "pyodide",
    "pyodide-legacy",
    "geas",
    "bloomfilter",
    "go-base36",
  ])
    assert.ok(registry.dependencies.some((d) => d.id === id));
});
test("all automatic entries update every registered linked occurrence", () =>
  sandbox((dir) => {
    const registry = readRegistry(root);
    const files = new Set([
      ".github/dependency-registry.json",
      ".github/dependabot.yml",
      ...registry.dependencies.flatMap((d) => [
        ...d.pins.map((p) => p.path),
        ...(d.reviewFiles ?? []),
      ]),
    ]);
    for (const dep of registry.dependencies.filter((d) => d.policy === "auto")) {
      for (const path of files) {
        mkdirSync(dirname(join(dir, path)), { recursive: true });
        cpSync(join(root, path), join(dir, path));
      }
      const parts = currentVersion(dep, dir).split(".");
      parts[parts.length - 1] = String(Number(parts.at(-1)) + 1);
      const version = parts.join(".");
      for (const [path, content] of replacements(dep, version, dir))
        writeFileSync(join(dir, path), content);
      assert.equal(currentVersion(dep, dir), version, dep.id);
      audit(dir);
    }
  }));
test("missing pins, duplicate owners, uncovered Makefile versions and Dependabot ownership fail", () =>
  sandbox((dir) => {
    fixture(dir);
    const linked = join(dir, "linked.txt");
    writeFileSync(linked, "example 1.2.3 / 1.2.4\n");
    assert.throws(() => audit(dir), /position drift/);
    writeFileSync(linked, "example 1.2.4 / 1.2.4\n");
    assert.throws(() => audit(dir), /disagree/);
    writeFileSync(linked, "example 1.2.3 / 1.2.3\n");
    writeFileSync(
      join(dir, "Makefile"),
      readFileSync(join(dir, "Makefile")) + "OTHER_VERSION ?= 1.0.0\n",
    );
    assert.throws(() => audit(dir), /unregistered/);
    git(["restore", "Makefile"], dir);
    const registryFile = join(dir, ".github/dependency-registry.json");
    const original = readFileSync(registryFile, "utf8");
    const duplicate = JSON.parse(original);
    duplicate.dependencies.push({ ...duplicate.dependencies[0], id: "duplicate" });
    writeFileSync(registryFile, JSON.stringify(duplicate));
    assert.throws(() => audit(dir), /duplicate pin ownership/);
    writeFileSync(registryFile, original);
    writeFileSync(join(dir, ".github/dependabot.yml"), "updates: []\n");
    assert.throws(() => audit(dir), /exclusion/);
  }));
test("discovery preserves network failures and explicit version errors", async () => {
  const registry = readRegistry(root);
  const rows = await discover(registry, root, "govulncheck", undefined, async () => {
    throw new Error("timeout");
  });
  assert.equal(rows[0].status, "error");
  assert.equal(rows[0].reason, "timeout");
  const major = await discover(registry, root, "govulncheck", "9.0.0", async () => ["9.0.0"]);
  assert.equal(major[0].status, "error");
  const special = await discover(registry, root, "node", undefined, async () => ["99.0.0"]);
  assert.equal(special[0].status, "review");
});
test("official release adapters filter revoked, yanked and prerelease releases and paginate", async () => {
  assert.deepEqual(
    await releases({ source: "npm", package: "x" }, async () => ({
      versions: { "1.0.0": {}, "1.1.0": { deprecated: "bad" } },
    })),
    ["1.0.0"],
  );
  assert.deepEqual(
    await releases({ source: "pypi", package: "x" }, async () => ({
      releases: { "1.0": [{ yanked: true }], 1.1: [{ yanked: false }], 1.2: [] },
    })),
    ["1.1"],
  );
  let calls = 0;
  const github = await releases({ source: "github", package: "a/b" }, async () =>
    ++calls === 1
      ? Array.from({ length: 100 }, () => ({ tag_name: "v2.0.0", prerelease: true }))
      : [{ tag_name: "v1.4.0" }],
  );
  assert.equal(calls, 2);
  assert.deepEqual(github, ["1.4.0"]);
  assert.deepEqual(
    await releases({ source: "github-tags", package: "golang/vuln" }, async () => [
      { name: "v1.9.0" },
      { name: "v2.0.0" },
    ]),
    ["1.9.0", "2.0.0"],
  );
  let seen;
  await releases({ source: "gomod", package: "example.com/Owner/module" }, async (url) => {
    seen = url;
    return "v1.0.0\nv1.2.0-rc1\n";
  });
  assert.match(seen, /!owner/);
});
test("bounded queries reject HTTP failures, invalid JSON and timeouts", async () => {
  await assert.rejects(
    fetchData(
      "https://pypi.org/pypi/x/json",
      true,
      async () => new Response("denied", { status: 403 }),
    ),
    /release query failed/,
  );
  await assert.rejects(
    fetchData("https://pypi.org/pypi/x/json", true, async () => new Response("invalid")),
    /release query failed/,
  );
  await assert.rejects(
    fetchData("https://pypi.org/pypi/x/json", true, async () => {
      throw new DOMException("expired", "TimeoutError");
    }),
    /timeout/,
  );
  await assert.rejects(fetchData("https://evil.example/a"), /unsupported/);
  let attempts = 0;
  assert.deepEqual(
    await fetchData("https://pypi.org/pypi/x/json", true, async () =>
      ++attempts < 3 ? new Response("busy", { status: 503 }) : new Response("{}"),
    ),
    {},
  );
  assert.equal(attempts, 3);
});
test("clean isolated preparation applies linked changes, does not commit, and retains exact evidence", () =>
  sandbox((dir) => {
    const dep = fixture(dir),
      head = git(["rev-parse", "HEAD"], dir);
    prepare(dep, "1.2.4", dir);
    assert.equal(currentVersion(dep, dir), "1.2.4");
    assert.equal(git(["rev-parse", "HEAD"], dir), head);
    assert.equal(git(["diff", "--cached", "--name-only"], dir), "");
    assert.throws(() => prepare(dep, "1.2.5", dir), /clean/);
  }));
test("failed checks, unexpected writes and untracked work never apply partial changes", () =>
  sandbox((dir) => {
    const dep = fixture(dir, [[process.execPath, "-e", "process.exit(7)"]]);
    assert.throws(() => prepare(dep, "1.2.4", dir));
    assert.equal(git(["status", "--porcelain"], dir), "");
    dep.commands = [
      [process.execPath, "-e", "require('fs').writeFileSync('unexpected.txt','bad')"],
    ];
    assert.throws(() => prepare(dep, "1.2.4", dir), /unexpected/);
    assert.equal(git(["status", "--porcelain"], dir), "");
    writeFileSync(join(dir, "user.txt"), "keep");
    assert.throws(() => prepare(dep, "1.2.4", dir), /clean/);
    assert.equal(readFileSync(join(dir, "user.txt"), "utf8"), "keep");
  }));
test("publication evidence binds base, paths, content hash and file modes", () =>
  sandbox((dir) => {
    const dep = fixture(dir),
      patch = Buffer.from("patch");
    const base = git(["rev-parse", "HEAD"], dir);
    const report = {
      schema: 1,
      dependency: dep.id,
      version: "1.2.4",
      base,
      files: ["Makefile"],
      checks: "passed",
      patch_sha256: digest(patch),
    };
    validateReport(report, patch, dep, base, "1.2.4");
    for (const change of [
      { base: "0".repeat(40) },
      { files: [".github/workflows/ci.yml"] },
      { checks: "failed" },
      { patch_sha256: "bad" },
      { files: ["Makefile", "Makefile"] },
    ])
      assert.throws(() => validateReport({ ...report, ...change }, patch, dep, base, "1.2.4"));
    validateChanges(":100644 100644 a b M\0Makefile\0", report);
    validateChanges(":100755 100755 a b M\0Makefile\0", report);
    assert.throws(() => validateChanges(":100644 100755 a b M\0Makefile\0", report), /mode/);
    assert.throws(() => validateChanges(":100644 100644 a b M\0other\0", report), /inventory/);
    assert.throws(() => branchName("../bad", "1.0.0"));
    for (const state of ["OPEN", "CLOSED", "MERGED"])
      assert.equal(disposition([{ state }]), "retain");
    assert.throws(
      () =>
        assertBotCommits([
          {
            author: { login: "human" },
            commit: { message: "Etherview-Dependency-Automation: v1" },
          },
        ]),
      /human/,
    );
  }));
test("publication rejects source changes beyond the registered version replacement", async () => {
  const dir = mkdtempSync(join(tmpdir(), "dependency-tamper-"));
  try {
    const checkout = join(dir, "checkout"),
      artifact = join(dir, "artifact");
    mkdirSync(checkout);
    const dep = fixture(checkout);
    prepare(dep, "1.2.4", checkout, artifact);
    run("git", ["apply", "-"], checkout, { input: readFileSync(join(artifact, "update.patch")) });
    const makefile = join(checkout, "Makefile");
    writeFileSync(makefile, readFileSync(makefile, "utf8").replace("@true", "@echo unapproved"));
    const patch = git(["diff", "--binary"], checkout) + "\n";
    const report = JSON.parse(readFileSync(join(artifact, "report.json")));
    report.patch_sha256 = digest(patch);
    writeFileSync(join(artifact, "report.json"), JSON.stringify(report));
    writeFileSync(join(artifact, "update.patch"), patch);
    git(["restore", "."], checkout);
    let calls = 0;
    await assert.rejects(
      publish({ root: checkout, dependency: dep.id, version: "1.2.4", artifact }, (args) => {
        calls++;
        assert.deepEqual(args.slice(0, 2), ["pr", "list"]);
        return "[]";
      }),
      /source patch differs/,
    );
    assert.equal(calls, 1);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
test("publication recovers failed PR creation without rewriting a branch, then retains the PR", async () => {
  const dir = mkdtempSync(join(tmpdir(), "dependency-publish-"));
  try {
    const checkout = join(dir, "checkout"),
      remote = join(dir, "remote.git"),
      artifact = join(dir, "artifact");
    mkdirSync(checkout);
    mkdirSync(remote);
    const dep = fixture(checkout),
      base = git(["rev-parse", "HEAD"], checkout);
    git(["init", "--bare", "-q"], remote);
    git(["remote", "add", "origin", remote], checkout);
    git(["push", "origin", "HEAD:refs/heads/main"], checkout);
    prepare(dep, "1.2.4", checkout, artifact);
    let fail = true,
      opened = false,
      creates = 0;
    const api = (args) => {
      if (args[0] === "pr" && args[1] === "list")
        return JSON.stringify(opened ? [{ state: "OPEN", isCrossRepository: false }] : []);
      if (args[0] === "api")
        return JSON.stringify({
          commits: [
            {
              author: { login: "github-actions[bot]" },
              commit: { message: "Etherview-Dependency-Automation: v1" },
            },
          ],
        });
      if (args[1] === "create") {
        creates++;
        if (fail) throw new Error("permission failure");
        opened = true;
        return "https://github.com/example/repo/pull/1";
      }
      throw new Error("unexpected API operation");
    };
    const args = { root: checkout, dependency: dep.id, version: "1.2.4", artifact };
    await assert.rejects(publish(args, api), /permission/);
    const branch = branchName(dep.id, "1.2.4"),
      pushed = git(["rev-parse", branch], checkout);
    git(["checkout", "--detach", base], checkout);
    fail = false;
    await publish(args, api);
    assert.equal(git(["rev-parse", branch], checkout), pushed);
    assert.equal(creates, 2);
    assert.equal(await publish(args, api), "retain");
    assert.equal(creates, 2);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
test("workflow isolates publication rights and keeps candidate validation in ordinary CI", () => {
  const workflow = readFileSync(join(root, ".github/workflows/dependency-updates.yml"), "utf8");
  assert.match(workflow, /47 1 \* \* 1/);
  const [before, publishJob] = workflow.split("\n  publish:\n");
  assert.ok(!before.includes("contents: write"));
  assert.ok(!before.includes("pull-requests: write"));
  assert.match(publishJob, /contents: write/);
  assert.match(publishJob, /pull-requests: write/);
  assert.ok(!workflow.includes("pull_request_target"));
  assert.match(
    readFileSync(join(root, "Makefile"), "utf8"),
    /test-workflow-scripts: test-dependency-updates/,
  );
});
