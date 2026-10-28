// Public development identities, scoped to the isolated Preview Bundler process.
import fs from "node:fs";
import { spawn } from "node:child_process";
import { mnemonicToAccount } from "viem/accounts";
import { createPublicClient, http, keccak256 } from "viem";
const manifest = JSON.parse(fs.readFileSync("/config/contracts.json"));
const mnemonic = "test test test test test test test test test test test junk";
const rpc = createPublicClient({
  transport: http("http://geth:8545", { timeout: 10000, retryCount: 0 }),
});
let ready = false;
for (let attempt = 0; attempt < 90; attempt++) {
  try {
    if ((await rpc.getChainId()) !== 48815) throw new Error("Preview chain mismatch");
    for (const name of ["EntryPoint", "PimlicoSimulations", "EntryPointSimulations09"]) {
      const item = manifest.contracts[name];
      const code = await rpc.getCode({ address: item.address, blockNumber: 0n });
      if (!code || keccak256(code) !== item.codeHash) throw new Error("Preview genesis mismatch");
    }
    ready = true;
    break;
  } catch {
    await new Promise((r) => setTimeout(r, 1000));
  }
}
if (!ready) throw new Error("Preview Bundler genesis verification failed");
const key = (name) =>
  mnemonicToAccount(mnemonic, { addressIndex: manifest.devAccounts[name].index }).getHdKey()
    .privateKey;
const args = [
  "src/esm/cli/alto.js",
  "--rpc-url",
  "http://geth:8545",
  "--entrypoints",
  manifest.contracts.EntryPoint.address,
  "--executor-private-keys",
  `0x${Buffer.from(key("bundlerExecutor")).toString("hex")}`,
  "--utility-private-key",
  `0x${Buffer.from(key("bundlerUtility")).toString("hex")}`,
  "--min-balance",
  "0",
  "--port",
  "4337",
  "--safe-mode",
  "true",
  "--dangerous-skip-user-operation-validation",
  "false",
  "--deploy-simulations-contract",
  "false",
  "--entrypoint-simulation-contract-v9",
  manifest.contracts.EntryPointSimulations09.address,
  "--pimlico-simulation-contract",
  manifest.contracts.PimlicoSimulations.address,
  "--glamsterdam-enabled",
  "true",
  "--rpc-methods",
  "eth_chainId,eth_supportedEntryPoints,eth_estimateUserOperationGas,eth_sendUserOperation,eth_getUserOperationByHash,eth_getUserOperationReceipt",
  "--log-level",
  "warn",
];
const child = spawn(process.execPath, args, { stdio: "inherit" });
for (const signal of ["SIGTERM", "SIGINT"]) process.on(signal, () => child.kill(signal));
child.on("exit", (code) => process.exit(code ?? 1));
