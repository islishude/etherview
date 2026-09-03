import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { lstatSync, readFileSync } from "node:fs";
import { join, posix } from "node:path";

const [runtimeRoot, rootfsPath, licensesRoot] = process.argv.slice(2);
if (!runtimeRoot || !rootfsPath || !licensesRoot) {
  throw new Error(
    "usage: node solcjs-runtime-image-check.mjs <runtime-root> <rootfs-list> <licenses-root>",
  );
}

const manifestRaw = readFileSync(join(runtimeRoot, "runtime-manifest.json"), "utf8");
const manifest = JSON.parse(manifestRaw);
assert.equal(manifestRaw, `${JSON.stringify(manifest)}\n`, "runtime manifest is not canonical");
const rootfs = new Set(
  readFileSync(rootfsPath, "utf8")
    .split("\n")
    .map((path) => path.replace(/^\.\//, "").replace(/\/$/, ""))
    .filter(Boolean),
);
assert.equal(manifest.schema, "etherview-solcjs-sea-runtime-v1");
assert.equal(manifest.node_version, "v26.8.1");
assert.equal(manifest.wrapper_package, "solc@0.8.36");
assert.equal(manifest.bundle_builder, "esbuild@0.28.2");
assert.deepEqual(manifest.sea, {
  main_format: "commonjs",
  use_snapshot: false,
  use_code_cache: false,
  exec_argv: [
    "--permission",
    "--disable-sigusr1",
    "--no-addons",
    "--no-global-search-paths",
    "--max-old-space-size=384",
  ],
  exec_argv_extension: "cli",
});
assert.ok(manifest.elf_interpreter.startsWith("/"), "ELF interpreter must be absolute");
assert.equal(
  posix.normalize(manifest.elf_interpreter),
  manifest.elf_interpreter,
  "ELF interpreter path must be clean",
);

const runtimePrefix = "opt/etherview/solcjs/";
const manifestFiles = new Set(
  manifest.files.map((file) => `${runtimePrefix}${file.path}`),
);
assert.deepEqual(
  manifest.files.filter((file) => file.kind === "executor").map((file) => file.path),
  ["etherview-solcjs"],
  "runtime must contain exactly one executor",
);
for (const path of manifestFiles) {
  assert.ok(rootfs.has(path), `rootfs is missing manifest file /${path}`);
}
for (const file of manifest.files) {
  assert.ok(
    file.path !== "runtime-manifest.json" && !file.path.startsWith("../") &&
      !file.path.startsWith("/") && posix.normalize(file.path) === file.path,
    `unsafe manifest file path ${file.path}`,
  );
  const localPath = join(runtimeRoot, ...file.path.split("/"));
  const stat = lstatSync(localPath);
  assert.equal(stat.isSymbolicLink(), false, `manifest file is a symlink: ${file.path}`);
  assert.equal(stat.isFile(), true, `manifest entry is not a file: ${file.path}`);
  const digest = createHash("sha256").update(readFileSync(localPath)).digest("hex");
  assert.equal(digest, file.sha256, `manifest digest mismatch: ${file.path}`);
  const mode = stat.mode & 0o777;
  if (file.kind === "executor") {
    assert.equal(mode, 0o555, "SEA executable mode");
  } else {
    assert.equal(mode, 0o444, `private library mode: ${file.path}`);
  }
}
const runtimeFiles = [...rootfs].filter(
  (path) =>
    path.startsWith(runtimePrefix) &&
    path !== "opt/etherview/solcjs/runtime-manifest.json" &&
    path !== "opt/etherview/solcjs/lib",
);
assert.deepEqual(runtimeFiles.sort(), [...manifestFiles].sort());

const privateDependencies = new Map(
  manifest.dependencies
    .filter((dependency) => dependency.provider === "runtime")
    .map((dependency) => [dependency.soname, dependency.path]),
);
const privateFiles = new Map(
  manifest.files
    .filter((file) => file.kind === "library")
    .map((file) => [file.soname, file.path]),
);
assert.deepEqual(privateFiles, privateDependencies);
let previousSONAME = "";
const seenDependencies = new Set();
for (const dependency of manifest.dependencies) {
  assert.ok(dependency.soname > previousSONAME, "dependencies are not strictly sorted");
  assert.equal(seenDependencies.has(dependency.soname), false, "duplicate dependency");
  seenDependencies.add(dependency.soname);
  previousSONAME = dependency.soname;
  assert.match(dependency.license_sha256, /^[0-9a-f]{64}$/);
  for (const field of ["package", "package_version", "package_architecture"]) {
    assert.ok(dependency[field], `dependency ${dependency.soname} lacks ${field}`);
  }
  if (dependency.provider === "runtime") {
    assert.equal(dependency.path, `lib/${dependency.soname}`);
    const licenseName = `${dependency.package.replaceAll(":", "_")}.copyright`;
    assert.ok(
      rootfs.has(`licenses/solcjs-runtime/${licenseName}`),
      `rootfs lacks runtime license ${licenseName}`,
    );
    const licenseDigest = createHash("sha256")
      .update(readFileSync(join(licensesRoot, licenseName)))
      .digest("hex");
    assert.equal(
      licenseDigest,
      dependency.license_sha256,
      `runtime license digest mismatch: ${dependency.package}`,
    );
  } else {
    assert.equal(dependency.provider, "base", `invalid provider for ${dependency.soname}`);
    assert.ok(dependency.path.startsWith("/"), `base path is not absolute: ${dependency.path}`);
    assert.equal(posix.normalize(dependency.path), dependency.path);
    assert.ok(rootfs.has(dependency.path.slice(1)), `rootfs lacks ${dependency.path}`);
  }
}
assert.equal(rootfs.has("usr/local/bin/node"), false, "general Node executable remains");
