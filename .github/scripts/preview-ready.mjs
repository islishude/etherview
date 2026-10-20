import { composeEndpoint } from "./preview-compose.mjs";
const execution = composeEndpoint("geth", 8545),
  beacon = composeEndpoint("lighthouse", 5052);
const deadline = Date.now() + 180_000;
let first;
let last = "waiting for genesis";
while (Date.now() < deadline) {
  try {
    const response = await fetch(execution, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "eth_blockNumber", params: [] }),
      signal: AbortSignal.timeout(3000),
    });
    const block = BigInt((await response.json()).result);
    const headResponse = await fetch(`${beacon}/eth/v1/beacon/headers/head`, {
      signal: AbortSignal.timeout(3000),
    });
    if (!headResponse.ok) throw new Error(`Beacon HTTP ${headResponse.status}`);
    const head = await headResponse.json();
    if (block > 0n && BigInt(head.data.header.message.slot) > 0n) {
      if (first === undefined) first = block;
      if (block > first) {
        console.log(
          `preview-ready: execution block ${block}, Beacon slot ${head.data.header.message.slot}`,
        );
        process.exit(0);
      }
    }
    last = `execution ${block}, Beacon slot ${head.data.header.message.slot}`;
  } catch (error) {
    last = error.message;
  }
  await new Promise((resolve) => setTimeout(resolve, 1500));
}
throw new Error(`Preview did not produce successive real PoS blocks: ${last}`);
