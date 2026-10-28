import assert from "node:assert/strict";
import test from "node:test";

import { sharedRuntimes } from "../../compiler/vyper/wasm/release.mjs";
import {
  mergeProductionEvidence,
  applicationVersions,
  productionIdentity,
} from "./production-evidence.mjs";

const hash = "a".repeat(64);
function fixture() {
  const identity = {
    revision: "c".repeat(40),
    architecture: "amd64",
    image_id: "sha256:" + hash,
    image_artifact_id: "123",
    packages_artifact_id: "456",
    lock_sha256: hash,
    policy_sha256: hash,
  };
  const expected = Object.fromEntries(
    applicationVersions.map((version) => [
      version,
      {
        version,
        name: `vyper-${version}.json`,
        digest: hash,
        compiler_sha256: hash,
        matrix: { executor_sha256: hash, cases: 2, fixtures_sha256: hash },
      },
    ]),
  );
  const matrix = {
    native_linux: true,
    image_id: identity.image_id,
    host_sha256: hash,
    shared_runtimes: sharedRuntimes,
    descriptors: Object.fromEntries(Object.values(expected).map((e) => [e.name, e.digest])),
    matrix: Object.fromEntries(Object.values(expected).map((e) => [e.version, e.matrix])),
  };
  const hardhat = {
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
  };
  const vyper = {
    jobs: 12,
    results: 12,
    succeeded_jobs: 12,
    succeeded_results: 12,
    cases: applicationVersions.flatMap((version) =>
      ["native", "etherscan"].map((protocol) => ({
        version,
        protocol,
        compiler_digest: hash,
        executor_digest: hash,
        abi_sha256: hash,
        sources_sha256: hash,
        constructor: "0x" + "0".repeat(62) + "2a",
        runtime_match: "partial",
        creation_match: version === "0.4.3" ? "full" : "partial",
      })),
    ),
  };
  const app = (role, snapshot) => ({
    schema: "etherview-application-result-v1",
    role,
    native_linux: true,
    architecture: "amd64",
    image_id: identity.image_id,
    cleanup: true,
    topologies: { monolith: structuredClone(snapshot), distributed: structuredClone(snapshot) },
  });
  const records = [
    ["fixtures", matrix],
    ["hardhat", app("hardhat", hardhat)],
    ["vyper", app("vyper", vyper)],
  ].map(([role, result]) => ({
    schema: "etherview-production-evidence-v1",
    role,
    identity: structuredClone(identity),
    result,
  }));
  return { identity, expected, records };
}
test("joins all three roles without changing final acceptance shape", () => {
  const { identity, expected, records } = fixture();
  assert.deepEqual(mergeProductionEvidence(records.reverse(), identity, expected), {
    ...records[2].result,
    monolith: true,
    split: true,
  });
});
for (const [name, mutate] of Object.entries({
  "missing role": (r) => r.pop(),
  "duplicate role": (r) => {
    r[2] = structuredClone(r[1]);
  },
  "wrong revision": (r) => {
    r[1].identity.revision = "d".repeat(40);
  },
  "wrong architecture": (r) => {
    r[1].identity.architecture = "arm64";
  },
  "wrong image": (r) => {
    r[1].identity.image_id = "sha256:" + "b".repeat(64);
  },
  "wrong image artifact": (r) => {
    r[1].identity.image_artifact_id = "789";
  },
  "wrong package artifact": (r) => {
    r[2].identity.packages_artifact_id = "789";
  },
  "wrong lock": (r) => {
    r[2].identity.lock_sha256 = "b".repeat(64);
  },
  "wrong runtime policy": (r) => {
    r[2].identity.policy_sha256 = "b".repeat(64);
  },
  "stale fixture": (r) => {
    r[0].result.matrix["0.4.3"].fixtures_sha256 = "b".repeat(64);
  },
  "missing version": (r) => {
    delete r[0].result.matrix["0.4.3"];
  },
  "extra descriptor": (r) => {
    r[0].result.descriptors.extra = hash;
  },
  "non native fixture": (r) => {
    r[0].result.native_linux = false;
  },
  "non native application": (r) => {
    r[1].result.native_linux = false;
  },
  "cleanup failure": (r) => {
    r[2].result.cleanup = false;
  },
  "missing topology": (r) => {
    delete r[1].result.topologies.distributed;
  },
  "parity mismatch": (r) => {
    r[2].result.topologies.distributed.jobs--;
  },
  "wrong suite": (r) => {
    r[1].result.role = "vyper";
  },
  "loaded wrong image": (r) => {
    r[1].result.image_id = "sha256:" + "b".repeat(64);
  },
  "missing Vyper case": (r) => {
    for (const s of Object.values(r[2].result.topologies)) s.cases.pop();
  },
  "duplicate Vyper case": (r) => {
    for (const s of Object.values(r[2].result.topologies)) s.cases[0] = s.cases[1];
  },
  "extra Hardhat result": (r) => {
    for (const s of Object.values(r[1].result.topologies)) s.CompilerResults++;
  },
  "wrong language": (r) => {
    for (const s of Object.values(r[1].result.topologies)) s.VyperJobs++;
  },
  "wrong executor": (r) => {
    for (const s of Object.values(r[2].result.topologies))
      s.cases[0].executor_digest = "b".repeat(64);
  },
  "wrong compiler": (r) => {
    for (const s of Object.values(r[2].result.topologies))
      s.cases[0].compiler_digest = "b".repeat(64);
  },
}))
  test(`rejects ${name}`, () => {
    const { identity, expected, records } = fixture();
    // Separate expected data from mutable fixture evidence.
    const independent = structuredClone(expected);
    mutate(records);
    assert.throws(() => mergeProductionEvidence(records, identity, independent));
  });

// Aggregation trusts only the producer output, checked against this checkout and lane.
test("producer identity is required and bound to revision and architecture", () => {
  const identity = {
    schema: "etherview-production-image-v1",
    revision: "c".repeat(40),
    architecture: "amd64",
    image_id: "sha256:" + hash,
  };
  assert.deepEqual(productionIdentity(identity, identity.revision, "amd64"), identity);
  for (const [key, value] of [
    ["schema", "unknown"],
    ["revision", "d".repeat(40)],
    ["architecture", "arm64"],
    ["image_id", "invalid"],
  ]) {
    assert.throws(() =>
      productionIdentity({ ...identity, [key]: value }, identity.revision, "amd64"),
    );
  }
  assert.throws(() => productionIdentity(undefined, identity.revision, "amd64"));
});
