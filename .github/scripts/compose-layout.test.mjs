import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { runCompose } from "./preview-compose.mjs";
import { prepareEnvironment } from "./preview-env.mjs";

const root = path.resolve(import.meta.dirname, "../..");

function render(args, overrides = {}, envFile = "/dev/null") {
  const env = { ...process.env, ...overrides };
  for (const key of ["COMPOSE_FILE", "COMPOSE_PROFILES", "COMPOSE_PROJECT_NAME"]) delete env[key];
  if (!Object.hasOwn(overrides, "ETHERVIEW_CONFIG_FILE")) delete env.ETHERVIEW_CONFIG_FILE;
  return JSON.parse(
    runCompose(["--env-file", envFile, ...args, "config", "--format", "json"], { env }),
  );
}

function assertApplicationPaths(service, config) {
  assert.equal(service.build.context, root, "build from the repository root");
  const mounts = service.volumes.filter((mount) => mount.target === "/etc/etherview/config.yaml");
  assert.equal(mounts.length, 1);
  assert.equal(mounts[0].source, path.join(root, config));
  assert.equal(mounts[0].read_only, true);
}

test("push private keys reach only the sending role in Preview and deployment", (t) => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "etherview-push-compose-"));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  const file = path.join(directory, "push.env");
  fs.writeFileSync(
    file,
    "PREVIEW_HTTPS_PORT=19443\nETHERVIEW_PUSH_ENABLED=true\nETHERVIEW_PUSH_PUBLIC_KEY=public-fixture\n" +
      "ETHERVIEW_PUSH_CONTACT=mailto:operator@example.com\n" +
      "ETHERVIEW_PUSH_PRIVATE_KEY=private-fixture\nETHERVIEW_PUSH_PRIVATE_KEY_FILE=/run/push-key\n" +
      "ETHERVIEW_PUSH_UNSAFE_ALLOW_PRIVATE_NETWORKS=true\n",
  );
  for (const args of [
    [],
    ...["monolith", "distributed"].map((mode) => [
      "--project-directory",
      root,
      "-f",
      "deploy/compose.yaml",
      "--profile",
      mode,
    ]),
  ]) {
    // Clear the host's push overrides so this proves loading from one env file.
    const config = render(
      args,
      Object.fromEntries(
        Object.keys(process.env)
          .filter((key) => key.startsWith("ETHERVIEW_PUSH_"))
          .map((key) => [key, undefined]),
      ),
      file,
    );
    if (args.length === 0)
      assert.equal(
        config.services["preview-gateway"].ports.find((port) => port.target === 443).published,
        "19443",
      );
    for (const [role, service] of Object.entries(config.services)) {
      const sender = role === "etherview" || role === "maintenance";
      assert.equal(
        service.environment?.ETHERVIEW_PUSH_UNSAFE_ALLOW_PRIVATE_NETWORKS,
        sender ? "true" : undefined,
        role,
      );
      assert.equal(
        service.environment?.ETHERVIEW_PUSH_PRIVATE_KEY,
        sender ? "private-fixture" : undefined,
        role,
      );
      assert.equal(
        service.environment?.ETHERVIEW_PUSH_PRIVATE_KEY_FILE,
        sender ? "/run/push-key" : undefined,
        role,
      );
      if (role === "api" || sender)
        assert.equal(service.environment.ETHERVIEW_PUSH_PUBLIC_KEY, "public-fixture");
    }
  }
});

test("both Compose entry points pass branding only to the application build", () => {
  for (const args of [
    [],
    ["--project-directory", root, "-f", "deploy/compose.yaml", "--profile", "monolith"],
  ]) {
    for (const directory of ["", "branding/custom brand"]) {
      const config = render(args, { ETHERVIEW_WEB_BRANDING_DIR: directory });
      for (const service of Object.values(config.services)) {
        assert.equal(service.environment?.ETHERVIEW_WEB_BRANDING_DIR, undefined);
        if (service.build?.target === "production") {
          assert.equal(
            service.build.args.ETHERVIEW_WEB_BRANDING_DIR,
            directory || "web/branding/default",
          );
        }
      }
    }
  }
});

test("default Compose selects Preview and retains its project and volumes", () => {
  assert.equal(fs.existsSync(path.join(root, "compose.preview.yaml")), false);
  const config = render([]);
  assert.equal(config.name, "etherview-preview");
  assert.equal(config.services.etherview, undefined);
  for (const role of ["api", "sync", "enrich", "trace", "metadata", "maintenance"]) {
    assertApplicationPaths(config.services[role], "preview/config.yaml");
    assert.equal(config.services[role].profiles, undefined);
  }
  for (const name of [
    "postgres-data",
    "compiler-cache",
    "geth-data",
    "lighthouse-data",
    "validator-data",
    "blob-data",
    "ipfs-data",
  ]) {
    assert.equal(config.volumes[name].name, `etherview-preview_${name}`);
  }
});

test("Preview shares the local UserOperation registry across application roles", (t) => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "etherview-userop-compose-"));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  const file = path.join(directory, "preview.env");
  const entries = [
    { address: "0x1111111111111111111111111111111111111111", version: "0.9", from_block: 42 },
  ];
  fs.writeFileSync(
    file,
    `ETHERVIEW_FEATURE_USER_OPERATIONS=true\nETHERVIEW_ERC4337_ENTRY_POINTS='${JSON.stringify(entries)}'\n`,
  );
  const config = render(
    [],
    { ETHERVIEW_FEATURE_USER_OPERATIONS: undefined, ETHERVIEW_ERC4337_ENTRY_POINTS: undefined },
    file,
  );
  for (const role of ["api", "sync", "enrich", "trace", "metadata", "maintenance"]) {
    assert.equal(config.services[role].environment.ETHERVIEW_FEATURE_USER_OPERATIONS, "true", role);
    assert.deepEqual(
      JSON.parse(config.services[role].environment.ETHERVIEW_ERC4337_ENTRY_POINTS),
      entries,
      role,
    );
  }
});

for (const mode of ["monolith", "distributed"]) {
  test(`deployment ${mode} retains root-relative paths and separate data`, () => {
    const args = ["--project-directory", root, "-f", "deploy/compose.yaml", "--profile", mode];
    const config = render(args);
    assert.equal(config.name, "etherview");
    const roles =
      mode === "monolith"
        ? ["etherview"]
        : ["api", "sync", "enrich", "trace", "metadata", "maintenance"];
    for (const role of [...roles, "migration"]) {
      assertApplicationPaths(config.services[role], "deploy/config.example.yaml");
    }
    for (const name of [
      "geth",
      "lighthouse",
      "validator",
      "ipfs",
      "preview-gateway",
      "archive-init",
    ]) {
      assert.equal(config.services[name], undefined);
    }
    for (const name of ["postgres-data", "compiler-cache"]) {
      assert.equal(config.volumes[name].name, `etherview_${name}`);
    }
    const override = render(args, { ETHERVIEW_CONFIG_FILE: "./custom/config.yaml" });
    assertApplicationPaths(override.services[roles[0]], "custom/config.yaml");
  });
}

test("deployment overlays keep repository-root build contexts and mounts", () => {
  const config = render(
    [
      "--project-directory",
      root,
      "-f",
      "deploy/compose.yaml",
      "-f",
      "e2e/runtime/compose.yaml",
      "--profile",
      "distributed",
    ],
    { ANVIL_ARGS: "" },
  );
  assertApplicationPaths(config.services.api, "deploy/config.example.yaml");
  assert.ok(config.services["runtime-fixture"]);
});

test("Preview Make commands pass the unified env path through the Compose native environment", (t) => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "preview compose env "));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  const file = path.join(directory, "preview.env");
  prepareEnvironment(file);
  fs.appendFileSync(file, "PREVIEW_HTTPS_PORT=19444\n");
  const config = JSON.parse(
    runCompose(["-f", "compose.yaml", "config", "--format", "json"], {
      env: { ...process.env, COMPOSE_ENV_FILES: file, PREVIEW_HTTPS_PORT: undefined },
    }),
  );
  assert.equal(
    config.services["preview-gateway"].ports.find((port) => port.target === 443).published,
    "19444",
  );
  assert.equal(config.services.maintenance.environment.ETHERVIEW_PUSH_ENABLED, "true");
  assert.equal(config.services.api.environment.ETHERVIEW_PUSH_PRIVATE_KEY, undefined);
  const makefile = fs.readFileSync(path.join(root, "Makefile"), "utf8");
  assert.match(
    makefile,
    /^start-preview recreate-preview stop-preview preview-images preview-network-runtime: export COMPOSE_ENV_FILES = \$\(PREVIEW_ENV_FILE\)$/m,
  );
});

for (const key of [
  "ETHERVIEW_BYTECODE_DB_UNSAFE_ALLOW_PRIVATE_NETWORKS",
  "ETHERVIEW_SOURCIFY_UNSAFE_ALLOW_PRIVATE_NETWORKS",
]) {
  test(`${key} reaches Preview and both deployment topologies`, () => {
    for (const value of [undefined, "false", "true"]) {
      for (const [args, owner] of [
        [[], "api"],
        [
          ["--project-directory", root, "-f", "deploy/compose.yaml", "--profile", "monolith"],
          "etherview",
        ],
        [
          ["--project-directory", root, "-f", "deploy/compose.yaml", "--profile", "distributed"],
          "api",
        ],
      ]) {
        const config = render(args, { [key]: value });
        assert.equal(config.services[owner].environment[key], value ?? null);
      }
    }
  });
}

test("image navigation origins preserve defaults and reach API in every topology", () => {
  const key = "ETHERVIEW_METADATA_IMAGE_NAVIGATION_ALLOWED_ORIGINS";
  for (const value of [undefined, "", "https://ipfs.etherview.localhost,https://127.0.0.1:8443"]) {
    for (const [args, owner] of [
      [[], "api"],
      [
        ["--project-directory", root, "-f", "deploy/compose.yaml", "--profile", "monolith"],
        "etherview",
      ],
      [
        ["--project-directory", root, "-f", "deploy/compose.yaml", "--profile", "distributed"],
        "api",
      ],
    ]) {
      const config = render(args, { [key]: value });
      assert.equal(config.services[owner].environment[key], value ?? null);
    }
  }
});
