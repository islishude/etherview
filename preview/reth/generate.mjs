// Deterministic, reviewed payment-only genesis; never run during startup.
import fs from "node:fs";
import { createRequire } from "node:module";

import { standardPaymentContracts } from "../contracts/x402-standard.mjs";
const require = createRequire(new URL("../contracts/package.json", import.meta.url));
const { mnemonicToAccount } = require("viem/accounts");
const manifest = JSON.parse(
  fs.readFileSync(new URL("../contracts/manifest.json", import.meta.url)),
);
const allocation = JSON.parse(
  fs.readFileSync(new URL("../contracts/allocation.json", import.meta.url)),
);
// Share the reviewed Amsterdam system allocations, without importing the
// explorer's application contracts or funding allocations.
const base = JSON.parse(fs.readFileSync(new URL("../genesis.json", import.meta.url)));
const systemAddresses = [
  "00000961ef480eb55e80d19ad83579a64c007002", // withdrawal queue
  "0000bbddc7ce488642fb579f8b00f3a590007251", // consolidation queue
  "0000bff46984e3725691fa540a8c7589300d8282", // builder deposits
  "000064d678505ad48f8ccb093bc65613800e8282", // builder exits
  "000f3df6d732807ef1319fb7b8bb8522d0beac02", // beacon roots
  "0000f90827f1c53a10cb7a02335b175320002935", // history storage
  "4e59b44847b379578588920ca78fbf26c0b4956c", // deterministic deployment factory
];
const sourceAlloc = Object.fromEntries(
  Object.entries(base.alloc).map(([key, value]) => [key.toLowerCase(), value]),
);
const alloc = Object.fromEntries(
  systemAddresses.map((address) => {
    if (!sourceAlloc[address]?.code) throw Error(`missing system allocation: ${address}`);
    return [address, sourceAlloc[address]];
  }),
);
for (const name of ["LocalBillingToken", "LocalBillingToken18"]) {
  const address = manifest.contracts[name].address.toLowerCase();
  alloc[address.slice(2)] = allocation[address];
}
for (const contract of standardPaymentContracts(31337)) {
  alloc[contract.address.slice(2).toLowerCase()] = {
    balance: "0x0",
    nonce: "0x1",
    code: contract.code,
    storage: {},
  };
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
    ...Object.fromEntries(Object.entries(base.config).filter(([key]) => key !== "chainId")),
  },
  gasLimit: "0x1c9c380",
  difficulty: "0x0",
  timestamp: "0x0",
  alloc,
};
const output = JSON.stringify(genesis, null, 2) + "\n";
const file = new URL("genesis.json", import.meta.url);
if (process.argv.includes("--check")) {
  if (fs.readFileSync(file, "utf8") !== output) throw Error("Reth genesis is stale");
} else fs.writeFileSync(file, output);
