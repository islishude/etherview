import assert from "node:assert/strict";
import { readFile, readdir, writeFile } from "node:fs/promises";
import { AbiCoder, Interface, keccak256, solidityPacked, toUtf8Bytes } from "ethers";
import { createTransactionSender } from "./transaction-sender.mjs";

const OPENZEPPELIN_VERSION = "5.6.1";
const ADMIN_SLOT =
  "0xb53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103";
const IMPLEMENTATION_SLOT =
  "0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc";

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

async function projectArtifact(path) {
  return JSON.parse(await readFile(new URL(path, import.meta.url)));
}

async function compilerArtifact(sourceSuffix, contractName) {
  const directory = new URL("./build/artifacts/build-info/", import.meta.url);
  for (const name of await readdir(directory)) {
    if (!name.endsWith(".output.json")) continue;
    const build = JSON.parse(await readFile(new URL(name, directory)));
    for (const [sourceName, contracts] of Object.entries(
      build.output?.contracts ?? {},
    )) {
      if (!sourceName.endsWith(sourceSuffix) || !(contractName in contracts)) {
        continue;
      }
      const contract = contracts[contractName];
      const bytecode = contract.evm?.bytecode?.object;
      assert.ok(bytecode, `${sourceName}:${contractName} bytecode is missing`);
      return { abi: contract.abi, bytecode: `0x${bytecode}` };
    }
  }
  throw new Error(`compiler artifact not found: ${sourceSuffix}:${contractName}`);
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

function receiptSummary(receipt) {
  return {
    hash: receipt.transactionHash,
    blockNumber: receipt.blockNumber,
    transactionIndex: receipt.transactionIndex,
    status: receipt.status,
    to: receipt.to,
    contractAddress: receipt.contractAddress,
  };
}

async function sendTransaction(from, transaction, expectedStatus = "0x1") {
  const hash = await transactionSender(transaction);
  if (manualMine) await minePendingTransaction();
  const receipt = await waitReceipt(hash);
  assert.equal(receipt.status, expectedStatus, `transaction failed: ${hash}`);
  return receiptSummary(receipt);
}

const coder = AbiCoder.defaultAbiCoder();

function constructorData(contract, values) {
  const constructor = contract.abi.find((item) => item.type === "constructor");
  const inputs = constructor?.inputs ?? [];
  assert.equal(inputs.length, values.length, "constructor argument count");
  const argumentsData = coder.encode(
    inputs.map((input) => input.type),
    values,
  );
  return {
    arguments: values,
    encoded: argumentsData,
    data: `${contract.bytecode}${argumentsData.slice(2)}`,
  };
}

async function deploy(from, contract, values = []) {
  const encoded = constructorData(contract, values);
  const transaction = await sendTransaction(from, { data: encoded.data });
  assert.ok(transaction.contractAddress, "deployment has no contract address");
  return {
    address: transaction.contractAddress,
    constructorArguments: encoded.arguments,
    encodedConstructorArguments: encoded.encoded,
    transaction,
  };
}

async function call(to, iface, functionName, values = []) {
  const data = iface.encodeFunctionData(functionName, values);
  const result = await rpc("eth_call", [{ to, data }, "latest"]);
  return iface.decodeFunctionResult(functionName, result);
}

function addressFromStorageWord(value) {
  assert.match(value, /^0x[0-9a-fA-F]{64}$/);
  return `0x${value.slice(-40)}`;
}

const accounts = await rpc("eth_accounts");
assert.ok(accounts.length > 0, "Anvil returned no unlocked account");
const owner = accounts[0];
const transactionSender = createTransactionSender({
  rpc,
  from: owner,
  privateKey: process.env.ETHERVIEW_HARDHAT3_PRIVATE_KEY,
});

for (const packageName of ["contracts", "contracts-upgradeable"]) {
  const metadata = JSON.parse(
    await readFile(
      new URL(`./node_modules/@openzeppelin/${packageName}/package.json`, import.meta.url),
    ),
  );
  assert.equal(
    metadata.version,
    OPENZEPPELIN_VERSION,
    `@openzeppelin/${packageName} version`,
  );
}

const implementationArtifact = await projectArtifact(
  "./build/artifacts/contracts/Implementation.sol/Implementation.json",
);
const implementationV2Artifact = await projectArtifact(
  "./build/artifacts/contracts/Implementation.sol/ImplementationV2.json",
);
const badUUIDArtifact = await projectArtifact(
  "./build/artifacts/contracts/Implementation.sol/BadUUIDImplementation.json",
);
const cloneFactoryArtifact = await projectArtifact(
  "./build/artifacts/contracts/CloneFactory.sol/CloneFactory.json",
);
const myAccountArtifact = await projectArtifact(
  "./build/artifacts/contracts/MyAccount.sol/MyAccount.json",
);
const myAccountFactoryArtifact = await projectArtifact(
  "./build/artifacts/contracts/MyAccountFactory.sol/MyAccountFactory.json",
);
const valueFacetArtifact = await projectArtifact(
  "./build/artifacts/contracts/DiamondFixture.sol/ValueFacet.json",
);
const mathFacetArtifact = await projectArtifact(
  "./build/artifacts/contracts/DiamondFixture.sol/MathFacet.json",
);
const diamondArtifact = await projectArtifact(
  "./build/artifacts/contracts/DiamondFixture.sol/FixtureDiamond.json",
);
const erc1967ProxyArtifact = await compilerArtifact(
  "/proxy/ERC1967/ERC1967Proxy.sol",
  "ERC1967Proxy",
);
const transparentProxyArtifact = await compilerArtifact(
  "/proxy/transparent/TransparentUpgradeableProxy.sol",
  "TransparentUpgradeableProxy",
);
const beaconArtifact = await compilerArtifact(
  "/proxy/beacon/UpgradeableBeacon.sol",
  "UpgradeableBeacon",
);
const beaconProxyArtifact = await compilerArtifact(
  "/proxy/beacon/BeaconProxy.sol",
  "BeaconProxy",
);

const implementationDeployment = await deploy(owner, implementationArtifact);
const implementationV2Deployment = await deploy(owner, implementationV2Artifact);
const badUUIDDeployment = await deploy(owner, badUUIDArtifact);

const implementationInterface = new Interface(implementationArtifact.abi);
const initializeData = (initialValue) =>
  implementationInterface.encodeFunctionData("initialize", [owner, initialValue]);

const transparentDeployment = await deploy(owner, transparentProxyArtifact, [
  implementationDeployment.address,
  owner,
  initializeData(11n),
]);
const transparentAdmin = addressFromStorageWord(
  await rpc("eth_getStorageAt", [transparentDeployment.address, ADMIN_SLOT, "latest"]),
);

const uupsDeployment = await deploy(owner, erc1967ProxyArtifact, [
  implementationDeployment.address,
  initializeData(21n),
]);

const beaconDeployment = await deploy(owner, beaconArtifact, [
  implementationDeployment.address,
  owner,
]);
const beaconProxyADeployment = await deploy(owner, beaconProxyArtifact, [
  beaconDeployment.address,
  initializeData(31n),
]);
const beaconProxyBDeployment = await deploy(owner, beaconProxyArtifact, [
  beaconDeployment.address,
  initializeData(32n),
]);

const cloneFactoryDeployment = await deploy(owner, cloneFactoryArtifact);
const cloneFactoryInterface = new Interface(cloneFactoryArtifact.abi);
const standardCloneTransaction = await sendTransaction(owner, {
  to: cloneFactoryDeployment.address,
  data: cloneFactoryInterface.encodeFunctionData("deployStandard", [
    implementationDeployment.address,
  ]),
});
const [standardClone] = await call(
  cloneFactoryDeployment.address,
  cloneFactoryInterface,
  "standardClone",
);
const standardCloneInitialization = await sendTransaction(owner, {
  to: standardClone,
  data: initializeData(41n),
});

const immutableArgs = coder.encode(
  ["address", "uint256", "bytes32"],
  [owner, 561n, keccak256(toUtf8Bytes("openzeppelin-5.6.1"))],
);
const immutableCloneTransaction = await sendTransaction(owner, {
  to: cloneFactoryDeployment.address,
  data: cloneFactoryInterface.encodeFunctionData("deployWithImmutableArgs", [
    implementationDeployment.address,
    immutableArgs,
  ]),
});
const [immutableArgsClone] = await call(
  cloneFactoryDeployment.address,
  cloneFactoryInterface,
  "immutableArgsClone",
);
const [observedImmutableArgs] = await call(
  cloneFactoryDeployment.address,
  cloneFactoryInterface,
  "immutableArgs",
  [immutableArgsClone],
);
assert.equal(observedImmutableArgs, immutableArgs, "immutable clone args");
const immutableCloneInitialization = await sendTransaction(owner, {
  to: immutableArgsClone,
  data: initializeData(42n),
});

const cwiaArtifactDeployment = await deploy(owner, myAccountArtifact);
const cwiaFactoryDeployment = await deploy(owner, myAccountFactoryArtifact);
const cwiaFactoryInterface = new Interface(myAccountFactoryArtifact.abi);
const myAccountInterface = new Interface(myAccountArtifact.abi);
const [cwiaImplementation] = await call(
  cwiaFactoryDeployment.address,
  cwiaFactoryInterface,
  "implementation",
);
const cwiaNumber = (1n << 200n) + 42n;
const cwiaData = toUtf8Bytes("hello,world");
const cwiaDataHex = solidityPacked(["bytes"], [cwiaData]);
const cwiaCreateTransaction = await sendTransaction(owner, {
  to: cwiaFactoryDeployment.address,
  data: cwiaFactoryInterface.encodeFunctionData("create", [owner, cwiaNumber, cwiaData]),
});
const [cwiaAccount] = await call(
  cwiaFactoryDeployment.address,
  cwiaFactoryInterface,
  "account",
);
const [observedCWIAOwner] = await call(cwiaAccount, myAccountInterface, "owner");
const [observedCWIANumber] = await call(cwiaAccount, myAccountInterface, "number");
const [observedCWIAData] = await call(cwiaAccount, myAccountInterface, "data");
const [infoOwner, infoNumber] = await call(cwiaAccount, myAccountInterface, "getInfo");
assert.equal(observedCWIAOwner.toLowerCase(), owner.toLowerCase(), "CWIA owner");
assert.equal(observedCWIANumber, cwiaNumber, "CWIA number");
assert.equal(observedCWIAData, cwiaDataHex, "CWIA data");
assert.equal(infoOwner.toLowerCase(), owner.toLowerCase(), "CWIA info owner");
assert.equal(infoNumber, cwiaNumber, "CWIA info number");
const cwiaSetStoredTransaction = await sendTransaction(owner, {
  to: cwiaAccount,
  data: myAccountInterface.encodeFunctionData("setStored", [777n]),
});
const [cwiaStored] = await call(cwiaAccount, myAccountInterface, "stored");
assert.equal(cwiaStored, 777n, "CWIA delegated storage write");
const cwiaImmutableArgs = solidityPacked(
  ["address", "uint256", "uint16", "bytes"],
  [owner, cwiaNumber, cwiaData.length, cwiaData],
);

const valueFacetDeployment = await deploy(owner, valueFacetArtifact);
const mathFacetDeployment = await deploy(owner, mathFacetArtifact);
const diamondDeployment = await deploy(owner, diamondArtifact, [
  valueFacetDeployment.address,
  mathFacetDeployment.address,
]);
const diamondInterface = new Interface([
  ...diamondArtifact.abi,
  ...valueFacetArtifact.abi,
  ...mathFacetArtifact.abi,
]);
const diamondSetValue = await sendTransaction(owner, {
  to: diamondDeployment.address,
  data: diamondInterface.encodeFunctionData("setValue", [2535n]),
});
const [diamondValue] = await call(
  diamondDeployment.address,
  diamondInterface,
  "value",
);
const [diamondDouble] = await call(
  diamondDeployment.address,
  diamondInterface,
  "double",
  [21n],
);
assert.equal(diamondValue, 2535n, "Diamond delegated storage value");
assert.equal(diamondDouble, 42n, "Diamond delegated pure call");

const currentUUPSImplementation = addressFromStorageWord(
  await rpc("eth_getStorageAt", [uupsDeployment.address, IMPLEMENTATION_SLOT, "latest"]),
);
assert.equal(
  currentUUPSImplementation.toLowerCase(),
  implementationDeployment.address.toLowerCase(),
  "UUPS implementation slot",
);

const output = {
  schemaVersion: 4,
  openzeppelinVersion: OPENZEPPELIN_VERSION,
  owner,
  // The primary fields keep the production Go harness concise while now
  // referring to a real UUPS implementation behind a real ERC1967Proxy.
  implementation: implementationDeployment.address,
  implementationV2: implementationV2Deployment.address,
  proxy: uupsDeployment.address,
  implementations: {
    v1: implementationDeployment.address,
    v2: implementationV2Deployment.address,
    badUUID: badUUIDDeployment.address,
  },
  transparent: {
    proxy: transparentDeployment.address,
    admin: transparentAdmin,
    implementation: implementationDeployment.address,
  },
  uups: {
    proxy: uupsDeployment.address,
    implementation: currentUUPSImplementation,
  },
  beacon: {
    beacon: beaconDeployment.address,
    implementation: implementationDeployment.address,
    proxies: [beaconProxyADeployment.address, beaconProxyBDeployment.address],
  },
  clones: {
    factory: cloneFactoryDeployment.address,
    standard: standardClone,
    immutableArgs: immutableArgsClone,
    immutableArgsData: immutableArgs,
  },
  cwia: {
    factory: cwiaFactoryDeployment.address,
    artifactSource: cwiaArtifactDeployment.address,
    implementation: cwiaImplementation,
    account: cwiaAccount,
    owner,
    number: cwiaNumber.toString(),
    data: cwiaDataHex,
    immutableArgs: cwiaImmutableArgs,
    stored: cwiaStored.toString(),
  },
  diamond: {
    address: diamondDeployment.address,
    facets: [valueFacetDeployment.address, mathFacetDeployment.address],
    value: diamondValue.toString(),
    doubled: diamondDouble.toString(),
  },
  transactions: {
    implementationV1: implementationDeployment.transaction,
    implementationV2: implementationV2Deployment.transaction,
    badUUID: badUUIDDeployment.transaction,
    transparent: transparentDeployment.transaction,
    uups: uupsDeployment.transaction,
    beacon: beaconDeployment.transaction,
    beaconProxyA: beaconProxyADeployment.transaction,
    beaconProxyB: beaconProxyBDeployment.transaction,
    cloneFactory: cloneFactoryDeployment.transaction,
    standardClone: standardCloneTransaction,
    standardCloneInitialization,
    immutableArgsClone: immutableCloneTransaction,
    immutableArgsCloneInitialization: immutableCloneInitialization,
    cwiaFactory: cwiaFactoryDeployment.transaction,
    cwiaArtifactSource: cwiaArtifactDeployment.transaction,
    cwiaCreate: cwiaCreateTransaction,
    cwiaSetStored: cwiaSetStoredTransaction,
    diamondValueFacet: valueFacetDeployment.transaction,
    diamondMathFacet: mathFacetDeployment.transaction,
    diamond: diamondDeployment.transaction,
    diamondSetValue,
  },
  constructorArguments: {
    transparent: transparentDeployment.encodedConstructorArguments,
    uups: uupsDeployment.encodedConstructorArguments,
    beacon: beaconDeployment.encodedConstructorArguments,
    beaconProxyA: beaconProxyADeployment.encodedConstructorArguments,
    beaconProxyB: beaconProxyBDeployment.encodedConstructorArguments,
    diamond: diamondDeployment.encodedConstructorArguments,
  },
  initializationData: {
    transparent: initializeData(11n),
    uups: initializeData(21n),
    beaconProxyA: initializeData(31n),
    beaconProxyB: initializeData(32n),
    standardClone: initializeData(41n),
    immutableArgsClone: initializeData(42n),
  },
};

await writeFile(
  outputFile,
  `${JSON.stringify(output)}\n`,
  // These are public chain facts retained for CI diagnostics. The container
  // runs as root, so the host artifact uploader needs world-read permission.
  { mode: 0o644 },
);
