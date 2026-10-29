import { spawn } from "node:child_process";
// Public development identity, scoped only to the isolated Preview Bundler.
import fs from "node:fs";
import { pathToFileURL } from "node:url";

export async function verifyGenesis(rpc, manifest, allocation) {
  if ((await rpc("eth_chainId", [])) !== "0xbeaf") throw new Error("Preview chain mismatch");
  const address = manifest.contracts.EntryPoint.address.toLowerCase();
  if ((await rpc("eth_getCode", [address, "0x0"])) !== allocation[address].code)
    throw new Error("Preview genesis mismatch");
}

async function main() {
  const manifest = JSON.parse(fs.readFileSync("/config/contracts.json"));
  const allocation = JSON.parse(fs.readFileSync("/config/allocation.json"));
  async function rpc(method, params) {
    const response = await fetch("http://geth:8545", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method, params }),
      signal: AbortSignal.timeout(10000),
    });
    const body = await response.json();
    if (!response.ok || body.error) throw new Error("Preview RPC unavailable");
    return body.result;
  }
  let ready = false;
  for (let attempt = 0; attempt < 90; attempt++) {
    try {
      await verifyGenesis(rpc, manifest, allocation);
      ready = true;
      break;
    } catch {
      await new Promise((r) => setTimeout(r, 1000));
    }
  }
  if (!ready) throw new Error("Preview Bundler genesis verification failed");
  const child = spawn(
    "/usr/local/bin/rundler",
    [
      "node",
      "--node_http",
      "http://geth:8545",
      "--chain_spec",
      "/config/rundler.toml",
      "--enabled_entry_points",
      "v0.9",
      "--rpc.host",
      "0.0.0.0",
      "--rpc.port",
      "4337",
      "--rpc.api",
      "eth",
    ],
    {
      stdio: "inherit",
      // Do not inherit ambient UNSAFE, ENABLE_UNSAFE_FALLBACK or signer overrides.
      env: {
        PATH: process.env.PATH,
        RUST_LOG: "warn",
        // Mnemonic index 9, prefunded exclusively in the local Preview genesis.
        SIGNER_PRIVATE_KEYS: "2a871d0798f97d79848a013d4936a73bf4cc922c825d33c1cf7073dff6d409c6",
      },
    },
  );
  for (const signal of ["SIGTERM", "SIGINT"]) process.on(signal, () => child.kill(signal));
  child.on("error", () => process.exit(1));
  child.on("exit", (code) => process.exit(code ?? 1));
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) await main();
