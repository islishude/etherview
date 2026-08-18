import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const config = JSON.parse(readFileSync(0, "utf8"));
const topology = process.env.ETHERVIEW_HARDHAT3_TOPOLOGY;
assert.ok(topology === "monolith" || topology === "distributed", "invalid topology");

const hardhatImage = process.env.ETHERVIEW_HARDHAT3_IMAGE;
const compilerOwner = topology === "monolith" ? "etherview" : "api";
const appServices = [
  "etherview", "api", "sync", "enrich", "trace", "metadata", "maintenance",
];
const cachePath = "/var/lib/etherview/compilers";
const unsafeDownloadEnvironment =
  "ETHERVIEW_VERIFICATION_UNSAFE_ALLOW_PRIVATE_DOWNLOAD_NETWORKS";
const removedEnvironment = [
  "ETHERVIEW_COMPILER_SANDBOX",
  "ETHERVIEW_VERIFICATION_RUNNER_ENDPOINT",
  "ETHERVIEW_VERIFICATION_RUNNER_IMAGE",
];

assert.equal(config.services.verify, undefined, "removed verify service");
assert.ok(
  !Object.keys(config.services).some((name) => name.includes("compiler-runner")),
  "removed compiler-runner service",
);
assertNoPlatform(config, "compose");

for (const name of appServices) {
  const service = config.services[name];
  if (service === undefined) continue;
  const volumes = service.volumes ?? [];
  assert.equal(
    volumes.some((volume) => volume.source === "/var/run/docker.sock" ||
      volume.target === "/var/run/docker.sock"),
    false,
    `${name} must not mount a Docker socket`,
  );
  assert.equal(service.privileged ?? false, false, `${name} must not be privileged`);
  assert.equal(
    service.environment?.ETHERVIEW_FEATURE_ENS,
    "false",
    `${name} Hardhat ENS isolation`,
  );
  assert.ok(
    [undefined, null, ""].includes(service.environment?.ETHERVIEW_ENS_RPC_URLS),
    `${name} must not receive an ENS RPC`,
  );
  for (const key of removedEnvironment) {
    assert.ok(
      !Object.hasOwn(service.environment ?? {}, key),
      `${name} still receives removed ${key}`,
    );
  }
  const cacheMounts = volumeMounts(service).filter((mount) => mount.target === cachePath);
  const hasCompilerCache = cacheMounts.length === 1 && cacheMounts[0].type === "volume";
  assert.equal(
    hasCompilerCache,
    name === compilerOwner,
    `${name} compiler cache scope`,
  );
  assert.equal(tmpfsTargets(service).includes(cachePath), false, `${name} compiler cache tmpfs`);
  assert.equal(
    service.environment?.[unsafeDownloadEnvironment],
    name === compilerOwner ? "true" : undefined,
    `${name} Hardhat fake-IP download exception scope`,
  );
}

function volumeMounts(service) {
  return (service.volumes ?? []).map((entry) =>
    typeof entry === "string"
      ? { type: "volume", source: entry.split(":", 1)[0], target: entry.split(":")[1] }
      : entry
  );
}

const hardhat = config.services.hardhat;
assert.ok(hardhat, "Hardhat client service is required");
assert.equal(hardhat.image, hardhatImage, "Hardhat client image");
assert.equal(hardhat.privileged ?? false, false, "Hardhat client must not be privileged");
assert.equal(
  (hardhat.volumes ?? []).some((volume) => volume.target === "/var/run/docker.sock"),
  false,
  "Hardhat client must not mount a Docker socket",
);
const buildVolumes = (hardhat.volumes ?? []).filter((volume) =>
  ["/workspace/build", "/workspace/artifacts", "/workspace/cache"].includes(volume.target)
);
assert.equal(buildVolumes.length, 1, "Hardhat build state must use one filesystem");
assert.equal(buildVolumes[0].target, "/workspace/build", "Hardhat build volume");
const cacheInspectionMounts = volumeMounts(hardhat).filter(
  (mount) => mount.target === cachePath,
);
assert.equal(cacheInspectionMounts.length, 1, "Hardhat compiler cache inspection mount");
assert.equal(cacheInspectionMounts[0].type, "volume", "Hardhat compiler cache mount type");
assert.equal(cacheInspectionMounts[0].source, "compiler-cache", "Hardhat compiler cache source");
assert.equal(
  cacheInspectionMounts[0].read_only,
  true,
  "Hardhat compiler cache inspection must be read-only",
);

function tmpfsTargets(service) {
  return (service.tmpfs ?? []).map((entry) =>
    typeof entry === "string" ? entry.split(":", 1)[0] : entry?.target
  );
}

function assertNoPlatform(value, path) {
  if (Array.isArray(value)) {
    value.forEach((item, index) => assertNoPlatform(item, `${path}[${index}]`));
    return;
  }
  if (!value || typeof value !== "object") return;
  for (const [key, child] of Object.entries(value)) {
    assert.notEqual(key, "platform", `${path} contains a platform field`);
    assertNoPlatform(child, `${path}.${key}`);
  }
}
