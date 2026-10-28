// Diagnostic only: one invocation, one input, one fresh restricted process.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync, writeFileSync, constants } from "node:fs";
import { createRequire } from "node:module";
import { join } from "node:path";
const [shared, bundle, version, mode, expectedPython, otherRuntime] = process.argv.slice(2);
const require = createRequire(import.meta.url);
let stage = "loader";
const emit = (value) => process.stdout.write(JSON.stringify(value) + "\n");
try {
  const manifest = JSON.parse(readFileSync(join(shared, "diagnostic-manifest.json")));
  for (const file of manifest.files) {
    assert.equal(
      createHash("sha256")
        .update(readFileSync(join(shared, file.path)))
        .digest("hex"),
      file.sha256,
    );
  }
  const { loadPyodide } = require(join(shared, "pyodide.js"));
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
  const python = p.runPython('import sys; ".".join(map(str, sys.version_info[:3]))');
  assert.equal(python, expectedPython);
  if (mode === "isolation") {
    const denied = (fn) => {
      try {
        fn();
        return false;
      } catch (e) {
        return e.code === "ERR_ACCESS_DENIED";
      }
    };
    const checks = {};
    checks.otherRuntime = denied(() => readFileSync(join(otherRuntime, "pyodide.js")));
    checks.read = denied(() => readFileSync("/etc/passwd"));
    checks.write = denied(() => writeFileSync(join(shared, "forbidden"), "x"));
    checks.child = denied(() =>
      require("node:child_process").spawnSync(process.execPath, ["--version"]),
    );
    checks.permissions = [
      "fs.write",
      "net",
      "child",
      "worker",
      "addons",
      "ffi",
      "wasi",
      "inspector",
    ].every((x) => !process.permission.has(x));
    try {
      await fetch("http://127.0.0.1:65534");
      checks.network = false;
    } catch (e) {
      checks.network = e?.cause?.code === "ERR_ACCESS_DENIED";
    }
    checks.memory = p.runPython(
      "try:\n    bytearray(385 << 20)\n    bounded=False\nexcept MemoryError:\n    bounded=True\nbounded",
    );
    emit({ ok: Object.values(checks).every(Boolean), stage: "isolation", python, checks });
  } else if (mode === "timeout" || mode === "kill") {
    if (mode === "kill") process.on("SIGTERM", () => {});
    emit({ ready: true, python });
    p.runPython("while True: pass");
  } else {
    stage = "dependency";
    const lock = JSON.parse(readFileSync(join(shared, "pyodide-lock.json")));
    await p.loadPackage(join(shared, lock.packages.pycryptodome.file_name));
    const bytes = readFileSync(join(bundle, "packages.zip"));
    const pkg = JSON.parse(readFileSync(join(bundle, "package-manifest.json")));
    assert.equal(createHash("sha256").update(bytes).digest("hex"), pkg.files[0].sha256);
    p.unpackArchive(new Uint8Array(bytes), "zip", { extractDir: "/packages" });
    stage = "import";
    p.runPython(
      "import sys; sys.path.insert(0,'/packages'); import json, vyper; from adapter import compile_input",
    );
    assert.equal(p.runPython("vyper.__version__"), version);
    stage = "input";
    let size = 0;
    const chunks = [];
    for await (const chunk of process.stdin) {
      size += chunk.length;
      if (size > 16 << 20) throw new Error("input limit");
      chunks.push(chunk);
    }
    p.globals.set("raw", Buffer.concat(chunks).toString("utf8"));
    stage = "compile";
    const output = p.runPython("json.dumps(compile_input(json.loads(raw)),default=str)");
    if (Buffer.byteLength(output) > 16 << 20) throw new Error("output limit");
    emit({ ok: true, python, version, output: JSON.parse(output) });
  }
} catch (e) {
  emit({ ok: false, stage, error: String(e.stack || e) });
  process.exitCode = 1;
}
