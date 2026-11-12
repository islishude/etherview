import { runCompose } from "../../.github/scripts/compose.mjs";
const execution = composeEndpoint("execution", 8545),
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
        const header = await executionRPC("eth_getBlockByNumber", [
          `0x${block.toString(16)}`,
          false,
        ]);
        if (!header?.blockAccessListHash || header.slotNumber == null) {
          throw new Error("execution header does not advertise BAL");
        }
        const bal = await executionRPC("eth_getBlockAccessListRaw", [header.hash]);
        if (typeof bal !== "string" || !/^0x[0-9a-f]+$/i.test(bal)) {
          throw new Error("execution BAL body unavailable");
        }
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

function composeEndpoint(service, port) {
  const output = runCompose(["-f", "compose.yaml", "port", service, String(port)]);
  return `http://${output.trim().replace(/^0\.0\.0\.0:/, "127.0.0.1:")}`;
}

async function executionRPC(method, params) {
  const response = await fetch(execution, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method, params }),
    signal: AbortSignal.timeout(3000),
  });
  const payload = await response.json();
  if (!response.ok || payload.error) throw new Error(`execution ${method} unavailable`);
  return payload.result;
}
