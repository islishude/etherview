// Explicit artifact regeneration only. Ordinary Preview startup consumes these outputs.
import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import net from "node:net";
import crypto from "node:crypto";
import { spawn, execFileSync } from "node:child_process";
import { gunzipSync } from "node:zlib";
import { createRequire } from "node:module";
import solc from "solc";
import {
  createPublicClient,
  createWalletClient,
  http,
  defineChain,
  encodeDeployData,
  getContractAddress,
  concatHex,
  keccak256,
  toHex,
  namehash,
  zeroAddress,
  zeroHash,
  parseEther,
} from "viem";
import { entryPoint09Abi, entryPoint09Address } from "viem/account-abstraction";
import { parseAbi } from "viem";
import { mnemonicToAccount } from "viem/accounts";
const here = import.meta.dirname;
const root = path.resolve(here, "../../..");
const require = createRequire(import.meta.url);
const ensSources = JSON.parse(
  fs.readFileSync(new URL("upstream/ens-sources.json", import.meta.url)),
);
const check = process.argv.includes("--check");
const sha = (value) => crypto.createHash("sha256").update(value).digest("hex");
const mnemonic = "test test test test test test test test test test test junk";
const deployer = mnemonicToAccount(mnemonic, { addressIndex: 19 });
const chain = defineChain({
  id: 48815,
  name: "Preview",
  nativeCurrency: { name: "Ether", symbol: "ETH", decimals: 18 },
  rpcUrls: { default: { http: [] } },
});
const factory = "0x4e59b44847b379578588920cA78FbF26c0B4956C";
const genesisPath = path.join(root, "deploy/preview.genesis.json");
const original = JSON.parse(fs.readFileSync(genesisPath));
const previous = fs.existsSync(path.join(here, "manifest.json"))
  ? JSON.parse(fs.readFileSync(path.join(here, "manifest.json")))
  : null;
const base = structuredClone(original);
for (const address of previous?.allocationAddresses ?? []) {
  const key = Object.keys(base.alloc).find(
    (k) => k.toLowerCase().replace(/^0x/, "") === address.slice(2).toLowerCase(),
  );
  if (key) delete base.alloc[key];
}
function upstream(name) {
  return JSON.parse(fs.readFileSync(path.join(here, "upstream", `${name}.json`)));
}
function ens(folder, name) {
  return compiled(`contracts/${folder}/${name}.sol`, name);
}
// Build the controller from the reviewed upstream source with only TLD substitutions.
const controllerPath = "contracts/ethregistrar/PreviewRegistrarController.sol";
const controllerSource = ensSources.sources[
  "contracts/ethregistrar/ETHRegistrarController.sol"
].content
  .replace("contract ETHRegistrarController is", "contract PreviewRegistrarController is")
  .replaceAll(
    "0x93cdeb708b7545dc668eb9280176169d1c33cfd8ed6f04690a0bcc88a93fc4ae",
    namehash("test"),
  )
  .replaceAll('".eth"', '".test"');
const oracleSource = `// SPDX-License-Identifier: MIT
pragma solidity 0.8.30;
import {IPriceOracle} from "contracts/ethregistrar/IPriceOracle.sol";
contract PreviewPriceOracle is IPriceOracle {
 function price(string calldata, uint256, uint256 duration) external pure returns (Price memory) { return Price(duration * 1 gwei, 0); }
 function supportsInterface(bytes4 id) external pure returns(bool) { return id == type(IPriceOracle).interfaceId || id == 0x01ffc9a7; }
}`;
const sources = {
  ...Object.fromEntries(
    Object.entries(ensSources.roots)
      .filter(([name]) => name !== "ETHRegistrarController")
      .map(([name, folder]) => {
        const key = `contracts/${folder}/${name}.sol`;
        return [key, ensSources.sources[key]];
      }),
  ),
  [controllerPath]: { content: controllerSource },
  "PreviewPriceOracle.sol": { content: oracleSource },
};
const input = {
  language: "Solidity",
  sources,
  settings: {
    optimizer: { enabled: true, runs: 200 },
    evmVersion: "cancun",
    outputSelection: { "*": { "*": ["abi", "evm.bytecode.object"] } },
  },
};
const compilation = JSON.parse(
  solc.compile(JSON.stringify(input), {
    import: (p) => {
      try {
        if (ensSources.sources[p]) return { contents: ensSources.sources[p].content };
        return {
          contents: fs.readFileSync(require.resolve(p), "utf8"),
        };
      } catch {
        return { error: `Missing pinned source ${p}` };
      }
    },
  }),
);
const errors = compilation.errors?.filter((e) => e.severity === "error") ?? [];
if (errors.length) throw new Error(errors.map((e) => e.formattedMessage).join("\n"));
function compiled(file, name) {
  const a = compilation.contracts[file][name];
  return { abi: a.abi, bytecode: `0x${a.evm.bytecode.object}` };
}
const artifacts = {
  ...upstream("alto-simulations").artifacts,
  EntryPoint: { abi: entryPoint09Abi },
  SimpleAccountFactory: {
    abi: parseAbi([
      "function accountImplementation() view returns (address)",
      "function getAddress(address owner,uint256 salt) view returns(address)",
      "function createAccount(address owner,uint256 salt) returns(address)",
    ]),
  },
  ENSRegistry: ens("registry", "ENSRegistry"),
  BaseRegistrarImplementation: ens("ethregistrar", "BaseRegistrarImplementation"),
  ReverseRegistrar: ens("reverseRegistrar", "ReverseRegistrar"),
  DefaultReverseRegistrar: ens("reverseRegistrar", "DefaultReverseRegistrar"),
  DefaultReverseResolver: ens("reverseResolver", "DefaultReverseResolver"),
  PublicResolver: ens("resolvers", "PublicResolver"),
  UniversalResolver: ens("universalResolver", "UniversalResolver"),
  GatewayProvider: ens("ccipRead", "GatewayProvider"),
  PreviewRegistrarController: compiled(controllerPath, "PreviewRegistrarController"),
  PreviewPriceOracle: compiled("PreviewPriceOracle.sol", "PreviewPriceOracle"),
};
const temp = fs.mkdtempSync(path.join(os.tmpdir(), "etherview-contract-genesis-"));
const server = net.createServer();
await new Promise((r) => server.listen(0, "127.0.0.1", r));
const port = server.address().port;
await new Promise((r) => server.close(r));
// Start from the authoritative existing allocations, including the CREATE2 factory.
fs.writeFileSync(
  path.join(temp, "genesis.json"),
  JSON.stringify({ ...base, timestamp: "0x6553f100" }),
);
if (
  !execFileSync(process.env.ANVIL || "anvil", ["--version"], { encoding: "utf8" }).includes("1.8.6")
)
  throw new Error("Artifact generation requires Anvil 1.8.6");
const child = spawn(
  process.env.ANVIL || "anvil",
  [
    "--host",
    "127.0.0.1",
    "--port",
    String(port),
    "--chain-id",
    "48815",
    "--hardfork",
    "cancun",
    "--accounts",
    "0",
    "--init",
    path.join(temp, "genesis.json"),
    "--silent",
  ],
  { stdio: ["ignore", "ignore", "pipe"] },
);
let diagnostics = "";
child.stderr.on("data", (d) => {
  diagnostics += d.toString().slice(0, 4096);
});
const transport = http(`http://127.0.0.1:${port}`, { retryCount: 0, timeout: 10000 });
const rpc = createPublicClient({ chain, transport, pollingInterval: 50 });
const wallet = createWalletClient({ chain, transport, account: deployer });
const addresses = {};
async function wait(hash) {
  const r = await rpc.waitForTransactionReceipt({ hash, timeout: 30000 });
  if (r.status !== "success") throw new Error(`Construction failed: ${hash}`);
  return r;
}
async function deploy(name, args = []) {
  const a = artifacts[name];
  const receipt = await wait(
    await wallet.deployContract({ abi: a.abi, bytecode: a.bytecode, args, gas: 20000000n }),
  );
  addresses[name] = receipt.contractAddress;
  return receipt.contractAddress;
}
async function deployAA(name, key, expected) {
  const data = upstream("account-abstraction-v09").calls[key];
  const address = getContractAddress({
    opcode: "CREATE2",
    from: factory,
    salt: data.slice(0, 66),
    bytecode: `0x${data.slice(66)}`,
  });
  if (address.toLowerCase() !== expected.toLowerCase())
    throw new Error("Canonical v0.9 address mismatch");
  await wait(await wallet.sendTransaction({ to: factory, data, gas: 20000000n }));
  addresses[name] = address;
  return address;
}
async function write(name, functionName, args = []) {
  await wait(
    await wallet.writeContract({
      address: addresses[name],
      abi: artifacts[name].abi,
      functionName,
      args,
      gas: 5000000n,
    }),
  );
}
try {
  let ready = false;
  for (let i = 0; i < 100; i++) {
    try {
      await rpc.getChainId();
      ready = true;
      break;
    } catch {
      if (child.exitCode !== null) break;
      await new Promise((r) => setTimeout(r, 100));
    }
  }
  if (!ready) throw new Error(`Anvil did not start: ${diagnostics}`);
  await rpc.request({
    method: "anvil_setBalance",
    params: [deployer.address, toHex(parseEther("1000"))],
  });
  await deployAA("EntryPoint", "ENTRY_POINT_V09_CREATECALL", entryPoint09Address);
  await deployAA(
    "SimpleAccountFactory",
    "SIMPLE_ACCOUNT_FACTORY_V09_CREATECALL",
    "0xf4a7018fdbf22804526ec4b77dce4f9b0d27a395",
  );
  const registry = await deploy("ENSRegistry");
  const baseRegistrar = await deploy("BaseRegistrarImplementation", [registry, namehash("test")]);
  const reverse = await deploy("ReverseRegistrar", [registry]);
  await write("ENSRegistry", "setSubnodeOwner", [
    zeroHash,
    keccak256(toHex("reverse")),
    deployer.address,
  ]);
  await write("ENSRegistry", "setSubnodeOwner", [
    namehash("reverse"),
    keccak256(toHex("addr")),
    reverse,
  ]);
  const defaultReverse = await deploy("DefaultReverseRegistrar");
  await deploy("DefaultReverseResolver", [defaultReverse]);
  await write("ENSRegistry", "setResolver", [
    namehash("reverse"),
    addresses.DefaultReverseResolver,
  ]);
  const prices = await deploy("PreviewPriceOracle");
  const controller = await deploy("PreviewRegistrarController", [
    baseRegistrar,
    prices,
    3n,
    86400n,
    reverse,
    defaultReverse,
    registry,
  ]);
  // A non-owning wrapper sentinel avoids invoking a missing wrapper for unowned nodes.
  const resolver = await deploy("PublicResolver", [
    registry,
    "0x0000000000000000000000000000000000000001",
    controller,
    reverse,
  ]);
  const gateways = await deploy("GatewayProvider", [deployer.address, []]);
  await deploy("UniversalResolver", [deployer.address, registry, gateways]);
  await write("ENSRegistry", "setSubnodeOwner", [
    zeroHash,
    keccak256(toHex("test")),
    baseRegistrar,
  ]);
  await write("ENSRegistry", "setSubnodeRecord", [
    namehash("reverse"),
    keccak256(toHex("default")),
    deployer.address,
    addresses.DefaultReverseResolver,
    0n,
  ]);
  await write("BaseRegistrarImplementation", "addController", [controller]);
  await write("BaseRegistrarImplementation", "setResolver", [resolver]);
  await write("ReverseRegistrar", "setDefaultResolver", [resolver]);
  await write("ReverseRegistrar", "setController", [controller, true]);
  await write("DefaultReverseRegistrar", "setController", [controller, true]);
  await deploy("PimlicoSimulations");
  await deploy("EntryPointSimulations09");
  const encoded = await rpc.request({ method: "anvil_dumpState", params: [] });
  let bytes = Buffer.from(encoded.slice(2), "hex");
  if (bytes[0] === 31 && bytes[1] === 139) bytes = gunzipSync(bytes);
  const state = JSON.parse(bytes.toString());
  const alloc = {};
  const existing = new Set(Object.keys(base.alloc).map((a) => a.toLowerCase().replace(/^0x/, "")));
  for (const [address, account] of Object.entries(state.accounts)) {
    if (
      !account.code ||
      account.code === "0x" ||
      existing.has(address.replace(/^0x/, "").toLowerCase())
    )
      continue;
    alloc[address.toLowerCase()] = {
      balance: toHex(BigInt(account.balance)),
      nonce: toHex(BigInt(account.nonce)),
      code: account.code,
      storage: Object.fromEntries(Object.entries(account.storage ?? {}).sort()),
    };
  }
  const devAccounts = {};
  for (const [name, index] of [
    ["bundlerExecutor", 9],
    ["bundlerUtility", 10],
    ["ensOwner", 19],
  ]) {
    const address = mnemonicToAccount(mnemonic, { addressIndex: index }).address;
    if (existing.has(address.slice(2).toLowerCase()))
      throw new Error(`Funding allocation collision: ${name}`);
    alloc[address.toLowerCase()] = {
      balance: toHex(parseEther("1000")),
      nonce: toHex(BigInt(state.accounts[address.toLowerCase()]?.nonce ?? 0)),
    };
    devAccounts[name] = { address, index };
  }
  addresses.SenderCreator = await rpc.readContract({
    address: addresses.EntryPoint,
    abi: entryPoint09Abi,
    functionName: "senderCreator",
  });
  addresses.SimpleAccount = await rpc.readContract({
    address: addresses.SimpleAccountFactory,
    abi: artifacts.SimpleAccountFactory.abi,
    functionName: "accountImplementation",
  });
  artifacts.SenderCreator = {
    abi: parseAbi(["function createSender(bytes initCode) returns(address)"]),
  };
  artifacts.SimpleAccount = {
    abi: parseAbi([
      "function initialize(address owner)",
      "function owner() view returns(address)",
      "function entryPoint() view returns(address)",
      "function execute(address dest,uint256 value,bytes func)",
      "function addDeposit() payable",
      "function getDeposit() view returns(uint256)",
    ]),
  };
  const contracts = {};
  for (const [name, address] of Object.entries(addresses))
    contracts[name] = {
      address,
      codeHash: keccak256(alloc[address.toLowerCase()].code),
      abi: artifacts[name].abi,
    };
  const manifest = {
    version: 1,
    chainId: 48815,
    coinType: "2147532463",
    tld: "test",
    compiler: solc.version(),
    anvil: "1.8.6",
    customCompilation: {
      settings: input.settings,
      controllerSourceSha256: sha(controllerSource),
      priceOracleSourceSha256: sha(oracleSource),
    },
    allocationSha256: sha(JSON.stringify(alloc, null, 2) + "\n"),
    sources: {
      accountAbstraction: "b36a1ed52ae00da6f8a4c8d50181e2877e4fa410",
      ens: ensSources.revision,
    },
    devAccounts,
    contracts,
    allocationAddresses: Object.keys(alloc).sort(),
  };
  const result = structuredClone(base);
  for (const [address, account] of Object.entries(alloc).sort())
    result.alloc[address.slice(2)] = account;
  const outputs = {
    "manifest.json": JSON.stringify(manifest, null, 2) + "\n",
    "allocation.json": JSON.stringify(alloc, null, 2) + "\n",
  };
  for (const [file, value] of Object.entries(outputs)) {
    const p = path.join(here, file);
    if (check) {
      if (fs.readFileSync(p, "utf8") !== value) throw new Error(`${file} is stale`);
    } else fs.writeFileSync(p, value);
  }
  const rendered = JSON.stringify(result, null, 2) + "\n";
  if (check) {
    if (fs.readFileSync(genesisPath, "utf8") !== rendered)
      throw new Error("Preview genesis is stale");
  } else fs.writeFileSync(genesisPath, rendered);
  console.log(
    `Preview contracts: ${Object.keys(alloc).length} allocations, sha256 ${sha(rendered)}`,
  );
} finally {
  child.kill("SIGTERM");
  await new Promise((r) => (child.exitCode !== null ? r() : child.once("exit", r)));
  fs.rmSync(temp, { recursive: true, force: true });
}
