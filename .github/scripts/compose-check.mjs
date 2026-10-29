import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import { resolve } from "node:path";

import { runCompose } from "./compose.mjs";

const root = resolve(import.meta.dirname, "../..");
const topologies = ["monolith", "distributed"];
const deployment = ["--project-directory", ".", "-f", "deploy/compose.yaml"];
const runtime = [...deployment, "-f", "e2e/runtime/compose.yaml"];
const verification = [...runtime, "-f", "e2e/runtime/verification.compose.yaml"];
const compilerPaths = {
  ETHERVIEW_VERIFICATION_EXECUTOR_PATH: "/custom/runtime/etherview-solcjs",
  ETHERVIEW_VERIFICATION_GEAS_PATH: "/custom/bin/etherview-geas-compiler",
};
const applicationOverrides = {
  ETHERVIEW_DATABASE_READ_MAX_CONNECTIONS: "7",
  ETHERVIEW_DATABASE_READ_MIN_CONNECTIONS: "1",
  ETHERVIEW_LOG_LEVEL: "debug",
  ETHERVIEW_LOG_FORMAT: "text",
  ETHERVIEW_SYNC_PROGRESS_LOG_INTERVAL: "45s",
  ETHERVIEW_USER_AUTH_API_KEY_RATE: "25",
  ETHERVIEW_USER_AUTH_API_KEY_BURST: "50",
  ETHERVIEW_USER_AUTH_MAX_ACTIVE_API_KEYS: "7",
};

function node(args) {
  const command = process.env.NODE || process.execPath;
  const executable = existsSync(resolve(root, command));
  const result = spawnSync(
    executable ? resolve(root, command) : "/bin/sh",
    executable ? args : ["-c", `exec ${command} "$@"`, "compose-check", ...args],
    {
      cwd: root,
      stdio: "inherit",
    },
  );
  if (result.error || result.status !== 0)
    throw new Error(
      `Node ${args[0]} failed (${result.status}): ${result.error?.message ?? "see diagnostics"}`,
    );
}

function scenario(name, run) {
  try {
    run();
  } catch (error) {
    throw new Error(`${name}: ${error.message}`);
  }
}

function config(name, args, overrides = {}, validator) {
  scenario(name, () => {
    const json = runCompose(
      [...args, "config", ...(validator ? ["--format", "json"] : ["--quiet"])],
      {
        env: { ...process.env, ...overrides },
        stdio: ["ignore", "pipe", "inherit"],
      },
    );
    if (validator) validator(JSON.parse(json));
  });
}

function main() {
  scenario("Docker Compose availability", () =>
    runCompose(["version"], { stdio: ["ignore", "ignore", "inherit"] }),
  );
  scenario("Compose layout regressions", () =>
    node(["--test", ".github/scripts/compose-layout.test.mjs"]),
  );
  for (const topology of topologies)
    config(`x402 ${topology}`, ["-f", "e2e/x402local/compose.yaml", "--profile", topology]);
  assert.ok(!existsSync(resolve(root, "compose.tls.yaml")), "compose.tls.yaml must remain removed");
  for (const topology of topologies)
    config(`deployment ${topology}`, [...deployment, "--profile", topology]);
  for (const topology of topologies)
    config(
      `S3 ${topology}`,
      [...deployment, "--profile", topology, "--profile", "accelerators"],
      {
        ETHERVIEW_S3_ACCESS_KEY: "compose-access",
        ETHERVIEW_S3_SECRET_KEY: "compose-secret",
        ETHERVIEW_S3_SESSION_TOKEN: "compose-session",
        AWS_ACCESS_KEY_ID: "aws-access",
        AWS_SECRET_ACCESS_KEY: "aws-secret",
        AWS_SESSION_TOKEN: "aws-session",
        AWS_CONTAINER_CREDENTIALS_FULL_URI: "http://169.254.170.23/v1/credentials",
        AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE: "/run/aws/pod-identity-token",
      },
      (document) => assertStorage(document, topology),
    );
  config("accelerators", [...deployment, "--profile", "accelerators"]);
  for (const topology of topologies)
    config(
      `compiler ${topology}`,
      [...deployment, "--profile", topology],
      compilerPaths,
      (document) => assertCompiler(document, topology),
    );
  for (const topology of topologies)
    config(`runtime ${topology}`, [...runtime, "--profile", topology]);
  for (const topology of topologies) {
    const image = process.env.HARDHAT3_IMAGE ?? "etherview-hardhat3:local";
    config(
      `Hardhat ${topology}`,
      [
        ...verification,
        "-f",
        "e2e/hardhat3/compose.yaml",
        "--profile",
        topology,
        "--profile",
        "hardhat-client",
      ],
      {
        ETHERVIEW_HARDHAT3_IMAGE: image,
        ETHERVIEW_HARDHAT3_API_SERVICE: topology === "monolith" ? "etherview" : "api",
        ETHERVIEW_HARDHAT3_ARTIFACT_DIR: "/tmp/etherview-hardhat3-compose-check",
      },
      (document) => assertVerification(document, topology, "hardhat", image),
    );
  }
  for (const topology of topologies)
    config(
      `Vyper ${topology}`,
      [...verification, "-f", "e2e/runtime/vyper.compose.yaml", "--profile", topology],
      {},
      (document) => assertVerification(document, topology, "vyper"),
    );
  for (const topology of topologies) {
    const image = process.env.FOUNDRY_IMAGE ?? "etherview-foundry:local";
    config(
      `Foundry ${topology}`,
      [
        ...runtime,
        "-f",
        "e2e/foundry/compose.yaml",
        "--profile",
        topology,
        "--profile",
        "foundry-client",
      ],
      {
        ETHERVIEW_FOUNDRY_IMAGE: image,
        ETHERVIEW_FOUNDRY_API_SERVICE: topology === "monolith" ? "etherview" : "api",
      },
      (document) => assertVerification(document, topology, "foundry", image),
    );
  }
  const args = [...deployment, "--env-file", "/dev/null", "--profile", "monolith"];
  config(
    "unset application overrides",
    args,
    Object.fromEntries(Object.keys(applicationOverrides).map((key) => [key, undefined])),
    ({ services }) => {
      for (const key of Object.keys(applicationOverrides))
        assert.equal(
          services.etherview.environment[key],
          null,
          `${key} must remain unset when no Compose override is supplied`,
        );
    },
  );
  config("application overrides", args, applicationOverrides, ({ services }) => {
    for (const [key, value] of Object.entries(applicationOverrides))
      assert.equal(
        services.etherview.environment[key],
        value,
        `Compose ${key} override was not preserved`,
      );
  });
  for (const [label, files, owner] of [
    ["monolith", [...deployment, "--profile", "monolith"], "etherview"],
    ["distributed", [...deployment, "--profile", "distributed"], "api"],
  ]) {
    config(
      `Turnstile ${label}`,
      files,
      {
        ETHERVIEW_TURNSTILE_ENABLED: "true",
        ETHERVIEW_TURNSTILE_SITE_KEY: "public-widget",
        ETHERVIEW_TURNSTILE_SECRET_KEY: "private-widget",
        ETHERVIEW_TURNSTILE_SECRET_KEY_FILE: "/run/secrets/turnstile",
      },
      ({ services }) => {
        assert.equal(services[owner].environment.ETHERVIEW_TURNSTILE_ENABLED, "true");
        assert.equal(services[owner].environment.ETHERVIEW_TURNSTILE_SITE_KEY, "public-widget");
        assert.equal(services[owner].environment.ETHERVIEW_TURNSTILE_SECRET_KEY, "private-widget");
        for (const [name, service] of Object.entries(services)) {
          if (["api", "etherview"].includes(name)) continue;
          assert.ok(
            !Object.hasOwn(service.environment ?? {}, "ETHERVIEW_TURNSTILE_SECRET_KEY"),
            `Turnstile secret leaked to ${name}`,
          );
          assert.ok(
            !Object.hasOwn(service.environment ?? {}, "ETHERVIEW_TURNSTILE_SECRET_KEY_FILE"),
            `Turnstile file leaked to ${name}`,
          );
        }
      },
    );
  }
}

function assertCompiler(config, topology) {
  const compilerOwner = topology === "monolith" ? "etherview" : "api";
  const applicationServices =
    topology === "monolith"
      ? ["etherview"]
      : ["api", "sync", "enrich", "trace", "metadata", "maintenance"];
  const cachePath = "/var/lib/etherview/compilers";
  const unsafeDownloadEnvironment = "ETHERVIEW_VERIFICATION_UNSAFE_ALLOW_PRIVATE_DOWNLOAD_NETWORKS";
  const unsafeMetadataEnvironment = "ETHERVIEW_METADATA_UNSAFE_ALLOW_PRIVATE_NETWORKS";
  const runtimePathEnvironment = {
    ETHERVIEW_VERIFICATION_EXECUTOR_PATH: "/custom/runtime/etherview-solcjs",
    ETHERVIEW_VERIFICATION_GEAS_PATH: "/custom/bin/etherview-geas-compiler",
  };
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
    assert.ok(
      !Object.hasOwn(service.environment ?? {}, unsafeMetadataEnvironment),
      `${name} must not receive the Preview-only metadata network escape hatch`,
    );
    if (name !== compilerOwner) {
      for (const key of Object.keys(runtimePathEnvironment)) {
        assert.ok(
          !Object.hasOwn(service.environment ?? {}, key),
          `${name} must not receive compiler runtime path ${key}`,
        );
      }
    }
  }

  for (const [key, expected] of Object.entries(runtimePathEnvironment)) {
    assert.equal(
      config.services[compilerOwner].environment?.[key],
      expected,
      `${compilerOwner} ${key}`,
    );
  }

  for (const name of applicationServices) {
    const service = config.services[name];
    assert.ok(service, `missing application service ${name}`);
    assertApplicationHealthcheck(service, name);
    const cacheMounts = volumeMounts(service).filter((mount) => mount.target === cachePath);
    if (name === compilerOwner) {
      assert.equal(
        cacheMounts.length,
        1,
        `${name} must receive exactly one persistent compiler cache`,
      );
      assert.equal(cacheMounts[0].type, "volume", `${name} compiler cache type`);
      assert.ok(cacheMounts[0].source, `${name} compiler cache source`);
      assert.equal(
        tmpfsTargets(service).includes(cachePath),
        false,
        `${name} compiler cache must not be tmpfs`,
      );
      assert.ok(
        Object.hasOwn(service.environment ?? {}, "ETHERVIEW_VERIFICATION_WORKER_COUNT"),
        `${name} must own verification workers`,
      );
    } else {
      assert.equal(cacheMounts.length, 0, `${name} must not receive the compiler cache`);
      assert.ok(
        !Object.hasOwn(service.environment ?? {}, "ETHERVIEW_VERIFICATION_WORKER_COUNT"),
        `${name} must not own verification workers`,
      );
    }
  }
}

function assertVerification(config, topology, suite, image) {
  const compilerOwner = topology === "monolith" ? "etherview" : "api";
  const appServices = ["etherview", "api", "sync", "enrich", "trace", "metadata", "maintenance"];
  const cachePath = "/var/lib/etherview/compilers";
  const unsafeDownloadEnvironment = "ETHERVIEW_VERIFICATION_UNSAFE_ALLOW_PRIVATE_DOWNLOAD_NETWORKS";
  const derivedEnvironment = [
    "ETHERVIEW_DERIVED_VERIFY_ENABLED",
    "ETHERVIEW_DERIVED_VERIFY_BACKFILL_ENABLED",
    "ETHERVIEW_DERIVED_VERIFY_FORWARD_ENABLED",
  ];
  const removedEnvironment = [
    "ETHERVIEW_FEATURE_PROXY_DETECTION_V2",
    "ETHERVIEW_FEATURE_PROXY_DETECTION_V2_PUBLIC",
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
      volumes.some(
        (volume) =>
          volume.source === "/var/run/docker.sock" || volume.target === "/var/run/docker.sock",
      ),
      false,
      `${name} must not mount a Docker socket`,
    );
    assert.equal(service.privileged ?? false, false, `${name} must not be privileged`);
    assert.equal(
      service.environment?.ETHERVIEW_FEATURE_ENS,
      "false",
      `${name} verification ENS isolation`,
    );
    assert.equal(
      service.environment?.ETHERVIEW_FEATURE_USER_OPERATIONS,
      "false",
      `${name} verification UserOperation isolation`,
    );
    assert.equal(
      service.environment?.ETHERVIEW_ERC4337_ENTRY_POINTS,
      "[]",
      `${name} must not receive a runtime EntryPoint registry`,
    );
    assert.ok(
      [undefined, null, ""].includes(service.environment?.ETHERVIEW_ENS_RPC_URLS),
      `${name} must not receive an ENS RPC`,
    );
    if (suite !== "foundry") {
      assert.equal(
        service.environment?.ETHERVIEW_FEATURE_SAFE_PROXY_DETECTION,
        "true",
        `${name} verification Safe proxy detection`,
      );
      assert.equal(
        service.environment?.ETHERVIEW_FEATURE_DIAMOND_PROXY_DETECTION,
        "true",
        `${name} verification Diamond proxy detection`,
      );
    }
    for (const key of removedEnvironment) {
      assert.ok(
        !Object.hasOwn(service.environment ?? {}, key),
        `${name} still receives removed ${key}`,
      );
    }
    const cacheMounts = volumeMounts(service).filter((mount) => mount.target === cachePath);
    const hasCompilerCache = cacheMounts.length === 1 && cacheMounts[0].type === "volume";
    assert.equal(hasCompilerCache, name === compilerOwner, `${name} compiler cache scope`);
    assert.equal(tmpfsTargets(service).includes(cachePath), false, `${name} compiler cache tmpfs`);
    assert.equal(
      service.environment?.[unsafeDownloadEnvironment],
      name === compilerOwner ? "true" : undefined,
      `${name} verification fake-IP download exception scope`,
    );
    for (const key of derivedEnvironment) {
      assert.equal(
        service.environment?.[key],
        name === compilerOwner ? "true" : undefined,
        `${name} verification derived verification scope ${key}`,
      );
    }
  }

  if (suite === "vyper") {
    assert.equal(config.services.hardhat, undefined, "Vyper must not start a Hardhat client");
  } else if (suite === "foundry") {
    const foundry = config.services.foundry;
    assert.ok(foundry, "Foundry client service is required");
    assert.equal(foundry.image, image, "Foundry client image");
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
      `http://${compilerOwner}:8080/v2/api?chainid=1`,
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
  } else {
    const hardhat = config.services.hardhat;
    assert.ok(hardhat, "Hardhat client service is required");
    assert.equal(hardhat.image, image, "Hardhat client image");
    assert.equal(hardhat.privileged ?? false, false, "Hardhat client must not be privileged");
    assert.equal(
      (hardhat.volumes ?? []).some((volume) => volume.target === "/var/run/docker.sock"),
      false,
      "Hardhat client must not mount a Docker socket",
    );
    const buildVolumes = (hardhat.volumes ?? []).filter((volume) =>
      [
        "/repo/e2e/hardhat3/build",
        "/repo/e2e/hardhat3/artifacts",
        "/repo/e2e/hardhat3/cache",
      ].includes(volume.target),
    );
    assert.equal(buildVolumes.length, 1, "Hardhat build state must use one filesystem");
    assert.equal(buildVolumes[0].target, "/repo/e2e/hardhat3/build", "Hardhat build volume");
    const cacheInspectionMounts = volumeMounts(hardhat).filter(
      (mount) => mount.target === cachePath,
    );
    assert.equal(cacheInspectionMounts.length, 1, "Hardhat compiler cache inspection mount");
    assert.equal(cacheInspectionMounts[0].type, "volume", "Hardhat compiler cache mount type");
    assert.equal(
      cacheInspectionMounts[0].source,
      "compiler-cache",
      "Hardhat compiler cache source",
    );
    assert.equal(
      cacheInspectionMounts[0].read_only,
      true,
      "Hardhat compiler cache inspection must be read-only",
    );
  }
}

function assertStorage(config, topology) {
  const services = config.services ?? {};
  const apiServiceName = topology === "monolith" ? "etherview" : "api";
  const apiEnvironment = services[apiServiceName]?.environment;
  if (!apiEnvironment) {
    throw new Error(`missing ${apiServiceName} environment`);
  }

  const expected = {
    ETHERVIEW_S3_ACCESS_KEY: "compose-access",
    ETHERVIEW_S3_SECRET_KEY: "compose-secret",
    ETHERVIEW_S3_SESSION_TOKEN: "compose-session",
    AWS_ACCESS_KEY_ID: "aws-access",
    AWS_SECRET_ACCESS_KEY: "aws-secret",
    AWS_SESSION_TOKEN: "aws-session",
    AWS_CONTAINER_CREDENTIALS_FULL_URI: "http://169.254.170.23/v1/credentials",
    AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE: "/run/aws/pod-identity-token",
  };
  for (const [name, value] of Object.entries(expected)) {
    if (apiEnvironment[name] !== value) {
      throw new Error(`${apiServiceName} ${name}=${apiEnvironment[name]}, want ${value}`);
    }
  }

  for (const serviceName of ["migration", "sync", "enrich", "trace", "metadata", "maintenance"]) {
    const environment = services[serviceName]?.environment;
    if (!environment) {
      continue;
    }
    for (const name of Object.keys(expected)) {
      if (Object.hasOwn(environment, name)) {
        throw new Error(`${serviceName} unexpectedly receives ${name}`);
      }
    }
  }

  const storage = services["object-storage"];
  if (
    storage?.image !== "rustfs/rustfs:1.0.4" ||
    JSON.stringify(storage.command) !== JSON.stringify(["/data"])
  ) {
    throw new Error("object-storage must run the pinned RustFS image with /data");
  }
  if (
    !storage.environment?.RUSTFS_ACCESS_KEY ||
    !storage.environment?.RUSTFS_SECRET_KEY ||
    Object.keys(storage.environment).some((key) => key.startsWith("MINIO_"))
  ) {
    throw new Error("object-storage requires RustFS credentials without MinIO aliases");
  }
  if (
    storage.user ||
    storage.ports?.length ||
    storage.environment.RUSTFS_ADDRESS !== ":9000" ||
    storage.environment.RUSTFS_CONSOLE_ADDRESS !== ":9001" ||
    String(storage.environment.RUSTFS_CONSOLE_ENABLE) !== "true"
  ) {
    throw new Error("RustFS must retain its default user and internal listeners");
  }
  if (
    !storage.volumes?.some(
      (volume) =>
        volume.type === "volume" && volume.source === "rustfs-data" && volume.target === "/data",
    ) ||
    storage.volumes.some((volume) => volume.source === "object-data")
  ) {
    throw new Error("RustFS must use its own fresh data volume");
  }
  if (
    JSON.stringify(storage.healthcheck?.test) !==
    JSON.stringify(["CMD", "curl", "-fsS", "http://127.0.0.1:9000/health/ready"])
  ) {
    throw new Error("RustFS storage readiness probe is missing");
  }
  for (const [name, service] of Object.entries(services)) {
    if (
      name !== "object-storage" &&
      (service.depends_on?.["object-storage"] ||
        Object.keys(service.environment ?? {}).some((key) => key.startsWith("RUSTFS_")))
    ) {
      throw new Error(`${name} must not depend on RustFS or receive its root credentials`);
    }
  }
}

function volumeMounts(service) {
  return (service.volumes ?? []).map((entry) =>
    typeof entry === "string"
      ? { type: "volume", source: entry.split(":", 1)[0], target: entry.split(":")[1] }
      : entry,
  );
}

function tmpfsTargets(service) {
  return (service.tmpfs ?? []).map((entry) => {
    if (typeof entry === "string") {
      return entry.split(":", 1)[0];
    }
    return entry?.target;
  });
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
  assert.equal(service.healthcheck.start_period, "10s", `${name} health start period`);
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

try {
  main();
} catch (error) {
  console.error(`compose-check: ${error.message}`);
  process.exitCode = 1;
}
