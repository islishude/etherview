import test from "node:test";
import assert from "node:assert/strict";
import { join } from "node:path";
import { HERE, readJSON, canonical, digest } from "./tooling.mjs";
import { evaluate, evaluateReport, findingDigest, queryCore } from "./audit.mjs";
import { VANIR_POLICY, vanirProjection, classifyAuxiliary } from "./audit-auxiliary.mjs";

const key = "core/cpython/CVE-2026-82049";
const fixture = (suffix = "-fix-range") =>
  readJSON(join(HERE, "testdata", `CVE-2026-82049${suffix}.json`));
const actual = { distributions: [] };
function context() {
  const all = readJSON(join(HERE, "audit-review.json"));
  const review = {
    ...all,
    core: all.core.filter((c) => c.name === "cpython"),
    findings: { [key]: all.findings[key] },
    inventory_sha256: digest(canonical(actual)),
  };
  const finding = fixture();
  const metadata = fixture("-vanir").affected[0].database_specific;
  Object.assign(finding.affected[0].database_specific, {
    vanir_signatures: metadata.vanir_signatures,
    vanir_signatures_modified: metadata.vanir_signatures_modified,
  });
  const reports = [{ component: "core/cpython", source: "osv", findings: [finding] }];
  return { review, finding, reports };
}

test("only Vanir changes pass with immutable complete evidence and a decision digest", () => {
  const { review, finding, reports } = context();
  const before = canonical({ review, finding, reports });
  const result = evaluateReport(actual, reports, review);
  assert.equal(result.status, "pass-with-auxiliary-changes");
  assert.equal(result.findings, 1);
  assert.equal(result.auxiliary_changes, 1);
  const change = result.changes[0];
  assert.equal(change.policy, VANIR_POLICY);
  assert.equal(change.raw_sha256, digest(canonical(finding)));
  assert.notEqual(change.raw_sha256, change.baseline_raw_sha256);
  assert.equal(change.decision_sha256, change.baseline_decision_sha256);
  assert.deepEqual(change.changed_paths, [
    "/affected/0/database_specific/vanir_signatures",
    "/affected/0/database_specific/vanir_signatures_modified",
  ]);
  assert.equal(before, canonical({ review, finding, reports }));
  assert.equal(result.raw_sha256, digest(canonical(reports)));
  // Removal and value changes are also auxiliary; absence is not null.
  delete finding.affected[0].database_specific.vanir_signatures;
  finding.affected[0].database_specific.vanir_signatures_modified = "2026-10-12T05:00:00Z";
  assert.equal(evaluateReport(actual, reports, review).status, "pass-with-auxiliary-changes");
  delete finding.affected[0].database_specific.vanir_signatures_modified;
  assert.equal(evaluateReport(actual, reports, review).status, "pass-with-reviewed-findings");
});

test("historical pure Vanir change projects equally, added fix range does not", () => {
  assert.deepEqual(vanirProjection(fixture("")), vanirProjection(fixture("-vanir")));
  assert.notDeepEqual(vanirProjection(fixture("-vanir")), vanirProjection(fixture()));
  const baseline = {
    finding: fixture("-vanir"),
    projection: vanirProjection(fixture("-vanir")),
    policy: VANIR_POLICY,
  };
  const report = { component: "core/cpython", source: "osv" };
  assert.equal(classifyAuxiliary(report, fixture(""), baseline).classification, "auxiliary-only");
  assert.equal(classifyAuxiliary(report, fixture(), baseline).classification, "review-required");
  const changed = fixture("-vanir");
  changed.affected[0].database_specific.vanir_signatures[0].digest.function_hash = "123";
  assert.equal(classifyAuxiliary(report, changed, baseline).classification, "auxiliary-only");
});

for (const [name, mutate] of [
  [
    "details",
    (f) => {
      f.details += " new prerequisite";
    },
  ],
  [
    "summary",
    (f) => {
      f.summary += " new impact";
    },
  ],
  [
    "severity",
    (f) => {
      f.severity = [];
    },
  ],
  [
    "withdrawn",
    (f) => {
      f.withdrawn = "2026-10-12T00:00:00Z";
    },
  ],
  [
    "references",
    (f) => {
      f.references.pop();
    },
  ],
  [
    "aliases",
    (f) => {
      f.aliases.push("CVE-new");
    },
  ],
  [
    "related",
    (f) => {
      f.related = [];
    },
  ],
  [
    "upstream",
    (f) => {
      f.upstream = ["CVE-new"];
    },
  ],
  [
    "credits",
    (f) => {
      f.credits = [];
    },
  ],
  [
    "schema version",
    (f) => {
      f.schema_version = "2.0.0";
    },
  ],
  [
    "fixed_in",
    (f) => {
      f.fixed_in = ["3.13.17"];
    },
  ],
  [
    "package",
    (f) => {
      f.affected[0].package = { name: "another" };
    },
  ],
  [
    "versions",
    (f) => {
      f.affected[0].versions.pop();
    },
  ],
  [
    "range",
    (f) => {
      f.affected[0].ranges[0].events.push({ fixed: "new-commit" });
    },
  ],
  [
    "repository",
    (f) => {
      f.affected[0].ranges[0].repo += "-other";
    },
  ],
  [
    "CWE",
    (f) => {
      f.database_specific.cwe_ids = ["CWE-unknown"];
    },
  ],
  [
    "source",
    (f) => {
      f.affected[0].database_specific.source += "-other";
    },
  ],
  [
    "unknown top field",
    (f) => {
      f.unknown = true;
    },
  ],
  [
    "unknown sibling",
    (f) => {
      f.affected[0].database_specific.new_severity = "critical";
    },
  ],
  [
    "unknown ecosystem field",
    (f) => {
      f.affected[0].ecosystem_specific = { affected_functions: ["new"] };
    },
  ],
  [
    "similar name",
    (f) => {
      f.affected[0].database_specific.vanir_signatures_extra = [];
    },
  ],
  [
    "wrong path",
    (f) => {
      f.database_specific.vanir_signatures = [];
    },
  ],
  [
    "missing details",
    (f) => {
      delete f.details;
    },
  ],
  [
    "missing parent",
    (f) => {
      delete f.affected[0].database_specific;
    },
  ],
  [
    "null parent",
    (f) => {
      f.affected[0].database_specific = null;
    },
  ],
  [
    "array parent",
    (f) => {
      f.affected[0].database_specific = [];
    },
  ],
  [
    "wrong affected type",
    (f) => {
      f.affected = {};
    },
  ],
  [
    "null signatures",
    (f) => {
      f.affected[0].database_specific.vanir_signatures = null;
    },
  ],
  [
    "wrong signatures type",
    (f) => {
      f.affected[0].database_specific.vanir_signatures = {};
    },
  ],
  [
    "invalid signature",
    (f) => {
      f.affected[0].database_specific.vanir_signatures = [{}];
    },
  ],
  [
    "unknown signature field",
    (f) => {
      f.affected[0].database_specific.vanir_signatures[0].severity = "critical";
    },
  ],
  [
    "unknown signature version",
    (f) => {
      f.affected[0].database_specific.vanir_signatures[0].signature_version = "v2";
    },
  ],
  [
    "unknown digest field",
    (f) => {
      f.affected[0].database_specific.vanir_signatures[0].digest.new_field = true;
    },
  ],
  [
    "wrong hash type",
    (f) => {
      f.affected[0].database_specific.vanir_signatures[0].digest.function_hash = 1;
    },
  ],
  [
    "unknown target field",
    (f) => {
      f.affected[0].database_specific.vanir_signatures[0].target.new_field = true;
    },
  ],
  [
    "wrong timestamp type",
    (f) => {
      f.affected[0].database_specific.vanir_signatures_modified = 123;
    },
  ],
  [
    "invalid timestamp",
    (f) => {
      f.affected[0].database_specific.vanir_signatures_modified = "not a date";
    },
  ],
])
  test("Vanir exception rejects mixed change: " + name, () => {
    const { review, finding, reports } = context();
    mutate(finding);
    const result = evaluateReport(actual, reports, review);
    assert.equal(result.status, "failed");
    assert.equal(result.changes[0].classification, "review-required");
    assert.match(result.error, /unreviewed or changed/);
    assert.throws(() => evaluate(actual, reports, review), /unreviewed or changed/);
  });

test("unreviewed IDs, unconfigured findings, other components and other sources stay strict", () => {
  for (const variation of [
    "new-id",
    "no-policy",
    "pypi",
    "other-core",
    "other-source",
    "missing-source",
  ]) {
    const { review, finding, reports } = context();
    if (variation === "new-id") finding.id = "CVE-new";
    if (variation === "no-policy") delete review.findings[key].auxiliary;
    if (variation === "other-source") reports[0].source = "pypi";
    if (variation === "missing-source") delete reports[0].source;
    if (variation === "pypi" || variation === "other-core") {
      const component = variation === "pypi" ? "PyPI/cpython/3.13.2" : "core/expat";
      const scope = variation === "pypi" ? "PyPI/cpython" : component;
      reports[0].component = component;
      review.findings[scope + "/" + finding.id] = { ...review.findings[key] };
      delete review.findings[key];
      review.core = variation === "pypi" ? [] : [{ name: "expat" }];
      const inventory = {
        distributions: variation === "pypi" ? [{ name: "cpython", version: "3.13.2" }] : [],
      };
      review.inventory_sha256 = digest(canonical(inventory));
      assert.throws(() => evaluate(inventory, reports, review), /invalid auxiliary review policy/);
    } else assert.throws(() => evaluate(actual, reports, review), /unreviewed or changed/);
  }
});

test("baseline must be explicitly authenticated and policy version understood", () => {
  for (const mutate of [
    (r) => {
      r.findings[key].sha256 = [];
    },
    (r) => {
      r.findings[key].reason = "unknown";
    },
    (r) => {
      r.findings[key].auxiliary.policy = "future";
    },
    (r) => {
      r.findings[key].auxiliary.baseline = "../testdata/CVE-2026-82049.json";
    },
    (r) => {
      r.findings[key].auxiliary.extra = true;
    },
    (r) => {
      r.core[0].repo = "untrusted/cpython";
    },
  ]) {
    const { review, reports } = context();
    mutate(review);
    assert.throws(() => evaluate(actual, reports, review), /auxiliary/);
  }
});

test("auxiliary changes cannot mask inventory, coverage or duplicate finding failures", () => {
  const { review, reports } = context();
  assert.throws(
    () => evaluate({ distributions: [{ name: "x", version: "1" }] }, reports, review),
    /inventory/,
  );
  for (const changed of [[], [...reports, ...reports]])
    assert.throws(() => evaluate(actual, changed, review), /coverage/);
  reports[0].findings.push(reports[0].findings[0]);
  assert.throws(() => evaluate(actual, reports, review), /duplicate/);
  assert.equal(evaluateReport(actual, reports, review).status, "failed");
});

test("queryCore keeps full-content consistency and propagates service errors", async () => {
  const { finding } = context();
  await assert.rejects(
    queryCore({ name: "cpython", commit: "pinned", advisories: [finding.id] }, async (url) =>
      url.endsWith("/query") ? { vulns: [finding] } : fixture(),
    ),
    /inconsistent core advisory/,
  );
  await assert.rejects(
    queryCore({ name: "cpython", commit: "pinned" }, async () => {
      throw new Error("unavailable");
    }),
    /unavailable/,
  );
  const report = await queryCore({ name: "cpython", commit: "pinned" }, async () => ({
    vulns: [finding],
  }));
  assert.equal(report.source, "osv");
  assert.equal(findingDigest(report.findings[0]), findingDigest(finding));
});
