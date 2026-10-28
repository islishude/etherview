// Called by the owned Go PoS harness. No mining/time-travel RPC is permitted here.
import fs from "node:fs";
import { execFileSync } from "node:child_process";
import os from "node:os";
import path from "node:path";
import assert from "node:assert/strict";
import { getUserOperationHash } from "viem/account-abstraction";
import { encodeFunctionData, keccak256, toHex } from "viem";
import {
  connect,
  contracts,
  manifest,
  stringify,
  manageName,
  operationReceipt,
  prepareOperation,
  rpcOperation,
  readState,
} from "./client.mjs";
const [rpc, bundler, phase, report] = process.argv.slice(2);
const states = fs.mkdtempSync(path.join(os.tmpdir(), "etherview-contract-secrets-"));
const deadline = Date.now() + 240000;
let stage = "connect";
const transactions = [];
function cli(index, args) {
  const result = JSON.parse(
    execFileSync(
      process.execPath,
      [
        path.join(import.meta.dirname, "cli.mjs"),
        ...args,
        "--rpc",
        rpc,
        "--bundler",
        bundler,
        "--index",
        String(index),
      ],
      { encoding: "utf8", timeout: 150000, maxBuffer: 1 << 20 },
    ),
  );
  if (result?.transactionHash)
    transactions.push({ operation: args.slice(0, 2).join(" "), hash: result.transactionHash });
  return result;
}
function rpcError(code) {
  return (error) => {
    for (let i = 0; error && i < 10; i++, error = error.cause) {
      if (error.code === code) return true;
    }
    return false;
  };
}
async function eventually(read, predicate) {
  while (Date.now() < deadline) {
    const value = await read();
    if (predicate(value)) return value;
    await new Promise((r) => setTimeout(r, 1000));
  }
  throw new Error("Contract scenario timed out");
}
try {
  const ctx = await connect({ rpc, bundler });
  const entries = await ctx.bundler.request({ method: "eth_supportedEntryPoints" });
  assert.deepEqual(
    entries.map((x) => x.toLowerCase()),
    [contracts.EntryPoint.address.toLowerCase()],
  );
  await assert.rejects(() =>
    ctx.bundler.request({ method: "debug_bundler_clearState", params: [] }),
  );
  const allocation = JSON.parse(fs.readFileSync(new URL("allocation.json", import.meta.url)));
  for (const [address, account] of Object.entries(allocation)) {
    assert.equal(
      await ctx.rpc.getTransactionCount({ address, blockNumber: 0n }),
      Number(BigInt(account.nonce ?? 0)),
    );
    assert.equal(await ctx.rpc.getBalance({ address, blockNumber: 0n }), BigInt(account.balance));
    if (account.code)
      assert.equal(await ctx.rpc.getCode({ address, blockNumber: 0n }), account.code);
    for (const [slot, want] of Object.entries(account.storage ?? {}))
      assert.equal(await ctx.rpc.getStorageAt({ address, slot, blockNumber: 0n }), want);
  }
  stage = "ENS";
  const name = `alice-${phase}.test`;
  const state = path.join(states, "ens.json");
  assert.equal(cli(0, ["ens", "quote", name]).available, true);
  cli(0, ["ens", "commit", name, "--state", state]);
  const committed = await ctx.read("PreviewRegistrarController", "commitments", [
    readState(state).commitment,
  ]);
  await eventually(
    () => ctx.rpc.getBlock(),
    (block) => block.timestamp >= committed + 3n,
  );
  const registered = cli(0, ["ens", "register", "--state", state]);
  const before = await ctx.read("BaseRegistrarImplementation", "nameExpires", [
    BigInt(keccak256(toHex(name.split(".")[0]))),
  ]);
  cli(0, ["ens", "renew", name, "--duration", "2419200"]);
  assert.equal(
    await ctx.read("BaseRegistrarImplementation", "nameExpires", [
      BigInt(keccak256(toHex(name.split(".")[0]))),
    ]),
    before + 2419200n,
  );
  cli(0, ["ens", "resolve", name]);
  assert.equal(
    (
      await ctx.read("UniversalResolver", "reverse", [
        ctx.account.address,
        BigInt(manifest.coinType),
      ])
    )[0],
    name,
  );
  const nextIndex = phase === "split" ? 19 : 18;
  const next = await connect({ rpc, bundler, index: nextIndex });
  if (phase === "all")
    await ctx.receipt(
      await ctx.wallet.sendTransaction({ to: next.account.address, value: 2000000000000000000n }),
    );
  await assert.rejects(() => manageName(next, "set-address", name));
  cli(0, ["ens", "transfer", name, "--to", next.account.address]);
  cli(nextIndex, ["ens", "reclaim", name]);
  cli(nextIndex, ["ens", "set-address", name]);
  cli(nextIndex, ["ens", "set-primary", name]);
  await assert.rejects(() =>
    ctx.read("UniversalResolver", "reverse", [ctx.account.address, BigInt(manifest.coinType)]),
  );
  assert.equal(
    (
      await next.read("UniversalResolver", "reverse", [
        next.account.address,
        BigInt(manifest.coinType),
      ])
    )[0],
    name,
  );
  const salt = phase === "split" ? 100n : 101n;
  stage = "fund smart account";
  const funded = cli(0, ["aa", "fund", "--salt", String(salt)]);
  stage = "submit UserOperation";
  const file = path.join(states, "userop.json");
  const submitted = cli(0, ["aa", "send", "--state", file, "--salt", String(salt)]);
  const receipt = await eventually(
    () => cli(0, ["aa", "receipt", "--state", file]),
    (r) => r !== null,
  );
  assert.equal(receipt.success, true);
  const saved = readState(file);
  try {
    await ctx.bundler.request({
      method: "eth_sendUserOperation",
      params: [saved.operation, contracts.EntryPoint.address],
    });
  } catch {
    /* already included/rejected duplicate */
  }
  assert.equal((await operationReceipt(ctx, file)).userOpHash, submitted.userOperationHash);
  const prepared = await prepareOperation(ctx, { salt });
  const invalid = {
    ...prepared.operation,
    signature: await next.account.sign({ hash: prepared.hash }),
  };
  await assert.rejects(
    () =>
      ctx.bundler.request({
        method: "eth_sendUserOperation",
        params: [rpcOperation(invalid), contracts.EntryPoint.address],
      }),
    rpcError(-32507),
  );
  await assert.rejects(() => prepareOperation(ctx, { salt: 999n }), rpcError(-32500));
  // Execution may revert after successful validation. Estimate a valid call, then
  // sign an operation whose account execution deliberately calls a reverting target.
  const reverted = {
    ...prepared.operation,
    callData: encodeFunctionData({
      abi: [
        {
          type: "function",
          name: "execute",
          inputs: [{ type: "address" }, { type: "uint256" }, { type: "bytes" }],
          outputs: [],
          stateMutability: "nonpayable",
        },
      ],
      functionName: "execute",
      args: [contracts.PreviewRegistrarController.address, 0n, "0xdeadbeef"],
    }),
  };
  const failedHash = getUserOperationHash({
    chainId: 48815,
    entryPointAddress: contracts.EntryPoint.address,
    entryPointVersion: "0.9",
    userOperation: reverted,
  });
  reverted.signature = await ctx.account.sign({ hash: failedHash });
  await ctx.bundler.request({
    method: "eth_sendUserOperation",
    params: [rpcOperation(reverted), contracts.EntryPoint.address],
  });
  const failed = await eventually(
    () => ctx.bundler.request({ method: "eth_getUserOperationReceipt", params: [failedHash] }),
    (r) => r !== null,
  );
  assert.equal(failed.success, false);
  fs.writeFileSync(
    report,
    stringify({
      phase,
      genesis: ctx.genesis,
      entryPoint: contracts.EntryPoint.address,
      name,
      nameOwner: next.account.address,
      registrationHash: registered.transactionHash,
      smartAccount: funded.address,
      userOperationHash: submitted.userOperationHash,
      transactionHash: receipt.receipt.transactionHash,
      revertedUserOperationHash: failedHash,
      revertedTransactionHash: failed.receipt.transactionHash,
      allocationAccounts: Object.keys(allocation).length,
      allocationSha256: manifest.allocationSha256,
      sources: manifest.sources,
      transactions,
    }),
    { mode: 0o600 },
  );
} catch (error) {
  console.error(stage, stage === "ENS" ? (error.shortMessage ?? error.message) : error.message);
  process.exitCode = 1;
} finally {
  fs.rmSync(states, { recursive: true, force: true });
}
