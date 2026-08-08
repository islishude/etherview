import assert from "node:assert/strict";
import { readFile, writeFile } from "node:fs/promises";

const ADMIN_SLOT =
  "0xb53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103";
const BEACON_SLOT =
  "0xa3f0ad74e5423aebfd80d3ef4346578335a9a72aeaee59ff6cb3582b35133d50";

const rpcURL = process.env.ETHERVIEW_HARDHAT3_RPC_URL;
const deploymentFile = process.env.ETHERVIEW_HARDHAT3_DEPLOYMENT_FILE;
const outputFile = process.env.ETHERVIEW_HARDHAT3_SLOT_TAMPER_FILE;
assert.ok(rpcURL, "ETHERVIEW_HARDHAT3_RPC_URL is required");
assert.ok(deploymentFile, "ETHERVIEW_HARDHAT3_DEPLOYMENT_FILE is required");
assert.ok(outputFile, "ETHERVIEW_HARDHAT3_SLOT_TAMPER_FILE is required");

const deployment = JSON.parse(await readFile(deploymentFile, "utf8"));
assert.equal(deployment.openzeppelinVersion, "5.6.1");

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

function storageWord(address) {
  assert.match(address, /^0x[0-9a-fA-F]{40}$/);
  return `0x${address.slice(2).padStart(64, "0")}`;
}

function addressFromStorageWord(value) {
  assert.match(value, /^0x[0-9a-fA-F]{64}$/);
  return `0x${value.slice(-40)}`;
}

const poisonedAdmin = deployment.implementations.badUUID;
const poisonedBeacon = deployment.implementationV2;
assert.notEqual(
  poisonedAdmin.toLowerCase(),
  deployment.transparent.admin.toLowerCase(),
  "admin poison must differ from the runtime immutable admin",
);
assert.notEqual(
  poisonedBeacon.toLowerCase(),
  deployment.beacon.beacon.toLowerCase(),
  "beacon poison must differ from the runtime immutable beacon",
);

await rpc("anvil_setStorageAt", [
  deployment.transparent.proxy,
  ADMIN_SLOT,
  storageWord(poisonedAdmin),
]);
for (const proxy of deployment.beacon.proxies) {
  await rpc("anvil_setStorageAt", [proxy, BEACON_SLOT, storageWord(poisonedBeacon)]);
}

// Commit the state override as the parent state of a new block so every
// subsequent EIP-1898 probe observes one immutable block identity.
const latest = await rpc("eth_getBlockByNumber", ["latest", false]);
await rpc("evm_setNextBlockTimestamp", [`0x${(BigInt(latest.timestamp) + 1n).toString(16)}`]);
await rpc("evm_mine");

// Exercise the production discovery path at the tampered state identity.
// These deliberately unknown calls may revert in the implementation, but the
// mined transactions are real proxy candidates and force proxy@2 to re-read
// the immutable authority plus compatibility slots at their exact block hash.
const pendingCandidates = [];
for (const to of [deployment.transparent.proxy, ...deployment.beacon.proxies]) {
  const hash = await rpc("eth_sendTransaction", [{
    from: deployment.owner,
    to,
    data: "0xffffffff",
    value: "0x0",
    gas: "0x30d40",
  }]);
  pendingCandidates.push({ hash, to });
}
const candidateParent = await rpc("eth_getBlockByNumber", ["latest", false]);
await rpc("evm_setNextBlockTimestamp", [
  `0x${(BigInt(candidateParent.timestamp) + 1n).toString(16)}`,
]);
await rpc("evm_mine");

const candidateTransactions = [];
for (const { hash, to } of pendingCandidates) {
  const receipt = await rpc("eth_getTransactionReceipt", [hash]);
  assert.ok(receipt, `tamper candidate receipt ${hash}`);
  candidateTransactions.push({
    number: receipt.blockNumber,
    hash,
    status: receipt.status,
    to,
  });
}

const observedAdmin = addressFromStorageWord(
  await rpc("eth_getStorageAt", [deployment.transparent.proxy, ADMIN_SLOT, "latest"]),
);
assert.equal(observedAdmin.toLowerCase(), poisonedAdmin.toLowerCase());

const observedBeacons = [];
for (const proxy of deployment.beacon.proxies) {
  const observed = addressFromStorageWord(
    await rpc("eth_getStorageAt", [proxy, BEACON_SLOT, "latest"]),
  );
  assert.equal(observed.toLowerCase(), poisonedBeacon.toLowerCase());
  observedBeacons.push(observed);
}

await writeFile(
  outputFile,
  `${JSON.stringify({
    schemaVersion: 1,
    openzeppelinVersion: deployment.openzeppelinVersion,
    transparent: {
      proxy: deployment.transparent.proxy,
      runtimeImmutableAdmin: deployment.transparent.admin,
      compatibilitySlotAdmin: observedAdmin,
    },
    beacon: {
      proxies: deployment.beacon.proxies,
      runtimeImmutableBeacon: deployment.beacon.beacon,
      compatibilitySlotBeacons: observedBeacons,
    },
    candidateTransactions,
  })}\n`,
  { mode: 0o644 },
);
