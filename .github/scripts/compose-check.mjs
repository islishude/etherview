import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import { resolve } from "node:path";

import { runCompose } from "./preview-compose.mjs";

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

function node(args, input, overrides = {}) {
  const command = process.env.NODE || process.execPath;
  const executable = existsSync(resolve(root, command));
  const result = spawnSync(
    executable ? resolve(root, command) : "/bin/sh",
    executable ? args : ["-c", `exec ${command} "$@"`, "compose-check", ...args],
    {
      cwd: root,
      env: { ...process.env, ...overrides },
      input,
      stdio: ["pipe", "inherit", "inherit"],
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

function config(name, args, overrides = {}, validator, validatorEnv = {}) {
  scenario(name, () => {
    const json = runCompose(
      [...args, "config", ...(validator ? ["--format", "json"] : ["--quiet"])],
      {
        env: { ...process.env, ...overrides },
        stdio: ["ignore", "pipe", "inherit"],
      },
    );
    if (typeof validator === "function") validator(JSON.parse(json));
    else if (validator)
      node([`.github/scripts/${validator}-compose-check.mjs`], json, validatorEnv);
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
      "s3",
      { ETHERVIEW_COMPOSE_TOPOLOGY: topology },
    );
  config("accelerators", [...deployment, "--profile", "accelerators"]);
  for (const topology of topologies)
    config(
      `compiler ${topology}`,
      [...deployment, "--profile", topology],
      compilerPaths,
      "compiler",
      {
        ETHERVIEW_COMPOSE_TOPOLOGY: topology,
        ETHERVIEW_EXPECT_COMPILER_RUNTIME_PATH_OVERRIDE: "true",
      },
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
      "hardhat3",
      { ETHERVIEW_HARDHAT3_TOPOLOGY: topology, ETHERVIEW_HARDHAT3_IMAGE: image },
    );
  }
  for (const topology of topologies)
    config(
      `Vyper ${topology}`,
      [...verification, "-f", "e2e/runtime/vyper.compose.yaml", "--profile", topology],
      {},
      "hardhat3",
      {
        ETHERVIEW_HARDHAT3_TOPOLOGY: topology,
        ETHERVIEW_VERIFICATION_SUITE: "vyper",
      },
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
      "foundry",
      { ETHERVIEW_FOUNDRY_TOPOLOGY: topology, ETHERVIEW_FOUNDRY_IMAGE: image },
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

try {
  main();
} catch (error) {
  console.error(`compose-check: ${error.message}`);
  process.exitCode = 1;
}
