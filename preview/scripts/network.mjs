import { spawnSync } from "node:child_process";
import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

import { runCompose } from "../../.github/scripts/compose.mjs";

export const generatorImage = "ethpandaops/ethereum-genesis-generator:6.2.7";
const root = path.resolve(import.meta.dirname, "../..");
const inputs = [
  "pnpm-lock.yaml",
  "pnpm-workspace.yaml",
  "preview/bundler.Dockerfile",
  "preview/rundler.toml",
  "preview/bundler-start.mjs",
  "preview/genesis.json",
  "preview/generate.sh",
  "cmd/previewensmanifest/main.go",
  "preview/contracts/manifest.json",
  "preview/contracts/allocation.json",
  "preview/contracts/generate.mjs",
  "e2e/x402local/contracts/LocalBillingToken.sol",
  "preview/contracts/upstream.mjs",
  "preview/contracts/ens-upstream.mjs",
  "preview/contracts/package.json",
  ...fs
    .readdirSync(path.join(root, "preview/contracts/upstream"))
    .sort()
    .map((name) => `preview/contracts/upstream/${name}`),
];
const required = [
  "metadata/genesis.json",
  "metadata/config.yaml",
  "metadata/genesis.ssz",
  "metadata/ens.json",
  "jwt.hex",
];
function digest(value) {
  return crypto.createHash("sha256").update(value).digest("hex");
}
export function fingerprint() {
  return digest(
    generatorImage +
      inputs.map((file) => fs.readFileSync(path.join(root, file), "utf8")).join("\n"),
  );
}
function files(directory, relative = "") {
  return fs
    .readdirSync(path.join(directory, relative), { withFileTypes: true })
    .sort((a, b) => a.name.localeCompare(b.name))
    .flatMap((entry) => {
      const name = path.join(relative, entry.name);
      if (entry.isSymbolicLink()) throw new Error("Network material must not contain symlinks");
      return entry.isDirectory() ? files(directory, name) : [name];
    });
}
export function validateNetwork(directory) {
  const manifest = JSON.parse(fs.readFileSync(path.join(directory, "manifest.json"), "utf8"));
  if (manifest.version !== 1 || manifest.fingerprint !== fingerprint())
    throw new Error("Preview network configuration changed; explicitly rebuild Preview");
  for (const file of required)
    if (!manifest.files[file]) throw new Error(`Missing Preview network material: ${file}`);
  const keys = Object.keys(manifest.files).filter((file) =>
    /^validator\/keys\/0x[0-9a-f]{96}\/voting-keystore\.json$/.test(file),
  );
  if (
    keys.length !== 64 ||
    keys.some((file) => !manifest.files[`validator/secrets/${file.split("/")[2]}`])
  )
    throw new Error("Preview requires all 64 validator keystores and passwords");
  const actual = files(directory).filter((file) => file !== "manifest.json");
  if (
    JSON.stringify(actual) !==
    JSON.stringify(Object.keys(manifest.files).sort((a, b) => a.localeCompare(b)))
  )
    throw new Error("Preview network file inventory changed");
  for (const file of actual)
    if (digest(fs.readFileSync(path.join(directory, file))) !== manifest.files[file])
      throw new Error(`Preview network integrity mismatch: ${file}`);
  return manifest;
}
function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: root,
    encoding: "utf8",
    stdio: "inherit",
    ...options,
  });
  if (result.error || result.status !== 0)
    throw new Error(
      `${command} failed (${result.status}): ${result.error?.message ?? "see diagnostics"}`,
    );
  return result.stdout;
}
export function rejectLegacyOverrides(env = process.env) {
  if (env.ETHERVIEW_GENESIS_FILE || env.GETH_GENESIS_FILE)
    throw new Error(
      "Use PREVIEW_NETWORK_DIR with a complete network bundle; individual Genesis overrides are unsupported",
    );
}
export function prepareNetwork(directory, { checkOnly = false, env = process.env } = {}) {
  rejectLegacyOverrides(env);
  if (fs.existsSync(directory)) return validateNetwork(directory);
  if (checkOnly) throw new Error("Preview network missing; run make start-preview first");
  const docker = env.DOCKER || "docker";
  const project = env.COMPOSE_PROJECT_NAME || "etherview-preview";
  const volumes = run(
    docker,
    ["volume", "ls", "-q", "--filter", `label=com.docker.compose.project=${project}`],
    { stdio: "pipe" },
  ).trim();
  if (volumes)
    throw new Error(
      "Existing Preview volumes without network material; run make stop-preview before initialization",
    );
  const available = spawnSync(docker, ["image", "inspect", generatorImage], { stdio: "ignore" });
  if (available.status !== 0) run(docker, ["pull", generatorImage]);
  fs.mkdirSync(path.dirname(directory), { recursive: true });
  const temporary = fs.mkdtempSync(`${directory}.tmp-`);
  try {
    fs.mkdirSync(path.join(temporary, "metadata"));
    const genesis = JSON.parse(fs.readFileSync(path.join(root, "preview/genesis.json"), "utf8"));
    genesis.timestamp = `0x${(BigInt(Math.floor(Date.now() / 1000)) + 90n).toString(16)}`;
    fs.writeFileSync(
      path.join(temporary, "metadata/genesis.json"),
      JSON.stringify(genesis, null, 2) + "\n",
    );
    fs.writeFileSync(path.join(temporary, "jwt.hex"), crypto.randomBytes(32).toString("hex"), {
      mode: 0o600,
    });
    run(docker, [
      "run",
      "--rm",
      "--network=none",
      "--user",
      `${process.getuid?.() ?? 0}:${process.getgid?.() ?? 0}`,
      "--entrypoint=/bin/bash",
      "-v",
      `${temporary}:/data`,
      "-v",
      `${path.join(root, "preview/generate.sh")}:/generate.sh:ro`,
      generatorImage,
      "/generate.sh",
    ]);
    run(
      env.GO || "go",
      [
        "run",
        "./cmd/previewensmanifest",
        "-genesis",
        path.join(temporary, "metadata/genesis.json"),
        "-output",
        path.join(temporary, "metadata/ens.json"),
      ],
      { cwd: root },
    );
    const manifest = { version: 1, fingerprint: fingerprint(), files: {} };
    for (const file of files(temporary))
      manifest.files[file] = digest(fs.readFileSync(path.join(temporary, file)));
    fs.writeFileSync(
      path.join(temporary, "manifest.json"),
      JSON.stringify(manifest, null, 2) + "\n",
    );
    validateNetwork(temporary);
    fs.renameSync(temporary, directory);
    return manifest;
  } finally {
    fs.rmSync(temporary, { recursive: true, force: true });
  }
}
export function composeEnvironment(
  compose = process.env.COMPOSE || path.join(root, ".github/scripts/compose.sh"),
) {
  // Let Compose resolve .env and interpolation, exactly as it does for mounts
  // and volume ownership. Capture its output without logging credentials.
  const output = runCompose(["-f", "compose.yaml", "config", "--environment"], {}, compose);
  const env = { ...process.env };
  for (const line of output.split("\n")) {
    const equals = line.indexOf("=");
    if (equals > 0) env[line.slice(0, equals)] = line.slice(equals + 1);
  }
  return env;
}
function main() {
  const mode = process.argv[2] || "prepare";
  const env = composeEnvironment();
  const directory = path.resolve(
    env.PREVIEW_NETWORK_DIR || path.join(root, ".local/preview-network"),
  );
  if (!["prepare", "check", "clean"].includes(mode))
    throw new Error("Expected prepare, check or clean");
  if (mode === "clean") {
    // Custom bundles belong to their caller and are never recursively removed.
    if (!env.PREVIEW_NETWORK_DIR) fs.rmSync(directory, { recursive: true, force: true });
    return;
  }
  prepareNetwork(directory, {
    checkOnly: mode === "check" || Boolean(env.PREVIEW_NETWORK_DIR),
    env,
  });
  console.log(`preview-network: verified ${directory}`);
}
if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  try {
    main();
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
