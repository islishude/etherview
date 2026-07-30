import assert from "node:assert/strict";
import { readFile, writeFile } from "node:fs/promises";

const rpcURL = process.env.ETHERVIEW_HARDHAT3_RPC_URL;
const outputFile = process.env.ETHERVIEW_HARDHAT3_DEPLOYMENT_FILE;
const manualMine = process.env.ETHERVIEW_HARDHAT3_MANUAL_MINE !== "false";
assert.ok(rpcURL, "ETHERVIEW_HARDHAT3_RPC_URL is required");
assert.ok(outputFile, "ETHERVIEW_HARDHAT3_DEPLOYMENT_FILE is required");

let requestID = 0;
async function rpc(method, params = []) {
  const response = await fetch(rpcURL, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ jsonrpc: "2.0", id: ++requestID, method, params }),
  });
  assert.equal(response.status, 200, `${method} HTTP status`);
  const body = await response.json();
  assert.equal(body.error, undefined, `${method}: ${JSON.stringify(body.error)}`);
  return body.result;
}

async function artifact(path) {
  return JSON.parse(await readFile(new URL(path, import.meta.url)));
}

async function waitReceipt(hash) {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    const receipt = await rpc("eth_getTransactionReceipt", [hash]);
    if (receipt !== null) return receipt;
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(`receipt timed out: ${hash}`);
}

async function minePendingTransaction() {
  const latest = await rpc("eth_getBlockByNumber", ["latest", false]);
  const timestamp = BigInt(latest.timestamp) + 1n;
  await rpc("evm_setNextBlockTimestamp", [`0x${timestamp.toString(16)}`]);
  await rpc("evm_mine");
}

function word(value) {
  return value.replace(/^0x/, "").padStart(64, "0");
}

async function deploy(from, bytecode, constructorArguments = "") {
  const hash = await rpc("eth_sendTransaction", [
    { from, data: `${bytecode}${constructorArguments}`, gas: "0x7a1200" },
  ]);
  if (manualMine) {
    await minePendingTransaction();
  }
  const receipt = await waitReceipt(hash);
  assert.equal(receipt.status, "0x1", `deployment failed: ${hash}`);
  assert.ok(receipt.contractAddress, `deployment has no address: ${hash}`);
  return receipt.contractAddress;
}

const accounts = await rpc("eth_accounts");
assert.ok(accounts.length > 0, "Anvil returned no unlocked account");
const implementationArtifact = await artifact(
  "./build/artifacts/contracts/Implementation.sol/Implementation.json",
);
const proxyArtifact = await artifact(
  "./build/artifacts/contracts/TestERC1967Proxy.sol/TestERC1967Proxy.json",
);
const implementation = await deploy(accounts[0], implementationArtifact.bytecode);
const implementationV2 = await deploy(accounts[0], implementationArtifact.bytecode);
const proxyArguments = `${word(implementation)}${word("0x40")}${word("0x0")}`;
const proxy = await deploy(accounts[0], proxyArtifact.bytecode, proxyArguments);

await writeFile(
  outputFile,
  `${JSON.stringify({ implementation, implementationV2, proxy, proxyArguments })}\n`,
  // This contains only public chain facts and is retained for CI diagnostics.
  // The container runs as root, so the host artifact uploader needs world-read.
  { mode: 0o644 },
);
