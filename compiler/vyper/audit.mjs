// Native 0.4.3 reference dependencies, independent of the WASM distribution audit.
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { ROOT, canonical, mapConcurrent, versions } from "./wasm/tooling.mjs";
import { queryPackage } from "./wasm/audit.mjs";
import { referencePackages } from "./reference.mjs";
const WHEEL = "3b9671727c888363740dc678e60336759871487d0e4e9fdd973048fa9635c4fd";
const CORRECTIONS = new Set(["PYSEC-2023-142", "GHSA-vgf2-gvx8-xwc3"]);
export function evaluateReference(pins, reports, compilerDigest) {
  const expected = new Set([...pins].map(([name, p]) => `PyPI/${name}/${p.version}`));
  if (
    reports.length !== expected.size ||
    new Set(reports.map((r) => r.component)).size !== expected.size ||
    reports.some((r) => !expected.has(r.component))
  )
    throw new Error("reference audit coverage is incomplete or duplicated");
  const corrected = [],
    problems = [];
  for (const report of reports) {
    const seen = new Set();
    for (const finding of report.findings) {
      if (!finding.id || seen.has(finding.id)) throw new Error("duplicate or invalid advisory");
      seen.add(finding.id);
      if (
        report.component === "PyPI/vyper/0.4.3" &&
        compilerDigest === WHEEL &&
        pins.get("vyper")?.hashes.includes(WHEEL) &&
        CORRECTIONS.has(finding.id)
      )
        corrected.push(finding.id);
      else problems.push(report.component + "/" + finding.id);
    }
  }
  if (problems.length)
    throw new Error("unresolved native reference advisories: " + problems.join(", "));
  return corrected;
}
if (import.meta.main) {
  const output = join(ROOT, ".local/vyper/audit");
  mkdirSync(output, { recursive: true });
  writeFileSync(join(output, "report.json"), '{"status":"incomplete"}\n');
  const pins = referencePackages("0.4.3");
  const reports = await mapConcurrent(
    [...pins].map(([name, p]) => [name, p.version]),
    queryPackage,
  );
  writeFileSync(join(output, "raw.json"), canonical(reports) + "\n");
  const corrected = evaluateReference(
    pins,
    reports,
    versions().find((v) => v.version === "0.4.3").compiler_sha256,
  );
  writeFileSync(
    join(output, "report.json"),
    canonical({ status: "pass", reports, corrected }) + "\n",
  );
  console.log(
    `Native reference audit: PASS (${reports.length} packages; version-range corrections: ${corrected.join(", ") || "none"})`,
  );
}
