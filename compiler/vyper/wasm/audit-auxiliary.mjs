// This policy never determines vulnerability applicability or changes a review.
import { join } from "node:path";
import { HERE, canonical, digest, regularFile } from "./tooling.mjs";

export const VANIR_POLICY = "osv-cpython-vanir-v1";
const fields = ["vanir_signatures", "vanir_signatures_modified"];
const object = (v) => v !== null && typeof v === "object" && !Array.isArray(v);
const string = (v) => typeof v === "string" && v.length > 0;
const keys = (v, allowed) => object(v) && Object.keys(v).every((k) => allowed.includes(k));
const hash = (v) => typeof v === "string" && /^[0-9]{1,39}$/.test(v);

function signature(v) {
  if (
    !keys(v, [
      "id",
      "signature_type",
      "signature_version",
      "source",
      "target",
      "digest",
      "deprecated",
    ]) ||
    !string(v.id) ||
    !string(v.source) ||
    v.signature_version !== "v1" ||
    (Object.hasOwn(v, "deprecated") && typeof v.deprecated !== "boolean")
  )
    return false;
  if (v.signature_type === "Function")
    return (
      keys(v.target, ["file", "function"]) &&
      string(v.target.file) &&
      string(v.target.function) &&
      keys(v.digest, ["function_hash", "length"]) &&
      hash(v.digest.function_hash) &&
      Number.isSafeInteger(v.digest.length) &&
      v.digest.length > 0
    );
  if (v.signature_type === "Line")
    return (
      keys(v.target, ["file"]) &&
      string(v.target.file) &&
      keys(v.digest, ["line_hashes", "threshold"]) &&
      Array.isArray(v.digest.line_hashes) &&
      v.digest.line_hashes.length > 0 &&
      v.digest.line_hashes.every(hash) &&
      typeof v.digest.threshold === "number" &&
      v.digest.threshold > 0 &&
      v.digest.threshold <= 1
    );
  return false;
}

// Remove only the two exact paths. Preserve their parent and every unknown key.
export function vanirProjection(finding) {
  if (!object(finding) || !Array.isArray(finding.affected)) return null;
  const result = structuredClone(finding);
  delete result.modified;
  delete result.published;
  for (const affected of result.affected) {
    if (!object(affected)) return null;
    if (!Object.hasOwn(affected, "database_specific")) continue;
    const db = affected.database_specific;
    if (!object(db)) return null;
    if (
      Object.hasOwn(db, fields[0]) &&
      (!Array.isArray(db[fields[0]]) || !db[fields[0]].every(signature))
    )
      return null;
    if (
      Object.hasOwn(db, fields[1]) &&
      (typeof db[fields[1]] !== "string" ||
        !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?Z$/.test(db[fields[1]]) ||
        !Number.isFinite(Date.parse(db[fields[1]])))
    )
      return null;
    for (const field of fields) delete db[field];
  }
  return result;
}

export function loadAuxiliaryBaselines(review, findingDigest) {
  const baselines = new Map();
  for (const [key, disposition] of Object.entries(review.findings)) {
    const config = disposition.auxiliary;
    if (config === undefined) continue;
    if (
      !keys(config, ["policy", "baseline"]) ||
      config.policy !== VANIR_POLICY ||
      !key.startsWith("core/cpython/") ||
      !review.core.some((c) => c.name === "cpython" && c.repo === "python/cpython") ||
      typeof config.baseline !== "string" ||
      !/^[A-Za-z0-9-]+\.json$/.test(config.baseline)
    )
      throw new Error("invalid auxiliary review policy");
    const finding = JSON.parse(
      regularFile(join(HERE, "audit-baselines", config.baseline), 1 << 20),
    );
    if (
      key !== "core/cpython/" + finding.id ||
      !disposition.sha256.includes(findingDigest(finding)) ||
      !Object.hasOwn(review.reasons, disposition.reason)
    )
      throw new Error("unreviewed auxiliary baseline");
    const projection = vanirProjection(finding);
    if (!projection) throw new Error("invalid auxiliary baseline structure");
    baselines.set(key, { finding, projection, policy: config.policy });
  }
  return baselines;
}

// JSON pointers are evidence only, never workflow commands. Bound report size.
export function changedPaths(before, after) {
  const paths = [];
  function visit(a, b, path) {
    if (paths.length >= 64 || (a !== undefined && b !== undefined && canonical(a) === canonical(b)))
      return;
    if (object(a) && object(b)) {
      for (const key of new Set([...Object.keys(a), ...Object.keys(b)])) {
        if (!path && ["modified", "published"].includes(key)) continue;
        const child = path + "/" + key.replaceAll("~", "~0").replaceAll("/", "~1");
        if (fields.includes(key)) {
          if (
            Object.hasOwn(a, key) !== Object.hasOwn(b, key) ||
            canonical(a[key] ?? null) !== canonical(b[key] ?? null)
          )
            paths.push(child);
        } else visit(a[key], b[key], child);
      }
    } else if (Array.isArray(a) && Array.isArray(b) && a.length === b.length) {
      a.forEach((v, i) => visit(v, b[i], path + "/" + i));
    } else paths.push(path || "/");
  }
  visit(before, after, "");
  return paths.slice(0, 64);
}

export function classifyAuxiliary(report, finding, baseline) {
  const projection = vanirProjection(finding);
  const auxiliary =
    report.component === "core/cpython" &&
    report.source === "osv" &&
    projection !== null &&
    canonical(projection) === canonical(baseline.projection);
  return {
    classification: auxiliary ? "auxiliary-only" : "review-required",
    component: report.component,
    id: finding.id,
    policy: baseline.policy,
    baseline_raw_sha256: digest(canonical(baseline.finding)),
    raw_sha256: digest(canonical(finding)),
    baseline_decision_sha256: digest(canonical(baseline.projection)),
    decision_sha256: projection === null ? null : digest(canonical(projection)),
    changed_paths: changedPaths(baseline.finding, finding),
  };
}
