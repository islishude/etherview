import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

import { Etherscan } from "./node_modules/@nomicfoundation/hardhat-verify/dist/src/internal/etherscan.js";

const expectedVersions = {
  hardhat: "3.11.1",
  "@nomicfoundation/hardhat-verify": "3.0.21",
};

for (const [dependency, expected] of Object.entries(expectedVersions)) {
  const manifest = JSON.parse(
    await readFile(new URL(`./node_modules/${dependency}/package.json`, import.meta.url)),
  );
  assert.equal(manifest.version, expected, `${dependency} version drifted`);
}

const requiredEnvironment = [
  "ETHERVIEW_HARDHAT3_BASE_URL",
  "ETHERVIEW_HARDHAT3_API_KEY",
  "ETHERVIEW_HARDHAT3_CHAIN_ID",
  "ETHERVIEW_HARDHAT3_ADDRESS",
  "ETHERVIEW_HARDHAT3_GUID",
];
for (const name of requiredEnvironment) {
  assert.ok(process.env[name], `${name} is required`);
}

const baseUrl = process.env.ETHERVIEW_HARDHAT3_BASE_URL;
const address = process.env.ETHERVIEW_HARDHAT3_ADDRESS;
const expectedGuid = process.env.ETHERVIEW_HARDHAT3_GUID;
const contractName = "contracts/Hardhat3Compatibility.sol:Hardhat3Compatibility";
const compilerInput = {
  language: "Solidity",
  sources: {
    "contracts/Hardhat3Compatibility.sol": {
      content:
        "// SPDX-License-Identifier: MIT\npragma solidity ^0.8.30;\ncontract Hardhat3Compatibility {}",
    },
  },
  settings: {
    optimizer: { enabled: false },
    outputSelection: {
      "*": {
        "*": ["abi", "evm.bytecode", "evm.deployedBytecode", "metadata"],
      },
    },
  },
};

const provider = new Etherscan({
  chainId: Number(process.env.ETHERVIEW_HARDHAT3_CHAIN_ID),
  name: "Etherview",
  url: baseUrl,
  apiUrl: `${baseUrl}/v2/api`,
  apiKey: process.env.ETHERVIEW_HARDHAT3_API_KEY,
});

assert.equal(await provider.isVerified(address), false);

const guid = await provider.verify({
  contractAddress: address,
  compilerInput,
  contractName,
  compilerVersion: "v0.8.30+commit.73712a01",
  constructorArguments: "1234",
});
assert.equal(guid, expectedGuid);

assert.deepEqual(await provider.pollVerificationStatus(guid, address, contractName), {
  success: true,
  message: "Pass - Verified",
  isRetryable: true,
});
