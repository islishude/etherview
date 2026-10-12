// Audit actual distributions, vendors and pinned WASM core. Unknown changes fail.
import { appendFileSync, mkdirSync, writeFileSync, rmSync, readdirSync, lstatSync } from "node:fs";
import { join } from "node:path";
import {
  ROOT,
  HERE,
  canonical,
  digest,
  regularFile,
  readJSON,
  safeMember,
  readURL,
  versions,
  mapConcurrent,
} from "./tooling.mjs";
import { metadata, normalized } from "./metadata.mjs";
import { sharedSHA256 } from "./release.mjs";
import { loadAuxiliaryBaselines, classifyAuxiliary } from "./audit-auxiliary.mjs";
export { metadata, vendoredMetadata } from "./metadata.mjs";
export const OUTPUT = join(ROOT, ".local/vyper-wasm/audit");
export function inventory() {
  const records = [],
    identities = {};
  const shared = join(ROOT, ".local/vyper-wasm/shared");
  if (!lstatSync(shared).isDirectory()) throw new Error("invalid shared directory");
  const raw = regularFile(join(shared, "shared-manifest.json"));
  if (digest(raw) !== sharedSHA256) throw new Error("unauthenticated shared runtime");
  identities.shared = digest(raw);
  const files = JSON.parse(raw).files;
  if (
    canonical(readdirSync(shared).sort()) !==
    canonical(["shared-manifest.json", ...files.map((f) => f.path)].sort())
  )
    throw new Error("unlisted shared payload");
  for (const entry of files) {
    safeMember(entry.path);
    const data = regularFile(join(shared, entry.path));
    if (digest(data) !== entry.sha256) throw new Error("shared runtime payload changed");
    if (/\.(whl|zip)$/.test(entry.path)) records.push(...metadata(data, "shared/" + entry.path));
  }
  for (const item of versions()) {
    const path = join(ROOT, ".local/vyper-wasm/packages", item.version);
    if (
      !lstatSync(path).isDirectory() ||
      canonical(readdirSync(path).sort()) !== canonical(["package-manifest.json", "packages.zip"])
    )
      throw new Error("invalid compiler package directory");
    const raw = regularFile(join(path, "package-manifest.json"));
    const manifest = JSON.parse(raw);
    if (manifest.version !== item.version || manifest.compiler_sha256 !== item.compiler_sha256)
      throw new Error("compiler identity changed");
    const data = regularFile(join(path, "packages.zip"));
    if (canonical(manifest.files) !== canonical([{ path: "packages.zip", sha256: digest(data) }]))
      throw new Error("compiler payload changed");
    const found = metadata(data, item.version);
    const direct = new Set([
      ...manifest.dependencies.map((d) => normalized(d.name) + "/" + d.version),
      "vyper/" + item.version,
    ]);
    const top = new Set(
      found.filter((d) => d.metadata.split("/").length === 2).map((d) => d.name + "/" + d.version),
    );
    if (canonical([...top].sort()) !== canonical([...direct].sort()))
      throw new Error("distribution inventory does not match package manifest");
    records.push(...found);
    identities[item.version] = digest(raw);
  }
  if (!records.length) throw new Error("empty distribution inventory");
  for (const name of [
    "compiler/vyper/adapter.py",
    "compiler/vyper/wasm/protocol.py",
    "compiler/vyper/wasm/execute.mjs",
    "compiler/package-lock.json",
    "internal/verify/vyper.go",
    "internal/verify/vyper_wasm_manifest.go",
    "compiler/vyper/wasm/licenses.lock.json",
  ])
    identities[name] = digest(regularFile(join(ROOT, name)));
  records.sort((a, b) => (canonical(a) < canonical(b) ? -1 : canonical(a) > canonical(b) ? 1 : 0));
  return { identities, distributions: records };
}
const fetchJSON = async (url, payload) => JSON.parse(await readURL(url, 32 << 20, payload));
export async function osv(query, fetcher = fetchJSON) {
  const findings = [],
    tokens = new Set();
  for (;;) {
    const response = await fetcher("https://api.osv.dev/v1/query", query);
    if (
      !response ||
      Array.isArray(response) ||
      typeof response !== "object" ||
      Object.keys(response).some((k) => !["vulns", "next_page_token"].includes(k))
    )
      throw new Error("invalid OSV response");
    if (response.vulns !== undefined && !Array.isArray(response.vulns))
      throw new Error("invalid OSV findings");
    findings.push(...(response.vulns ?? []));
    const token = response.next_page_token;
    if (!token) return findings;
    if (typeof token !== "string" || tokens.has(token))
      throw new Error("repeated or invalid OSV page");
    tokens.add(token);
    query = { ...query, page_token: token };
  }
}
export async function queryPackage([name, version], fetcher = fetchJSON) {
  let findings;
  if (name === "vyper" && version === "0.2.0")
    findings = await osv({ package: { ecosystem: "PyPI", name }, version }, fetcher);
  else {
    const response = await fetcher(`https://pypi.org/pypi/${name}/${version}/json`);
    if (normalized(response.info.name) !== name || response.info.version !== version)
      throw new Error("audit-service identity mismatch");
    findings = response.vulnerabilities;
    if (!Array.isArray(findings)) throw new Error("invalid PyPI findings");
  }
  return { component: `PyPI/${name}/${version}`, findings };
}
export async function queryCore(component, fetcher = fetchJSON) {
  const response = await osv({ commit: component.commit }, fetcher);
  const findings = new Map(response.map((f) => [f.id, f]));
  if (findings.size !== response.length) throw new Error("duplicate core advisory");
  for (const identifier of component.advisories ?? []) {
    const finding = await fetcher("https://api.osv.dev/v1/vulns/" + identifier);
    if (finding.id !== identifier) throw new Error("core advisory identity mismatch");
    if (
      findings.has(identifier) &&
      findingDigest(findings.get(identifier)) !== findingDigest(finding)
    )
      throw new Error("inconsistent core advisory");
    findings.set(identifier, finding);
  }
  return { component: "core/" + component.name, source: "osv", findings: [...findings.values()] };
}
export function findingDigest(finding) {
  return digest(
    canonical(
      Object.fromEntries(
        Object.entries(finding).filter(([k]) => !["modified", "published"].includes(k)),
      ),
    ),
  );
}
export function evaluate(actual, reports, review, changes = []) {
  if (digest(canonical(actual)) !== review.inventory_sha256)
    throw new Error("WASM distribution inventory changed; review required");
  const expected = new Set([
    ...actual.distributions.map((d) => "PyPI/" + d.name + "/" + d.version),
    ...review.core.map((c) => "core/" + c.name),
  ]);
  if (
    reports.length !== expected.size ||
    new Set(reports.map((r) => r.component)).size !== expected.size ||
    reports.some((r) => !expected.has(r.component))
  )
    throw new Error("audit coverage is incomplete or duplicated");
  const baselines = loadAuxiliaryBaselines(review, findingDigest);
  const problems = [];
  let count = 0;
  for (const report of reports) {
    const seen = new Set();
    for (const finding of report.findings) {
      if (!finding || typeof finding.id !== "string" || !finding.id || seen.has(finding.id))
        throw new Error("duplicate or invalid advisory");
      seen.add(finding.id);
      const scope = report.component.startsWith("PyPI/")
        ? report.component.slice(0, report.component.lastIndexOf("/"))
        : report.component;
      const key = scope + "/" + finding.id,
        disposition = review.findings[key];
      if (!disposition || !Object.hasOwn(review.reasons, disposition.reason)) {
        problems.push(key);
        changes.push({
          classification: "review-required",
          component: report.component,
          id: finding.id,
        });
      } else if (!disposition.sha256.includes(findingDigest(finding))) {
        const baseline = baselines.get(key);
        const change = baseline
          ? classifyAuxiliary(report, finding, baseline)
          : { classification: "review-required", component: report.component, id: finding.id };
        changes.push(change);
        if (change.classification !== "auxiliary-only") problems.push(key);
      }
      count++;
    }
  }
  if (problems.length) throw new Error("unreviewed or changed advisories: " + problems.join(", "));
  return count;
}

export function evaluateReport(actual, reports, review) {
  const changes = [];
  const result = {
    status: "failed",
    queried_at: new Date().toISOString(),
    inventory_sha256: digest(canonical(actual)),
    raw_sha256: digest(canonical(reports)),
    review_sha256: digest(canonical(review)),
    distributions: actual.distributions.length,
    components: reports.length,
    changes,
  };
  try {
    result.findings = evaluate(actual, reports, review, changes);
    result.auxiliary_changes = changes.filter((c) => c.classification === "auxiliary-only").length;
    result.status = result.auxiliary_changes
      ? "pass-with-auxiliary-changes"
      : "pass-with-reviewed-findings";
  } catch (error) {
    result.error = error.message;
  }
  return result;
}
export async function main() {
  mkdirSync(OUTPUT, { recursive: true });
  const reportPath = join(OUTPUT, "report.json");
  writeFileSync(reportPath, '{"status":"incomplete"}\n');
  for (const name of ["inventory.json", "raw.json"]) rmSync(join(OUTPUT, name), { force: true });
  const actual = inventory(),
    review = readJSON(join(HERE, "audit-review.json"));
  writeFileSync(join(OUTPUT, "inventory.json"), canonical(actual) + "\n");
  const identities = [...new Set(actual.distributions.map((d) => d.name + "/" + d.version))]
    .sort()
    .map((s) => s.split("/"));
  const reports = [
    ...(await mapConcurrent(identities, queryPackage)),
    ...(await mapConcurrent(review.core, queryCore)),
  ];
  writeFileSync(join(OUTPUT, "raw.json"), canonical(reports) + "\n");
  const result = evaluateReport(actual, reports, review);
  result.review_sha256 = digest(regularFile(join(HERE, "audit-review.json")));
  writeFileSync(reportPath, JSON.stringify(result, null, 2) + "\n");
  if (process.env.GITHUB_STEP_SUMMARY)
    appendFileSync(
      process.env.GITHUB_STEP_SUMMARY,
      `\nWASM audit: ${result.status}. Auxiliary-only changes: ${result.changes.filter((c) => c.classification === "auxiliary-only").length}; review-required changes: ${result.changes.filter((c) => c.classification === "review-required").length}. See the audit artifact for exact paths and digests.\n`,
    );
  if (result.status === "failed") throw new Error(result.error);
  console.log(
    `WASM security audit: PASS (${reports.length} components, ${result.findings} findings, ${result.auxiliary_changes} auxiliary-only changes; not zero vulnerabilities)`,
  );
}
if (import.meta.main) await main();
