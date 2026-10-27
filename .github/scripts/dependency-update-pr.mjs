// Publication uses only the trusted base implementation and a validated data-only patch.
import { readFileSync, appendFileSync, lstatSync } from "node:fs";
import { join, resolve } from "node:path";
import { parseArgs } from "node:util";
import {
  audit,
  clean,
  digest,
  git,
  replacements,
  run,
  versionParts,
} from "./dependency-updates.mjs";

export function branchName(dependency, version) {
  if (!/^[a-z][a-z0-9-]*$/.test(dependency) || !versionParts(version))
    throw new Error("invalid update identity");
  return `codex/dependency-update-${dependency}-${version}`;
}
export function disposition(prs) {
  return prs.length ? "retain" : "create";
}
export function validateReport(report, patch, dep, base, version) {
  const allowed = new Set([...dep.pins.map((p) => p.path), ...dep.generated]);
  if (
    report.schema !== 1 ||
    report.dependency !== dep.id ||
    report.version !== version ||
    report.base !== base ||
    !/^[a-f0-9]{40}$/.test(base) ||
    report.checks !== "passed" ||
    patch.length > 32 << 20 ||
    report.patch_sha256 !== digest(patch) ||
    !Array.isArray(report.files) ||
    !report.files.length ||
    new Set(report.files).size !== report.files.length ||
    report.files.some((p) => !allowed.has(p))
  )
    throw new Error("invalid candidate evidence");
}
export function validateChanges(raw, report) {
  const fields = raw.split("\0"),
    paths = [];
  for (let i = 0; i < fields.length && fields[i]; i += 2) {
    const [oldMode, newMode, , , status] = fields[i].split(" ");
    if (![":100644", ":100755"].includes(oldMode) || oldMode.slice(1) !== newMode || status !== "M")
      throw new Error("unexpected file mode, addition or deletion");
    paths.push(fields[i + 1]);
  }
  if (JSON.stringify(paths.sort()) !== JSON.stringify([...report.files].sort()))
    throw new Error("patch inventory mismatch");
}
export function assertBotCommits(commits) {
  if (
    !commits.length ||
    commits.some(
      (c) =>
        c.author?.login !== "github-actions[bot]" ||
        !c.commit.message.includes("Etherview-Dependency-Automation: v1"),
    )
  )
    throw new Error("branch contains human or unknown commits; refusing overwrite");
}
export async function publish(
  { root, dependency, version, artifact, inspect = false },
  api = (args) => run("gh", args, root),
) {
  const registry = audit(root),
    dep = registry.dependencies.find((d) => d.id === dependency);
  if (!dep || dep.policy !== "auto") throw new Error("unknown or report-only dependency");
  const branch = branchName(dependency, version);
  const prs = JSON.parse(
    api([
      "pr",
      "list",
      "--head",
      branch,
      "--base",
      "main",
      "--state",
      "all",
      "--json",
      "number,state,isCrossRepository,url",
    ]),
  ).filter((p) => !p.isCrossRepository);
  if (disposition(prs) === "retain") {
    console.log("Existing open, closed or merged PR retained without modification");
    return "retain";
  }
  if (inspect) return "create";
  clean(root);
  const base = git(["rev-parse", "HEAD"], root);
  for (const name of ["report.json", "update.patch"]) {
    const stat = lstatSync(join(artifact, name));
    if (!stat.isFile() || stat.size > 32 << 20) throw new Error("invalid candidate artifact");
  }
  const report = JSON.parse(readFileSync(join(artifact, "report.json"), "utf8"));
  const patch = readFileSync(join(artifact, "update.patch"));
  validateReport(report, patch, dep, base, version);
  const expected = replacements(dep, version, root);
  run(
    "git",
    ["-c", "core.hooksPath=/dev/null", "apply", "--index", "--whitespace=error", "-"],
    root,
    { input: patch },
  );
  validateChanges(run("git", ["diff", "--cached", "--raw", "-z", "--no-renames"], root), report);
  for (const [path, content] of expected)
    if (readFileSync(join(root, path), "utf8") !== content)
      throw new Error("source patch differs from the registered version replacement");
  const tree = git(["write-tree"], root);
  // Recover a push that succeeded before PR creation failed; never force-push.
  const remote = git(["ls-remote", "--heads", "origin", `refs/heads/${branch}`], root);
  if (remote) {
    git(["fetch", "--no-tags", "origin", branch], root);
    assertBotCommits(
      JSON.parse(api(["api", `repos/${process.env.GITHUB_REPOSITORY}/compare/${base}...${branch}`]))
        .commits,
    );
    if (
      git(["rev-parse", "FETCH_HEAD^{tree}"], root) !== tree ||
      git(["rev-parse", "FETCH_HEAD^"], root) !== base
    )
      throw new Error("existing branch has different base or content; manual recovery required");
  } else {
    git(["checkout", "-b", branch], root);
    git(
      [
        "-c",
        "user.name=github-actions[bot]",
        "-c",
        "user.email=41898282+github-actions[bot]@users.noreply.github.com",
        "-c",
        "commit.gpgsign=false",
        "commit",
        "-m",
        `Update ${dependency} to ${version}\n\nEtherview-Dependency-Automation: v1`,
      ],
      root,
    );
    const auth = Buffer.from(`x-access-token:${process.env.GH_TOKEN}`).toString("base64");
    run(
      "git",
      ["-c", "core.hooksPath=/dev/null", "push", "origin", `HEAD:refs/heads/${branch}`],
      root,
      {
        env: {
          ...process.env,
          GIT_CONFIG_COUNT: "1",
          GIT_CONFIG_KEY_0: "http.https://github.com/.extraheader",
          GIT_CONFIG_VALUE_0: `AUTHORIZATION: basic ${auth}`,
        },
      },
    );
  }
  const body = `Updates ${dependency} to ${version}, including its registered linked pins and package-manager-generated locks.\n\nRegistered preparation commands (where applicable), dependency regressions, workflow script regressions and documentation checks passed. Full PR CI remains required: select **Approve workflows to run**, review the result and merge manually.\n\nEvidence: ${process.env.GITHUB_SERVER_URL}/${process.env.GITHUB_REPOSITORY}/actions/runs/${process.env.GITHUB_RUN_ID}\nBase: ${base}\n`;
  const url = api([
    "pr",
    "create",
    "--base",
    "main",
    "--head",
    branch,
    "--title",
    `Update ${dependency} to ${version}`,
    "--body",
    body,
  ]);
  if (process.env.GITHUB_STEP_SUMMARY)
    appendFileSync(
      process.env.GITHUB_STEP_SUMMARY,
      `\nCreated ${url}. Approve workflows to run full CI before merging.\n`,
    );
  console.log(url);
  return url;
}
if (import.meta.main) {
  const { values } = parseArgs({
    options: {
      dependency: { type: "string" },
      version: { type: "string" },
      artifact: { type: "string" },
      inspect: { type: "boolean" },
    },
  });
  publish({
    root: process.cwd(),
    dependency: values.dependency,
    version: values.version,
    artifact: values.artifact && resolve(values.artifact),
    inspect: values.inspect,
  })
    .then((action) => {
      if (values.inspect && process.env.GITHUB_OUTPUT)
        appendFileSync(process.env.GITHUB_OUTPUT, `action=${action}\n`);
    })
    .catch((error) => {
      console.error(
        `dependency publication failed: ${error.status !== undefined ? `Git/GitHub command exited ${error.status}; check permissions and API availability` : error.message}`,
      );
      process.exitCode = 1;
    });
}
