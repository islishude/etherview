import { spawnSync } from "node:child_process";
import { dirname } from "node:path";
// Reproducible pure-Python bundles from authenticated upstream archives.
import {
  existsSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
  readdirSync,
  lstatSync,
} from "node:fs";
import { join } from "node:path";
import { parseArgs } from "node:util";
import {
  ROOT,
  HERE,
  MAX_ARCHIVE,
  digest,
  canonical,
  readURL,
  readJSON,
  regularFile,
  versions,
} from "./tooling.mjs";
import { makeTarGzip } from "./archives.mjs";
export const CACHE = join(ROOT, ".local/vyper-wasm/downloads");
export async function download(url, expected, cache = CACHE) {
  if (!/^[0-9a-f]{64}$/.test(expected) || !url.startsWith("https://"))
    throw new Error("invalid authenticated download");
  mkdirSync(cache, { recursive: true });
  const path = join(cache, expected);
  const cached = existsSync(path);
  const data = cached ? regularFile(path) : await readURL(url, MAX_ARCHIVE);
  if (digest(data) !== expected)
    throw new Error(cached ? "cached artifact identity mismatch" : "artifact identity mismatch");
  if (!cached) writeFileSync(path, data);
  return data;
}
export function lockedPackages(path) {
  const selected = new Map();
  let current;
  for (const line of readFileSync(path, "utf8").split("\n")) {
    const match = /^([\w-]+)==([^\s;\\]+)/.exec(line);
    if (match) {
      current = { version: match[2], hashes: [] };
      selected.set(match[1], current);
    } else if (current)
      current.hashes.push(...[...line.matchAll(/--hash=sha256:([0-9a-f]{64})/g)].map((m) => m[1]));
  }
  if (!selected.size || [...selected.values()].some((p) => !p.hashes.length))
    throw new Error("incomplete package lock");
  return selected;
}
export async function bundle(item, destination) {
  const lock = readJSON(join(HERE, "../application-lock.json"));
  const recipe = lock.recipes.find((r) => r.version === item.version);
  if (!recipe || recipe.compiler_sha256 !== item.compiler_sha256)
    throw new Error("unknown application recipe");
  for (const archive of recipe.archives) {
    if (archive.url) await download(archive.url, archive.sha256);
  }
  const result = spawnSync(
    "go",
    [
      "run",
      "./compiler/vyper/cmd/bundle",
      "--cache",
      CACHE,
      "--output",
      dirname(destination),
      "--version",
      item.version,
    ],
    { cwd: ROOT, stdio: "inherit" },
  );
  if (result.status !== 0) throw new Error("application package assembly failed");
  return readJSON(join(destination, "package-manifest.json"));
}
export async function packRuntime(runtime, archivePath) {
  const names = ["package-manifest.json", "packages.zip"];
  if (
    lstatSync(runtime).isSymbolicLink() ||
    canonical(readdirSync(runtime).sort()) !== canonical(names)
  )
    throw new Error("unexpected compiler package files");
  const raw = await makeTarGzip(names.map((name) => [name, regularFile(join(runtime, name))]));
  if (raw.length > MAX_ARCHIVE) throw new Error("release archive exceeds limit");
  writeFileSync(archivePath, raw, { flag: "wx" });
}
if (import.meta.main) {
  const { values } = parseArgs({
    options: {
      version: { type: "string", multiple: true },
      output: { type: "string", default: join(ROOT, ".local/vyper-wasm/packages") },
    },
  });
  const items = versions().filter((v) => !values.version || values.version.includes(v.version));
  if (values.version && items.length !== new Set(values.version).size)
    throw new Error("unknown locked compiler version");
  for (const item of items) {
    await bundle(item, join(values.output, item.version));
    console.log("packaged", item.version);
  }
}
