import assert from "node:assert/strict";

const config = JSON.parse(await readStdin());
const topology = process.env.ETHERVIEW_COMPOSE_TOPOLOGY;
assert.ok(
  topology === "monolith" || topology === "distributed",
  "ETHERVIEW_COMPOSE_TOPOLOGY must be monolith or distributed",
);

const compilerOwner = topology === "monolith" ? "etherview" : "api";
const applicationServices =
  topology === "monolith"
    ? ["etherview"]
    : ["api", "sync", "enrich", "trace", "metadata", "maintenance"];
const cachePath = "/var/lib/etherview/compilers";
const unsafeDownloadEnvironment =
  "ETHERVIEW_VERIFICATION_UNSAFE_ALLOW_PRIVATE_DOWNLOAD_NETWORKS";
const removedEnvironment = [
  "ETHERVIEW_COMPILER_SANDBOX",
  "ETHERVIEW_VERIFICATION_RUNNER_ENDPOINT",
  "ETHERVIEW_VERIFICATION_RUNNER_IMAGE",
];

assert.ok(config.services?.[compilerOwner], `missing ${compilerOwner} service`);
assert.ok(
  !Object.keys(config.services).some((name) => name.includes("compiler-runner")),
  "compiler-runner service must not exist",
);
assertNoPlatform(config, "compose");

for (const [name, service] of Object.entries(config.services)) {
  for (const key of removedEnvironment) {
    assert.ok(
      !Object.hasOwn(service.environment ?? {}, key),
      `${name} still receives removed ${key}`,
    );
  }
  assert.ok(
    !Object.hasOwn(service.environment ?? {}, unsafeDownloadEnvironment),
    `${name} must not receive the Preview/E2E-only unsafe download escape hatch`,
  );
}

for (const name of applicationServices) {
  const service = config.services[name];
  assert.ok(service, `missing application service ${name}`);
  const cacheTargets = tmpfsTargets(service).filter(
    (target) => target === cachePath,
  );
  if (name === compilerOwner) {
    assert.equal(
      cacheTargets.length,
      1,
      `${name} must receive exactly one private compiler cache`,
    );
    assert.ok(
      Object.hasOwn(
        service.environment ?? {},
        "ETHERVIEW_VERIFICATION_WORKER_COUNT",
      ),
      `${name} must own verification workers`,
    );
  } else {
    assert.equal(
      cacheTargets.length,
      0,
      `${name} must not receive the compiler cache`,
    );
    assert.ok(
      !Object.hasOwn(
        service.environment ?? {},
        "ETHERVIEW_VERIFICATION_WORKER_COUNT",
      ),
      `${name} must not own verification workers`,
    );
  }
}

function tmpfsTargets(service) {
  return (service.tmpfs ?? []).map((entry) => {
    if (typeof entry === "string") {
      return entry.split(":", 1)[0];
    }
    return entry?.target;
  });
}

function assertNoPlatform(value, path) {
  if (Array.isArray(value)) {
    value.forEach((item, index) => assertNoPlatform(item, `${path}[${index}]`));
    return;
  }
  if (!value || typeof value !== "object") {
    return;
  }
  for (const [key, child] of Object.entries(value)) {
    assert.notEqual(key, "platform", `${path} contains a platform field`);
    assertNoPlatform(child, `${path}.${key}`);
  }
}

async function readStdin() {
  const chunks = [];
  for await (const chunk of process.stdin) {
    chunks.push(chunk);
  }
  return Buffer.concat(chunks).toString("utf8");
}
