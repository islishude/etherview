// Read-only, offline verification of reviewed upstream notices.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { resolve, join } from "node:path";
import { fileURLToPath } from "node:url";
import { sha256 } from "./release.mjs";
const root = resolve(
  process.argv[2] ?? fileURLToPath(new URL("../../../licenses/python-wasm", import.meta.url)),
);
const records = JSON.parse(readFileSync(new URL("./licenses.lock.json", import.meta.url)));
for (const record of records) {
  const bytes = readFileSync(join(root, record.path));
  assert.ok(
    bytes.length <= 1024 * 1024 && sha256(bytes) === record.sha256,
    `checked-in upstream notice identity mismatch: ${record.path}`,
  );
}
assert.deepEqual(JSON.parse(readFileSync(join(root, "sources.json"))), records);
console.log("WASM core upstream notices: PASS");
