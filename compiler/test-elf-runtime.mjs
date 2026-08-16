import { appendFileSync, copyFileSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, dirname, join, resolve } from "node:path";

import { indexELFProviders, validateAssembledRuntime } from "./elf-runtime.mjs";

const [targetRootArgument] = process.argv.slice(2);
if (!targetRootArgument) {
  throw new Error("usage: node test-elf-runtime.mjs <target-root>");
}
const targetRoot = resolve(targetRootArgument);
const executorPath = "/opt/etherview/solcjs/etherview-solcjs";
const manifest = JSON.parse(
  readFileSync(join(targetRoot, "opt/etherview/solcjs/runtime-manifest.json"), "utf8"),
);
const validation = {
  targetRoot,
  executorPath,
  interpreter: manifest.elf_interpreter,
  dependencies: manifest.dependencies,
};
validateAssembledRuntime(validation);

const runtimeDependency = manifest.dependencies.find(
  (dependency) => dependency.provider === "runtime",
);
if (!runtimeDependency) {
  throw new Error("ELF regression requires one automatically copied dependency");
}
const runtimePath = join(
  targetRoot,
  "opt/etherview/solcjs",
  runtimeDependency.path,
);
const missingPath = `${runtimePath}.missing-test`;
renameSync(runtimePath, missingPath);
let missingRejected = false;
try {
  validateAssembledRuntime(validation);
} catch {
  missingRejected = true;
} finally {
  renameSync(missingPath, runtimePath);
}
if (!missingRejected) {
  throw new Error("assembled closure accepted a removed dependency");
}
validateAssembledRuntime(validation);

const fixtureRoot = mkdtempSync(join(tmpdir(), "etherview-elf-conflict-"));
try {
  const one = join(fixtureRoot, "one", basename(runtimePath));
  const two = join(fixtureRoot, "two", basename(runtimePath));
  mkdirSync(dirname(one), { recursive: true });
  mkdirSync(dirname(two), { recursive: true });
  copyFileSync(runtimePath, one);
  copyFileSync(runtimePath, two);
  appendFileSync(two, "conflict");
  let conflictRejected = false;
  try {
    indexELFProviders(fixtureRoot);
  } catch (error) {
    conflictRejected = String(error).includes("conflicting providers");
  }
  if (!conflictRejected) {
    throw new Error("ELF provider index accepted conflicting SONAME content");
  }
} finally {
  rmSync(fixtureRoot, { recursive: true, force: true });
}
