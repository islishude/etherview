import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { pathToFileURL } from "node:url";

import { loadInventory, submission, published, verifyAll } from "./verify.mjs";
const directory = new URL("./", import.meta.url);
const genesis = JSON.parse(fs.readFileSync(new URL("../genesis.json", import.meta.url)));
const { inventory, bundles } = loadInventory(directory, genesis);
test("inventory covers every code allocation including constructor children", () => {
  assert.equal(inventory.contracts.length, 27);
  assert.equal(inventory.contracts.filter((c) => c.name === "LocalBillingToken").length, 2);
  for (const name of ["SenderCreator", "SimpleAccount", "LocalBillingToken", "BuilderExit", "Permit2", "x402ExactPermit2Proxy"])
    assert.ok(inventory.contracts.some((c) => c.name === name));
  for (const c of inventory.contracts) {
    const body = submission(c, bundles);
    assert.ok(body.compiler_version);
    assert.equal(body.creation_entrypoint, undefined);
    assert.ok(
      body.sources || Object.values(body.input.sources).every((s) => typeof s.content === "string"),
    );
  }
});
test("missing, extra, duplicate, and changed genesis entries fail before requests", (t) => {
  const changed = structuredClone(genesis);
  delete changed.alloc[Object.keys(changed.alloc).find((a) => changed.alloc[a].code)];
  assert.throws(() => loadInventory(directory, changed), /Incomplete/);
  const code = structuredClone(genesis);
  code.alloc[Object.keys(code.alloc).find((a) => code.alloc[a].code)].code = "0x00";
  assert.throws(() => loadInventory(directory, code), /code mismatch/);
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "genesis-verification-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  fs.cpSync(directory, dir, { recursive: true });
  fs.appendFileSync(path.join(dir, "local.json"), " ");
  assert.throws(() => loadInventory(pathToFileURL(dir + "/"), genesis), /checksum mismatch/);
  fs.copyFileSync(new URL("local.json", directory), path.join(dir, "local.json"));
  const duplicate = structuredClone(inventory);
  duplicate.contracts[1] = duplicate.contracts[0];
  fs.writeFileSync(path.join(dir, "inventory.json"), JSON.stringify(duplicate));
  assert.throws(
    () => loadInventory(pathToFileURL(dir + "/"), genesis),
    /Invalid genesis inventory/,
  );
});
function fixture(overrides = {}) {
  const c = inventory.contracts[0];
  const artifact = {
    kind: "verification_success",
    resolution: "exact_address",
    target: { chain_id: inventory.chainId, address: c.address, code_hash: c.codeHash },
    runtime_match: { match_type: "full" },
  };
  const imported = {
    data: [{ type: "contract", address: c.address, code_hash: c.codeHash, block_hash: "genesis" }],
    meta: { chain_id: inventory.chainId },
  };
  let submitted = false,
    time = 0,
    posts = 0;
  const logs = [];
  const options = {
    inventory: { ...inventory, contracts: [c] },
    bundles,
    log: (s) => logs.push(s),
    now: () => time,
    sleep: async (ms) => {
      time += ms;
    },
    timeout: 3000,
    rpc: async (method) => (method === "eth_chainId" ? "0xbeaf" : { hash: "genesis" }),
    request: async (p, body) => {
      if (p.includes("genesis/accounts")) return { status: 200, body: imported };
      if (body) {
        posts++;
        submitted = true;
        return { status: 202, body: { data: { id: "00000000-0000-0000-0000-000000000001" } } };
      }
      if (p.includes("verifier/jobs"))
        return { status: 200, body: { data: { status: "succeeded", outcome: artifact } } };
      return { status: submitted ? 200 : 404, body: { data: artifact } };
    },
  };
  return { c, artifact, imported, logs, posts: () => posts, options: { ...options, ...overrides } };
}
test("normal publication and idempotent replay", async () => {
  const f = fixture();
  await verifyAll(f.options);
  assert.equal(f.posts(), 1);
  await verifyAll(f.options);
  assert.equal(f.posts(), 1);
  assert.match(f.logs.at(-1), /1 reused/);
});
test("published reuse requires exact chain, address, code and runtime evidence", () => {
  const f = fixture();
  assert.ok(published(f.artifact, f.c, inventory.chainId));
  for (const change of [
    { resolution: "code_hash" },
    { runtime_match: null },
    { target: { ...f.artifact.target, code_hash: "wrong" } },
    { target: { ...f.artifact.target, chain_id: "1" } },
  ])
    assert.equal(published({ ...f.artifact, ...change }, f.c, inventory.chainId), false);
});
for (const [name, change, pattern] of [
  [
    "wrong chain",
    (f) => {
      f.options.rpc = async () => "0x1";
    },
    /RPC chain mismatch/,
  ],
  [
    "wrong genesis",
    (f) => {
      f.imported.data[0].block_hash = "other";
    },
    /identity mismatch/,
  ],
  [
    "partial inventory",
    (f) => {
      f.imported.meta.next_cursor = "more";
    },
    /inventory mismatch/,
  ],
  [
    "compile failure",
    (f) => {
      const req = f.options.request;
      f.options.request = async (p, b) =>
        p.includes("verifier/jobs")
          ? {
              status: 200,
              body: { data: { status: "failed", outcome: { kind: "compilation_failure" } } },
            }
          : req(p, b);
    },
    /verification failed/,
  ],
  [
    "false succeeded",
    (f) => {
      const req = f.options.request;
      f.options.request = async (p, b) =>
        p.includes("verifier/jobs")
          ? {
              status: 200,
              body: { data: { status: "succeeded", outcome: { kind: "verification_failure" } } },
            }
          : req(p, b);
    },
    /invalid genesis match/,
  ],
  [
    "stuck job",
    (f) => {
      const req = f.options.request;
      f.options.request = async (p, b) =>
        p.includes("verifier/jobs")
          ? { status: 200, body: { data: { status: "queued" } } }
          : req(p, b);
    },
    /verification timeout/,
  ],
  [
    "unavailable import",
    (f) => {
      f.options.request = async () => ({ status: 503 });
    },
    /import timeout/,
  ],
  [
    "unavailable compiler",
    (f) => {
      const req = f.options.request;
      f.options.request = async (p, b) => (b ? { status: 503 } : req(p, b));
    },
    /submission timeout/,
  ],
  [
    "artifact not published",
    (f) => {
      const req = f.options.request;
      f.options.request = async (p, b) =>
        !b && p.endsWith("/verification") ? { status: 404 } : req(p, b);
    },
    /published artifact mismatch/,
  ],
])
  test(name, async () => {
    const f = fixture();
    change(f);
    await assert.rejects(verifyAll(f.options), pattern);
  });
test("startup verifies after chain readiness and recreation skips verification", () => {
  const make = fs.readFileSync(new URL("../../Makefile", import.meta.url), "utf8");
  const startup = make.split("\nstart-preview:")[1].split(/\n\S/)[0];
  assert.ok(
    startup.indexOf("preview/scripts/verify.mjs") > startup.indexOf("preview/scripts/ready.mjs"),
  );
  const recreation = make.split("\nrecreate-preview:")[1].split(/\n\S/)[0];
  assert.doesNotMatch(recreation, /preview\/scripts\/verify|verify-preview-genesis|genesis-verify/);
  const compose = fs.readFileSync(new URL("../../compose.yaml", import.meta.url), "utf8");
  const service = compose.split("  genesis-verify:\n")[1].split("\n  bundler:")[0];
  assert.match(service, /profiles: \["genesis-verify"\]/);
  assert.match(service, /read_only: true/);
  assert.doesNotMatch(service, /docker.sock|DATABASE_URL|PEPPER|ports:/);
});

test("temporary key is passed only through stdin and revoked on success or failure", async () => {
  const { verifyPreview } = await import("../scripts/verify.mjs");
  for (const fail of [false, true]) {
    const calls = [];
    const invoke = (args, options) => {
      calls.push(args);
      assert.ok(!args.includes("test-token"));
      if (args.includes("create"))
        return JSON.stringify({ token: "test-token", prefix: "test-prefix" });
      if (args.includes("run")) {
        assert.equal(options.input, "test-token");
        assert.equal(options.env, undefined);
        if (fail) throw Error("test failed");
      }
      return "{}";
    };
    if (fail) assert.throws(() => verifyPreview(invoke), /test failed/);
    else verifyPreview(invoke);
    assert.ok(calls.at(-1).includes("revoke"));
    assert.ok(calls.at(-1).includes("test-prefix"));
  }
});
