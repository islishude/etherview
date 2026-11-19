import assert from "node:assert/strict";
import test from "node:test";

import { decodeFunctionData, encodeFunctionResult } from "viem";

import { contracts } from "../client.mjs";
import { defiDeployments } from "../defi.mjs";
import {
  setupDeFi,
  actionDeFi,
  readDeFiPages,
  readAt,
  concentratedAmount,
  defiAvailability,
} from "./defi.mjs";
import { links } from "./state.mjs";
const owner = "0x1111111111111111111111111111111111111111";
const target = "0x2222222222222222222222222222222222222222";
function engine(to) {
  const sent = [];
  return {
    sent,
    account: { address: owner },
    run: { state: { id: "test-run", to }, save() {} },
    async tx(s, id, tx) {
      if (!s.steps[id]) {
        s.steps[id] = tx;
        sent.push({ id, ...tx });
      }
    },
  };
}
test("DeFi batches target the selected wallet, resume without duplicate transactions and isolate finalization", async () => {
  for (const to of [undefined, target]) {
    const e = engine(to),
      s = { entities: {}, steps: {} };
    await setupDeFi(e, s);
    assert.equal(s.entities.recipient, to ?? owner);
    assert.equal(e.sent.length, 9);
    await setupDeFi(e, s);
    assert.equal(e.sent.length, 9);
    assert.ok(
      links({ endpoints: { app: "http://localhost" } }, { name: "defi", ...s }).some(
        (l) =>
          l.label === "uniswap-v2 factory" &&
          l.url.endsWith(contracts.PreviewDeFiV2Factory.address),
      ),
    );
    const initial = s.entities.batches.initial;
    await actionDeFi(e, s, "activity", "action-1-activity");
    assert.equal(e.sent.length, 18);
    assert.notEqual(initial, s.entities.batches["action-1-activity"]);
    await actionDeFi(e, s, "finalize", "action-2-finalize");
    assert.equal(e.sent.length, 20);
    await actionDeFi(e, s, "finalize", "action-2-finalize");
    assert.equal(e.sent.length, 20);
    for (const tx of e.sent) {
      const name = Object.keys(contracts).find((n) => contracts[n].address === tx.to);
      const decoded = decodeFunctionData({ abi: contracts[name].abi, data: tx.data });
      if (decoded.functionName === "mint")
        assert.equal(decoded.args[0].toLowerCase(), (to ?? owner).toLowerCase());
      else assert.ok(Object.values(s.entities.batches).includes(String(decoded.args[0])));
    }
    assert.match(
      links({ endpoints: { app: "http://localhost" } }, { name: "defi", ...s }).find(
        (l) => l.label === "DeFi assets",
      ).url,
      /\?tab=assets&asset=defi$/,
    );
  }
});
function page(positions, cursor) {
  return {
    status: 200,
    body: { data: { positions, deployments: [] }, meta: { next_cursor: cursor } },
  };
}
test("DeFi pagination binds owners, rejects duplicates and retries invalidated publications", async () => {
  const p = { id: "one", owner, deployment: "preview-lido" };
  let calls = 0;
  const all = await readDeFiPages("", owner, async (_, route) => {
    calls++;
    if (calls === 1) return page([p], "a+b");
    assert.match(route, /cursor=a%2Bb/);
    return page([{ ...p, id: "two" }]);
  });
  assert.equal(all.positions.length, 2);
  await assert.rejects(
    readDeFiPages("", owner, async () => page([p, p])),
    /Duplicate/,
  );
  await assert.rejects(
    readDeFiPages("", target, async () => page([p])),
    /cross-owner/,
  );
  assert.equal(
    await readDeFiPages("", owner, async () => ({
      status: 400,
      body: { error: { code: "invalid_cursor" } },
    })),
    null,
  );
  calls = 0;
  await assert.rejects(
    readDeFiPages("", owner, async () => page([{ ...p, id: String(calls++) }], "same")),
    /Repeated/,
  );
});
test("DeFi exact state reads require the publication hash and preserve large quantities", async () => {
  const hash = "0x" + "ab".repeat(32);
  const rpc = {
    async request({ method, params }) {
      assert.equal(method, "eth_call");
      assert.deepEqual(params[1], { blockHash: hash, requireCanonical: true });
      return encodeFunctionResult({
        abi: contracts.PreviewDeFiSupply.abi,
        functionName: "balanceOf",
        result: 10n ** 24n,
      });
    },
  };
  assert.equal(await readAt(rpc, "PreviewDeFiSupply", "balanceOf", [owner], hash), 10n ** 24n);
  assert.equal(concentratedAmount, 49996250312472658710n);
});
test("doctor distinguishes disabled or mismatched DeFi manifests", async () => {
  assert.match(await defiAvailability({}, "", async () => page([])), /manifest missing/);
  const deployments = defiDeployments.map((d) => ({
    protocol: d.protocol,
    deployment: "preview-" + d.protocol,
    state: "scanning",
  }));
  const rpc = {
    async getBlock() {
      return { hash: "0x" + "ab".repeat(32) };
    },
    async request() {
      return "0x00";
    },
  };
  assert.match(
    await defiAvailability(rpc, "", async () => ({ status: 200, body: { data: { deployments } } })),
    /code mismatch/,
  );
  deployments[0].state = "unavailable";
  assert.match(
    await defiAvailability({}, "", async () => ({ status: 200, body: { data: { deployments } } })),
    /not ready: uniswap-v2/,
  );
});

test("six published protocols reconcile cumulative balances, NFT ownership and finalization at their exact hashes", async () => {
  const { reconcileDeFi } = await import("./defi.mjs");
  const hash = "0x" + "ab".repeat(32);
  const e = engine(target),
    s = { entities: {}, steps: {} };
  await setupDeFi(e, s);
  for (const step of Object.values(s.steps)) step.receipt = { blockNumber: "10" };
  const id = s.entities.batches.initial;
  const quantities = {
    PreviewDeFiV2Pair: 2n * 10n ** 18n,
    PreviewDeFiSupply: 2n * 10n ** 20n,
    PreviewDeFiDebt: 2n * 10n ** 19n,
    PreviewDeFiCurveLP: 4n * 10n ** 18n,
    PreviewDeFiStETH: 10n ** 19n,
    PreviewDeFiWstETH: 8n * 10n ** 18n,
  };
  let finalized = false;
  const reads = [];
  e.rpc = {
    async request({ method, params }) {
      assert.equal(method, "eth_call");
      assert.deepEqual(params[1], { blockHash: hash, requireCanonical: true });
      const name = Object.keys(contracts).find(
        (n) => contracts[n].address.toLowerCase() === params[0].to.toLowerCase(),
      );
      const abi = contracts[name].abi;
      const { functionName } = decodeFunctionData({ abi, data: params[0].data });
      reads.push(functionName);
      const result =
        functionName === "balanceOf"
          ? quantities[name]
          : functionName === "ownerOf"
            ? target
            : finalized;
      return encodeFunctionResult({ abi, functionName, result });
    },
  };
  const asset = (name, n) => ({
    kind: name ? "erc20" : "native",
    ...(name ? { address: contracts[name].address } : {}),
    amount: String(n),
  });
  const a = (n) => asset("PreviewDeFiToken0", n),
    b = (n) => asset("PreviewDeFiToken1", n);
  const position = (name, protocol, extra) => ({
    id: name,
    protocol,
    deployment: "preview-" + protocol,
    owner: target,
    contract: contracts[name].address,
    state: "available",
    block_hash: hash,
    block_number: "10",
    assets: [],
    debts: [],
    claimable: [],
    unsettled_fees: [],
    ...extra,
  });
  const range = {
    lower_tick: 0,
    upper_tick: 1,
    current_tick: 0,
    sqrt_price_x96: String(1n << 96n),
    in_range: true,
  };
  const page = {
    deployments: defiDeployments.map((d) => ({
      protocol: d.protocol,
      deployment: "preview-" + d.protocol,
      state: "complete",
      block_number: "10",
      block_hash: hash,
    })),
    positions: [
      position("PreviewDeFiV2Pair", "uniswap-v2", {
        receipt_amount: String(quantities.PreviewDeFiV2Pair),
        assets: [a(4n * 10n ** 18n), b(6n * 10n ** 18n)],
      }),
      position("PreviewDeFiSupply", "aave-v3", {
        kind: "supply",
        collateral: true,
        receipt_amount: String(quantities.PreviewDeFiSupply),
        assets: [a(2n * 10n ** 20n)],
      }),
      position("PreviewDeFiDebt", "aave-v3", {
        kind: "borrow",
        receipt_amount: String(quantities.PreviewDeFiDebt),
        debts: [a(2n * 10n ** 19n)],
      }),
      position("PreviewDeFiCurveLP", "curve", {
        receipt_amount: String(quantities.PreviewDeFiCurveLP),
        assets: [a(16n * 10n ** 18n), b(20n * 10n ** 18n)],
      }),
      position("PreviewDeFiStETH", "lido", {
        receipt_amount: String(quantities.PreviewDeFiStETH),
        assets: [asset("PreviewDeFiStETH", 10n ** 19n)],
      }),
      position("PreviewDeFiWstETH", "lido", {
        receipt_amount: String(quantities.PreviewDeFiWstETH),
        assets: [asset("PreviewDeFiStETH", 88n * 10n ** 17n)],
      }),
      position("PreviewDeFiV3Positions", "uniswap-v3", {
        token_id: id,
        range,
        liquidity: String(10n ** 24n),
        assets: [a(concentratedAmount), b(0)],
        claimable: [a(10n ** 17n), b(2n * 10n ** 17n)],
        unsettled_fees: [a(0), b(0)],
      }),
      position("PreviewDeFiV4Positions", "uniswap-v4", {
        token_id: id,
        range,
        liquidity: String(10n ** 24n),
        assets: [a(concentratedAmount), b(0)],
        unsettled_fees: [a(0), b(0)],
      }),
      position("PreviewDeFiWithdrawals", "lido", {
        token_id: id,
        state: "pending",
        assets: [asset("PreviewDeFiStETH", 3n * 10n ** 18n)],
      }),
    ],
  };
  assert.equal((await reconcileDeFi(e, s, page)).length, 9);
  assert.ok(reads.includes("ownerOf"));
  for (const mutate of [
    (p) => (p.positions[0].assets[0].amount = "0"),
    (p) => (p.deployments[0].state = "partial"),
    (p) => (p.deployments[0].block_number = "9"),
    (p) => (p.positions[6].token_id = "1"),
    (p) => (p.positions[2].debts = []),
  ]) {
    const bad = structuredClone(page);
    mutate(bad);
    assert.equal(await reconcileDeFi(e, s, bad), null);
  }
  finalized = true;
  page.positions[8].state = "claimable";
  page.positions[8].assets = [];
  page.positions[8].claimable = [asset(null, 3n * 10n ** 18n)];
  assert.equal((await reconcileDeFi(e, s, page)).length, 9);
  const wrongHash = structuredClone(page);
  wrongHash.positions[0].block_hash = "0x" + "cd".repeat(32);
  await assert.rejects(reconcileDeFi(e, s, wrongHash));
});
