import { basename } from "node:path";

import { zipEntries } from "./archives.mjs";
export const normalized = (name) => name.toLowerCase().replace(/[-_.]+/g, "-");
export function distributionIdentity(raw) {
  // Core metadata uses RFC822 headers: unfold continuation lines, stop at body.
  const headers = raw
    .toString("utf8")
    .split(/\r?\n\r?\n/, 1)[0]
    .replace(/\r?\n[ \t]+/g, " ");
  const fields = (key) =>
    [...headers.matchAll(new RegExp("^" + key + ":[ \\t]*(.*)$", "gmi"))].map((m) => m[1].trim());
  const names = fields("Name"),
    versions = fields("Version");
  if (names.length !== 1 || versions.length !== 1 || !names[0] || !versions[0])
    throw new Error("missing or ambiguous distribution identity");
  return { name: normalized(names[0]), version: versions[0] };
}
export function vendoredMetadata(entries, origin) {
  const records = [];
  for (const entry of entries) {
    const name = entry.entryName;
    if (!/\/(vendor|vendored)\.txt$/.test(name)) continue;
    for (let line of entry.getData().toString("utf8").split("\n")) {
      line = line.split("#")[0].trim();
      if (!line) continue;
      const match = /^([\w.-]+)==([\w.+-]+)$/.exec(line);
      if (!match) throw new Error("unresolved vendored distribution");
      const [, pkg, version] = match;
      const prefix = name.slice(0, name.lastIndexOf("/") + 1) + pkg.replaceAll("-", "_") + "/";
      const members = entries.filter((e) => e.entryName.startsWith(prefix));
      const notices = members.filter((e) => /^(license|copying)/i.test(basename(e.entryName)));
      if (
        !members.some((e) => e.entryName.endsWith(".py")) ||
        !notices.length ||
        notices.some((e) => !e.getData().toString("utf8").trim())
      )
        throw new Error("missing vendored implementation or license");
      records.push({ name: normalized(pkg), version, origin, metadata: name + "#" + pkg });
    }
  }
  return records;
}
export function metadata(raw, origin) {
  const entries = zipEntries(raw);
  return [
    ...entries
      .filter((e) => e.entryName.endsWith(".dist-info/METADATA"))
      .map((e) => ({ ...distributionIdentity(e.getData()), origin, metadata: e.entryName })),
    ...vendoredMetadata(entries, origin),
  ];
}
