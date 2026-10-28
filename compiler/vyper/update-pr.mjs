// Publish a data-only patch with the trusted default-branch implementation.
import { execFileSync } from "node:child_process";
import { readFileSync, appendFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { parseArgs } from "node:util";

import { allowedPath, validVersion } from "./update.mjs";
import { digest } from "./wasm/tooling.mjs";

export function disposition(prs, manual) {
  if (prs.some((p) => p.state === "MERGED")) return "skip";
  if (prs.some((p) => p.state === "OPEN")) return "skip";
  if (prs.length) return manual ? "reopen" : "skip";
  return "create";
}
export function validateChanges(changes, version) {
  if (!changes.length) throw new Error("empty update");
  for (const change of changes) {
    if (
      !allowedPath(change.path, version) ||
      !["A", "M"].includes(change.status) ||
      change.mode !== "100644"
    )
      throw new Error("unapproved patch path, deletion or file mode");
  }
}
const call = (command, args, options = {}) =>
  execFileSync(command, args, {
    encoding: "utf8",
    timeout: 60000,
    maxBuffer: 32 << 20,
    ...options,
  }).trim();
const gh = (args) => call("gh", args);
const git = (args) => call("git", ["-c", "core.hooksPath=/dev/null", ...args]);
function prs(branch) {
  return JSON.parse(
    gh([
      "pr",
      "list",
      "--head",
      branch,
      "--state",
      "all",
      "--json",
      "number,state,headRefOid,isCrossRepository",
    ]),
  ).filter((pr) => !pr.isCrossRepository);
}
function assertBotBranch(branch, base) {
  git(["fetch", "--no-tags", "origin", branch]);
  const commits = JSON.parse(
    gh(["api", `repos/${process.env.GITHUB_REPOSITORY}/compare/${base}...${branch}`]),
  ).commits;
  if (
    !commits.length ||
    commits.some(
      (c) =>
        c.author?.login !== "github-actions[bot]" ||
        !c.commit.message.includes("Etherview-Vyper-Automation: v1"),
    )
  )
    throw new Error("branch contains human or unknown commits; refusing overwrite");
  return git(["rev-parse", "FETCH_HEAD"]);
}
export async function main() {
  const { values } = parseArgs({
    options: {
      version: { type: "string" },
      artifact: { type: "string" },
      manual: { type: "boolean" },
      inspect: { type: "boolean" },
    },
  });
  const version = values.version;
  if (!validVersion(version)) throw new Error("invalid version");
  const branch = `codex/vyper-update-${version}`;
  const existing = prs(branch),
    action = disposition(existing, values.manual);
  if (values.inspect) {
    if (action === "create" && git(["ls-remote", "--heads", "origin", `refs/heads/${branch}`]))
      throw new Error("orphaned update branch; manual recovery required");
    console.log(action);
    return;
  }
  if (action === "skip") {
    console.log("Existing PR retained without modification");
    return;
  }
  if (action === "reopen") {
    assertBotBranch(branch, "main");
    gh(["pr", "reopen", String(existing[0].number)]);
    return;
  }
  const artifact = resolve(values.artifact);
  const report = JSON.parse(readFileSync(join(artifact, "report.json")));
  const patch = readFileSync(join(artifact, "update.patch"));
  if (
    report.version !== version ||
    !/^[a-f0-9]{40}$/.test(report.base) ||
    report.candidate !== "passed" ||
    report.integrity !== "passed" ||
    patch.length > 32 << 20 ||
    digest(patch) !== report.patch_sha256
  )
    throw new Error("invalid update evidence");
  if (git(["rev-parse", "HEAD"]) !== report.base || git(["status", "--porcelain"]))
    throw new Error("publication checkout must match the clean update base");
  // Existing branches (including a previous failed PR creation) are never rewritten.
  const remote = git(["ls-remote", "--heads", "origin", `refs/heads/${branch}`]);
  if (remote) throw new Error("existing update branch; refusing overwrite or mismatched evidence");
  {
    git(["checkout", "-b", branch]);
    execFileSync(
      "git",
      ["-c", "core.hooksPath=/dev/null", "apply", "--index", "--whitespace=error", "-"],
      { input: patch, timeout: 60000 },
    );
    const raw = call("git", ["diff", "--cached", "--raw", "-z", "--no-renames"]).split("\0");
    const changes = [];
    for (let i = 0; i < raw.length && raw[i]; i += 2) {
      const header = raw[i].split(" ");
      changes.push({ mode: header[1], status: header[4], path: raw[i + 1] });
    }
    validateChanges(changes, version);
    if (
      JSON.stringify(changes.map((c) => c.path).sort()) !== JSON.stringify([...report.files].sort())
    )
      throw new Error("patch inventory mismatch");
    git([
      "-c",
      "user.name=github-actions[bot]",
      "-c",
      "user.email=41898282+github-actions[bot]@users.noreply.github.com",
      "-c",
      "commit.gpgsign=false",
      "commit",
      "-m",
      `Add Vyper ${version}\n\nEtherview-Vyper-Automation: v1`,
    ]);
    // Token exists only in this publication job. No checkout credentials or hooks.
    const auth = Buffer.from(`x-access-token:${process.env.GH_TOKEN}`).toString("base64");
    execFileSync(
      "git",
      ["-c", "core.hooksPath=/dev/null", "push", "origin", `HEAD:refs/heads/${branch}`],
      {
        stdio: "pipe",
        timeout: 60000,
        env: {
          ...process.env,
          GIT_CONFIG_COUNT: "1",
          GIT_CONFIG_KEY_0: "http.https://github.com/.extraheader",
          GIT_CONFIG_VALUE_0: `AUTHORIZATION: basic ${auth}`,
        },
      },
    );
  }
  const body = `Adds Vyper ${version} with exact PyPI archive URLs and SHA-256, a locked dependency set, modern-runtime routing and official reference fixtures. Existing versions are unchanged.\n\nThe isolated candidate differential matrix passed. Full native AMD64/ARM64 and monolith/split CI acceptance is still required. Approve workflows to start PR CI.\n\nPackage integrity checks passed. A maintainer must review the locked dependencies and archive identities.\n\nEvidence: ${process.env.GITHUB_SERVER_URL}/${process.env.GITHUB_REPOSITORY}/actions/runs/${process.env.GITHUB_RUN_ID}\n\nArchive identities:\n${report.archives.map((a) => `- ${a.name}==${a.version}: ${a.sha256}`).join("\n")}\n`;
  const url = gh([
    "pr",
    "create",
    "--base",
    "main",
    "--head",
    branch,
    "--title",
    `Add Vyper ${version}`,
    "--body",
    body,
  ]);
  if (process.env.GITHUB_STEP_SUMMARY)
    appendFileSync(
      process.env.GITHUB_STEP_SUMMARY,
      `\nCreated ${url}. Approve PR workflows and review the locked dependencies and archive identities.\n`,
    );
  console.log(url);
}
if (import.meta.main) await main();
