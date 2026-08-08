import hardhatVerify from "@nomicfoundation/hardhat-verify";
import { configVariable, defineConfig } from "hardhat/config";
import { fileURLToPath } from "node:url";

const rpcURL = process.env.ETHERVIEW_HARDHAT3_RPC_URL ?? "http://127.0.0.1:8545";
const explorerURL = process.env.ETHERVIEW_HARDHAT3_EXPLORER_URL ?? "http://127.0.0.1:8080";
const apiURL = process.env.ETHERVIEW_HARDHAT3_API_URL ?? `${explorerURL}/v2/api`;
const solcPath = fileURLToPath(
  new URL("./node_modules/solc/soljson.js", import.meta.url),
);
const chainID = Number(process.env.ETHERVIEW_HARDHAT3_CHAIN_ID ?? "1");
if (!Number.isSafeInteger(chainID) || chainID <= 0) {
  throw new Error("ETHERVIEW_HARDHAT3_CHAIN_ID must be a positive safe integer");
}

export default defineConfig({
  plugins: [hardhatVerify],
  // Hardhat 3 moves build-info files from cache into artifacts. Keep both
  // paths on one disposable volume so the move remains atomic in containers.
  paths: {
    artifacts: "./build/artifacts",
    cache: "./build/cache",
  },
  solidity: {
    npmFilesToBuild: [
      "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol",
      "@openzeppelin/contracts/proxy/beacon/BeaconProxy.sol",
      "@openzeppelin/contracts/proxy/beacon/UpgradeableBeacon.sol",
      "@openzeppelin/contracts/proxy/transparent/ProxyAdmin.sol",
      "@openzeppelin/contracts/proxy/transparent/TransparentUpgradeableProxy.sol",
    ],
    profiles: {
      default: {
        version: "0.8.30",
        path: solcPath,
        settings: {
          evmVersion: "shanghai",
          optimizer: { enabled: false, runs: 200 },
        },
      },
      production: {
        version: "0.8.30",
        path: solcPath,
        settings: {
          evmVersion: "shanghai",
          optimizer: { enabled: false, runs: 200 },
        },
      },
    },
  },
  networks: {
    etherview: {
      type: "http",
      chainType: "l1",
      chainId: chainID,
      url: rpcURL,
      accounts: "remote",
    },
  },
  verify: {
    etherscan: {
      apiKey: configVariable("ETHERVIEW_API_KEY"),
    },
  },
  chainDescriptors: {
    [chainID]: {
      name: "Etherview E2E",
      chainType: "l1",
      blockExplorers: {
        etherscan: {
          name: "Etherview",
          url: explorerURL,
          apiUrl: apiURL,
        },
      },
    },
  },
});
