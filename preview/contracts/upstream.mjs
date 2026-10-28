// The canonical CREATE2 inputs retain their immutable upstream provenance.
// They do not depend on the Bundler image or its simulation implementation.
import { createHash } from "node:crypto";
import fs from "node:fs";
const file = new URL("upstream/account-abstraction-v09.json", import.meta.url);
const pinned = JSON.parse(fs.readFileSync(file));
const sourceIndex = process.argv.indexOf("--source");
let source;
if (sourceIndex !== -1) source = fs.readFileSync(process.argv[sourceIndex + 1], "utf8");
else {
  const response = await fetch(pinned.source, { signal: AbortSignal.timeout(30000) });
  if (!response.ok) throw new Error("Canonical AA source unavailable");
  source = await response.text();
}
if (createHash("sha256").update(source).digest("hex") !== pinned.sourceSha256)
  throw new Error("Canonical AA source checksum mismatch");
const calls = {};
for (const key of ["ENTRY_POINT_V09_CREATECALL", "SIMPLE_ACCOUNT_FACTORY_V09_CREATECALL"]) {
  const match = source.match(new RegExp(key + ': Hex =\\s*"([^"]+)"'));
  if (!match) throw new Error("Missing canonical AA creation input");
  calls[key] = match[1];
}
const value = JSON.stringify({ ...pinned, calls }, null, 2) + "\n";
if (process.argv.includes("--check")) {
  if (fs.readFileSync(file, "utf8") !== value) throw new Error("Canonical AA inputs are stale");
} else fs.writeFileSync(file, value);
