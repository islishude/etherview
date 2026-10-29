// Inspect licenses in actual authenticated bundles, including nested vendors.
import { basename, join } from "node:path";

import { zipEntries } from "./archives.mjs";
import { vendoredMetadata } from "./metadata.mjs";
import { lockedPackages } from "./packages.mjs";
import { ROOT, HERE, canonical, digest, regularFile, versions } from "./tooling.mjs";
export function checkPackage(item, path) {
  const manifest = JSON.parse(regularFile(join(path, "package-manifest.json")));
  if (manifest.version !== item.version || manifest.compiler_sha256 !== item.compiler_sha256)
    throw new Error("compiler identity mismatch");
  const raw = regularFile(join(path, "packages.zip"));
  if (canonical(manifest.files) !== canonical([{ path: "packages.zip", sha256: digest(raw) }]))
    throw new Error("package digest mismatch");
  const locked = lockedPackages(join(HERE, "../versions", item.version + ".lock"));
  const entries = zipEntries(raw);
  for (const dependency of manifest.dependencies) {
    const pin = locked.get(dependency.name);
    if (!pin || pin.version !== dependency.version || !pin.hashes.includes(dependency.sha256))
      throw new Error("unauthenticated dependency");
  }
  const roots = new Set(
    entries
      .filter((e) => e.entryName.includes(".dist-info/"))
      .map((e) => e.entryName.split(".dist-info/")[0] + ".dist-info/"),
  );
  for (const root of roots) {
    const notices = entries.filter(
      (e) => e.entryName.startsWith(root) && /^(license|copying)/i.test(basename(e.entryName)),
    );
    if (!notices.length || notices.some((e) => !e.getData().toString("utf8").trim()))
      throw new Error("missing distribution license: " + root);
  }
  if (roots.size < manifest.dependencies.length + 1)
    throw new Error("missing distribution metadata");
  return roots.size + vendoredMetadata(entries, item.version).length;
}
if (import.meta.main) {
  const items = versions();
  let count = 0;
  for (const item of items)
    count += checkPackage(item, join(ROOT, ".local/vyper-wasm/packages", item.version));
  console.log(
    `WASM package licenses: PASS (${items.length} versions, ${count} distributions including vendored dependencies)`,
  );
}
