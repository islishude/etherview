// Differential gate. References are read-only and compiler packages are built
// from authenticated official archives, never copied from installed venvs.
import assert from "node:assert/strict";
import { runtimeFor } from "./runtime-policy.mjs";
import { readFileSync, readdirSync, constants } from "node:fs";
import { createRequire } from "node:module";
import { resolve, join } from "node:path";
import { createHash } from "node:crypto";
const [sharedArg, packagesArg, fixturesArg, version] = process.argv.slice(2);
const shared = resolve(sharedArg),
  packages = resolve(packagesArg),
  fixtures = resolve(fixturesArg);
const hash = (b) => createHash("sha256").update(b).digest("hex");
function manifest(root, name) {
  const m = JSON.parse(readFileSync(join(root, name)));
  for (const f of m.files) assert.equal(hash(readFileSync(join(root, f.path))), f.sha256);
  return m;
}
const rt = manifest(shared, "shared-manifest.json");
const pkg = manifest(packages, "package-manifest.json");
const spec = runtimeFor(version);
assert.equal(hash(readFileSync(join(shared, "shared-manifest.json"))), spec.shared_sha256);
assert.equal(rt.pyodide, spec.pyodide);
assert.equal(rt.python, spec.python);
assert.equal(pkg.version, version);
assert.equal(pkg.pyodide, spec.pyodide);
assert.equal(pkg.python, spec.python);
const require = createRequire(import.meta.url);
const { loadPyodide } = require(join(shared, "pyodide.js"));
// Emscripten's NODEFS initialization asks for fs constants through an obsolete
// private API. Supply only the public constants; all other bindings stay denied.
const originalBinding = process.binding;
process.binding = (name) => (name === "constants" ? { fs: constants } : originalBinding(name));
let p;
try {
  p = await loadPyodide({
    indexURL: shared + "/",
    packageCacheDir: shared,
    stdout: () => {},
    stderr: () => {},
  });
} finally {
  process.binding = originalBinding;
}
const lock = JSON.parse(readFileSync(join(shared, "pyodide-lock.json")));
await p.loadPackage(join(shared, lock.packages.pycryptodome.file_name));
p.unpackArchive(new Uint8Array(readFileSync(join(packages, "packages.zip"))), "zip", {
  extractDir: "/packages",
});
p.runPython(
  "import sys; sys.path.insert(0, '/packages'); import json, vyper; from adapter import compile_input",
);
assert.equal(p.runPython("vyper.__version__"), version);
const chunks = [];
for await (const chunk of process.stdin) chunks.push(chunk);
const request = JSON.parse(Buffer.concat(chunks));
p.globals.set("raw", request.raw);
const actual = JSON.parse(p.runPython("json.dumps(compile_input(json.loads(raw)),default=str)"));
if (request.invalid)
  assert(
    actual.errors?.some(
      (e) => e.severity === "error" && request.expected.errors?.some((r) => r.type === e.type),
    ),
  );
else assert.deepEqual(actual.contracts, request.expected.contracts);
console.log(
  JSON.stringify({
    version,
    python: p.runPython('".".join(map(str,sys.version_info[:3]))'),
    pyodide: rt.pyodide,
    cases: 1,
    package_sha256: hash(readFileSync(join(packages, "package-manifest.json"))),
    shared_sha256: hash(readFileSync(join(shared, "shared-manifest.json"))),
  }),
);
