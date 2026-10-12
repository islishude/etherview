// Unsigned transports only. Native acceptance and signing are separate gates.
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  rmSync,
  renameSync,
  writeFileSync,
  lstatSync,
} from "node:fs";
import { dirname, join, resolve } from "node:path";
import { parseArgs } from "node:util";
import { ROOT, canonical, digest, regularFile, versions } from "./tooling.mjs";
import { sharedSHA256 } from "./release.mjs";
import { packRuntime } from "./packages.mjs";
export async function descriptor(item, packagePath, shared, output, sharedDigest = sharedSHA256) {
  if (digest(regularFile(shared)) !== sharedDigest)
    throw new Error("shared runtime identity mismatch");
  if (!lstatSync(packagePath).isDirectory()) throw new Error("invalid package directory");
  const raw = regularFile(join(packagePath, "package-manifest.json"));
  const manifest = JSON.parse(raw);
  const expected = {
    schema: "etherview-vyper-wasm-package-v1",
    version: item.version,
    compiler_sha256: item.compiler_sha256,
    pyodide: "0.29.5",
    python: "3.13.2",
  };
  if (Object.entries(expected).some(([k, v]) => manifest[k] !== v))
    throw new Error("compiler package identity mismatch");
  const payload = regularFile(join(packagePath, "packages.zip"));
  if (canonical(manifest.files) !== canonical([{ path: "packages.zip", sha256: digest(payload) }]))
    throw new Error("compiler package payload mismatch");
  const name = "vyper-" + item.version + "-emscripten-wasm32";
  const archive = name + ".tar.gz";
  await packRuntime(packagePath, join(output, archive));
  const data = regularFile(join(output, archive));
  const record = {
    version: item.version,
    compiler_sha256: item.compiler_sha256,
    platform: "emscripten-wasm32",
    protocol: expected.schema,
    manifest_sha256: digest(raw),
    shared_sha256: sharedDigest,
    archive,
    sha256: digest(data),
    max_bytes: data.length,
  };
  writeFileSync(join(output, name + ".json"), canonical(record) + "\n", { flag: "wx" });
  return record;
}
export async function buildRelease(
  packages,
  shared,
  output,
  items = versions(),
  sharedDigest = sharedSHA256,
) {
  if (existsSync(output)) throw new Error("release output must not already exist");
  mkdirSync(dirname(output), { recursive: true });
  const temporary = mkdtempSync(join(dirname(output), ".vyper-release-"));
  try {
    const staging = join(temporary, "release");
    mkdirSync(staging);
    for (const item of items)
      await descriptor(item, join(packages, item.version), shared, staging, sharedDigest);
    if (existsSync(output)) throw new Error("release output must not already exist");
    renameSync(staging, output);
  } finally {
    rmSync(temporary, { recursive: true, force: true });
  }
}
if (import.meta.main) {
  const { values } = parseArgs({
    options: {
      packages: { type: "string", default: join(ROOT, ".local/vyper-wasm/packages") },
      shared: {
        type: "string",
        default: join(ROOT, ".local/vyper-wasm/shared/shared-manifest.json"),
      },
      output: { type: "string" },
    },
  });
  if (!values.output) throw new Error("--output is required");
  await buildRelease(values.packages, values.shared, resolve(values.output));
}
