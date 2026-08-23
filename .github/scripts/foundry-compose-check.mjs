import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const config = JSON.parse(readFileSync(0, "utf8"));
const topology = process.env.ETHERVIEW_FOUNDRY_TOPOLOGY;
assert.ok(topology === "monolith" || topology === "distributed", "invalid topology");

const foundryImage = process.env.ETHERVIEW_FOUNDRY_IMAGE;
const apiService = topology === "monolith" ? "etherview" : "api";
const compilerOwner = apiService;
const appServices = [
  "etherview", "api", "sync", "enrich", "trace", "metadata", "maintenance",
];
const cachePath = "/var/lib/etherview/compilers";
const unsafeDownloadEnvironment =
  "ETHERVIEW_VERIFICATION_UNSAFE_ALLOW_PRIVATE_DOWNLOAD_NETWORKS";
const derivedEnvironment = [
  "ETHERVIEW_DERIVED_VERIFY_ENABLED",
  "ETHERVIEW_DERIVED_VERIFY_BACKFILL_ENABLED",
  "ETHERVIEW_DERIVED_VERIFY_FORWARD_ENABLED",
];

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
    `${name} Foundry ENS isolation`,
  );
  assert.ok(
    [undefined, null, ""].includes(service.environment?.ETHERVIEW_ENS_RPC_URLS),
    `${name} must not receive an ENS RPC`,
  );
  const cacheMounts = volumeMounts(service).filter((mount) => mount.target === cachePath);
  const hasCompilerCache = cacheMounts.length === 1 && cacheMounts[0].type === "volume";
  assert.equal(hasCompilerCache, name === compilerOwner, `${name} compiler cache scope`);
  assert.equal(tmpfsTargets(service).includes(cachePath), false, `${name} compiler cache tmpfs`);
  assert.equal(
    service.environment?.[unsafeDownloadEnvironment],
    name === compilerOwner ? "true" : undefined,
    `${name} Foundry fake-IP download exception scope`,
  );
  for (const key of derivedEnvironment) {
    assert.equal(
      service.environment?.[key],
      name === compilerOwner ? "true" : undefined,
      `${name} Foundry derived verification scope ${key}`,
    );
  }
}

function volumeMounts(service) {
  return (service.volumes ?? []).map((entry) =>
    typeof entry === "string"
      ? { type: "volume", source: entry.split(":", 1)[0], target: entry.split(":")[1] }
      : entry
  );
}

const foundry = config.services.foundry;
assert.ok(foundry, "Foundry client service is required");
assert.equal(foundry.image, foundryImage, "Foundry client image");
assert.equal(foundry.read_only, true, "Foundry client root filesystem");
assert.equal(foundry.privileged ?? false, false, "Foundry client must not be privileged");
assert.deepEqual(foundry.cap_drop, ["ALL"], "Foundry client capabilities");
assert.equal(
  (foundry.volumes ?? []).length,
  0,
  "Foundry client must not receive host or named volumes",
);
assert.equal(
  foundry.environment?.ETHERVIEW_FOUNDRY_RPC_URL,
  "http://runtime-fixture:8545",
  "Foundry RPC URL",
);
assert.equal(
  foundry.environment?.ETHERVIEW_FOUNDRY_VERIFIER_URL,
  `http://${apiService}:8080/v2/api?chainid=1`,
  "Foundry verifier URL",
);
assert.equal(foundry.environment?.FOUNDRY_OFFLINE, "true", "Foundry runtime compiler mode");
assert.ok(
  !Object.keys(foundry.environment ?? {}).some((key) => key.includes("API_KEY")),
  "Foundry client must not receive a static API key",
);
const foundryTmpfs = tmpfsTargets(foundry);
assert.equal(
  foundryTmpfs.includes("/workspace/cache"),
  false,
  "Foundry disposable project cache must not be writable",
);
for (const target of [
  "/tmp",
  "/workspace/out",
  "/workspace/broadcast",
  "/home/foundry/.cache",
  "/home/foundry/.foundry",
]) {
  assert.ok(foundryTmpfs.includes(target), `missing Foundry tmpfs ${target}`);
}

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
