import assert from "node:assert/strict";
import test from "node:test";

import { decodeFunctionData } from "viem";

import { contracts } from "../client.mjs";
import { catalog } from "./catalog.mjs";
import {
  checkRequests,
  requestContracts,
  requestInput,
  requestSamples,
  requestsAvailability,
  setupRequests,
} from "./requests.mjs";
import { links } from "./state.mjs";
const owner = "0x" + "ab".repeat(20);
const hash = "0x" + "11".repeat(32);
const nextHash = "0x" + "22".repeat(32);

test("five request inputs encode protocol lengths, big-endian queue amounts and bounded payments", () => {
  const samples = requestSamples("one", owner);
  assert.deepEqual(requestSamples("one", owner), samples);
  assert.notDeepEqual(requestSamples("two", owner), samples);
  assert.deepEqual(catalog.requests.actions, []);
  const inputs = samples.map((s) => requestInput(s, 3n));
  const deposit = decodeFunctionData({ abi: contracts.PreviewDeposit.abi, data: inputs[0].data });
  assert.equal(deposit.functionName, "deposit");
  assert.deepEqual(deposit.args, [
    samples[0].pubkey,
    samples[0].withdrawal_credentials,
    samples[0].signature,
  ]);
  assert.deepEqual(
    inputs.slice(1).map((i) => (i.data.length - 2) / 2),
    [56, 96, 184, 48],
  );
  assert.equal(inputs[2].data, samples[2].source_pubkey + samples[2].target_pubkey.slice(2));
  assert.equal(inputs[3].data.slice(162, 178), "000000003b9aca00");
  assert.deepEqual(
    inputs.map((i) => i.value),
    [10n ** 18n, 3n, 3n, 10n ** 18n + 3n, 3n],
  );
  assert.throws(() => requestInput(samples[1], 10n ** 18n), /budget/);
  assert.throws(() => requestInput({ request_type: "0xff" }), /Unknown/);
});

test("setup resumes fixed identities and skips fee quotes for journaled transactions", async () => {
  const s = { entities: {}, steps: {} };
  let quotes = 0,
    sends = 0;
  const e = {
    run: { state: { id: "one" }, save() {} },
    account: { address: owner },
    rpc: {
      async getBlock() {
        return { hash };
      },
      async request(input) {
        assert.deepEqual(input.params[1], { blockHash: hash, requireCanonical: true });
        quotes++;
        return "0x" + "0".repeat(63) + "1";
      },
    },
    async tx(s, id, input) {
      if (s.steps[id]) {
        assert.deepEqual(input, {});
        return;
      }
      sends++;
      s.steps[id] = { hash, input };
    },
  };
  await setupRequests(e, s);
  const original = structuredClone(s);
  await setupRequests(e, s);
  assert.deepEqual(s, original);
  assert.equal(sends, 5);
  assert.equal(quotes, 4);
});

test("requests scan paginated complete publications and report actual queue-drain blocks without sending", async () => {
  const samples = requestSamples("one", owner);
  const s = {
    name: "requests",
    entities: { requests: samples },
    steps: Object.fromEntries(
      samples.map((sample) => [
        "request-" + sample.request_type,
        { receipt: { blockHash: hash, blockNumber: "10" } },
      ]),
    ),
    checks: [],
  };
  let calls = 0;
  let invalidated = false;
  const e = {
    run: { state: { endpoints: { app: "https://app.invalid" } }, save() {} },
    rpc: {
      async getBlock({ blockNumber } = {}) {
        return {
          hash: blockNumber === 10n ? hash : nextHash,
          parentHash: hash,
          number: blockNumber ?? 11n,
        };
      },
    },
    async wait(read, predicate) {
      for (let n = 0; n < 10; n++) if (predicate(await read())) return;
      throw Error("timeout");
    },
    check(s, label, route, expected) {
      s.checks.push({ label, route, expected });
    },
  };
  await checkRequests(e, s, async (_, route) => {
    calls++;
    const url = new URL(route, "https://app.invalid");
    const sample = samples.find((s) => s.request_type === url.searchParams.get("request_type"));
    if (sample.request_type === "0x00" && url.searchParams.has("cursor") && !invalidated) {
      invalidated = true;
      return { status: 400, body: { error: { code: "invalid_cursor" } } };
    }
    if (route.includes(hash))
      return { status: 200, body: { data: { state: "complete", items: [] } } };
    return {
      status: 200,
      body: {
        data: { state: "complete", items: url.searchParams.has("cursor") ? [sample] : [] },
        meta: url.searchParams.has("cursor") ? {} : { next_cursor: "page2" },
      },
    };
  });
  assert.equal(calls, 17);
  assert.deepEqual(Object.values(s.entities.requestBlocks), Array(5).fill(nextHash));
  assert.ok(s.checks.every((c) => c.status === "passed"));
  const result = links({ endpoints: { app: "https://app.invalid" } }, s).filter((l) =>
    l.label.startsWith("Request "),
  );
  assert.equal(result.length, 5);
  assert.ok(result.every((l) => l.url.endsWith(nextHash + "?tab=requests")));
  e.rpc.getBlock = async () => ({ hash: nextHash, number: 11n });
  await assert.rejects(
    checkRequests(e, s, () => assert.fail("reorg must stop before API")),
    /chain changed/,
  );
});

test("doctor rejects missing deposit genesis before a demo can send funds", async () => {
  assert.match(await requestsAvailability({}, {}), /consensus/);
  const rpc = {
    async getBlock() {
      return { hash };
    },
    async request(input) {
      assert.equal(input.params[0], requestContracts["0x00"]);
      assert.deepEqual(input.params[1], { blockHash: hash, requireCanonical: true });
      return "0x";
    },
  };
  assert.match(await requestsAvailability(rpc, { consensus: true }), /explicitly reinitialize/);
});
