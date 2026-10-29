// Optional official native reference environments. Never builds an executable or release.
import { execFileSync } from "node:child_process";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { parseArgs } from "node:util";

import { lockedPackages } from "./wasm/packages.mjs";
import { ROOT, versions } from "./wasm/tooling.mjs";
export const RETIRED = new Set([
  "pyinstaller",
  "pyinstaller-hooks-contrib",
  "altgraph",
  "macholib",
  "pefile",
  "pywin32-ctypes",
]);
export function referencePackages(version) {
  return new Map(
    [...lockedPackages(join(ROOT, "compiler/vyper/versions", version + ".lock"))].filter(
      ([name]) => !RETIRED.has(name),
    ),
  );
}
export async function prepareReference(item) {
  // This read-only command lets uv enforce the project version range before writes.
  execFileSync("uv", ["--project", join(ROOT, "compiler/vyper"), "cache", "dir"]);
  const workspace = join(ROOT, ".local/vyper-builds", item.version);
  mkdirSync(workspace, { recursive: true });
  const venv = join(workspace, "build-venv"),
    python = join(venv, "bin/python"),
    lock = join(workspace, "reference.lock");
  const pins = [...referencePackages(item.version)].map(
    ([name, p]) => `${name}==${p.version} ${p.hashes.map((h) => "--hash=sha256:" + h).join(" ")}`,
  );
  writeFileSync(lock, pins.join("\n") + "\n");
  const run = (args) => execFileSync("uv", args, { stdio: "inherit" });
  run(["venv", "--allow-existing", "--python", item.python, venv]);
  run(["pip", "sync", "--python", python, "--require-hashes", "--only-binary=:all:", lock]);
  run(["pip", "check", "--python", python]);
  console.log("official reference ready", item.version, python);
}
if (import.meta.main) {
  const { values } = parseArgs({ options: { version: { type: "string", multiple: true } } });
  const items = versions().filter((v) => !values.version || values.version.includes(v.version));
  if (values.version && items.length !== new Set(values.version).size)
    throw new Error("unknown locked compiler version");
  for (const item of items) await prepareReference(item);
}
