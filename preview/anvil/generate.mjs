// Deterministic, reviewed payment-only genesis; never run during startup.
import fs from "node:fs";
import { standardPaymentContracts } from "../contracts/x402-standard.mjs";
import { createRequire } from "node:module";
const require = createRequire(new URL("../contracts/package.json", import.meta.url));
const { mnemonicToAccount } = require("viem/accounts");
const manifest = JSON.parse(
  fs.readFileSync(new URL("../contracts/manifest.json", import.meta.url)),
);
const allocation = JSON.parse(
  fs.readFileSync(new URL("../contracts/allocation.json", import.meta.url)),
);
const alloc = {};
for (const name of ["LocalBillingToken", "LocalBillingToken18"]) {
  const address = manifest.contracts[name].address.toLowerCase();
  alloc[address.slice(2)] = allocation[address];
}
for (const contract of standardPaymentContracts(31337)) {
  alloc[contract.address.slice(2).toLowerCase()] = { balance: "0x0", nonce: "0x1", code: contract.code, storage: {} };
}
for (let addressIndex = 0; addressIndex < 20; addressIndex++) {
  const account = mnemonicToAccount("test test test test test test test test test test test junk", {
    addressIndex,
  });
  alloc[account.address.slice(2).toLowerCase()] = { balance: "0x3635c9adc5dea00000" };
}
const genesis = {
  config: {
    chainId: 31337,
    homesteadBlock: 0,
    eip150Block: 0,
    eip155Block: 0,
    eip158Block: 0,
    byzantiumBlock: 0,
    constantinopleBlock: 0,
    petersburgBlock: 0,
    istanbulBlock: 0,
    berlinBlock: 0,
    londonBlock: 0,
    shanghaiTime: 0,
    cancunTime: 0,
    pragueTime: 0,
  },
  gasLimit: "0x1c9c380",
  difficulty: "0x0",
  timestamp: "0x0",
  alloc,
};
const output = JSON.stringify(genesis, null, 2) + "\n";
const file = new URL("genesis.json", import.meta.url);
if (process.argv.includes("--check")) {
  if (fs.readFileSync(file, "utf8") !== output) throw Error("Anvil genesis is stale");
} else fs.writeFileSync(file, output);

// Export Anvil's native snapshot offline because --init cannot be combined with
// its persistence flags. Runtime only copies this snapshot into an empty volume.
const { spawn, execFileSync } = await import("node:child_process");
const net = await import("node:net");
const { gunzipSync } = await import("node:zlib");
const { fileURLToPath } = await import("node:url");
const binary = process.env.ANVIL || "anvil";
if (!execFileSync(binary, ["--version"], { encoding: "utf8" }).includes("1.8.7"))
  throw Error("Anvil version mismatch");
const listener = net.createServer();
await new Promise((resolve) => listener.listen(0, "127.0.0.1", resolve));
const port = listener.address().port;
await new Promise((resolve) => listener.close(resolve));
const child = spawn(
  binary,
  [
    "--init",
    fileURLToPath(file),
    "--accounts",
    "0",
    "--chain-id",
    "31337",
    "--hardfork",
    "prague",
    "--host",
    "127.0.0.1",
    "--port",
    String(port),
    "--silent",
  ],
  { stdio: "ignore" },
);
let spawnError;
child.on("error", (error) => {
  spawnError = error;
});
const rpc = async (method) => {
  const response = await fetch(`http://127.0.0.1:${port}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method, params: [] }),
    signal: AbortSignal.timeout(1000),
  });
  const body = await response.json();
  if (body.error) throw Error("Anvil export RPC failed");
  return body.result;
};
try {
  let ready = false;
  for (let attempt = 0; attempt < 100; attempt++) {
    if (spawnError) throw spawnError;
    if (child.exitCode !== null) throw Error("Anvil export exited");
    try {
      ready = (await rpc("eth_chainId")) === "0x7a69";
    } catch {}
    if (ready) break;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  if (!ready) throw Error("Anvil export startup timeout");
  let bytes = Buffer.from((await rpc("anvil_dumpState")).slice(2), "hex");
  if (bytes[0] === 31 && bytes[1] === 139) bytes = gunzipSync(bytes);
  const snapshot = JSON.stringify(JSON.parse(bytes.toString()), null, 2) + "\n";
  const target = new URL("state.json", import.meta.url);
  if (process.argv.includes("--check")) {
    if (fs.readFileSync(target, "utf8") !== snapshot) throw Error("Anvil snapshot is stale");
  } else fs.writeFileSync(target, snapshot);
} finally {
  child.kill("SIGTERM");
  await new Promise((resolve) => {
    if (child.exitCode !== null || child.signalCode) resolve();
    else child.once("exit", resolve);
  });
}
