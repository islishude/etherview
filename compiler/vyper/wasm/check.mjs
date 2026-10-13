// Offline candidate comparison. Production SEA/native acceptance remains separate.
import { execFileSync } from "node:child_process";
import { join, resolve } from "node:path";
import { writeFileSync, readFileSync, readdirSync, mkdirSync } from "node:fs";
import { runtimeFor } from "./runtime-policy.mjs";
import { fixtureIdentity } from "./release.mjs";
import { ROOT, HERE, versions } from "./tooling.mjs";
import { bundle } from "./packages.mjs";

const outputBase = resolve(process.argv[2] ?? join(ROOT, ".local/vyper-wasm"));
mkdirSync(outputBase, { recursive: true });
execFileSync(process.execPath, [join(HERE, "prepare.mjs"), join(outputBase, "shared")], { cwd: ROOT, stdio: "inherit" });
const reports = [];
for (const item of versions()) {
  const destination = join(outputBase, "packages", item.version);
  const fixtureRoot = join(ROOT, "internal/verify/testdata/compiler/vyper/versions", item.version);
  const identity = fixtureIdentity(item.version);
  const report = { version: item.version, ...identity, passed: 0, failures: [] };
  reports.push(report);
  try {
    await bundle(item, destination);
    const shared = join(outputBase, "shared", runtimeFor(item.version).id);
    for (const file of readdirSync(fixtureRoot).filter(n => n.endsWith(".input.json")).sort()) {
      const stem = file.slice(0, -".input.json".length);
      for (const [suffix, out] of [["input", "output"], ["modified", "modified_output"]]) {
        try {
          const output = execFileSync(process.execPath, [
            "--permission", "--allow-fs-read=" + shared, "--allow-fs-read=" + destination,
            "--allow-fs-read=" + HERE, "--no-addons", "--max-old-space-size=128",
            "--wasm-max-mem-pages=6144", join(HERE, "matrix.mjs"), shared, destination,
            fixtureRoot, item.version,
          ], {
            cwd: ROOT, env: { PATH: process.env.PATH, LANG: "C", TZ: "UTC" },
            timeout: 30000, killSignal: "SIGKILL", maxBuffer: 1 << 20,
            input: JSON.stringify({
              raw: readFileSync(join(fixtureRoot, `${stem}.${suffix}.json`), "utf8"),
              expected: JSON.parse(readFileSync(join(fixtureRoot, `${stem}.${out}.json`))),
              invalid: stem.startsWith("invalid"),
            }),
          });
          const result = JSON.parse(output);
          report.python = result.python;
          report.pyodide = result.pyodide;
          report.package_sha256 = result.package_sha256;
          report.shared_sha256 = result.shared_sha256;
          report.passed++;
        } catch (error) {
          report.failures.push({ input: `${stem}.${suffix}.json`, stage: "compile", error: String(error.stderr?.length ? error.stderr : error.message).slice(0, 4096) });
        }
      }
    }
    if (report.passed + report.failures.length !== identity.cases) throw new Error("fixture count changed");
    if (fixtureIdentity(item.version).fixtures_sha256 !== identity.fixtures_sha256) throw new Error("fixtures changed during matrix");
  } catch (error) {
    report.failures.push({ stage: "prepare", error: error.message });
  }
  writeFileSync(join(outputBase, "matrix-results.json"), JSON.stringify(reports, null, 2) + "\n");
  console.log(item.version, report.passed, "/", report.cases, "cases passed");
}
writeFileSync(join(outputBase, "matrix-results.json"), JSON.stringify(reports, null, 2) + "\n");
if (reports.some(r => r.failures.length)) throw new Error("candidate matrix failed; see matrix-results.json");
