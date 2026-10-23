// Offline join of native fixtures and two independently cleaned application suites.
import assert from "node:assert/strict";
import { readFileSync, writeFileSync, mkdirSync, readdirSync } from "node:fs";
import { join, resolve } from "node:path";
import { execFileSync } from "node:child_process";
import { canonical } from "../../compiler/vyper/wasm/tooling.mjs";
import { sha256, sharedRuntimes } from "../../compiler/vyper/wasm/release.mjs";
import { versionEvidence, versions } from "../../compiler/vyper/wasm/production-shards.mjs";

export const applicationVersions = ["0.2.1", "0.3.1", "0.3.4", "0.3.10", "0.4.0", "0.4.3"];
export function validateApplication(role, result, identity) {
  assert.equal(result.schema, "etherview-application-result-v1");
  assert.equal(result.role, role);
  assert.equal(result.native_linux, true, "non-native application evidence");
  assert.equal(result.architecture, identity.architecture);
  assert.equal(result.image_id, identity.image_id);
  assert.equal(result.cleanup, true, "application cleanup incomplete");
  assert.deepEqual(Object.keys(result.topologies).sort(), ["distributed", "monolith"]);
  const snapshot = result.topologies.monolith;
  assert.deepEqual(snapshot, result.topologies.distributed, "application parity mismatch");
  if (role === "hardhat") {
    for (const [key, value] of Object.entries({
      AddressJobs: 9,
      CompilerResults: 11,
      VyperJobs: 0,
      VyperResults: 0,
      DerivedJobs: 1,
      DerivedResults: 1,
      ProxyJobs: 11,
      ProxyResults: 11,
      ProxyBindings: 11,
      YulJobs: 1,
      YulResults: 1,
      ExecutorProvenance: true,
      CompilerProvenance: true,
      YulProvenance: true,
    }))
      assert.equal(snapshot[key], value, `incorrect Hardhat ${key}`);
  } else {
    assert.equal(role, "vyper");
    for (const key of ["jobs", "results", "succeeded_jobs", "succeeded_results"])
      assert.equal(snapshot[key], 12);
    assert.deepEqual(
      snapshot.cases.map((c) => `${c.version}/${c.protocol}`).sort(),
      applicationVersions.flatMap((v) => ["native", "etherscan"].map((p) => `${v}/${p}`)).sort(),
      "missing or duplicate Vyper case",
    );
    for (const c of snapshot.cases) {
      for (const key of ["compiler_digest", "executor_digest", "abi_sha256", "sources_sha256"])
        assert.match(c[key], /^[a-f0-9]{64}$/);
      assert.equal(c.constructor, "0x" + "0".repeat(62) + "2a");
      assert.equal(c.runtime_match, "partial");
      assert.equal(c.creation_match, c.version === "0.4.3" ? "full" : "partial");
    }
  }
}

export function mergeProductionEvidence(records, identity, expected) {
  assert.deepEqual(
    records.map((r) => r.role).sort(),
    ["fixtures", "hardhat", "vyper"],
    "missing or duplicate evidence role",
  );
  for (const record of records) {
    assert.equal(record.schema, "etherview-production-evidence-v1");
    assert.deepEqual(record.identity, identity, "evidence identity mismatch");
  }
  const matrix = records.find((r) => r.role === "fixtures").result;
  assert.equal(matrix.native_linux, true);
  assert.equal(matrix.image_id, identity.image_id);
  assert.deepEqual(matrix.shared_runtimes, sharedRuntimes);
  assert.deepEqual(Object.keys(matrix.matrix).sort(), Object.keys(expected).sort());
  assert.deepEqual(
    Object.keys(matrix.descriptors).sort(),
    Object.values(expected)
      .map((e) => e.name)
      .sort(),
  );
  for (const entry of Object.values(expected)) {
    assert.equal(matrix.descriptors[entry.name], entry.digest);
    assert.deepEqual(matrix.matrix[entry.version], entry.matrix, "stale fixture/package evidence");
  }
  for (const role of ["hardhat", "vyper"])
    validateApplication(role, records.find((r) => r.role === role).result, identity);
  for (const c of records.find((r) => r.role === "vyper").result.topologies.monolith.cases) {
    assert.equal(
      c.executor_digest,
      expected[c.version].matrix.executor_sha256,
      "application executor differs from matrix",
    );
    assert.equal(
      c.compiler_digest,
      expected[c.version].compiler_sha256,
      "application compiler differs from lock",
    );
  }
  return { ...matrix, monolith: true, split: true };
}

export function productionIdentity(metadata, revision, architecture) {
  assert.equal(metadata.revision, revision, "image source revision mismatch");
  assert.equal(metadata.architecture, architecture, "image architecture mismatch");
  assert.equal(metadata.schema, "etherview-production-image-v1");
  assert(["amd64", "arm64"].includes(metadata.architecture));
  assert.match(metadata.image_id, /^sha256:[a-f0-9]{64}$/);
  return metadata;
}

function context() {
  const metadata = productionIdentity(
    JSON.parse(process.env.PRODUCTION_IMAGE_IDENTITY),
    process.env.GITHUB_SHA ??
      execFileSync("git", ["rev-parse", "HEAD"], { encoding: "utf8" }).trim(),
    process.env.EXPECTED_ARCH,
  );
  const identity = { ...metadata };
  delete identity.schema;
  identity.image_artifact_id = process.env.IMAGE_ARTIFACT_ID;
  identity.packages_artifact_id = process.env.PACKAGES_ARTIFACT_ID;
  for (const key of ["image_artifact_id", "packages_artifact_id"])
    assert.match(identity[key] ?? "", /^[1-9][0-9]*$/, "exact artifact ID required");
  identity.lock_sha256 = sha256(readFileSync("compiler/vyper/application-lock.json"));
  identity.policy_sha256 = sha256(readFileSync("compiler/vyper/wasm/runtime-policy.json"));
  return identity;
}

if (import.meta.main) {
  const [mode, role, ...extra] = process.argv.slice(2);
  assert.equal(extra.length, 0);
  const identity = context();
  const directory = resolve(".local/production-evidence");
  mkdirSync(directory, { recursive: true });
  if (mode === "record") {
    assert(["fixtures", "hardhat", "vyper"].includes(role));
    const file =
      role === "fixtures"
        ? `.local/vyper-releases/matrix-${identity.architecture}.json`
        : `.local/production-results/${role}.json`;
    const result = JSON.parse(readFileSync(file));
    if (role !== "fixtures") validateApplication(role, result, identity);
    assert.equal(result.image_id, identity.image_id);
    assert.equal(result.native_linux, true);
    writeFileSync(
      join(directory, role + ".json"),
      JSON.stringify({ schema: "etherview-production-evidence-v1", role, identity, result }) + "\n",
      { flag: "wx" },
    );
    console.log(
      `${role}: ${identity.architecture} image=${identity.image_id} image-artifact=${identity.image_artifact_id} packages-artifact=${identity.packages_artifact_id}`,
    );
  } else {
    assert.equal(mode, "merge");
    assert.equal(role, undefined);
    assert.deepEqual(readdirSync(directory).sort(), [
      "fixtures.json",
      "hardhat.json",
      "vyper.json",
    ]);
    const records = ["fixtures", "hardhat", "vyper"].map((r) =>
      JSON.parse(readFileSync(join(directory, r + ".json"))),
    );
    const matrix = records.find((r) => r.role === "fixtures").result;
    const recipes = JSON.parse(readFileSync("compiler/vyper/application-lock.json")).recipes;
    const expected = Object.fromEntries(
      versions.map((item) => {
        const entry = versionEvidence(
          item,
          ".local/vyper-releases",
          ".local/vyper-wasm/packages",
          matrix.host_sha256,
        );
        const recipe = recipes.find((r) => r.version === item.version);
        assert(recipe);
        assert.equal(entry.matrix.package_sha256, sha256(canonical(recipe.manifest) + "\n"));
        return [
          item.version,
          { ...entry, version: item.version, compiler_sha256: item.compiler_sha256 },
        ];
      }),
    );
    const evidence = mergeProductionEvidence(records, identity, expected);
    writeFileSync(
      `.local/vyper-releases/acceptance-${identity.architecture}.json`,
      JSON.stringify({ ["linux-" + identity.architecture]: evidence }) + "\n",
      { flag: "wx" },
    );
    console.log(
      `accepted: ${identity.architecture} image=${identity.image_id} image-artifact=${identity.image_artifact_id} packages-artifact=${identity.packages_artifact_id}`,
    );
  }
}
