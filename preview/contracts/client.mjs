import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import {
  createPublicClient,
  createWalletClient,
  http,
  defineChain,
  encodeFunctionData,
  parseAbi,
  namehash,
  keccak256,
  toHex,
  parseEther,
  zeroHash,
} from "viem";
import { mnemonicToAccount, privateKeyToAccount } from "viem/accounts";
import { normalize, packetToBytes } from "viem/ens";
import { getUserOperationHash } from "viem/account-abstraction";
export const manifest = JSON.parse(fs.readFileSync(new URL("manifest.json", import.meta.url)));
export const contracts = manifest.contracts;
export const stringify = (value) =>
  JSON.stringify(value, (_, v) => (typeof v === "bigint" ? v.toString() : v), 2) + "\n";
const mnemonic = "test test test test test test test test test test test junk";
export function saveState(file, value, exclusive = false) {
  fs.mkdirSync(path.dirname(path.resolve(file)), { recursive: true });
  if (exclusive) {
    fs.writeFileSync(file, stringify(value), { flag: "wx", mode: 0o600 });
    return;
  }
  const temporary = `${file}.${crypto.randomBytes(8).toString("hex")}.tmp`;
  fs.writeFileSync(temporary, stringify(value), { flag: "wx", mode: 0o600 });
  fs.renameSync(temporary, file);
}
export function readState(file) {
  const s = fs.lstatSync(file);
  if (!s.isFile() || s.isSymbolicLink() || (s.mode & 0o077) !== 0)
    throw new Error("State file must be a regular private 0600 file");
  if (s.size > 65536) throw new Error("State file exceeds 64 KiB");
  return JSON.parse(fs.readFileSync(file, "utf8"));
}
export async function connect(options = {}) {
  const chain = defineChain({
    id: 48815,
    name: "Ethereum Preview",
    nativeCurrency: { name: "Ether", symbol: "ETH", decimals: 18 },
    rpcUrls: { default: { http: [] } },
  });
  const account = options.keyFile
    ? privateKeyToAccount(fs.readFileSync(options.keyFile, "utf8").trim())
    : mnemonicToAccount(mnemonic, { addressIndex: Number(options.index ?? 0) });
  const transport = http(options.rpc ?? "https://geth.etherview.localhost", {
    retryCount: 0,
    timeout: 30000,
  });
  const rpc = createPublicClient({ chain, transport, pollingInterval: 300 });
  const wallet = createWalletClient({ chain, transport, account });
  const bundler = createPublicClient({
    transport: http(options.bundler ?? "https://bundler.etherview.localhost", {
      retryCount: 0,
      timeout: 60000,
    }),
  });
  if ((await rpc.getChainId()) !== 48815) throw new Error("Expected Preview chain 48815");
  const genesis = (await rpc.getBlock({ blockNumber: 0n })).hash;
  for (const name of ["EntryPoint", "ENSRegistry", "UniversalResolver"]) {
    const code = await rpc.getCode({ address: contracts[name].address, blockNumber: 0n });
    if (!code || keccak256(code) !== contracts[name].codeHash)
      throw new Error(`Preview genesis contract mismatch: ${name}`);
  }
  const read = (name, functionName, args = []) =>
    rpc.readContract({ ...contracts[name], functionName, args });
  const receipt = async (hash) => {
    const r = await rpc.waitForTransactionReceipt({ hash, timeout: 120000 });
    if (r.status !== "success") throw new Error(`Transaction reverted: ${hash}`);
    return r;
  };
  const write = async (name, functionName, args = [], value = 0n) => {
    const { request } = await rpc.simulateContract({
      ...contracts[name],
      account,
      functionName,
      args,
      value,
    });
    return wallet.writeContract(request);
  };
  return { rpc, wallet, bundler, account, genesis, read, write, receipt };
}
export function localName(raw) {
  const name = normalize(raw);
  const labels = name.split(".");
  if (labels.length !== 2 || labels[1] !== "test" || [...labels[0]].length < 3)
    throw new Error("Expected a name with at least three characters under .test");
  return name;
}
export function assertIdentity(ctx, state) {
  if (
    state.chainId !== 48815 ||
    state.genesis !== ctx.genesis ||
    state.owner.toLowerCase() !== ctx.account.address.toLowerCase()
  )
    throw new Error("Saved operation belongs to another chain or owner");
}
export async function quote(ctx, name, duration = 31536000n) {
  const label = localName(name).split(".")[0];
  return {
    available: await ctx.read("PreviewRegistrarController", "available", [label]),
    price: await ctx.read("PreviewRegistrarController", "rentPrice", [label, duration]),
  };
}
export async function commitName(ctx, name, file, duration = 31536000n) {
  name = localName(name);
  const registration = {
    label: name.split(".")[0],
    owner: ctx.account.address,
    duration,
    secret: toHex(crypto.randomBytes(32)),
    resolver: contracts.PublicResolver.address,
    data: [
      encodeFunctionData({
        abi: contracts.PublicResolver.abi,
        functionName: "setAddr",
        args: [namehash(name), BigInt(manifest.coinType), ctx.account.address],
      }),
    ],
    reverseRecord: 2,
    referrer: zeroHash,
  };
  const commitment = await ctx.read("PreviewRegistrarController", "makeCommitment", [registration]);
  const state = {
    kind: "ens",
    chainId: 48815,
    genesis: ctx.genesis,
    owner: ctx.account.address,
    name,
    registration,
    commitment,
  };
  saveState(file, state, true);
  // A saved commitment can be queried after an uncertain submission; never replace its secret.
  state.commitHash = await ctx.write("PreviewRegistrarController", "commit", [commitment]);
  saveState(file, state);
  await ctx.receipt(state.commitHash);
  return { name, commitment, transactionHash: state.commitHash, stateFile: file };
}
export async function registerName(ctx, file) {
  const state = readState(file);
  assertIdentity(ctx, state);
  if (state.kind !== "ens") throw new Error("Expected an ENS state file");
  if (state.registerHash) {
    await ctx.receipt(state.registerHash);
    return { name: state.name, transactionHash: state.registerHash };
  }
  const registration = { ...state.registration, duration: BigInt(state.registration.duration) };
  const committed = await ctx.read("PreviewRegistrarController", "commitments", [state.commitment]);
  const timestamp = (await ctx.rpc.getBlock()).timestamp;
  if (committed === 0n)
    throw new Error("Commitment is absent; inspect the saved commit transaction before retrying");
  if (timestamp < committed + 3n)
    throw new Error("Commitment is too new; wait for another Preview block");
  if (timestamp >= committed + 86400n) throw new Error("Commitment expired");
  const price = await ctx.read("PreviewRegistrarController", "rentPrice", [
    registration.label,
    registration.duration,
  ]);
  state.registerHash = await ctx.write(
    "PreviewRegistrarController",
    "register",
    [registration],
    price.base + price.premium,
  );
  saveState(file, state);
  await ctx.receipt(state.registerHash);
  return { name: state.name, transactionHash: state.registerHash };
}
export async function manageName(ctx, action, name, options = {}) {
  name = localName(name);
  const label = name.split(".")[0];
  const token = BigInt(keccak256(toHex(label)));
  let hash;
  switch (action) {
    case "renew": {
      const duration = BigInt(options.duration ?? 31536000);
      const price = await ctx.read("PreviewRegistrarController", "rentPrice", [label, duration]);
      hash = await ctx.write(
        "PreviewRegistrarController",
        "renew",
        [label, duration, zeroHash],
        price.base,
      );
      break;
    }
    case "transfer":
      hash = await ctx.write("BaseRegistrarImplementation", "transferFrom", [
        ctx.account.address,
        options.to,
        token,
      ]);
      break;
    case "reclaim":
      hash = await ctx.write("BaseRegistrarImplementation", "reclaim", [
        token,
        ctx.account.address,
      ]);
      break;
    case "set-address":
      hash = await ctx.write("PublicResolver", "setAddr", [
        namehash(name),
        BigInt(manifest.coinType),
        options.to ?? ctx.account.address,
      ]);
      break;
    case "set-primary":
      hash = await ctx.write("DefaultReverseRegistrar", "setName", [name]);
      break;
    default:
      throw new Error("Unknown ENS operation");
  }
  await ctx.receipt(hash);
  return { name, transactionHash: hash };
}
export async function resolveName(ctx, name) {
  name = localName(name);
  return ctx.read("UniversalResolver", "resolve", [
    toHex(packetToBytes(name)),
    encodeFunctionData({
      abi: contracts.PublicResolver.abi,
      functionName: "addr",
      args: [namehash(name), BigInt(manifest.coinType)],
    }),
  ]);
}
export async function smartAddress(ctx, salt = 0n) {
  return ctx.read("SimpleAccountFactory", "getAddress", [ctx.account.address, salt]);
}
const accountABI = parseAbi(["function execute(address dest,uint256 value,bytes func)"]);
export async function prepareOperation(ctx, { to, value = 0n, data = "0x", salt = 0n } = {}) {
  const sender = await smartAddress(ctx, salt);
  const ep = contracts.EntryPoint.address;
  const nonce = await ctx.read("EntryPoint", "getNonce", [sender, 0n]);
  const fees = await ctx.rpc.estimateFeesPerGas();
  const op = {
    sender,
    nonce,
    callData: encodeFunctionData({
      abi: accountABI,
      functionName: "execute",
      args: [to ?? ctx.account.address, value, data],
    }),
    maxFeePerGas: fees.maxFeePerGas,
    maxPriorityFeePerGas: fees.maxPriorityFeePerGas,
    signature: await ctx.account.sign({ hash: zeroHash }),
  };
  if (!(await ctx.rpc.getCode({ address: sender }))) {
    op.factory = contracts.SimpleAccountFactory.address;
    op.factoryData = encodeFunctionData({
      abi: contracts.SimpleAccountFactory.abi,
      functionName: "createAccount",
      args: [ctx.account.address, salt],
    });
  }
  const estimate = await ctx.bundler.request({
    method: "eth_estimateUserOperationGas",
    params: [rpcOperation(op), ep],
  });
  for (const key of ["callGasLimit", "verificationGasLimit", "preVerificationGas"])
    op[key] = (BigInt(estimate[key]) * 120n + 99n) / 100n;
  const hash = getUserOperationHash({
    chainId: 48815,
    entryPointAddress: ep,
    entryPointVersion: "0.9",
    userOperation: op,
  });
  op.signature = await ctx.account.sign({ hash });
  return { operation: op, hash };
}
export function rpcOperation(operation) {
  return Object.fromEntries(
    Object.entries(operation).map(([k, v]) => [k, typeof v === "bigint" ? toHex(v) : v]),
  );
}
export async function sendOperation(ctx, file, options = {}) {
  if (fs.existsSync(file))
    throw new Error("Operation file already exists; query its receipt instead of submitting again");
  const { operation, hash } = await prepareOperation(ctx, options);
  const state = {
    kind: "userop",
    chainId: 48815,
    genesis: ctx.genesis,
    owner: ctx.account.address,
    hash,
    operation: rpcOperation(operation),
  };
  saveState(file, state, true);
  const accepted = await ctx.bundler.request({
    method: "eth_sendUserOperation",
    params: [state.operation, contracts.EntryPoint.address],
  });
  if (accepted.toLowerCase() !== hash.toLowerCase())
    throw new Error("Bundler returned an inconsistent operation hash");
  state.accepted = true;
  saveState(file, state);
  return { userOperationHash: hash, stateFile: file };
}
export async function operationReceipt(ctx, file) {
  const state = readState(file);
  assertIdentity(ctx, state);
  if (state.kind !== "userop") throw new Error("Expected a UserOperation state file");
  return ctx.bundler.request({ method: "eth_getUserOperationReceipt", params: [state.hash] });
}
export async function fundAccount(ctx, salt = 0n, amount = parseEther("1")) {
  const address = await smartAddress(ctx, salt);
  const hash = await ctx.wallet.sendTransaction({ to: address, value: amount });
  await ctx.receipt(hash);
  return { address, transactionHash: hash };
}
