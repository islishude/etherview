// Version discovery and isolated candidate preparation. No publication credentials required.
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync, writeFileSync, mkdirSync, mkdtempSync, rmSync, lstatSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { parseArgs } from "node:util";

export const registryPath = ".github/dependency-registry.json";
export const digest = (bytes) => createHash("sha256").update(bytes).digest("hex");
export const run = (cmd, args, cwd = process.cwd(), options = {}) =>
  execFileSync(cmd, args, {
    cwd,
    encoding: "utf8",
    timeout: 600000,
    maxBuffer: 32 << 20,
    ...options,
  })?.trim() ?? "";
export const git = (args, cwd) => run("git", ["-c", "core.hooksPath=/dev/null", ...args], cwd);
export function versionParts(version) {
  if (!/^(0|[1-9]\d*)\.(0|[1-9]\d*)(?:\.(0|[1-9]\d*))?$/.test(version)) return null;
  const parts = version.split(".").map(Number);
  if (!parts.every(Number.isSafeInteger)) return null;
  return [parts[0], parts[1], parts[2] ?? 0];
}
export function compare(a, b) {
  const left = versionParts(a),
    right = versionParts(b);
  if (!left || !right) throw new Error("invalid stable version");
  for (let i = 0; i < 3; i++) if (left[i] !== right[i]) return left[i] - right[i];
  return 0;
}
export function compatible(current, next) {
  const a = versionParts(current),
    b = versionParts(next);
  return !!a && !!b && compare(next, current) > 0 && a[0] === b[0] && (a[0] !== 0 || a[1] === b[1]);
}
export function selectVersions(current, releases, policy) {
  const newer = [...new Set(releases)]
    .filter((v) => versionParts(v) && compare(v, current) > 0)
    .sort(compare);
  return {
    latest: newer.at(-1) ?? null,
    candidate:
      policy === "auto" ? (newer.filter((v) => compatible(current, v)).at(-1) ?? null) : null,
  };
}
const escape = (value) => value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
function pinRegex(pin) {
  const json = pin.path.endsWith(".json");
  if (
    typeof pin.template !== "string" ||
    !pin.template.includes("{{version}}") ||
    (json
      ? !Array.isArray(pin.jsonPath) ||
        !pin.jsonPath.length ||
        pin.jsonPath.some((key) => typeof key !== "string" || !key.length) ||
        pin.count !== undefined
      : pin.jsonPath !== undefined || !Number.isInteger(pin.count) || pin.count < 1)
  )
    throw new Error("invalid pin template");
  let versionSeen = false,
    other = 0;
  const number = "[0-9]+\\.[0-9]+(?:\\.[0-9]+)?";
  const pattern = pin.template
    .split(/({{version}}|{{other}})/)
    .map((piece) => {
      if (piece === "{{other}}") return `(?<other${other++}>${number})`;
      if (piece !== "{{version}}") return escape(piece);
      if (versionSeen) return "\\k<version>";
      versionSeen = true;
      return `(?<version>${number})`;
    })
    .join("");
  return new RegExp("^" + pattern + "$", json ? "g" : "gm");
}
function jsonField(document, pin) {
  let parent = document;
  for (const key of pin.jsonPath.slice(0, -1)) {
    if (
      !parent ||
      typeof parent !== "object" ||
      Array.isArray(parent) ||
      !Object.hasOwn(parent, key)
    )
      throw new Error(`missing JSON field in ${pin.path}: ${JSON.stringify(pin.jsonPath)}`);
    parent = parent[key];
  }
  const key = pin.jsonPath.at(-1);
  if (!parent || typeof parent !== "object" || Array.isArray(parent) || !Object.hasOwn(parent, key))
    throw new Error(`missing JSON field in ${pin.path}: ${JSON.stringify(pin.jsonPath)}`);
  if (typeof parent[key] !== "string")
    throw new Error(`JSON version field must be a string in ${pin.path}`);
  return { parent, key };
}
function renderPin(pin, version, groups) {
  let other = 0;
  return pin.template
    .replaceAll("{{version}}", version)
    .replaceAll("{{other}}", () => groups[`other${other++}`]);
}
export function safePath(path) {
  return (
    typeof path === "string" &&
    /^[a-zA-Z0-9_.\-/]+$/.test(path) &&
    !path.startsWith("/") &&
    !path.split("/").some((p) => p === ".." || p === ".git" || p === "")
  );
}
export function readRegistry(root = process.cwd()) {
  const registry = JSON.parse(readFileSync(join(root, registryPath), "utf8"));
  if (registry.schema !== 1 || !Array.isArray(registry.dependencies))
    throw new Error("invalid registry");
  const ids = new Set();
  for (const dep of registry.dependencies) {
    if (
      !/^[a-z][a-z0-9-]*$/.test(dep.id) ||
      ids.has(dep.id) ||
      !["auto", "report"].includes(dep.policy) ||
      !dep.pins.length
    )
      throw new Error("invalid dependency registration");
    ids.add(dep.id);
    for (const pin of dep.pins) pinRegex(pin);
    for (const path of [
      ...dep.pins.map((p) => p.path),
      ...dep.generated,
      ...(dep.reviewFiles ?? []),
    ])
      if (!safePath(path)) throw new Error("unsafe dependency path");
  }
  return registry;
}
export function currentVersion(dep, root = process.cwd()) {
  const versions = new Set();
  for (const pin of dep.pins) {
    const path = join(root, pin.path);
    if (!lstatSync(path).isFile()) throw new Error(`${dep.id}: pin is not a regular file`);
    const regex = pinRegex(pin);
    let text = readFileSync(path, "utf8");
    if (pin.jsonPath) {
      const { parent, key } = jsonField(JSON.parse(text), pin);
      text = parent[key];
    }
    const matches = [...text.matchAll(regex)];
    if (
      matches.length !== (pin.jsonPath ? 1 : pin.count) ||
      (pin.jsonPath && matches[0]?.[0] !== text)
    )
      throw new Error(`${dep.id}: pin position drift in ${pin.path}`);
    for (const match of matches) versions.add(match.groups.version);
  }
  if (versions.size !== 1 || !versionParts([...versions][0]))
    throw new Error(`${dep.id}: linked versions disagree`);
  return [...versions][0];
}
export function replacements(dep, version, root = process.cwd()) {
  const current = currentVersion(dep, root);
  if (dep.policy !== "auto" || !compatible(current, version))
    throw new Error(`${dep.id}: version requires a dedicated upgrade`);
  const result = new Map();
  for (const pin of dep.pins) {
    const text = result.get(pin.path) ?? readFileSync(join(root, pin.path), "utf8");
    if (pin.jsonPath) {
      const document = JSON.parse(text);
      const { parent, key } = jsonField(document, pin);
      parent[key] = parent[key].replace(pinRegex(pin), (...args) =>
        renderPin(pin, version, args.at(-1)),
      );
      result.set(pin.path, JSON.stringify(document, null, 2) + "\n");
      continue;
    }
    result.set(
      pin.path,
      text.replace(pinRegex(pin), (...args) => renderPin(pin, version, args.at(-1))),
    );
  }
  return result;
}
export function audit(root = process.cwd()) {
  const registry = readRegistry(root);
  const ownership = new Set();
  const dependabot = readFileSync(join(root, ".github/dependabot.yml"), "utf8");
  for (const dep of registry.dependencies) {
    for (const path of dep.reviewFiles ?? [])
      if (!lstatSync(join(root, path)).isFile()) throw new Error("missing review fixture");
    for (const pin of dep.pins) {
      const key = JSON.stringify([pin.path, pin.jsonPath ?? pin.template]);
      if (ownership.has(key)) throw new Error("duplicate pin ownership");
      ownership.add(key);
    }
    currentVersion(dep, root);
    for (const [ecosystem, names] of Object.entries(dep.ignores)) {
      const block =
        dependabot
          .split(`  - package-ecosystem: ${ecosystem}\n`)[1]
          ?.split("  - package-ecosystem:")[0] ?? "";
      const ignored = block.split("    ignore:\n")[1]?.split(/^    [a-z]/m)[0] ?? "";
      for (const name of names)
        if (!ignored.includes(`dependency-name: "${name}"`))
          throw new Error(`${dep.id}: missing Dependabot ownership exclusion`);
    }
  }
  const makefile = readFileSync(join(root, "Makefile"), "utf8");
  for (const match of makefile.matchAll(/^([A-Z_]+_VERSION) \?= /gm)) {
    if (
      !registry.dependencies.some((dep) =>
        dep.pins.some((p) => p.path === "Makefile" && p.template.startsWith(match[1] + " ")),
      )
    )
      throw new Error(`unregistered Makefile version: ${match[1]}`);
  }
  for (const [ecosystem, paths] of [
    ["docker", ["/e2e/*"]],
    ["docker-compose", ["/e2e/*", "/deploy"]],
  ]) {
    const block =
      dependabot
        .split(`  - package-ecosystem: ${ecosystem}\n`)[1]
        ?.split("  - package-ecosystem:")[0] ?? "";
    for (const path of paths)
      if (!block.includes(`      - ${path}\n`)) throw new Error(`missing ${ecosystem} directory`);
  }
  return registry;
}

// Fetch only fixed public registries. Credentials are sent only to GitHub's API.
export async function fetchData(url, json = true, fetcher = fetch, timeout = 20000) {
  const parsed = new URL(url);
  if (
    parsed.protocol !== "https:" ||
    ![
      "proxy.golang.org",
      "registry.npmjs.org",
      "pypi.org",
      "api.github.com",
      "go.dev",
      "nodejs.org",
    ].includes(parsed.hostname)
  )
    throw new Error("unsupported release source");
  const headers = { "User-Agent": "etherview-dependency-updates" };
  if (parsed.hostname === "api.github.com" && process.env.GH_TOKEN)
    headers.Authorization = `Bearer ${process.env.GH_TOKEN}`;
  let failure = "network or invalid response";
  for (let attempt = 0; attempt < 3; attempt++) {
    try {
      const response = await fetcher(url, {
        headers,
        signal: AbortSignal.timeout(timeout),
        redirect: "error",
      });
      if (!response.ok) {
        failure = `HTTP ${response.status}`;
        if (response.status !== 429 && response.status < 500) break;
        throw new Error("retryable HTTP response");
      }
      const reader = response.body.getReader();
      const chunks = [];
      let size = 0;
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        size += value.length;
        if (size > 32 << 20) {
          await reader.cancel();
          throw new Error("release response too large");
        }
        chunks.push(Buffer.from(value));
      }
      const body = Buffer.concat(chunks).toString("utf8");
      return json ? JSON.parse(body) : body;
    } catch (error) {
      if (error.name === "TimeoutError") failure = "timeout";
      if (attempt < 2) await new Promise((resolve) => setTimeout(resolve, 250 * (attempt + 1)));
    }
  }
  throw new Error(`release query failed (${parsed.hostname}): ${failure}`);
}
export async function releases(dep, request = fetchData) {
  switch (dep.source) {
    case "gomod": {
      const path = dep.package.replace(/[A-Z]/g, (c) => "!" + c.toLowerCase());
      return (await request(`https://proxy.golang.org/${path}/@v/list`, false))
        .split(/\s+/)
        .map((v) => v.replace(/^v/, ""));
    }
    case "npm":
      return Object.entries(
        (await request(`https://registry.npmjs.org/${encodeURIComponent(dep.package)}`)).versions,
      )
        .filter(([, value]) => !value.deprecated)
        .map(([version]) => version);
    case "pypi":
      return Object.entries((await request(`https://pypi.org/pypi/${dep.package}/json`)).releases)
        .filter(([, files]) => files.some((f) => !f.yanked))
        .map(([version]) => version);
    case "go":
      return (await request("https://go.dev/dl/?mode=json&include=all"))
        .filter((v) => v.stable)
        .map((v) => v.version.replace(/^go/, ""));
    case "github-tags":
    case "github": {
      const tags = dep.source === "github-tags";
      const versions = [];
      // Paginate to retain compatible maintenance releases behind newer majors.
      for (let page = 1; page <= 20; page++) {
        const rows = await request(
          `https://api.github.com/repos/${dep.package}/${tags ? "tags" : "releases"}?per_page=100&page=${page}`,
        );
        versions.push(
          ...rows
            .filter((v) => !v.draft && !v.prerelease)
            .map((v) => (tags ? v.name : v.tag_name).replace(/^v/, "")),
        );
        if (rows.length < 100) return versions;
      }
      throw new Error("release pagination limit exceeded");
    }
    default:
      throw new Error("unknown release source");
  }
}
export async function discover(registry, root, selected, requestedVersion, getReleases = releases) {
  const deps = selected
    ? registry.dependencies.filter((d) => d.id === selected)
    : registry.dependencies;
  if (!deps.length) throw new Error("unknown dependency");
  if (requestedVersion && !selected) throw new Error("exact version requires a dependency");
  const rows = [];
  for (const dep of deps) {
    let current;
    try {
      current = currentVersion(dep, root);
      const available = await getReleases(dep);
      if (!available.some((v) => versionParts(v)))
        throw new Error("release source returned no stable versions");
      const result = selectVersions(current, available, dep.policy);
      if (requestedVersion) {
        if (
          !available.includes(requestedVersion) ||
          dep.policy !== "auto" ||
          !compatible(current, requestedVersion)
        )
          throw new Error("requested version is unavailable or requires dedicated upgrade");
        result.candidate = requestedVersion;
      }
      rows.push({
        id: dep.id,
        owner: "dependency-updates",
        current,
        ...result,
        status: result.candidate ? "update" : result.latest ? "review" : "current",
        reason:
          dep.reason ||
          (result.latest && !compatible(current, result.latest)
            ? "Latest release crosses the automatic upgrade boundary"
            : ""),
      });
    } catch (error) {
      rows.push({
        id: dep.id,
        owner: "dependency-updates",
        current,
        status: "error",
        reason: error.message,
      });
    }
  }
  return rows;
}
export function clean(root) {
  if (git(["status", "--porcelain", "--untracked-files=all"], root))
    throw new Error("dependency-update requires a clean working tree (including untracked files)");
}
export const validationCommands = [
  ["make", "test-workflow-scripts", "docs-check"],
  ["git", "diff", "--check"],
];
export function prepare(dep, version, root, output) {
  clean(root);
  const base = git(["rev-parse", "HEAD"], root);
  const temp = mkdtempSync(join(tmpdir(), "etherview-dependency-"));
  const candidate = join(temp, "checkout"),
    bin = join(temp, "bin");
  let added = false;
  try {
    git(["worktree", "add", "--detach", candidate, base], root);
    added = true;
    const changes = replacements(dep, version, candidate);
    for (const [path, text] of changes) writeFileSync(join(candidate, path), text);
    mkdirSync(bin);
    const env = { ...process.env, GOBIN: bin, PATH: `${bin}:${process.env.PATH}` };
    // Candidate package commands never inherit a publication token.
    delete env.GH_TOKEN;
    delete env.GITHUB_TOKEN;
    for (const [cmd, ...args] of [...dep.commands, ...validationCommands])
      run(
        cmd,
        args.map((s) => s.replaceAll("{{version}}", version)),
        candidate,
        { env, stdio: ["ignore", "inherit", "inherit"] },
      );
    audit(candidate);
    for (const [path, expected] of changes)
      if (readFileSync(join(candidate, path), "utf8") !== expected)
        throw new Error("candidate changed source beyond the registered version replacement");
    if (git(["diff", "--cached", "--name-only"], candidate))
      throw new Error("unexpected staged candidate changes");
    if (currentVersion(dep, candidate) !== version) throw new Error("candidate version mismatch");
    const files = git(["diff", "--name-only"], candidate).split("\n").filter(Boolean).sort();
    const allowed = new Set([...changes.keys(), ...dep.generated]);
    if (
      !files.length ||
      files.some((p) => !allowed.has(p)) ||
      git(["ls-files", "--others", "--exclude-standard"], candidate)
    )
      throw new Error("unexpected candidate changes");
    for (const path of files)
      if (
        !lstatSync(join(candidate, path)).isFile() ||
        (lstatSync(join(candidate, path)).mode & 0o777) !==
          (lstatSync(join(root, path)).mode & 0o777)
      )
        throw new Error("non-file or mode-changing candidate change");
    const patch = execFileSync("git", ["diff", "--binary", "--no-ext-diff", "--no-renames"], {
      cwd: candidate,
      encoding: "utf8",
      maxBuffer: 32 << 20,
    });
    const report = {
      schema: 1,
      dependency: dep.id,
      version,
      base,
      files,
      patch_sha256: digest(patch),
      checks: "passed",
    };
    if (output) {
      mkdirSync(output, { recursive: true });
      writeFileSync(join(output, "update.patch"), patch);
      writeFileSync(join(output, "report.json"), JSON.stringify(report, null, 2) + "\n");
    } else {
      clean(root);
      if (git(["rev-parse", "HEAD"], root) !== base) throw new Error("update base changed");
      run("git", ["apply", "--check", "-"], root, { input: patch });
      run("git", ["apply", "-"], root, { input: patch });
    }
    return report;
  } finally {
    if (added) git(["worktree", "remove", "--force", candidate], root);
    rmSync(temp, { recursive: true, force: true });
  }
}
export async function main() {
  const { values, positionals } = parseArgs({
    allowPositionals: true,
    options: {
      dependency: { type: "string" },
      version: { type: "string" },
      output: { type: "string" },
    },
  });
  const root = process.cwd(),
    registry = audit(root),
    command = positionals[0];
  if (command === "audit") {
    console.log(`Dependency registry: ${registry.dependencies.length} entries validated`);
    return;
  }
  if (!["check", "update"].includes(command))
    throw new Error(
      "usage: dependency-updates.mjs check|update|audit [--dependency ID] [--version VERSION] [--output DIR]",
    );
  if (command === "update") {
    if (!values.dependency || !values.version)
      throw new Error("update requires dependency and version");
    clean(root);
  }
  const rows = await discover(registry, root, values.dependency, values.version);
  if (command === "check") {
    const report = { base: git(["rev-parse", "HEAD"], root), rows, delegated: registry.delegated };
    console.log(JSON.stringify(report, null, 2));
    if (values.output) {
      mkdirSync(values.output, { recursive: true });
      writeFileSync(join(values.output, "discovery.json"), JSON.stringify(report, null, 2) + "\n");
    }
    if (process.env.GITHUB_OUTPUT) {
      const { appendFileSync } = await import("node:fs");
      appendFileSync(
        process.env.GITHUB_OUTPUT,
        `base=${report.base}\nmatrix=${JSON.stringify(rows.filter((r) => r.candidate).map((r) => ({ dependency: r.id, version: r.candidate })))}\n`,
      );
    }
    if (process.env.GITHUB_STEP_SUMMARY) {
      const { appendFileSync } = await import("node:fs");
      appendFileSync(
        process.env.GITHUB_STEP_SUMMARY,
        "\n## Dependency discovery\n\n" +
          rows
            .map(
              (r) =>
                `- ${r.id}: ${r.current ?? "unknown"} → ${r.candidate ?? r.latest ?? "none"}; ${r.status}. ${r.reason}`,
            )
            .join("\n") +
          "\n",
      );
    }
    if (rows.some((r) => r.status === "error")) process.exitCode = 1;
  } else {
    if (rows[0].status === "error") throw new Error(rows[0].reason);
    console.log(
      JSON.stringify(
        prepare(
          registry.dependencies.find((d) => d.id === values.dependency),
          values.version,
          root,
          values.output && resolve(values.output),
        ),
      ),
    );
  }
}
if (import.meta.main)
  main().catch((error) => {
    console.error(`dependency-updates: ${error.message}`);
    process.exitCode = 1;
  });
