import crypto from "node:crypto";
import fs from "node:fs";
import { pathToFileURL } from "node:url";
const hash = (bytes) => crypto.createHash("sha256").update(bytes).digest("hex");
export function loadInventory(directory, genesis) {
  const inventory = JSON.parse(fs.readFileSync(new URL("inventory.json", directory)));
  if (String(genesis.config.chainId) !== inventory.chainId) throw Error("Genesis chain mismatch");
  const accounts = new Map(
    Object.entries(genesis.alloc)
      .filter(([, a]) => a.code && a.code !== "0x")
      .map(([a, v]) => ["0x" + a.replace(/^0x/, "").toLowerCase(), v]),
  );
  if (accounts.size !== inventory.contracts.length)
    throw Error("Incomplete genesis verification inventory");
  const seen = new Set();
  const bundles = {};
  for (const [name, digest] of Object.entries(inventory.bundles)) {
    if (!/^[A-Za-z0-9-]+\.json$/.test(name)) throw Error("Invalid source bundle path");
    const bytes = fs.readFileSync(new URL(name, directory));
    if (hash(bytes) !== digest) throw Error(`Source bundle checksum mismatch: ${name}`);
    bundles[name] = JSON.parse(bytes);
  }
  for (const c of inventory.contracts) {
    if (seen.has(c.address) || !accounts.has(c.address) || !bundles[c.bundle])
      throw Error("Invalid genesis inventory entry");
    seen.add(c.address);
    if (hash(Buffer.from(accounts.get(c.address).code.slice(2), "hex")) !== c.codeSha256)
      throw Error(`Genesis code mismatch: ${c.name}`);
  }
  return { inventory, bundles };
}
export function submission(contract, bundles) {
  const bundle = bundles[contract.bundle];
  if (contract.entrypoint)
    return {
      language: "geas",
      compiler_version: "0.3.3",
      input_kind: "geas_sources",
      runtime_entrypoint: contract.entrypoint,
      sources: bundle.sources,
      contract_name_hint: contract.name,
    };
  if (bundle.input_kind) return { ...bundle, contract_name_hint: contract.name };
  return {
    language: bundle.compilation.language.toLowerCase(),
    compiler_version: bundle.compilation.compilerVersion,
    input_kind: "standard_json",
    contract_name_hint: contract.name,
    input: {
      language: bundle.compilation.language,
      sources: bundle.sources,
      settings: bundle.compilation.compilerSettings,
    },
  };
}
export function published(data, contract, chainId) {
  return (
    data?.kind === "verification_success" &&
    data.resolution === "exact_address" &&
    data.target?.chain_id === chainId &&
    data.target.address.toLowerCase() === contract.address &&
    data.target.code_hash === contract.codeHash &&
    ["full", "partial"].includes(data.runtime_match?.match_type)
  );
}
export async function verifyAll({
  inventory,
  bundles,
  request,
  rpc,
  sleep = (ms) => new Promise((r) => setTimeout(r, ms)),
  now = Date.now,
  log = console.log,
  timeout = 180000,
}) {
  const chain = await rpc("eth_chainId", []);
  if (BigInt(chain).toString() !== inventory.chainId) throw Error("RPC chain mismatch");
  const block = await rpc("eth_getBlockByNumber", ["0x0", false]);
  const deadline = now() + timeout;
  let imported;
  while (now() < deadline) {
    const response = await request("/api/v1/genesis/accounts?limit=100");
    if (response.status === 200) {
      imported = response.body;
      break;
    }
    if (response.status !== 503) throw Error(`Genesis import HTTP ${response.status}`);
    await sleep(1000);
  }
  if (!imported) throw Error("Genesis import timeout");
  const accounts = imported.data.filter((a) => a.type === "contract");
  if (
    imported.meta.chain_id !== inventory.chainId ||
    imported.meta.next_cursor ||
    accounts.length !== inventory.contracts.length
  )
    throw Error("Imported genesis inventory mismatch");
  for (const c of inventory.contracts) {
    const account = accounts.find((a) => a.address.toLowerCase() === c.address);
    if (account?.code_hash !== c.codeHash || account?.block_hash !== block.hash)
      throw Error(`Imported genesis identity mismatch: ${c.name}`);
  }
  let reused = 0;
  for (const c of inventory.contracts) {
    const path = `/api/v1/contracts/${c.address}/verification`;
    const prior = await request(path);
    if (prior.status === 200 && published(prior.body.data, c, inventory.chainId)) {
      reused++;
      log(`genesis-verify: ${c.name} already verified`);
      continue;
    }
    if (![200, 404].includes(prior.status)) throw Error(`${c.name}: artifact HTTP ${prior.status}`);
    const end = now() + timeout;
    let accepted;
    // A cold catalog or import can be temporarily unavailable before admission.
    while (now() < end) {
      accepted = await request(path, submission(c, bundles));
      if (accepted.status === 202) break;
      if (![429, 503].includes(accepted.status))
        throw Error(`${c.name}: submit HTTP ${accepted.status}`);
      await sleep(1000);
    }
    const id = accepted?.status === 202 && accepted.body.data?.id;
    if (!id || !/^[0-9a-f-]{36}$/.test(id))
      throw Error(`${c.name}: submission timeout or invalid job`);
    let complete = false;
    while (now() < end) {
      const result = await request(`/api/v1/verifier/jobs/${id}`);
      if (result.status !== 200) throw Error(`${c.name}: job HTTP ${result.status}`);
      const job = result.body.data;
      if (["failed", "cancelled"].includes(job.status))
        throw Error(
          `${c.name}: verification ${job.status} (${job.error_code ?? job.outcome?.kind ?? "unknown"})`,
        );
      if (job.status === "succeeded") {
        if (
          job.outcome?.kind !== "verification_success" ||
          !job.outcome.runtime_match ||
          job.outcome.creation_match
        )
          throw Error(
            `${c.name}: invalid genesis match evidence (${job.outcome?.kind ?? "missing"})`,
          );
        complete = true;
        break;
      }
      if (!["queued", "running"].includes(job.status)) throw Error(`${c.name}: invalid job status`);
      await sleep(1000);
    }
    if (!complete) throw Error(`${c.name}: verification timeout (job ${id})`);
    const artifact = await request(path);
    if (artifact.status !== 200 || !published(artifact.body.data, c, inventory.chainId))
      throw Error(`${c.name}: published artifact mismatch`);
    log(`genesis-verify: ${c.name} verified`);
  }
  log(
    `genesis-verify: ${inventory.contracts.length}/${inventory.contracts.length} contracts verified (${reused} reused)`,
  );
}
async function main() {
  // Exit the one-shot container even if a transport/worker stalls across contracts.
  const timer = setTimeout(
    () => {
      console.error("genesis-verify: overall deadline exceeded");
      process.exit(1);
    },
    25 * 60 * 1000,
  );
  timer.unref();
  const token = fs.readFileSync(0, "utf8").trim();
  if (!token || token.length > 1024) throw Error("Missing verification credential");
  const { inventory, bundles } = loadInventory(
    new URL("./", import.meta.url),
    JSON.parse(fs.readFileSync("/config/genesis.json")),
  );
  async function json(url, options) {
    const response = await fetch(url, {
      ...options,
      redirect: "error",
      signal: AbortSignal.timeout(15000),
    });
    const chunks = [];
    let size = 0;
    for await (const chunk of response.body) {
      size += chunk.length;
      if (size > 8 * 1024 * 1024) throw Error("Response too large");
      chunks.push(chunk);
    }
    return { status: response.status, body: JSON.parse(Buffer.concat(chunks).toString()) };
  }
  await verifyAll({
    inventory,
    bundles,
    request: (path, body) =>
      json(`http://api:8080${path}`, {
        method: body ? "POST" : "GET",
        headers: { "Content-Type": "application/json", "X-API-Key": token },
        ...(body ? { body: JSON.stringify(body) } : {}),
      }),
    rpc: async (method, params) => {
      const response = await json("http://geth:8545", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ jsonrpc: "2.0", id: 1, method, params }),
      });
      if (response.status !== 200 || response.body.error) throw Error("Genesis RPC unavailable");
      return response.body.result;
    },
  });
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    console.error(`genesis-verify: ${error.message}`);
    process.exitCode = 1;
  });
}
