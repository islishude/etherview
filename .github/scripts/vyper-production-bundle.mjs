// Transfer the exact production image that passed the native fixture matrix.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { copyFileSync, constants, mkdirSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";

const [mode, directory, architecture, ...extra] = process.argv.slice(2);
assert(["save", "load"].includes(mode) && directory && extra.length === 0,
  "usage: vyper-production-bundle.mjs save|load DIRECTORY amd64|arm64");
assert(["amd64", "arm64"].includes(architecture), "unsupported architecture");
const bundle = resolve(directory);
const image = process.env.IMAGE ?? "etherview:local";
const docker = (...args) => execFileSync(process.env.DOCKER ?? "docker", args, { encoding: "utf8" });
const filename = `matrix-${architecture}.json`;
const evidenceDirectory = resolve(".local/vyper-releases");
const source = join(mode === "save" ? evidenceDirectory : bundle, filename);
const evidence = JSON.parse(readFileSync(source, "utf8"));
assert.equal(evidence.native_linux, true, "native Linux fixture evidence required");
assert.match(evidence.image_id, /^sha256:[a-f0-9]{64}$/, "invalid fixture image ID");

if (mode === "load") docker("image", "load", "--input", join(bundle, "production-image.tar"));
const [identity] = JSON.parse(docker("image", "inspect", image));
assert.equal(identity.Id, evidence.image_id, "production image differs from fixture evidence");
assert.equal(identity.Architecture, architecture, "production image architecture mismatch");
assert.equal(identity.Os, "linux", "production image must target Linux");

if (mode === "save") {
  mkdirSync(bundle, { recursive: true });
  docker("image", "save", "--output", join(bundle, "production-image.tar"), image);
  copyFileSync(source, join(bundle, filename), constants.COPYFILE_EXCL);
} else {
  mkdirSync(evidenceDirectory, { recursive: true });
  copyFileSync(source, join(evidenceDirectory, filename), constants.COPYFILE_EXCL);
}
console.log(`${mode}: ${architecture} ${evidence.image_id}`);
