import { encodeFunctionData, decodeFunctionResult, keccak256, toHex, zeroAddress } from "viem";

import { contracts } from "../client.mjs";
import { defiDeployments } from "../defi.mjs";

const lower = (s) => s.toLowerCase();
const address = (name) => contracts[name].address;
const fungible = [
  ["PreviewDeFiV2Pair", "uniswap-v2", 10n ** 18n],
  ["PreviewDeFiSupply", "aave-v3", 10n ** 20n],
  ["PreviewDeFiDebt", "aave-v3", 10n ** 19n],
  ["PreviewDeFiCurveLP", "curve", 2n * 10n ** 18n],
  ["PreviewDeFiStETH", "lido", 5n * 10n ** 18n],
  ["PreviewDeFiWstETH", "lido", 4n * 10n ** 18n],
];
const nfts = [
  ["PreviewDeFiV3Positions", "uniswap-v3"],
  ["PreviewDeFiV4Positions", "uniswap-v4"],
  ["PreviewDeFiWithdrawals", "lido"],
];
async function call(e, s, step, name, functionName, args) {
  return e.tx(s, step, {
    to: address(name),
    data: encodeFunctionData({ abi: contracts[name].abi, functionName, args }),
  });
}
// Every comparison uses the publication's canonical hash on the selected RPC endpoint.
export async function readAt(rpc, name, functionName, args, hash) {
  const abi = contracts[name].abi;
  const data = await rpc.request({
    method: "eth_call",
    params: [
      { to: address(name), data: encodeFunctionData({ abi, functionName, args }) },
      { blockHash: hash, requireCanonical: true },
    ],
  });
  return decodeFunctionResult({ abi, functionName, data });
}
export async function defiAvailability(rpc, app, request) {
  const result = await request(app, "/api/v1/addresses/" + zeroAddress + "/defi-positions");
  if (result.status !== 200) return "DeFi API unavailable";
  const deployments = result.body.data?.deployments ?? [];
  if (
    !defiDeployments.every((d) =>
      deployments.some(
        (s) =>
          s.protocol === d.protocol &&
          s.deployment === "preview-" + d.protocol &&
          s.state !== "not_configured",
      ),
    )
  )
    return "Preview DeFi manifest missing or mismatched; recreate with the matching network bundle";
  const failed = deployments.find(
    (d) => d.state === "unavailable" || d.state === "partial" || d.failure_code,
  );
  if (failed)
    return (
      "Preview DeFi deployment is not ready: " +
      failed.protocol +
      "; inspect its coverage and configured identities"
    );
  const genesis = await rpc.getBlock({ blockNumber: 0n });
  const names = new Set(
    defiDeployments.flatMap((d) => [
      ...Object.values(d.contracts),
      ...(d.receipts ?? []),
      ...(d.pools ?? []),
    ]),
  );
  for (const name of names) {
    const code = await rpc.request({
      method: "eth_getCode",
      params: [address(name), { blockHash: genesis.hash, requireCanonical: true }],
    });
    if (keccak256(code) !== contracts[name].codeHash)
      return "Preview DeFi genesis code mismatch: " + name;
  }
  return "available";
}
export async function setupDeFi(e, s) {
  s.entities.recipient ??= e.run.state.to ?? e.account.address;
  s.entities.demo = "Local demonstration contracts; not upstream protocol compatibility evidence";
  s.entities.projects = Object.fromEntries(
    defiDeployments.map((d) => [
      d.protocol,
      Object.fromEntries(Object.entries(d.contracts).map(([role, name]) => [role, address(name)])),
    ]),
  );
  e.run.save();
  await mintBatch(e, s, "initial");
}
async function mintBatch(e, s, batch) {
  s.entities.batches ??= {};
  s.entities.batches[batch] ??= BigInt(
    keccak256(toHex(e.run.state.id + ":defi:" + batch)),
  ).toString();
  e.run.save();
  for (const [name, , amount] of fungible)
    await call(e, s, batch + "-" + name, name, "mint", [s.entities.recipient, amount]);
  for (const [name] of nfts)
    await call(e, s, batch + "-" + name, name, "mint", [
      s.entities.recipient,
      BigInt(s.entities.batches[batch]),
    ]);
}
export async function actionDeFi(e, s, name, id) {
  if (name === "activity") return mintBatch(e, s, id);
  if (name !== "finalize") throw Error("Unsupported DeFi action");
  for (const [batch, tokenID] of Object.entries(s.entities.batches))
    await call(e, s, id + "-" + batch, "PreviewDeFiWithdrawals", "finalize", [BigInt(tokenID)]);
}
export async function readDeFiPages(app, owner, request) {
  const positions = [],
    seen = new Set(),
    cursors = new Set();
  let cursor, deployments;
  for (let page = 0; page < 200; page++) {
    const query = new URLSearchParams({ limit: "5", ...(cursor ? { cursor } : {}) });
    const r = await request(app, "/api/v1/addresses/" + owner + "/defi-positions?" + query);
    if (
      [409, 429, 503].includes(r.status) ||
      (r.status === 400 && r.body.error?.code === "invalid_cursor")
    )
      return null;
    if (r.status !== 200) throw Error("DeFi API check HTTP " + r.status);
    if (!Array.isArray(r.body.data?.positions) || !Array.isArray(r.body.data?.deployments))
      throw Error("Invalid DeFi page");
    deployments ??= r.body.data.deployments;
    if (JSON.stringify(deployments) !== JSON.stringify(r.body.data.deployments)) return null;
    for (const p of r.body.data.positions) {
      const key = p.deployment + ":" + p.id;
      if (seen.has(key) || lower(p.owner) !== lower(owner))
        throw Error("Duplicate or cross-owner DeFi position");
      seen.add(key);
      positions.push(p);
    }
    cursor = r.body.meta?.next_cursor;
    if (!cursor) return { positions, deployments };
    if (cursors.has(cursor)) throw Error("Repeated DeFi cursor");
    cursors.add(cursor);
  }
  throw Error("DeFi page limit exceeded");
}
const asset = (name, amount) => ({
  kind: name ? "erc20" : "native",
  ...(name ? { address: lower(address(name)) } : {}),
  amount: String(amount),
});
function sameAssets(actual, expected) {
  return (
    JSON.stringify(
      actual.map((a) => ({
        kind: a.kind,
        ...(a.address ? { address: lower(a.address) } : {}),
        amount: a.amount,
      })),
    ) === JSON.stringify(expected)
  );
}
// Independently derived from the fixed Preview price at tick 0 and range [0,1].
export const concentratedAmount =
  (10n ** 24n * (79232123823359799118286999568n - (1n << 96n))) / 79232123823359799118286999568n;
export async function reconcileDeFi(e, s, page) {
  const floor = Object.values(s.steps).reduce(
    (n, step) =>
      step.receipt && BigInt(step.receipt.blockNumber) > n ? BigInt(step.receipt.blockNumber) : n,
    0n,
  );
  if (
    !defiDeployments.every((d) =>
      page.deployments.some(
        (p) =>
          p.deployment === "preview-" + d.protocol &&
          p.protocol === d.protocol &&
          p.state === "complete" &&
          BigInt(p.block_number ?? 0) >= floor,
      ),
    )
  )
    return null;
  const expected = [];
  const find = (name, protocol, id = "") =>
    page.positions.find(
      (p) =>
        p.deployment === "preview-" + protocol &&
        p.protocol === protocol &&
        lower(p.contract) === lower(address(name)) &&
        (p.token_id ?? "") === id,
    );
  const compare = (p, want) => {
    if (
      !p ||
      p.state !== want.state ||
      !["assets", "debts", "claimable", "unsettled_fees"].every((k) =>
        sameAssets(p[k], want[k] ?? []),
      )
    )
      return false;
    const publication = page.deployments.find((d) => d.deployment === p.deployment);
    if (p.block_hash !== publication.block_hash || p.block_number !== publication.block_number)
      throw Error("DeFi publication identity mismatch");
    expected.push({
      id: p.id,
      protocol: p.protocol,
      contract: p.contract,
      token_id: p.token_id,
      block_hash: p.block_hash,
      block_number: p.block_number,
      ...want,
    });
    return true;
  };
  for (const [name, protocol, minted] of fungible) {
    const p = find(name, protocol);
    if (!p) return null;
    const balance = await readAt(e.rpc, name, "balanceOf", [s.entities.recipient], p.block_hash);
    const batches = Object.keys(s.entities.batches).length;
    if (balance < minted * BigInt(batches) || p.receipt_amount !== String(balance)) return null;
    let assets = [],
      debts = [];
    if (protocol === "uniswap-v2")
      assets = [asset("PreviewDeFiToken0", balance * 2n), asset("PreviewDeFiToken1", balance * 3n)];
    if (protocol === "curve")
      assets = [asset("PreviewDeFiToken0", balance * 4n), asset("PreviewDeFiToken1", balance * 5n)];
    if (name === "PreviewDeFiSupply") {
      assets = [asset("PreviewDeFiToken0", balance)];
      if (p.collateral !== true || p.kind !== "supply") return null;
    }
    if (name === "PreviewDeFiDebt") {
      debts = [asset("PreviewDeFiToken0", balance)];
      if (p.kind !== "borrow") return null;
    }
    if (protocol === "lido")
      assets = [
        asset("PreviewDeFiStETH", name === "PreviewDeFiWstETH" ? (balance * 11n) / 10n : balance),
      ];
    if (!compare(p, { state: "available", assets, debts, receipt_amount: String(balance) }))
      return null;
  }
  for (const [batch, id] of Object.entries(s.entities.batches)) {
    for (const [name, protocol] of nfts) {
      const p = find(name, protocol, id);
      if (!p) return null;
      if (
        lower(await readAt(e.rpc, name, "ownerOf", [BigInt(id)], p.block_hash)) !==
        lower(s.entities.recipient)
      )
        return null;
      if (protocol === "lido") {
        const finalized = await readAt(e.rpc, name, "finalized", [BigInt(id)], p.block_hash);
        if (
          Object.keys(s.steps).some((k) => k.endsWith("-" + batch) && k.includes("-finalize-")) &&
          !finalized
        )
          return null;
        if (
          !compare(p, {
            state: finalized ? "claimable" : "pending",
            assets: finalized ? [] : [asset("PreviewDeFiStETH", 3n * 10n ** 18n)],
            claimable: finalized ? [asset(null, 3n * 10n ** 18n)] : [],
          })
        )
          return null;
      } else {
        if (
          p.liquidity !== String(10n ** 24n) ||
          p.range?.lower_tick !== 0 ||
          p.range?.upper_tick !== 1 ||
          p.range?.current_tick !== 0 ||
          p.range?.sqrt_price_x96 !== String(1n << 96n) ||
          p.range?.in_range !== true
        )
          return null;
        if (
          !compare(p, {
            state: "available",
            assets: [asset("PreviewDeFiToken0", concentratedAmount), asset("PreviewDeFiToken1", 0)],
            claimable:
              protocol === "uniswap-v3"
                ? [
                    asset("PreviewDeFiToken0", 10n ** 17n),
                    asset("PreviewDeFiToken1", 2n * 10n ** 17n),
                  ]
                : [],
            unsettled_fees: [asset("PreviewDeFiToken0", 0), asset("PreviewDeFiToken1", 0)],
          })
        )
          return null;
      }
    }
  }
  return expected;
}
export async function checkDeFi(e, s, request) {
  if (s.name !== "defi") return;
  s.activeStep = "check:DeFi publications";
  e.run.save();
  const expected = await e.wait(
    async () => {
      const page = await readDeFiPages(e.run.state.endpoints.app, s.entities.recipient, request);
      const expected = page && (await reconcileDeFi(e, s, page));
      if (!expected) await new Promise((resolve) => setTimeout(resolve, 2000));
      return expected;
    },
    Boolean,
    "six Preview DeFi publications and exact-block amounts",
  );
  e.check(
    s,
    "DeFi exact-block accounting",
    "/api/v1/addresses/" + s.entities.recipient + "/defi-positions",
    { positions: expected },
  );
  s.checks.find((c) => c.label === "DeFi exact-block accounting").status = "passed";
  e.run.save();
}
