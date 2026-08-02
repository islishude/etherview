import assert from "node:assert/strict";
import { readFile, writeFile } from "node:fs/promises";
import { Interface } from "ethers";
import { createTransactionSender } from "./transaction-sender.mjs";

const IMPLEMENTATION_SLOT =
  "0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc";
const rpcURL = process.env.ETHERVIEW_HARDHAT3_RPC_URL;
const deploymentFile = process.env.ETHERVIEW_HARDHAT3_DEPLOYMENT_FILE;
const outputFile = process.env.ETHERVIEW_HARDHAT3_UPGRADE_FILE;
const manualMine = process.env.ETHERVIEW_HARDHAT3_MANUAL_MINE !== "false";
assert.ok(rpcURL, "ETHERVIEW_HARDHAT3_RPC_URL is required");
assert.ok(deploymentFile, "ETHERVIEW_HARDHAT3_DEPLOYMENT_FILE is required");
assert.ok(outputFile, "ETHERVIEW_HARDHAT3_UPGRADE_FILE is required");

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

async function artifact(path) {
  return JSON.parse(await readFile(new URL(path, import.meta.url)));
}

async function minePendingTransaction() {
  const latest = await rpc("eth_getBlockByNumber", ["latest", false]);
  const timestamp = BigInt(latest.timestamp) + 1n;
  await rpc("evm_setNextBlockTimestamp", [`0x${timestamp.toString(16)}`]);
  await rpc("evm_mine");
}

async function waitReceipt(hash) {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    const receipt = await rpc("eth_getTransactionReceipt", [hash]);
    if (receipt !== null) return receipt;
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(`receipt timed out: ${hash}`);
}

function receiptSummary(receipt) {
  return {
    hash: receipt.transactionHash,
    blockNumber: receipt.blockNumber,
    transactionIndex: receipt.transactionIndex,
    status: receipt.status,
    to: receipt.to,
  };
}

async function send(to, data, expectedStatus = "0x1") {
  const hash = await transactionSender({ to, data });
  if (manualMine) await minePendingTransaction();
  const receipt = await waitReceipt(hash);
  assert.equal(receipt.status, expectedStatus, `transaction status: ${hash}`);
  return receiptSummary(receipt);
}

function addressFromStorageWord(value) {
  assert.match(value, /^0x[0-9a-fA-F]{64}$/);
  return `0x${value.slice(-40)}`;
}

const transactionSender = createTransactionSender({
  rpc,
  from: deployment.owner,
  privateKey: process.env.ETHERVIEW_HARDHAT3_PRIVATE_KEY,
});

const implementationArtifact = await artifact(
  "./build/artifacts/contracts/Implementation.sol/Implementation.json",
);
const implementationV2Artifact = await artifact(
  "./build/artifacts/contracts/Implementation.sol/ImplementationV2.json",
);
const implementationInterface = new Interface(implementationArtifact.abi);
const implementationV2Interface = new Interface(implementationV2Artifact.abi);
const proxyAdminInterface = new Interface([
  "function upgradeAndCall(address proxy,address implementation,bytes data) payable",
]);
const beaconInterface = new Interface([
  "function upgradeTo(address newImplementation)",
]);

// A wrong ERC-1822 UUID must fail and leave the UUPS proxy unchanged.
const failedBadUUIDUpgrade = await send(
  deployment.uups.proxy,
  implementationInterface.encodeFunctionData("upgradeToAndCall", [
    deployment.implementations.badUUID,
    "0x",
  ]),
  "0x0",
);
const afterBadUUID = addressFromStorageWord(
  await rpc("eth_getStorageAt", [deployment.uups.proxy, IMPLEMENTATION_SLOT, "latest"]),
);
assert.equal(
  afterBadUUID.toLowerCase(),
  deployment.implementations.v1.toLowerCase(),
  "failed UUID upgrade changed implementation",
);

const transparentReinitializer = implementationV2Interface.encodeFunctionData(
  "reinitializeV2",
  ["transparent-v2"],
);
const transparentUpgrade = await send(
  deployment.transparent.admin,
  proxyAdminInterface.encodeFunctionData("upgradeAndCall", [
    deployment.transparent.proxy,
    deployment.implementations.v2,
    transparentReinitializer,
  ]),
);

const uupsReinitializer = implementationV2Interface.encodeFunctionData(
  "reinitializeV2",
  ["uups-v2"],
);
const uupsUpgrade = await send(
  deployment.uups.proxy,
  implementationInterface.encodeFunctionData("upgradeToAndCall", [
    deployment.implementations.v2,
    uupsReinitializer,
  ]),
);

const beaconUpgrade = await send(
  deployment.beacon.beacon,
  beaconInterface.encodeFunctionData("upgradeTo", [
    deployment.implementations.v2,
  ]),
);
const beaconReinitializations = [];
for (const [index, proxy] of deployment.beacon.proxies.entries()) {
  beaconReinitializations.push(
    await send(
      proxy,
      implementationV2Interface.encodeFunctionData("reinitializeV2", [
        `beacon-${index + 1}-v2`,
      ]),
    ),
  );
}

const currentUUPSImplementation = addressFromStorageWord(
  await rpc("eth_getStorageAt", [deployment.uups.proxy, IMPLEMENTATION_SLOT, "latest"]),
);
assert.equal(
  currentUUPSImplementation.toLowerCase(),
  deployment.implementations.v2.toLowerCase(),
  "UUPS implementation after upgrade",
);

await writeFile(
  outputFile,
  `${JSON.stringify({
    schemaVersion: 1,
    openzeppelinVersion: deployment.openzeppelinVersion,
    failedBadUUIDUpgrade,
    transparentUpgrade,
    uupsUpgrade,
    beaconUpgrade,
    beaconReinitializations,
    currentUUPSImplementation,
    impact: {
      transparent: [deployment.transparent.proxy],
      uups: [deployment.uups.proxy],
      beacon: deployment.beacon.proxies,
    },
  })}\n`,
  { mode: 0o644 },
);
