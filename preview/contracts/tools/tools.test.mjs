import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { compile, digest } from "./artifacts.mjs";
import { catalog, lifecycle, actionProblem } from "./catalog.mjs";
import { argumentsFor, safeError, main } from "./cli.mjs";
import {
  Engine,
  endpoint,
  matches,
  assertNetwork,
  retryDelay,
  request,
  checkScenario,
} from "./engine.mjs";
import { createRun, openRun, acquireLock, publicReport, writeReport } from "./state.mjs";
const endpoints = {
  rpc: "http://127.0.0.1:8545",
  bundler: "http://127.0.0.1:4337",
  app: "http://127.0.0.1:8080",
};
test("rate limit backoff honors bounded Retry-After without replaying a request", async (t) => {
  assert.equal(retryDelay("2"), 2000);
  assert.equal(retryDelay("999999"), 30000);
  assert.equal(retryDelay("invalid"), 1000);
  assert.equal(retryDelay(null), 1000);
  assert.equal(retryDelay("Thu, 01 Jan 1970 00:00:05 GMT", 1000), 4000);
  const original = globalThis.fetch;
  t.after(() => {
    globalThis.fetch = original;
  });
  let requests = 0;
  globalThis.fetch = async () => {
    requests++;
    return new Response("{}", { status: 429, headers: { "Retry-After": "1" } });
  };
  const start = Date.now();
  assert.equal((await request(endpoints.app, "/verification", { input: "fixture" })).status, 429);
  assert.equal(requests, 1);
  assert.ok(Date.now() - start >= 900);
});
function fixture(t) {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), "preview-tools-test-"));
  t.after(() => fs.rmSync(base, { recursive: true, force: true }));
  const run = createRun(base, ["transactions"], endpoints);
  run.state.chainId = "48815";
  run.state.genesis = "0x1234";
  run.save();
  return { base, run };
}
test("blob recheck retains verified downloads when a later blob is rate limited", async (t) => {
  const { run } = fixture(t);
  const s = run.state.scenarios.transactions;
  s.setupComplete = true;
  const bodies = [Buffer.alloc(131072, 1), Buffer.alloc(131072, 2)];
  const blobs = bodies.map((body, i) => ({
    versionedHash: "0x0" + (i + 1),
    commitment: "0xc" + i,
    sha256: digest(body),
  }));
  s.steps.blob = {
    kind: "transaction",
    hash: "0xabc",
    expected: "success",
    blobs,
    receipt: { blockHash: "0xdef" },
  };
  const e = new Engine(run, { timeout: 5 }, {});
  e.reconcile = async (step) => step;
  const keyActions = [];
  e.apiKeyAdmin = (args) => {
    keyActions.push(args);
    return { prefix: "fixture-prefix", token: "fixture-token" };
  };
  const original = globalThis.fetch;
  t.after(() => {
    globalThis.fetch = original;
  });
  const downloads = [0, 0];
  globalThis.fetch = async (url, options) => {
    const parsed = new URL(url);
    if (parsed.pathname === "/v2/api") {
      assert.equal(options.headers["X-API-Key"], "fixture-token");
      assert.equal(parsed.searchParams.get("action"), "getblob");
      assert.equal(parsed.searchParams.get("blockhash"), "0xdef");
      const i = Number(parsed.searchParams.get("index"));
      downloads[i]++;
      if (i === 1 && downloads[i] === 1) return new Response("{}", { status: 429 });
      return Response.json({ status: "1", result: "0x" + bodies[i].toString("hex") });
    }
    const data = url.endsWith("/blobs")
      ? {
          state: "complete",
          items: blobs.map((b, index) => ({
            index,
            versioned_hash: b.versionedHash,
            commitment: b.commitment,
            sha256: b.sha256,
            transaction_hash: "0xabc",
          })),
        }
      : url.includes("/transactions/")
        ? {
            kind: "included",
            transaction: { hash: "0xabc", status: "success" },
          }
        : { state: "complete" };
    return Response.json({ data });
  };
  await checkScenario(e, s);
  assert.deepEqual(downloads, [1, 2]);
  assert.equal(keyActions[0][0], "create");
  assert.deepEqual(keyActions[1], ["revoke", "fixture-prefix"]);
  assert.equal(s.status, "api_published");
});
test("CLI rejects unsafe inputs before network operations", () => {
  for (const args of [
    ["run", "blob", "--to", "invalid"],
    ["doctor", "--timeout", "NaN"],
    ["doctor", "--timeout", "0"],
    ["doctor", "--unknown"],
  ])
    assert.throws(() => argumentsFor(args));
  for (const url of [
    "https://mainnet.example",
    "http://user:secret@localhost",
    "http://localhost/path",
    "http://localhost/#key",
  ])
    assert.throws(() => endpoint(url));
  assert.equal(
    endpoint("https://geth.etherview.localhost:8443"),
    "https://geth.etherview.localhost:8443",
  );
});
test("run IDs, permissions and writer locks isolate durable state", (t) => {
  const { base, run } = fixture(t);
  assert.equal(fs.statSync(run.dir).mode & 0o777, 0o700);
  assert.equal(fs.statSync(path.join(run.dir, "state.json")).mode & 0o777, 0o600);
  assert.throws(() => openRun(base, "../escape"));
  const unlock = acquireLock(base, "signer");
  assert.throws(() => acquireLock(base, "signer"));
  unlock();
  acquireLock(base, "signer")();
  assert.equal(openRun(base, run.state.id).state.id, run.state.id);
  fs.chmodSync(path.join(run.dir, "state.json"), 0o644);
  assert.throws(() => openRun(base, run.state.id));
});
test("shareable reports omit raw payloads, keys, registration secrets and credentials", (t) => {
  const { run } = fixture(t);
  const s = run.state.scenarios.transactions;
  s.registration = { secret: "ENS-SECRET" };
  s.token = "API-SECRET";
  run.state.funding.steps.owner = { hash: "0xfund", raw: "FUND-SIGNED-SECRET" };
  s.steps.send = {
    kind: "transaction",
    hash: "0xabc",
    raw: "SIGNED-SECRET",
    operation: { signature: "SIGNATURE-SECRET" },
    status: "submitted",
    expected: "success",
  };
  const json = JSON.stringify(publicReport(run.state));
  for (const secret of [
    "ENS-SECRET",
    "API-SECRET",
    "SIGNED-SECRET",
    "SIGNATURE-SECRET",
    "FUND-SIGNED-SECRET",
  ])
    assert.ok(!json.includes(secret));
  const a = writeReport(run);
  assert.equal(a.manualStatus, "not_run");
  assert.ok(a.scenarios[0].links.some((l) => l.url.endsWith("/tx/0xabc")));
});
test("ambiguous broadcast is journaled before RPC and never automatically resubmitted", async (t) => {
  const { run } = fixture(t);
  const e = new Engine(run, { timeout: 1 }, {});
  const s = run.state.scenarios.transactions;
  let sends = 0;
  e.rpc = {
    sendRawTransaction: async () => {
      sends++;
      assert.equal(
        openRun(path.dirname(path.dirname(run.dir)), run.state.id).state.scenarios.transactions
          .steps.test.hash,
        "0x123",
      );
      throw Error("lost response");
    },
  };
  await assert.rejects(
    e.tx(s, "test", {}, { prepared: async () => ({ raw: "0xdead", hash: "0x123" }) }),
    /lost response/,
  );
  assert.equal(s.steps.test.status, "submitted");
  e.reconcile = async () => null;
  await assert.rejects(
    e.tx(
      s,
      "another-scenario",
      {},
      {
        prepared: () => {
          throw Error("must not sign another payment");
        },
      },
    ),
    /Prior operation outcome is unknown/,
  );
  await assert.rejects(
    e.userop(s, "another-operation", () => {
      throw Error("must not sign another operation");
    }),
    /Prior operation outcome is unknown/,
  );
  assert.equal(s.steps["another-scenario"], undefined);
  assert.equal(s.steps["another-operation"], undefined);
  e.reconcile = async (step) => {
    step.status = "mined";
    step.receipt = { transactionHash: step.hash };
    return step;
  };
  await e.tx(
    s,
    "test",
    {},
    {
      prepared: () => {
        throw Error("must not prepare twice");
      },
    },
  );
  assert.equal(sends, 1);
});
test("prepared but unsent state also fails closed instead of creating a new transaction", async (t) => {
  const { run } = fixture(t);
  const e = new Engine(run, { timeout: 1 }, {});
  const s = run.state.scenarios.transactions;
  s.steps.old = {
    kind: "transaction",
    hash: "0xabc",
    raw: "0xdef",
    status: "prepared",
    expected: "success",
  };
  e.wait = async (read) => {
    assert.equal(await read(), null);
    throw Error("pending");
  };
  e.reconcile = async () => null;
  await assert.rejects(
    e.tx(
      s,
      "old",
      {},
      {
        prepared: () => {
          throw Error("should not sign");
        },
      },
    ),
    /pending/,
  );
});
test("AA rejection requires the exact expected RPC code; transport failures remain uncertain", async (t) => {
  const { run } = fixture(t);
  const e = new Engine(run, { timeout: 1 }, {});
  const s = run.state.scenarios.transactions;
  let sends = 0;
  e.bundler = {
    request: async () => {
      sends++;
      throw Object.assign(Error("bad signature"), { code: -32507 });
    },
  };
  await e.userop(
    s,
    "bad",
    async () => ({ hash: "0x123", operation: { signature: "private" } }),
    "success",
    [-32507],
  );
  assert.equal(s.steps.bad.status, "rejected");
  await e.userop(
    s,
    "bad",
    () => {
      throw Error("must not sign twice");
    },
    "success",
    [-32507],
  );
  assert.equal(sends, 1);
  e.bundler.request = async () => {
    throw Error("socket closed");
  };
  await assert.rejects(
    e.userop(s, "uncertain", async () => ({ hash: "0x234", operation: {} }), "success", [-32507]),
  );
  assert.equal(s.steps.uncertain.status, "submitted");
});
test("receipts must preserve hash, canonical block and expected execution status", async (t) => {
  const { run } = fixture(t);
  const e = new Engine(run, {}, {});
  const step = { kind: "transaction", hash: "0xabc", expected: "reverted" };
  e.rpc = {
    getTransactionReceipt: async () => ({
      transactionHash: "0xabc",
      blockNumber: 1n,
      blockHash: "0x111",
      status: "reverted",
    }),
    getBlock: async () => ({ hash: "0x222" }),
  };
  await assert.rejects(e.reconcile(step), /canonical/);
  e.rpc.getBlock = async () => ({ hash: "0x111" });
  assert.equal((await e.reconcile(step)).status, "mined");
  step.expected = "success";
  await assert.rejects(e.reconcile(step), /outcome/);
});
test("typed API checks retain integers as strings and compare hashes case insensitively", () => {
  assert.equal(
    matches(
      {
        data: {
          id: "1606938044258990275541962092341162602522202993782792835301418",
          address: "0xaBc",
        },
      },
      {
        "data.id": "1606938044258990275541962092341162602522202993782792835301418",
        "data.address": "0xabc",
      },
    ),
    true,
  );
  assert.equal(matches({ data: { id: 1 } }, { "data.id": "1" }), false);
  assert.equal(matches({}, { "data.state": "complete" }), false);
  assert.ok(!safeError({ shortMessage: "secret", message: "private" }).includes("private"));
  assert.ok(
    !safeError(new SyntaxError("Unexpected secret-state-content")).includes("secret-state-content"),
  );
});
test("catalog lifecycle actions exist and every proxy family is independently selectable", () => {
  for (const [name, actions] of Object.entries(lifecycle))
    for (const a of actions) assert.ok(catalog[name].actions.includes(a), name + " " + a);
  for (const kind of [
    "erc1967",
    "transparent",
    "uups",
    "beacon",
    "erc1167",
    "immutable",
    "cwia",
    "safe",
    "diamond",
  ])
    assert.ok(catalog["proxy." + kind]);
});
test("locked fixtures compile, preserving official Safe bytes and exact verification inputs", () => {
  const a = compile();
  assert.ok(a.Safe.bytecode.startsWith("0x"));
  assert.equal(a.Safe.upstream, "@safe-global/safe-contracts@1.4.1");
  for (const name of [
    "ToolCalls",
    "ToolERC20",
    "ToolERC721",
    "ToolERC1155",
    "ToolDiamond",
    "MyAccount",
    "ImplementationV2",
    "TransparentUpgradeableProxy",
    "BeaconProxy",
  ]) {
    assert.ok(a[name].bytecode.length > 2, name);
    assert.equal(a[name].input.settings.evmVersion, "cancun");
    assert.equal(a[name].compiler, "0.8.30+commit.73712a01");
    assert.ok(a[name].input.sources[a[name].file]);
  }
});

test("public transaction checks use the included envelope and failed API status", async (t) => {
  const { run } = fixture(t);
  const s = run.state.scenarios.transactions;
  s.setupComplete = true;
  s.steps.revert = {
    kind: "transaction",
    hash: "0xabc",
    expected: "reverted",
    status: "mined",
    receipt: { transactionHash: "0xabc", blockHash: "0x111", status: "reverted" },
  };
  const { checkScenario } = await import("./engine.mjs");
  const e = new Engine(run, {}, {});
  e.reconcile = async (step) => step;
  e.wait = async (read, predicate) => {
    const result = await read();
    assert.equal(predicate(result), true);
    return result;
  };
  t.mock.method(
    globalThis,
    "fetch",
    async () =>
      new Response(
        JSON.stringify({
          data: { kind: "included", transaction: { hash: "0xabc", status: "failed" } },
        }),
        { status: 200 },
      ),
  );
  await checkScenario(e, s);
  assert.equal(s.status, "api_published");
  assert.equal(s.checks[0].status, "passed");
});
test("read-only checks cannot complete an unstarted scenario or an unfinished action", async (t) => {
  const { run } = fixture(t);
  const s = run.state.scenarios.transactions;
  const e = new Engine(run, {}, {});
  await assert.rejects(checkScenario(e, s), /unfinished steps/);
  assert.notEqual(s.status, "api_published");
  s.setupComplete = true;
  s.actions.push({ name: "activity", status: "started" });
  await assert.rejects(checkScenario(e, s), /unfinished steps/);
  assert.equal(s.actions[0].status, "started");
  assert.notEqual(s.status, "api_published");
});
test("publication timeout is bounded and preserves the saved operation", async (t) => {
  const { run } = fixture(t);
  const e = new Engine(run, { timeout: 0.01 }, {});
  run.state.scenarios.transactions.steps.pending = { hash: "0xabc", status: "submitted" };
  run.save();
  await assert.rejects(
    e.wait(async () => null, Boolean, "publication"),
    /Timed out: publication/,
  );
  assert.equal(
    openRun(path.dirname(path.dirname(run.dir)), run.state.id).state.scenarios.transactions.steps
      .pending.hash,
    "0xabc",
  );
});
test("AA receipt must agree with the execution chain and canonical block", async (t) => {
  const { run } = fixture(t);
  const e = new Engine(run, {}, {});
  const step = { kind: "userop", hash: "0xabc", expected: "success" };
  e.bundler = {
    request: async () => ({
      userOpHash: "0xabc",
      success: true,
      receipt: { transactionHash: "0xdef", blockHash: "0x111" },
    }),
  };
  e.rpc = {
    getTransactionReceipt: async () => ({
      transactionHash: "0xdef",
      blockHash: "0x222",
      blockNumber: 1n,
      status: "success",
    }),
  };
  await assert.rejects(e.reconcile(step), /block mismatch/);
  e.rpc.getTransactionReceipt = async () => ({
    transactionHash: "0xdef",
    blockHash: "0x111",
    blockNumber: 1n,
    status: "success",
  });
  e.rpc.getBlock = async () => ({ hash: "0x222" });
  await assert.rejects(e.reconcile(step), /canonical/);
});
test("resuming deployment retains verification job identity and exports constructor input", async (t) => {
  const { run } = fixture(t);
  const e = new Engine(
    run,
    {},
    {
      Test: {
        identity: "build-1",
        name: "Test",
        abi: [],
        bytecode: "0x6000",
        input: { language: "Solidity", sources: {}, settings: {} },
        compiler: "0.8.30+commit.73712a01",
      },
    },
  );
  const s = run.state.scenarios.transactions;
  e.tx = async () => ({ contractAddress: "0x1111111111111111111111111111111111111111" });
  await e.deploy(s, "test", "Test");
  s.deployments.test.job = "existing-verification";
  await e.deploy(s, "test", "Test");
  assert.equal(s.deployments.test.job, "existing-verification");
  assert.equal(s.deployments.test.constructorArguments, "0x");
  assert.ok(fs.existsSync(path.join(run.dir, "verification", "transactions-test-input.json")));
});
test("CWIA submits its own compilation even when derived exact source already exists", async (t) => {
  const { run } = fixture(t);
  const s = run.state.scenarios.transactions;
  s.name = "proxy.cwia";
  const address = "0x1111111111111111111111111111111111111111";
  s.deployments = { implementation: { address, artifact: "MyAccount" } };
  const e = new Engine(
    run,
    {},
    {
      MyAccount: {
        name: "MyAccount",
        identity: "fixture",
        compiler: "0.8.30+commit.73712a01",
        input: {},
      },
    },
  );
  let submissions = 0,
    revocations = 0;
  e.compose = (args) => {
    if (args.includes("revoke")) {
      revocations++;
      return "{}";
    }
    return JSON.stringify({ prefix: "test-prefix", token: "private-test-token" });
  };
  t.mock.method(globalThis, "fetch", async (_url, options) => {
    if (options.method === "POST") {
      submissions++;
      return Response.json({ data: { id: "native-job" } }, { status: 202 });
    }
    if (_url.includes("/verifier/jobs/")) return Response.json({ data: { status: "succeeded" } });
    return Response.json({
      data: { kind: "verification_success", resolution: "exact_address", target: { address } },
    });
  });
  await e.verify(s, ["implementation"]);
  assert.equal(submissions, 1);
  assert.equal(s.deployments.implementation.job, "native-job");
  assert.equal(revocations, 1);
  await e.verify(s, ["implementation"]);
  assert.equal(submissions, 1);
  assert.equal(revocations, 2);
});

test("invalid saved-run actions fail before funding or network access", async (t) => {
  const { run, base } = fixture(t);
  let calls = 0;
  t.mock.method(globalThis, "fetch", async () => {
    calls++;
    throw Error("unexpected network access");
  });
  t.mock.method(console, "log", () => {});
  await assert.rejects(
    main(["act", "invalid-action", "--run", run.state.id, "--dir", base]),
    /Unsupported action/,
  );
  assert.equal(calls, 0);
  assert.deepEqual(openRun(base, run.state.id).state.funding.steps, {});
});

test("read-only reconciliation cannot turn an accepted negative AA test into a pass", async (t) => {
  const { run } = fixture(t);
  const e = new Engine(run, {}, {});
  e.bundler = { request: async () => ({ userOpHash: "0xabc", success: true }) };
  await assert.rejects(
    e.reconcile({
      kind: "userop",
      hash: "0xabc",
      expected: "success",
      expectedRejection: [-32507],
      status: "submitted",
    }),
    /Expected admission rejection/,
  );
});

test("network identity and action dependencies reject unsafe continuation", () => {
  assert.throws(
    () => assertNetwork({ chainId: "1", genesis: "0xabc" }, "0xabc"),
    /another Preview network/,
  );
  assert.throws(
    () => assertNetwork({ chainId: "48815", genesis: "0xabc" }, "0xdef"),
    /another Preview network/,
  );
  assert.doesNotThrow(() => assertNetwork({ chainId: "48815", genesis: "0xabc" }, "0xabc"));
  const ens = { name: "ens", actions: [] };
  assert.match(actionProblem(ens, "reclaim"), /transfer/);
  ens.actions.push({ name: "transfer", status: "started" });
  assert.match(actionProblem(ens, "reclaim"), /transfer/);
  ens.actions[0].status = "complete";
  assert.equal(actionProblem(ens, "reclaim"), undefined);
  const diamond = { name: "proxy.diamond", actions: [] };
  assert.match(actionProblem(diamond, "remove"), /add/);
  diamond.actions.push({ name: "add", status: "complete" });
  assert.equal(actionProblem(diamond, "remove"), undefined);
});

test("holder checks find exact balances in address-sorted pages", () => {
  const expected = { data: { $contains: { holder_address: "0xff", balance: "9007199254740993" } } };
  assert.equal(
    matches(
      {
        data: [
          { holder_address: "0x01", balance: "1" },
          { holder_address: "0xFF", balance: "9007199254740993" },
        ],
      },
      expected,
    ),
    true,
  );
  assert.equal(
    matches({ data: [{ holder_address: "0xff", balance: "9007199254740992" }] }, expected),
    false,
  );
});
test("custom state directories share the developer-account writer lock", (t) => {
  const first = fixture(t),
    second = fixture(t);
  first.run.state.genesis = second.run.state.genesis = "lock-test-" + first.run.state.id;
  const a = new Engine(first.run, { dir: first.base }, {});
  const b = new Engine(second.run, { dir: second.base }, {});
  const unlock = a.lockSender("0xabc");
  try {
    assert.throws(() => b.lockSender("0xabc"), /Another writer/);
  } finally {
    unlock();
  }
  b.lockSender("0xabc")();
});
test("ambiguous shared funding retains its writer lock across independent runs", async (t) => {
  const first = fixture(t),
    second = fixture(t);
  first.run.state.genesis = second.run.state.genesis = "uncertain-funding-" + first.run.state.id;
  const a = new Engine(first.run, {}, {}),
    b = new Engine(second.run, {}, {});
  a.account = { address: "0x0000000000000000000000000000000000000001" };
  a.wallet = () => ({
    prepareTransactionRequest: async (input) => input,
    signTransaction: async () => "0xdead",
  });
  a.rpc = {
    sendRawTransaction: async () => {
      throw Error("lost funding response");
    },
  };
  const lock = a.lockSender.bind(a);
  let release,
    address,
    released = false;
  a.lockSender = (sender) => {
    address = sender;
    release = lock(sender);
    return () => {
      released = true;
      release();
    };
  };
  t.after(() => {
    if (release && !released) release();
  });
  await assert.rejects(a.fund(), /lost funding response/);
  assert.equal(first.run.state.funding.steps.owner.status, "submitted");
  assert.equal(released, false);
  assert.throws(() => b.lockSender(address), /Another writer/);
});
