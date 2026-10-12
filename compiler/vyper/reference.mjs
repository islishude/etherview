// Optional official native reference environments. Never builds an executable or release.
import { execFileSync } from "node:child_process";
import { mkdirSync, writeFileSync, mkdtempSync, rmSync } from "node:fs";
import { join, dirname } from "node:path";
import { tmpdir } from "node:os";
import { parseArgs } from "node:util";
import { ROOT, versions, safeMember } from "./wasm/tooling.mjs";
import { lockedPackages, download } from "./wasm/packages.mjs";
import { sourceEntries } from "./wasm/archives.mjs";
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
  if (!execFileSync("uv", ["--version"], { encoding: "utf8" }).startsWith("uv 0.12.24"))
    throw new Error("uv 0.12.24 required");
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
  if (item.version === "0.2.0") {
    const temporary = mkdtempSync(join(tmpdir(), "vyper-reference-"));
    try {
      const entries = await sourceEntries(await download(item.source_url, item.compiler_sha256));
      const roots = new Set();
      for (const [name, bytes] of entries) {
        roots.add(safeMember(name)[0]);
        const path = join(temporary, name);
        mkdirSync(dirname(path), { recursive: true });
        writeFileSync(path, bytes);
      }
      if (roots.size !== 1) throw new Error("invalid upstream source root");
      const tree = join(temporary, [...roots][0]);
      writeFileSync(
        join(tree, "vyper/vyper_git_version.txt"),
        item.source_url
          .split("/")
          .at(-1)
          .replace(/\.tar\.gz$/, ""),
      );
      run(["pip", "install", "--python", python, "--no-deps", "--no-build-isolation", tree]);
    } finally {
      rmSync(temporary, { recursive: true, force: true });
    }
  }
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
