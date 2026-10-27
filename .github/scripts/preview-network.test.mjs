import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import crypto from "node:crypto";
import test from "node:test";
import { spawnSync } from "node:child_process";
import {
  fingerprint,
  validateNetwork,
  prepareNetwork,
  rejectLegacyOverrides,
  composeEnvironment,
} from "./preview-network.mjs";

function fixture(t) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "etherview-preview-network-"));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  const material = {
    "metadata/genesis.json": "{}",
    "metadata/config.yaml": "PRESET_BASE: mainnet",
    "metadata/genesis.ssz": "state",
    "jwt.hex": "a".repeat(64),
  };
  for (let i = 0; i < 64; i++) {
    const pubkey = `0x${i.toString(16).padStart(96, "0")}`;
    material[`validator/keys/${pubkey}/voting-keystore.json`] = "{}";
    material[`validator/secrets/${pubkey}`] = "fixture-password";
  }
  const manifest = { version: 1, fingerprint: fingerprint(), files: {} };
  for (const [file, content] of Object.entries(material)) {
    fs.mkdirSync(path.dirname(path.join(directory, file)), { recursive: true });
    fs.writeFileSync(path.join(directory, file), content);
    manifest.files[file] = crypto.createHash("sha256").update(content).digest("hex");
  }
  fs.writeFileSync(path.join(directory, "manifest.json"), JSON.stringify(manifest));
  return { directory, manifest };
}
test("recreate verifies all material without changing bytes or timestamps", (t) => {
  const { directory } = fixture(t);
  const file = path.join(directory, "metadata/genesis.json");
  const before = fs.statSync(file).mtimeMs;
  assert.equal(prepareNetwork(directory, { checkOnly: true }).version, 1);
  assert.equal(fs.statSync(file).mtimeMs, before);
  assert.equal(fs.readFileSync(file, "utf8"), "{}");
});

test("the shared generator fixes three-second slots even with ambient overrides", (t) => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "etherview-preview-duration-"));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  fs.writeFileSync(path.join(directory, "jq"), "#!/bin/sh\necho 0x1\n", { mode: 0o700 });
  const script = fs.readFileSync(
    new URL("../../deploy/preview/generate.sh", import.meta.url),
    "utf8",
  );
  // Exercise the real generator's environment setup without invoking the container tools.
  const setup =
    script.split("/work/entrypoint.sh cl")[0] +
    '\nprintf "%s %s" "$SLOT_DURATION_IN_SECONDS" "$SLOT_DURATION_MS"\n';
  const result = spawnSync("bash", ["-c", setup], {
    encoding: "utf8",
    env: {
      ...process.env,
      PATH: `${directory}:${process.env.PATH}`,
      SLOT_DURATION_IN_SECONDS: "6",
      SLOT_DURATION_MS: "6000",
    },
  });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout, "3 3000");
});
test("missing, partial, altered and extra material fail closed", (t) => {
  const { directory } = fixture(t);
  assert.throws(
    () => prepareNetwork(path.join(directory, "missing"), { checkOnly: true }),
    /network missing/,
  );
  fs.writeFileSync(path.join(directory, "metadata/genesis.ssz"), "tampered");
  assert.throws(() => validateNetwork(directory), /integrity mismatch/);
  fs.unlinkSync(path.join(directory, "metadata/genesis.ssz"));
  assert.throws(() => validateNetwork(directory), /inventory changed/);
});
test("configuration drift and symlinks cannot be reused", (t) => {
  const { directory, manifest } = fixture(t);
  manifest.fingerprint = "wrong";
  fs.writeFileSync(path.join(directory, "manifest.json"), JSON.stringify(manifest));
  assert.throws(() => validateNetwork(directory), /configuration changed/);
  manifest.fingerprint = fingerprint();
  fs.writeFileSync(path.join(directory, "manifest.json"), JSON.stringify(manifest));
  fs.symlinkSync("jwt.hex", path.join(directory, "extra"));
  assert.throws(() => validateNetwork(directory), /symlinks/);
});
test("a self-consistent inventory still requires every validator key and password", (t) => {
  const { directory, manifest } = fixture(t);
  const key = Object.keys(manifest.files).find((file) => file.startsWith("validator/keys/"));
  delete manifest.files[key];
  fs.unlinkSync(path.join(directory, key));
  fs.writeFileSync(path.join(directory, "manifest.json"), JSON.stringify(manifest));
  assert.throws(() => validateNetwork(directory), /64 validator/);
});
test("individual genesis overrides are rejected even when both are supplied", () => {
  assert.throws(
    () =>
      rejectLegacyOverrides({ ETHERVIEW_GENESIS_FILE: "el.json", GETH_GENESIS_FILE: "el.json" }),
    /complete network bundle/,
  );
  assert.doesNotThrow(() => rejectLegacyOverrides({ PREVIEW_NETWORK_DIR: "network" }));
});

test("Compose-resolved environment controls custom bundles and rejects legacy overrides", (t) => {
  const { directory } = fixture(t);
  const compose = path.join(directory, "compose");
  fs.writeFileSync(
    compose,
    `#!/bin/sh\nprintf '%s\\n' 'PREVIEW_NETWORK_DIR=/custom/network' 'COMPOSE_PROJECT_NAME=custom-project' 'GETH_GENESIS_FILE=/old/genesis.json'\n`,
    { mode: 0o700 },
  );
  const env = composeEnvironment(compose);
  assert.equal(env.PREVIEW_NETWORK_DIR, "/custom/network");
  assert.equal(env.COMPOSE_PROJECT_NAME, "custom-project");
  assert.throws(() => prepareNetwork(directory, { env }), /complete network bundle/);
});

test("old volumes reject initialization and generator failure leaves no published bundle", (t) => {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), "etherview-preview-failure-"));
  t.after(() => fs.rmSync(base, { recursive: true, force: true }));
  const docker = path.join(base, "docker");
  const directory = path.join(base, "network");
  const oldDocker = process.env.DOCKER;
  t.after(() => {
    if (oldDocker === undefined) delete process.env.DOCKER;
    else process.env.DOCKER = oldDocker;
  });
  process.env.DOCKER = docker;
  fs.writeFileSync(docker, '#!/bin/sh\nif [ "$1" = volume ]; then echo old-chain; fi\n', {
    mode: 0o700,
  });
  assert.throws(() => prepareNetwork(directory), /Existing Preview volumes/);
  assert.equal(fs.existsSync(directory), false);
  fs.writeFileSync(docker, '#!/bin/sh\nif [ "$1" = run ]; then exit 42; fi\n');
  assert.throws(() => prepareNetwork(directory), /failed \(42\)/);
  assert.equal(fs.existsSync(directory), false);
  assert.deepEqual(fs.readdirSync(base), ["docker"]);
});
