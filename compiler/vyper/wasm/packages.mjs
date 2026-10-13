import {runtimeFor} from "./runtime-policy.mjs";
// Reproducible pure-Python bundles from authenticated upstream archives.
import {
  existsSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
  readdirSync,
  lstatSync,
} from "node:fs";
import { join, basename, extname } from "node:path";
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
  safeMember,
  versions,
} from "./tooling.mjs";
import { zipEntries, makeZip, sourceEntries, makeTarGzip } from "./archives.mjs";
export const CACHE = join(ROOT, ".local/vyper-wasm/downloads");
// Keep setuptools for historical pkg_resources imports and its vendors.
export const OMIT = new Set([
  "pyinstaller",
  "pyinstaller-hooks-contrib",
  "altgraph",
  "macholib",
  "pefile",
  "pywin32-ctypes",
  "pytest-runner",
  "wheel",
  "pycryptodome",
]);
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
async function wheel(name, locked) {
  mkdirSync(CACHE, { recursive: true });
  const path = join(CACHE, name + "-" + locked.version + ".json");
  if (!existsSync(path))
    writeFileSync(
      path,
      await readURL(`https://pypi.org/pypi/${name}/${locked.version}/json`, 8 << 20),
    );
  const choices = readJSON(path).urls.filter(
    (x) => x.filename.endsWith(".whl") && locked.hashes.includes(x.digests.sha256),
  );
  choices.sort(
    (a, b) =>
      Number(!a.filename.endsWith("-none-any.whl")) -
        Number(!b.filename.endsWith("-none-any.whl")) ||
      (a.filename < b.filename ? -1 : a.filename > b.filename ? 1 : 0),
  );
  if (!choices.length) throw new Error("no authenticated wheel: " + name);
  const chosen = choices[0];
  return {
    raw: await download(chosen.url, chosen.digests.sha256),
    identity: {
      name,
      version: locked.version,
      sha256: chosen.digests.sha256,
      filename: chosen.filename,
    },
  };
}
export function unpackWheel(data, files) {
  for (const entry of zipEntries(data)) {
    if (entry.isDirectory) continue;
    const name = entry.entryName;
    const parts = safeMember(name);
    if (
      [".so", ".pyd", ".dll", ".dylib", ".pyc"].includes(extname(name)) ||
      parts.includes("__pycache__") ||
      parts[0].endsWith(".data")
    )
      continue;
    const value = entry.getData();
    if (files.has(name) && !files.get(name).equals(value))
      throw new Error("conflicting package member");
    files.set(name, value);
  }
}
export async function sourceVyper(item, files) {
  const raw = await download(item.source_url, item.compiler_sha256);
  if (item.source_url.endsWith(".whl")) {
    unpackWheel(raw, files);
    return;
  }
  for (const [name, value] of await sourceEntries(raw)) {
    const parts = safeMember(name).slice(1);
    const relative = parts.join("/");
    if (parts[0] === "vyper" || /^(license|copying)/i.test(basename(relative)))
      files.set(relative, value);
  }
  files.set(
    "vyper/vyper_git_version.txt",
    Buffer.from(basename(item.source_url).replace(/\.tar\.gz$/, "")),
  );
  files.set(
    "vyper-0.2.0.dist-info/METADATA",
    Buffer.from("Metadata-Version: 2.1\nName: vyper\nVersion: 0.2.0\n"),
  );
}
export async function bundle(item, destination) {
  const files = new Map();
  const identities = [];
  const locks = lockedPackages(join(HERE, "../versions", item.version + ".lock"));
  for (const [name, locked] of [...locks].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))) {
    if (OMIT.has(name) || name === "vyper") continue;
    const { raw, identity } = await wheel(name, locked);
    unpackWheel(raw, files);
    identities.push(identity);
  }
  await sourceVyper(item, files);
  files.set("adapter.py", readFileSync(join(HERE, "../adapter.py")));
  const raw = makeZip(files);
  const manifest = {
    schema: "etherview-vyper-wasm-package-v1",
    version: item.version,
    compiler_sha256: item.compiler_sha256,
    pyodide: runtimeFor(item.version).pyodide,
    python: runtimeFor(item.version).python,
    dependencies: identities,
    files: [{ path: "packages.zip", sha256: digest(raw) }],
  };
  mkdirSync(destination, { recursive: true });
  writeFileSync(join(destination, "packages.zip"), raw);
  writeFileSync(join(destination, "package-manifest.json"), canonical(manifest) + "\n");
  return manifest;
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
