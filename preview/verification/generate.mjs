import crypto from "node:crypto";
// Explicit offline generation. Startup consumes checked-in inline compiler inputs.
import fs from "node:fs";
import { createRequire } from "node:module";

import { resolveENSImport } from "../contracts/ens-imports.mjs";

const require = createRequire(new URL("../contracts/package.json", import.meta.url));
const solc = require("solc");
const { namehash, keccak256 } = require("viem");
const read = (p) => JSON.parse(fs.readFileSync(new URL(p, import.meta.url)));
const manifest = read("../contracts/manifest.json");
const ens = read("../contracts/upstream/ens-sources.json");
const controller = ens.sources["contracts/ethregistrar/ETHRegistrarController.sol"].content
  .replace("contract ETHRegistrarController is", "contract PreviewRegistrarController is")
  .replaceAll(
    "0x93cdeb708b7545dc668eb9280176169d1c33cfd8ed6f04690a0bcc88a93fc4ae",
    namehash("test"),
  )
  .replaceAll('".eth"', '".test"');
const oracle = `// SPDX-License-Identifier: MIT
pragma solidity 0.8.30;
import {IPriceOracle} from "contracts/ethregistrar/IPriceOracle.sol";
contract PreviewPriceOracle is IPriceOracle {
 function price(string calldata, uint256, uint256 duration) external pure returns (Price memory) { return Price(duration * 1 gwei, 0); }
 function supportsInterface(bytes4 id) external pure returns(bool) { return id == type(IPriceOracle).interfaceId || id == 0x01ffc9a7; }
}`;
const token = fs.readFileSync(
  new URL("../../e2e/x402local/contracts/LocalBillingToken.sol", import.meta.url),
  "utf8",
);
for (const [content, key] of [
  [controller, "controllerSourceSha256"],
  [oracle, "priceOracleSourceSha256"],
  [token, "billingSourceSha256"],
]) {
  if (crypto.createHash("sha256").update(content).digest("hex") !== manifest.customCompilation[key])
    throw Error(`Stale local source: ${key}`);
}
const input = {
  language: "Solidity",
  sources: {
    ...Object.fromEntries(
      Object.entries(ens.roots)
        .filter(([n]) => n !== "ETHRegistrarController")
        .map(([n, p]) => [`contracts/${p}/${n}.sol`, ens.sources[`contracts/${p}/${n}.sol`]]),
    ),
    "contracts/ethregistrar/PreviewRegistrarController.sol": { content: controller },
    "PreviewPriceOracle.sol": { content: oracle },
    "LocalBillingToken.sol": { content: token },
  },
  settings: manifest.customCompilation.settings,
};
const output = JSON.parse(
  solc.compile(JSON.stringify(input), {
    import(p) {
      const content = ens.sources[p]?.content ?? fs.readFileSync(resolveENSImport(p), "utf8");
      input.sources[p] = { content };
      return { contents: content };
    },
  }),
);
if (output.errors?.some((e) => e.severity === "error"))
  throw Error("Local source compilation failed");
const value =
  JSON.stringify(
    {
      language: "solidity",
      compiler_version: manifest.compiler.replace(/\.Emscripten\.clang$/, ""),
      input_kind: "standard_json",
      input,
    },
    null,
    2,
  ) + "\n";
const file = new URL("local.json", import.meta.url);
if (process.argv.includes("--check")) {
  if (fs.readFileSync(file, "utf8") !== value) throw Error("Local verification inputs are stale");
} else fs.writeFileSync(file, value);
const genesis = read("../genesis.json");
const system = {
  "00000961ef480eb55e80d19ad83579a64c007002": ["WithdrawalQueue", "withdrawals"],
  "0000bbddc7ce488642fb579f8b00f3a590007251": ["ConsolidationQueue", "consolidations"],
  "0000bff46984e3725691fa540a8c7589300d8282": ["BuilderDeposit", "builder_deposits"],
  "000064d678505ad48f8ccb093bc65613800e8282": ["BuilderExit", "builder_exits"],
  "000f3df6d732807ef1319fb7b8bb8522d0beac02": ["BeaconRoots", "beacon_root"],
  "0000f90827f1c53a10cb7a02335b175320002935": ["HistoryStorage", "execution_hash"],
  "4e59b44847b379578588920ca78fbf26c0b4956c": ["DeterministicFactory", "factory"],
};
const inventory = [];
for (const [raw, account] of Object.entries(genesis.alloc)) {
  if (!account.code || account.code === "0x") continue;
  const address = "0x" + raw.replace(/^0x/, "").toLowerCase();
  const app = Object.entries(manifest.contracts).find(
    ([, c]) => c.address.toLowerCase() === address,
  );
  let name, bundle, entrypoint;
  if (system[address.slice(2)]) {
    [name, entrypoint] = system[address.slice(2)];
    bundle = "sys-asm.json";
    entrypoint += "/main.eas";
  } else {
    name =
      app?.[0] ??
      {
        "0xca11bde05977b3631167028862be2a173976ca11": "Multicall3",
        "0x1820a4b7618bde71dce8cdc73aab6c95905fad24": "ERC1820Registry",
      }[address];
    if (!name) throw Error(`Missing source mapping: ${address}`);
    bundle = ["EntryPoint", "Multicall3", "ERC1820Registry", "SimpleAccountFactory"].includes(name)
      ? `${name}.json`
      : name === "SimpleAccount"
        ? "SimpleAccountFactory.json"
        : name === "SenderCreator"
          ? "EntryPoint.json"
          : "local.json";
  }
  inventory.push({
    address,
    name,
    bundle,
    ...(entrypoint ? { entrypoint } : {}),
    codeHash: keccak256(account.code),
    codeSha256: crypto
      .createHash("sha256")
      .update(Buffer.from(account.code.slice(2), "hex"))
      .digest("hex"),
  });
}
const bundles = Object.fromEntries(
  [...new Set(inventory.map((c) => c.bundle))].sort().map((p) => [
    p,
    crypto
      .createHash("sha256")
      .update(fs.readFileSync(new URL(p, import.meta.url)))
      .digest("hex"),
  ]),
);
const inventoryValue =
  JSON.stringify(
    {
      chainId: "48815",
      bundles,
      contracts: inventory.sort((a, b) => a.address.localeCompare(b.address)),
    },
    null,
    2,
  ) + "\n";
const inventoryFile = new URL("inventory.json", import.meta.url);
if (process.argv.includes("--check")) {
  if (fs.readFileSync(inventoryFile, "utf8") !== inventoryValue)
    throw Error("Verification inventory is stale");
} else fs.writeFileSync(inventoryFile, inventoryValue);
