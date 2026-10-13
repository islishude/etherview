import test from "node:test";
import { retirePreviousFiles } from "./prepare.mjs";
import assert from "node:assert/strict";
import {
  mkdtempSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
  chmodSync,
  symlinkSync,
  existsSync,
  rmSync,
  readdirSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import AdmZip from "adm-zip";
import { gunzipSync } from "node:zlib";
import { canonical, digest, HERE, safeMember, readJSON } from "./tooling.mjs";
import { makeZip, zipEntries } from "./archives.mjs";
import { download, packRuntime } from "./packages.mjs";
import { descriptor, buildRelease } from "./build-release.mjs";
import { metadata } from "./metadata.mjs";
import { inventory } from "./check-integrity.mjs";
import { checkPackage } from "./package-licenses.mjs";
import { referencePackages, RETIRED } from "../reference.mjs";
function temporary(t) {
  const path = mkdtempSync(join(tmpdir(), "vyper-tools-"));
  t.after(() => rmSync(path, { recursive: true, force: true }));
  return path;
}
const zip = (values) =>
  makeZip(new Map(Object.entries(values).map(([k, v]) => [k, Buffer.from(v)])));
const item = { version: "0.4.3", compiler_sha256: "1".repeat(64) };
function packageFixture(root, payload = Buffer.from("authenticated upstream package")) {
  const path = join(root, item.version);
  mkdirSync(path);
  writeFileSync(join(path, "packages.zip"), payload);
  const manifest = {
    ...item,
    schema: "etherview-vyper-wasm-package-v1",
    pyodide: "314.0.7",
    python: "3.14.2",
    dependencies: [],
    files: [{ path: "packages.zip", sha256: digest(payload) }],
  };
  writeFileSync(join(path, "package-manifest.json"), canonical(manifest));
  return path;
}
test("release archive is deterministic and preserves USTAR framing and permissions", async (t) => {
  const root = temporary(t),
    path = packageFixture(root);
  const a = join(root, "a.gz"),
    b = join(root, "b.gz");
  await packRuntime(path, a);
  chmodSync(join(path, "packages.zip"), 0o755);
  await packRuntime(path, b);
  assert.deepEqual(readFileSync(a), readFileSync(b));
  const raw = gunzipSync(readFileSync(a));
  assert.equal(raw.length % 10240, 0);
  assert.equal(parseInt(raw.subarray(100, 108).toString(), 8), 0o444);
  await assert.rejects(packRuntime(path, a), /EEXIST/);
  writeFileSync(join(path, "unexpected"), "extra");
  await assert.rejects(packRuntime(path, join(root, "c.gz")), /unexpected/);
});
test("cached bytes are authenticated again", async (t) => {
  const root = temporary(t),
    expected = digest("original");
  writeFileSync(join(root, expected), "tampered");
  await assert.rejects(
    download("https://example.invalid/file", expected, root),
    /cached artifact identity/,
  );
});
test("reject unsafe archive paths, symlinks and oversized members", () => {
  for (const name of ["../file.py", "/file.py", "a/../file.py", "a\\file.py", "a//b", "a/./b"])
    assert.throws(() => safeMember(name), /unsafe/);
  const archive = new AdmZip();
  archive.addFile("link", Buffer.from("../escape"));
  archive.getEntry("link").header.attr = 0o120777 * 65536;
  assert.throws(() => zipEntries(archive.toBuffer()), /unsafe wheel/);
  const raw = zip({ "x.py": "x" });
  const central = raw.indexOf(Buffer.from("504b0102", "hex"));
  raw.writeUInt32LE(201 << 20, central + 24);
  assert.throws(() => zipEntries(raw), /unsafe wheel/);
});
test("package zip is deterministic with fixed timestamps and permissions", () => {
  const files = new Map([
    ["z.py", Buffer.from("last")],
    ["a.py", Buffer.from("first")],
  ]);
  assert.deepEqual(makeZip(files), makeZip(new Map([...files].reverse())));
  for (const e of zipEntries(makeZip(files))) {
    assert.equal(e.header.timeval, 0x00210000);
    assert.equal(e.attr >>> 16, 0o100444);
  }
});
test("authenticated transport rejects changed identities, bytes, symlinks", async (t) => {
  const root = temporary(t),
    path = packageFixture(root),
    shared = join(root, "shared.json");
  writeFileSync(shared, "locked shared manifest");
  const hash = digest(readFileSync(shared));
  const a = join(root, "a"),
    b = join(root, "b");
  mkdirSync(a);
  mkdirSync(b);
  const first = await descriptor(item, path, shared, a, hash),
    second = await descriptor(item, path, shared, b, hash);
  assert.deepEqual(first, second);
  assert.deepEqual(readFileSync(join(a, first.archive)), readFileSync(join(b, second.archive)));
  assert.equal(first.platform, "emscripten-wasm32");
  assert.ok(!("executor_digests" in first));
  await assert.rejects(
    descriptor({ ...item, version: "0.4.2" }, path, shared, b, hash),
    /compiler package identity/,
  );
  writeFileSync(join(path, "packages.zip"), "tampered");
  await assert.rejects(descriptor(item, path, shared, b, hash), /payload mismatch/);
  writeFileSync(shared, "tampered");
  await assert.rejects(descriptor(item, path, shared, b, hash), /shared runtime identity/);
  const link = join(root, "linked");
  symlinkSync(path, link);
  await assert.rejects(packRuntime(link, join(root, "linked.gz")), /unexpected/);
});
test("release set is published only after every package validates; never overwrites", async (t) => {
  const root = temporary(t);
  packageFixture(root);
  const shared = join(root, "shared");
  writeFileSync(shared, "shared");
  const output = join(root, "release"),
    hash = digest("shared");
  await assert.rejects(
    buildRelease(root, shared, output, [item, { ...item, version: "0.4.2" }], hash),
  );
  assert.equal(existsSync(output), false);
  assert.equal(
    readdirSync(root).some((n) => n.startsWith(".vyper-release-")),
    false,
  );
  await buildRelease(root, shared, output, [item], hash);
  await assert.rejects(buildRelease(root, shared, output, [item], hash), /must not already exist/);
});
test("metadata includes nested dist-info and vendor manifests, rejects ambiguities", () => {
  const records = metadata(
    zip({
      "setuptools-1.dist-info/METADATA": "Name: setuptools\nVersion: 1\n",
      "setuptools/_vendor/wheel-2.dist-info/METADATA": "Name: wheel\nVersion: 2\n",
    }),
    "test",
  );
  assert.deepEqual(records.map((d) => [d.name, d.version]).sort(), [
    ["setuptools", "1"],
    ["wheel", "2"],
  ]);
  assert.throws(
    () => metadata(zip({ "x.dist-info/METADATA": "Name: x\nName: y\nVersion: 1\n" }), "test"),
    /ambiguous/,
  );
  const files = {
    "wheel/vendored/vendor.txt": "packaging==24.0\n",
    "wheel/vendored/packaging/__init__.py": "",
  };
  assert.throws(() => metadata(zip(files), "test"), /missing vendored/);
  const [record] = metadata(
    zip({ ...files, "wheel/vendored/packaging/LICENSE": "upstream notice" }),
    "test",
  );
  assert.equal(record.name, "packaging");
  assert.equal(record.version, "24.0");
});
test("licenses cover nested distributions, authentic dependency hashes and payload", (t) => {
  const root = temporary(t),
    files = {
      "vyper-0.4.3.dist-info/LICENSE": "upstream notice",
      "vendor/dependency-1.dist-info/METADATA": "Name: dependency",
    };
  const path = packageFixture(root, zip(files));
  assert.throws(() => checkPackage(item, path), /missing distribution license/);
  const raw = zip({
    ...files,
    "vendor/dependency-1.dist-info/licenses/LICENSE": "vendored notice",
  });
  writeFileSync(join(path, "packages.zip"), raw);
  const manifest = readJSON(join(path, "package-manifest.json"));
  manifest.files[0].sha256 = digest(raw);
  writeFileSync(join(path, "package-manifest.json"), canonical(manifest));
  assert.equal(checkPackage(item, path), 2);
  manifest.dependencies = [{ name: "unlocked", version: "1", sha256: "0".repeat(64) }];
  writeFileSync(join(path, "package-manifest.json"), canonical(manifest));
  assert.throws(() => checkPackage(item, path), /unauthenticated/);
  writeFileSync(join(path, "packages.zip"), "changed");
  assert.throws(() => checkPackage(item, path), /package digest mismatch/);
});
test("reference environment excludes retired packaging", () => {
  const pins = referencePackages("0.4.3");
  for (const name of RETIRED) assert.equal(pins.has(name), false);
});

test("ZIP rejects duplicate paths and corrupt contents before use", () => {
  const raw = zip({ "a.py": "first", "b.py": "second" });
  for (let at = raw.indexOf("b.py"); at !== -1; at = raw.indexOf("b.py", at + 4))
    raw.write("a.py", at);
  assert.throws(() => zipEntries(raw), /duplicate/i);
  const corrupt = zip({ "x.py": "source" });
  corrupt[34] ^= 0xff;
  assert.throws(() => zipEntries(corrupt)[0].getData());
});
test("source archive rejects links, traversal and duplicate files", async () => {
  const { default: tar } = await import("tar-stream");
  const { gzipSync } = await import("node:zlib");
  const { sourceEntries } = await import("./archives.mjs");
  async function archive(headers) {
    const pack = tar.pack();
    const chunks = [];
    const consume = (async () => {
      for await (const c of pack) chunks.push(c);
    })();
    for (const header of headers) pack.entry({ ...header, mtime: new Date(0) }, Buffer.alloc(0));
    pack.finalize();
    await consume;
    return gzipSync(Buffer.concat(chunks));
  }
  for (const headers of [
    [{ name: "root/link", type: "symlink", linkname: "../escape" }],
    [{ name: "../escape", type: "file" }],
    [
      { name: "root/a.py", type: "file" },
      { name: "root/a.py", type: "file" },
    ],
  ]) {
    await assert.rejects(sourceEntries(await archive(headers)), /unsafe|duplicate/);
  }
});

test("runtime upgrade retires only unchanged listed payloads and retains unknown files", (t) => {
  const root = temporary(t);
  for (const name of ["old.whl", "current.whl", "unknown.whl"])
    writeFileSync(join(root, name), "payload");
  const previous = {
    schema: "etherview-python-wasm-v1",
    files: [{ path: "old.whl", sha256: digest("payload") }],
  };
  retirePreviousFiles(root, previous, [{ path: "current.whl" }]);
  assert.deepEqual(readdirSync(root).sort(), ["current.whl", "unknown.whl"]);
  writeFileSync(join(root, "old.whl"), "changed");
  assert.throws(() => retirePreviousFiles(root, previous, []), /payload changed/);
  assert.equal(readFileSync(join(root, "old.whl"), "utf8"), "changed");
  previous.files[0].path = "../outside.whl";
  assert.throws(() => retirePreviousFiles(root, previous, []), /invalid previous runtime path/);
});

test("runtime policy covers the exact supported set and rejects incomplete evidence", async () => {
  const { runtimeFor, runtimeByID, runtimePolicy, sharedRuntimes, validateRuntimeSet } =
    await import("./runtime-policy.mjs");
  const index = readJSON(join(HERE, "../versions/index.json")).versions;
  const mapped = Object.values(runtimePolicy).flatMap((p) => p.versions);
  assert.deepEqual([...mapped].sort(), index.map((v) => v.version).sort());
  assert.equal(new Set(mapped).size, index.length);
  assert(runtimePolicy.modern.versions.length >= 4);
  assert.equal(runtimePolicy.legacy.versions.length, 21);
  for (const item of index) {
    const expected = runtimePolicy.legacy.versions.includes(item.version) ? "legacy" : "modern";
    assert.equal(runtimeFor(item.version).id, expected);
    assert.equal(runtimeByID(expected).shared_sha256, sharedRuntimes[expected]);
  }
  for (const value of ["0.2.0", "0.3.5", "latest", "../legacy", "__proto__"])
    assert.throws(() => runtimeFor(value));
  assert.throws(() => runtimeByID("__proto__"));
  assert(validateRuntimeSet({ ...sharedRuntimes }));
  for (const value of [
    null,
    {},
    { modern: sharedRuntimes.modern },
    { ...sharedRuntimes, extra: "x" },
    { modern: sharedRuntimes.legacy, legacy: sharedRuntimes.modern },
  ])
    assert(!validateRuntimeSet(value));
});

function integrityFixture(t) {
  const root = temporary(t),
    runtimes = {},
    items = [];
  for (const [runtime, version, pyodide, python] of [
    ["legacy", "0.3.10", "0.29.5", "3.13.2"],
    ["modern", "0.4.3", "314.0.7", "3.14.2"],
  ]) {
    const shared = join(root, ".local/vyper-wasm/shared", runtime);
    mkdirSync(shared, { recursive: true });
    const payload = Buffer.from("authenticated WASM " + runtime);
    writeFileSync(join(shared, "python.wasm"), payload);
    const raw = canonical({ files: [{ path: "python.wasm", sha256: digest(payload) }] });
    writeFileSync(join(shared, "shared-manifest.json"), raw);
    runtimes[runtime] = digest(raw);
    const path = join(root, ".local/vyper-wasm/packages", version);
    mkdirSync(path, { recursive: true });
    const data = zip({
      [`vyper-${version}.dist-info/METADATA`]: `Name: vyper\nVersion: ${version}\n`,
    });
    const entry = { version, compiler_sha256: "a".repeat(64) };
    items.push(entry);
    writeFileSync(join(path, "packages.zip"), data);
    writeFileSync(
      join(path, "package-manifest.json"),
      canonical({
        ...entry,
        pyodide,
        python,
        dependencies: [],
        files: [{ path: "packages.zip", sha256: digest(data) }],
      }),
    );
  }
  return { root, runtimes, items, check: () => inventory(root, runtimes, items) };
}

test("local integrity validates both runtimes and all selected compiler metadata without advisory data", (t) => {
  const { check } = integrityFixture(t);
  assert.deepEqual(
    check()
      .distributions.map((d) => d.version)
      .sort(),
    ["0.3.10", "0.4.3"],
  );
});
for (const runtime of ["legacy", "modern"]) {
  for (const scenario of [
    "changed-manifest",
    "changed-payload",
    "extra-file",
    "missing-file",
    "extra-runtime",
    "symlink",
  ]) {
    test(`${runtime} integrity rejects ${scenario}`, (t) => {
      const { root, check } = integrityFixture(t);
      const shared = join(root, ".local/vyper-wasm/shared", runtime);
      if (scenario === "changed-manifest")
        writeFileSync(join(shared, "shared-manifest.json"), "{}");
      if (scenario === "changed-payload") writeFileSync(join(shared, "python.wasm"), "tampered");
      if (scenario === "extra-file") writeFileSync(join(shared, "extra"), "unlisted");
      if (scenario === "missing-file") rmSync(join(shared, "python.wasm"));
      if (scenario === "extra-runtime") mkdirSync(join(shared, "..", "unexpected"));
      if (scenario === "symlink") {
        rmSync(join(shared, "python.wasm"));
        symlinkSync("shared-manifest.json", join(shared, "python.wasm"));
      }
      assert.throws(check);
    });
  }
}
for (const version of ["0.3.10", "0.4.3"]) {
  for (const scenario of ["payload", "runtime", "source", "metadata", "extra-file"]) {
    test(`${version} integrity rejects changed ${scenario}`, (t) => {
      const { root, check } = integrityFixture(t);
      const path = join(root, ".local/vyper-wasm/packages", version);
      const manifest = readJSON(join(path, "package-manifest.json"));
      if (scenario === "payload") writeFileSync(join(path, "packages.zip"), "tampered");
      if (scenario === "runtime") manifest.python = "wrong-python";
      if (scenario === "source") manifest.compiler_sha256 = "b".repeat(64);
      if (scenario === "metadata") {
        const raw = zip({ [`vyper-${version}.dist-info/METADATA`]: "Name: other\nVersion: 1\n" });
        writeFileSync(join(path, "packages.zip"), raw);
        manifest.files[0].sha256 = digest(raw);
      }
      if (scenario === "extra-file") writeFileSync(join(path, "extra"), "unlisted");
      writeFileSync(join(path, "package-manifest.json"), canonical(manifest));
      assert.throws(check);
    });
  }
}
