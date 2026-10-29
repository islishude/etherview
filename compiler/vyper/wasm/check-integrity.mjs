// Validate local package identities and metadata; no advisory-service queries.
import { readdirSync, lstatSync } from "node:fs";
import { join } from "node:path";

import { metadata, normalized } from "./metadata.mjs";
import { sharedRuntimes, runtimeFor } from "./release.mjs";
import { ROOT, canonical, digest, regularFile, safeMember, versions } from "./tooling.mjs";
export function inventory(root = ROOT, runtimes = sharedRuntimes, items = versions()) {
  const records = [];
  const base = join(root, ".local/vyper-wasm/shared");
  if (canonical(readdirSync(base).sort()) !== canonical(Object.keys(runtimes).sort()))
    throw new Error("unlisted runtime directory");
  for (const [runtime, sharedSHA256] of Object.entries(runtimes)) {
    const shared = join(base, runtime);
    if (!lstatSync(shared).isDirectory()) throw new Error("invalid shared directory");
    const raw = regularFile(join(shared, "shared-manifest.json"));
    if (digest(raw) !== sharedSHA256) throw new Error("unauthenticated shared runtime");
    const files = JSON.parse(raw).files;
    if (
      canonical(readdirSync(shared).sort()) !==
      canonical(["shared-manifest.json", ...files.map((f) => f.path)].sort())
    )
      throw new Error("unlisted shared payload");
    for (const entry of files) {
      safeMember(entry.path);
      const data = regularFile(join(shared, entry.path));
      if (digest(data) !== entry.sha256) throw new Error("shared runtime payload changed");
      if (/\.(whl|zip)$/.test(entry.path))
        records.push(...metadata(data, "shared/" + runtime + "/" + entry.path));
    }
  }
  for (const item of items) {
    const path = join(root, ".local/vyper-wasm/packages", item.version);
    if (
      !lstatSync(path).isDirectory() ||
      canonical(readdirSync(path).sort()) !== canonical(["package-manifest.json", "packages.zip"])
    )
      throw new Error("invalid compiler package directory");
    const raw = regularFile(join(path, "package-manifest.json"));
    const manifest = JSON.parse(raw);
    if (
      manifest.version !== item.version ||
      manifest.compiler_sha256 !== item.compiler_sha256 ||
      manifest.pyodide !== runtimeFor(item.version).pyodide ||
      manifest.python !== runtimeFor(item.version).python
    )
      throw new Error("compiler identity changed");
    const data = regularFile(join(path, "packages.zip"));
    if (canonical(manifest.files) !== canonical([{ path: "packages.zip", sha256: digest(data) }]))
      throw new Error("compiler payload changed");
    const found = metadata(data, item.version);
    const direct = new Set([
      ...manifest.dependencies.map((d) => normalized(d.name) + "/" + d.version),
      "vyper/" + item.version,
    ]);
    const top = new Set(
      found.filter((d) => d.metadata.split("/").length === 2).map((d) => d.name + "/" + d.version),
    );
    if (canonical([...top].sort()) !== canonical([...direct].sort()))
      throw new Error("distribution inventory does not match package manifest");
    records.push(...found);
  }
  if (!records.length) throw new Error("empty distribution inventory");
  records.sort((a, b) => (canonical(a) < canonical(b) ? -1 : canonical(a) > canonical(b) ? 1 : 0));
  return { distributions: records };
}
if (import.meta.main) {
  const result = inventory();
  console.log(
    `WASM package integrity: PASS (${result.distributions.length} distribution occurrences)`,
  );
}
