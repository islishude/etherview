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
import { download, unpackWheel, packRuntime } from "./packages.mjs";
import { descriptor, buildRelease } from "./build-release.mjs";
import { metadata, evaluate, findingDigest, osv, queryPackage, queryCore } from "./audit.mjs";
import { checkPackage } from "./package-licenses.mjs";
import { evaluateReference } from "../audit.mjs";
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
    pyodide: "0.29.5",
    python: "3.13.2",
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
test("unsafe archive paths, native code, conflicts, symlinks and oversized members", () => {
  for (const name of ["../file.py", "/file.py", "a/../file.py", "a\\file.py", "a//b", "a/./b"])
    assert.throws(() => safeMember(name), /unsafe/);
  const files = new Map();
  unpackWheel(
    zip({
      "fallback/map.py": "upstream python",
      "fallback/map.abi3.so": "native",
      "fallback/__pycache__/map.pyc": "bytecode",
    }),
    files,
  );
  assert.deepEqual(files, new Map([["fallback/map.py", Buffer.from("upstream python")]]));
  assert.throws(
    () =>
      unpackWheel(zip({ "module.py": "other" }), new Map([["module.py", Buffer.from("original")]])),
    /conflicting/,
  );
  const archive = new AdmZip();
  archive.addFile("link", Buffer.from("../escape"));
  archive.getEntry("link").header.attr = 0o120777 * 65536;
  assert.throws(() => unpackWheel(archive.toBuffer(), new Map()), /unsafe wheel/);
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
test("changed advisory, inventory, missing/duplicate reports fail closed", () => {
  const actual = { distributions: [{ name: "x", version: "1" }] },
    finding = { id: "CVE-test", details: "reviewed prerequisite" };
  const reports = [{ component: "PyPI/x/1", findings: [finding] }];
  const review = {
    inventory_sha256: digest(canonical(actual)),
    core: [],
    reasons: { unreachable: "specific prerequisite absent" },
    findings: { "PyPI/x/CVE-test": { sha256: [findingDigest(finding)], reason: "unreachable" } },
  };
  assert.equal(evaluate(actual, reports, review), 1);
  for (const rs of [[], [...reports, ...reports]])
    assert.throws(() => evaluate(actual, rs, review), /coverage/);
  for (const changed of [
    { id: "CVE-new" },
    { ...finding, details: "new impact" },
    { ...finding, severity: "critical" },
  ])
    assert.throws(
      () => evaluate(actual, [{ ...reports[0], findings: [changed] }], review),
      /unreviewed or changed/,
    );
  assert.throws(
    () => evaluate({ distributions: [{ name: "x", version: "2" }] }, reports, review),
    /inventory changed/,
  );
  assert.throws(
    () => evaluate(actual, [{ ...reports[0], findings: [finding, finding] }], review),
    /duplicate/,
  );
});
for (const [component, identifier, suffix] of [
  ["cpython", "CVE-2026-82049", ""],
  ["cpython", "CVE-2026-82049", "-vanir"],
  ["cpython", "CVE-2026-82049", "-fix-range"],
  ["expat", "CVE-2026-77214", ""],
]) {
  test("retained reviewed advisory " + identifier + suffix, () => {
    const finding = readJSON(join(HERE, "testdata", identifier + suffix + ".json"));
    const actual = { distributions: [] },
      review = {
        ...readJSON(join(HERE, "audit-review.json")),
        core: [
          {
            name: component,
            repo: component === "cpython" ? "python/cpython" : "libexpat/libexpat",
          },
        ],
        findings: Object.fromEntries(
          Object.entries(readJSON(join(HERE, "audit-review.json")).findings).filter(([key]) =>
            key.startsWith("core/" + component + "/"),
          ),
        ),
        inventory_sha256: digest(canonical(actual)),
      };
    const run = (f) =>
      evaluate(actual, [{ component: "core/" + component, findings: [f] }], review);
    assert.equal(run(finding), 1);
    assert.equal(run({ ...finding, modified: "timestamp-only change" }), 1);
    const previous = structuredClone(review);
    previous.findings[`core/${component}/${identifier}`].sha256 = previous.findings[
      `core/${component}/${identifier}`
    ].sha256.filter((h) => h !== findingDigest(finding));
    assert.throws(
      () => evaluate(actual, [{ component: "core/" + component, findings: [finding] }], previous),
      /unreviewed/,
    );
    for (const field of [
      "details",
      "affected",
      "references",
      "database_specific",
      "related",
      "severity",
    ])
      assert.throws(
        () => run({ ...finding, [field]: "unreviewed revision" }),
        /unreviewed or changed/,
      );
  });
}
test("OSV pagination and service identity fail closed", async () => {
  let requests = 0;
  const fetcher = async (_url, query) => {
    requests++;
    return query.page_token
      ? { vulns: [{ id: "second" }] }
      : { vulns: [{ id: "first" }], next_page_token: "next" };
  };
  assert.deepEqual(
    (await osv({}, fetcher)).map((f) => f.id),
    ["first", "second"],
  );
  assert.equal(requests, 2);
  await assert.rejects(
    osv({}, async () => ({ next_page_token: "same" })),
    /repeated/,
  );
  await assert.rejects(
    osv({}, async () => ({ vulns: {} })),
    /invalid/,
  );
  await assert.rejects(
    queryPackage(["x", "1"], async () => ({
      info: { name: "y", version: "1" },
      vulnerabilities: [],
    })),
    /identity/,
  );
  await assert.rejects(
    queryCore({ name: "x", commit: "a", advisories: ["CVE-x"] }, async (url) =>
      url.endsWith("/query") ? {} : { id: "CVE-y" },
    ),
    /identity/,
  );
});
test("reference environment excludes retired packaging; audit corrections stay exactly scoped", () => {
  const pins = referencePackages("0.4.3");
  for (const name of RETIRED) assert.equal(pins.has(name), false);
  const hash = "3b9671727c888363740dc678e60336759871487d0e4e9fdd973048fa9635c4fd";
  const selected = new Map([["vyper", pins.get("vyper")]]);
  const reports = [{ component: "PyPI/vyper/0.4.3", findings: [{ id: "PYSEC-2023-142" }] }];
  assert.deepEqual(evaluateReference(selected, reports, hash), ["PYSEC-2023-142"]);
  assert.throws(() => evaluateReference(selected, reports, "0".repeat(64)), /unresolved/);
  assert.throws(() => evaluateReference(selected, [], hash), /coverage/);
  assert.throws(
    () => evaluateReference(selected, [{ ...reports[0], findings: [{ id: "new" }] }], hash),
    /unresolved/,
  );
});

test("ZIP rejects duplicate paths and corrupt contents before use", () => {
  const raw = zip({ "a.py": "first", "b.py": "second" });
  for (let at = raw.indexOf("b.py"); at !== -1; at = raw.indexOf("b.py", at + 4))
    raw.write("a.py", at);
  assert.throws(() => zipEntries(raw), /duplicate/i);
  const corrupt = zip({ "x.py": "source" });
  corrupt[34] ^= 0xff;
  assert.throws(() => unpackWheel(corrupt, new Map()));
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
  for (const name of ["old.whl", "current.whl", "unknown.whl"]) writeFileSync(join(root, name), "payload");
  const previous = {schema: "etherview-python-wasm-v1", files: [{path: "old.whl", sha256: digest("payload")}]};
  retirePreviousFiles(root, previous, [{path: "current.whl"}]);
  assert.deepEqual(readdirSync(root).sort(), ["current.whl", "unknown.whl"]);
  writeFileSync(join(root, "old.whl"), "changed");
  assert.throws(() => retirePreviousFiles(root, previous, []), /payload changed/);
  assert.equal(readFileSync(join(root, "old.whl"), "utf8"), "changed");
  previous.files[0].path = "../outside.whl";
  assert.throws(() => retirePreviousFiles(root, previous, []), /invalid previous runtime path/);
});
