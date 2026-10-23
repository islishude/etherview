// Revalidate all shard evidence against the current image, packages and fixtures.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync, writeFileSync, mkdtempSync, readdirSync, rmSync } from "node:fs";
import { resolve, join } from "node:path";
import { arch, platform, tmpdir } from "node:os";
import { randomUUID } from "node:crypto";
import { sha256, sharedRuntimes, runtimeFor } from "./release.mjs";
import {
  assignments,
  mergeEvidence,
  shardFile,
  shardOptions,
  versionEvidence,
  versions,
} from "./production-shards.mjs";

const { count } = shardOptions(process.argv.slice(2), true);
const docker = process.env.DOCKER ?? "docker";
const image = process.env.IMAGE ?? "etherview:local";
const architecture = arch() === "x64" ? "amd64" : arch();
assert(["amd64", "arm64"].includes(architecture));
const root = resolve(".local/vyper-releases");
const packages = resolve(".local/vyper-wasm/packages");
const names = Array.from({ length: count }, (_, index) => shardFile(architecture, index, count));
assert.deepEqual(
  readdirSync(root)
    .filter((name) => name.startsWith(`matrix-${architecture}-shard-`))
    .sort(),
  [...names].sort(),
  "missing or extra shard files",
);
const imageID = execFileSync(docker, ["image", "inspect", "--format", "{{.Id}}", image], {
  encoding: "utf8",
}).trim();
assert.equal(
  execFileSync(docker, ["image", "inspect", "--format", "{{.Architecture}}", imageID], {
    encoding: "utf8",
  }).trim(),
  architecture,
);
const temporary = mkdtempSync(join(tmpdir(), "vyper-matrix-merge-"));
const container = `vyper-matrix-merge-${randomUUID()}`;
try {
  execFileSync(docker, ["create", "--name", container, imageID]);
  execFileSync(docker, [
    "cp",
    `${container}:/opt/etherview/solcjs/runtime-manifest.json`,
    join(temporary, "host.json"),
  ]);

  for (const [id, digest] of Object.entries(sharedRuntimes)) {
    execFileSync(docker, [
      "cp",
      `${container}:/opt/etherview/python-wasm/${id}/shared-manifest.json`,
      join(temporary, id + ".json"),
    ]);
    assert.equal(sha256(readFileSync(join(temporary, id + ".json"))), digest);
  }
  const identity = {
    native_linux: platform() === "linux",
    image_id: imageID,
    host_sha256: sha256(readFileSync(join(temporary, "host.json"))),
    shared_runtimes: sharedRuntimes,
  };
  const expected = Object.fromEntries(
    versions.map((item) => [
      item.version,
      versionEvidence(item, root, packages, identity.host_sha256),
    ]),
  );
  const evidence = mergeEvidence(
    names.map((name) => JSON.parse(readFileSync(join(root, name)))),
    assignments(count),
    architecture,
    identity,
    expected,
  );
  writeFileSync(join(root, `matrix-${architecture}.json`), JSON.stringify(evidence) + "\n", {
    flag: "wx",
  });
  console.log(`${architecture}: merged ${count} shards, ${versions.length} versions`);
} finally {
  try {
    execFileSync(docker, ["rm", "-f", container], { timeout: 5000 });
  } finally {
    rmSync(temporary, { recursive: true, force: true });
  }
}
