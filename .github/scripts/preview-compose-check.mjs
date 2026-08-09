import assert from "node:assert/strict";
import fs from "node:fs";

const config = JSON.parse(fs.readFileSync(0, "utf8"));
const services = config.services ?? {};
const roles = ["api", "sync", "enrich", "trace", "metadata", "maintenance"];
const tlsKeys = ["ETHERVIEW_SERVER_TLS_CERT_FILE", "ETHERVIEW_SERVER_TLS_KEY_FILE"];
const tlsTargets = ["/run/etherview-tls/tls.crt", "/run/etherview-tls/tls.key"];
const cachePath = "/var/lib/etherview/compilers";
const unsafeDownloadEnvironment =
  "ETHERVIEW_VERIFICATION_UNSAFE_ALLOW_PRIVATE_DOWNLOAD_NETWORKS";
const runtimePathEnvironment = {
  ETHERVIEW_VERIFICATION_NODE_PATH: "/custom/bin/node",
  ETHERVIEW_VERIFICATION_WRAPPER_PATH: "/custom/runtime/compile.mjs",
  ETHERVIEW_VERIFICATION_MANIFEST_PATH:
    "/custom/runtime/runtime-manifest.json",
};
const expectRuntimePathOverride =
  process.env.ETHERVIEW_EXPECT_COMPILER_RUNTIME_PATH_OVERRIDE === "true";
const removedEnvironment = [
  "ETHERVIEW_COMPILER_SANDBOX",
  "ETHERVIEW_VERIFICATION_RUNNER_ENDPOINT",
  "ETHERVIEW_VERIFICATION_RUNNER_IMAGE",
];

assert.equal(services.etherview, undefined, "Preview monolith service must not exist");
assert.equal(services.verify, undefined, "removed Preview verify service");
assert.equal(services.reth, undefined, "removed Preview Reth service");
assert.ok(
  !Object.keys(services).some((name) => name.includes("compiler-runner")),
  "removed Preview compiler-runner service",
);
assertNoPlatform(config, "compose.preview.yaml");

const geth = requireService("geth");
assert.equal(geth.image, "ethereum/client-go:stable", "Preview Geth image");
assert.deepEqual(
  geth.entrypoint,
  ["/bin/sh", "/usr/local/bin/etherview-geth-entrypoint.sh"],
  "Preview Geth shell entrypoint",
);
assert.deepEqual(
  geth.command,
  [
    "--dev",
    "--dev.period=5",
    "--http",
    "--http.addr=0.0.0.0",
    "--http.port=8545",
    "--http.api=eth,net,web3,debug,txpool",
    "--http.corsdomain=*",
    "--http.vhosts=*",
    "--ws",
    "--ws.addr=0.0.0.0",
    "--ws.port=8546",
    "--ws.api=eth,net,web3,debug,txpool",
    "--ws.origins=*",
  ],
  "Preview Geth command",
);
assert.ok(
  (geth.volumes ?? []).some(
    (volume) => volume.source === "geth-data" && volume.target === "/gethdata",
  ),
  "Preview Geth data volume",
);
assert.ok(
  (geth.volumes ?? []).some(
    (volume) =>
      volume.target === "/usr/local/bin/etherview-geth-entrypoint.sh" &&
      volume.read_only,
  ),
  "Preview Geth shell entrypoint mount",
);
assert.ok(
  (geth.volumes ?? []).some(
    (volume) => volume.target === "/config/genesis.json" && volume.read_only,
  ),
  "Preview Geth Genesis mount",
);

const applicationImage = requireService("api").image;
for (const role of roles) {
  const service = requireService(role);
  assert.equal(service.image, applicationImage, `${role} production image`);
  assert.equal(service.environment.ETHERVIEW_ROLES, role, `${role} role`);
  assert.ok(
    Object.hasOwn(service.environment, "ETHERVIEW_SYNC_PROGRESS_LOG_INTERVAL"),
    `${role} sync progress override`,
  );
  assert.ok(service.command.includes(`--roles=${role}`), `${role} command`);
  assertApplicationHealthcheck(service, role);
  assert.equal(
    service.environment.ETHERVIEW_RPC_URLS,
    "http://geth:8545,ws://geth:8546",
    `${role} Preview Geth RPC URLs`,
  );
  for (const dependency of ["postgres", "migration", "geth"]) {
    assert.ok(service.depends_on?.[dependency], `${role} dependency ${dependency}`);
  }
  assert.equal(
    role !== "api" && (service.ports?.length ?? 0) > 0,
    false,
    `${role} published ports`,
  );
  assert.equal(service.privileged ?? false, false, `${role} privilege`);
  assert.equal(
    (service.volumes ?? []).some((volume) =>
      `${volume.source ?? ""}:${volume.target ?? ""}`.includes("docker.sock")),
    false,
    `${role} Docker socket`,
  );
  for (const key of removedEnvironment) {
    assert.ok(
      !Object.hasOwn(service.environment ?? {}, key),
      `${role} still receives removed ${key}`,
    );
  }
  assert.equal(
    tmpfsTargets(service).includes(cachePath),
    role === "api",
    `${role} compiler cache scope`,
  );
  assert.equal(
    service.environment?.[unsafeDownloadEnvironment],
    role === "api" ? "true" : undefined,
    `${role} Preview fake-IP download exception scope`,
  );
  for (const [key, expected] of Object.entries(runtimePathEnvironment)) {
    assert.equal(
      service.environment?.[key],
      role === "api" && expectRuntimePathOverride ? expected : undefined,
      `${role} ${key} scope`,
    );
  }
}

const api = requireService("api");
const apiTargets = new Set((api.ports ?? []).map((port) => port.target));
assert.deepEqual(apiTargets, new Set([8080, 9090]), "Preview API published ports");
assert.ok(api.environment.ETHERVIEW_SESSION_PEPPER, "Preview API session pepper");
for (const key of tlsKeys) {
  assert.ok(api.environment[key], `Preview API TLS environment ${key}`);
}
for (const target of tlsTargets) {
  assert.ok(
    (api.volumes ?? []).some((volume) =>
      volume.target === target && volume.read_only),
    `Preview API read-only TLS mount ${target}`,
  );
}

for (const [name, service] of Object.entries(services)) {
  assert.equal(service.privileged ?? false, false, `${name} privilege`);
  assert.equal((service.cap_add ?? []).length, 0, `${name} added capabilities`);
  for (const volume of service.volumes ?? []) {
    assert.equal(
      `${volume.source ?? ""}:${volume.target ?? ""}`.includes("docker.sock"),
      false,
      `${name} Docker socket`,
    );
  }
  if (name !== "api") {
    assert.ok(
      !Object.hasOwn(service.environment ?? {}, "ETHERVIEW_SESSION_PEPPER"),
      `Preview session pepper leaked to ${name}`,
    );
    assert.ok(
      !tlsKeys.some((key) => Object.hasOwn(service.environment ?? {}, key)),
      `Preview TLS environment leaked to ${name}`,
    );
    assert.ok(
      !(service.volumes ?? []).some((volume) => tlsTargets.includes(volume.target)),
      `Preview TLS material leaked to ${name}`,
    );
  }
}

function requireService(name) {
  const service = services[name];
  assert.ok(service, `missing service ${name}`);
  return service;
}

function tmpfsTargets(service) {
  return (service.tmpfs ?? []).map((entry) =>
    typeof entry === "string" ? entry.split(":", 1)[0] : entry?.target
  );
}

function assertApplicationHealthcheck(service, name) {
  assert.deepEqual(
    service.healthcheck?.test,
    ["CMD", "/etherview", "healthcheck"],
    `${name} application-native healthcheck`,
  );
  assert.equal(service.healthcheck.timeout, "3s", `${name} health timeout`);
  assert.equal(service.healthcheck.interval, "5s", `${name} health interval`);
  assert.equal(service.healthcheck.retries, 34, `${name} health retries`);
  assert.equal(
    service.healthcheck.start_period,
    "10s",
    `${name} health start period`,
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
