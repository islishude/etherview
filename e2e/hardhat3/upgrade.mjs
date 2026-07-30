import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

const rpcURL = process.env.ETHERVIEW_HARDHAT3_RPC_URL;
const deploymentFile = process.env.ETHERVIEW_HARDHAT3_DEPLOYMENT_FILE;
assert.ok(rpcURL, "ETHERVIEW_HARDHAT3_RPC_URL is required");
assert.ok(deploymentFile, "ETHERVIEW_HARDHAT3_DEPLOYMENT_FILE is required");

const deployment = JSON.parse(await readFile(deploymentFile, "utf8"));
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

function word(value) {
  return value.replace(/^0x/, "").padStart(64, "0");
}

async function minePendingTransaction() {
  const latest = await rpc("eth_getBlockByNumber", ["latest", false]);
  const timestamp = BigInt(latest.timestamp) + 1n;
  await rpc("evm_setNextBlockTimestamp", [`0x${timestamp.toString(16)}`]);
  await rpc("evm_mine");
}

const accounts = await rpc("eth_accounts");
const hash = await rpc("eth_sendTransaction", [{
  from: accounts[0],
  to: deployment.proxy,
  data: `0x3659cfe6${word(deployment.implementationV2)}`,
  gas: "0x7a120",
}]);
await minePendingTransaction();
const receipt = await rpc("eth_getTransactionReceipt", [hash]);
assert.equal(receipt?.status, "0x1", `proxy upgrade failed: ${hash}`);
