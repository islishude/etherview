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
  for (const key of removedEnvironment) {
    assert.ok(
      !Object.hasOwn(service.environment ?? {}, key),
      `${name} still receives removed ${key}`,
    );
  }
  const hasCompilerCache = tmpfsTargets(service).includes(cachePath);
  assert.equal(
    hasCompilerCache,
    name === compilerOwner,
    `${name} compiler cache scope`,
  );
  assert.equal(
    service.environment?.[unsafeDownloadEnvironment],
    name === compilerOwner ? "true" : undefined,
    `${name} Hardhat fake-IP download exception scope`,
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
