import crypto from "node:crypto";

import {
  encodeFunctionData,
  encodePacked,
  parseAbi,
  parseEther,
  keccak256,
  toHex,
  namehash,
  zeroAddress,
  zeroHash,
  decodeEventLog,
  toFunctionSelector,
} from "viem";
import { getUserOperationHash } from "viem/account-abstraction";

import {
  connect,
  contracts,
  manifest,
  prepareOperation,
  rpcOperation,
  smartAddress,
} from "../client.mjs";
import { actor, request, matches } from "./engine.mjs";

const largeID = (1n << 200n) + 42n;
const implSlot = "0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc";
const adminSlot = "0xb53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103";
const data = (e, artifact, fn, args = []) =>
  encodeFunctionData({ abi: e.artifacts[artifact].abi, functionName: fn, args });
const readGenesis = (e, contract, fn, args = []) =>
  e.rpc.readContract({ ...contracts[contract], functionName: fn, args });
const genesisCall = (e, s, id, contract, fn, args = [], options = {}) =>
  e.tx(
    s,
    id,
    {
      to: contracts[contract].address,
      data: encodeFunctionData({ abi: contracts[contract].abi, functionName: fn, args }),
      value: options.value ?? 0n,
    },
    options,
  );
function setProxyCheck(e, s, implementation) {
  for (const address of [s.entities.proxy, s.entities.proxy2].filter(Boolean))
    e.check(s, "proxy " + address, "/api/v1/contracts/" + address + "/proxy", {
      "data.implementation.address": implementation,
    });
}
async function transactions(e, s) {
  const target = await e.deploy(s, "target", "ToolCalls");
  const recipient = actor(e.run, "recipient").address;
  s.entities.recipient = recipient;
  for (const type of ["legacy", "eip2930", "eip1559"])
    await e.tx(s, type, {
      type,
      to: recipient,
      value: 10000000000000001n,
      ...(type === "eip2930" ? { accessList: [] } : {}),
    });
  await e.call(s, "internal-transfer", target, "ToolCalls", "forward", [recipient], {
    value: 12345678901234567n,
  });
  await e.call(s, "write", target, "ToolCalls", "setValue", [largeID]);
  await e.call(s, "revert", target, "ToolCalls", "fail", [], { expected: "reverted" });
  await e.verify(s, ["target"]);
  for (const panel of ["trace", "logs", "state-changes", "native-transfers"])
    e.check(
      s,
      panel,
      "/api/v1/transactions/" +
        s.steps[panel === "native-transfers" ? "internal-transfer" : "write"].hash +
        "/" +
        panel,
      panel === "trace" || panel === "state-changes" ? { "data.state": "complete" } : {},
    );
}
function uploadIPFS(e, content) {
  const cid = e
    .compose(
      [
        "exec",
        "-T",
        "ipfs",
        "ipfs",
        "add",
        "--offline",
        "--quieter",
        "--cid-version=1",
        "--pin=true",
      ],
      content,
    )
    .trim();
  if (!/^b[a-z2-7]{20,120}$/.test(cid)) throw Error("Invalid local IPFS upload result");
  return "ipfs://" + cid;
}
function tokenMetadata(e, s) {
  if (s.entities.metadataInitial) return;
  const image = uploadIPFS(
    e,
    Buffer.from(
      "iVBORw0KGgoAAAANSUhEUgAAAEAAAABACAIAAAAlC+aJAAAAmElEQVR4nO3PMQ2AUBTFUGQwIRFNKEEEijDw38BUmjS5Y4d7tuO+ltvPZ7m/9dvfDgWgDwWgDwWgD30GWI5OfQC6D0D3AejeD7AcnfoAdB+A7gPQvR9gOTr1Aeg+AN0HoHs/wHJ06gPQfQC6D0D3foDl6NQHoPsAdB+A7v0Ay9GpD0D3Aeg+AN37AZajUx+A7gPQfQC61wNeTPahS2dkkHUAAAAASUVORK5CYII=",
      "base64",
    ),
  );
  s.entities.metadataInitial = uploadIPFS(
    e,
    JSON.stringify({ name: "Preview Tool NFT", description: "Initial local IPFS document", image }),
  );
  s.entities.metadataUpdated = uploadIPFS(
    e,
    JSON.stringify({
      name: "Preview Tool NFT updated",
      description: "Updated local IPFS document",
      image,
    }),
  );
  s.entities.metadataInvalid = uploadIPFS(e, "{not valid JSON");
  e.run.save();
}
function metadataCheck(e, s, state, name) {
  e.check(s, "NFT metadata", "/api/v1/nfts/" + s.entities.erc721 + "/" + largeID + "/metadata", {
    "data.state": state,
    ...(name
      ? { "data.name": name, "data.image.state": "available", "data.content_stale": false }
      : {}),
  });
}
async function tokenChecks(e, s) {
  const balance = String(
    await e.read(s.entities.erc20, "ToolERC20", "balanceOf", [e.account.address]),
  );
  const supply = String(await e.read(s.entities.erc20, "ToolERC20", "totalSupply"));
  s.entities.erc20OwnerBalance = balance;
  s.entities.erc20TotalSupply = supply;
  e.check(s, "ERC20", "/api/v1/tokens/" + s.entities.erc20, {
    "data.standard": "erc20",
    "data.decimals": 18,
  });
  e.check(s, "holders", "/api/v1/tokens/" + s.entities.erc20 + "/holders", {
    data: { $contains: { holder_address: e.account.address, balance } },
    "meta.total_supply": supply,
  });
}
async function tokens(e, s) {
  tokenMetadata(e, s);
  const owner = e.account.address;
  for (const [id, a] of [
    ["erc20", "ToolERC20"],
    ["erc721", "ToolERC721"],
    ["erc1155", "ToolERC1155"],
  ])
    await e.deploy(s, id, a);
  s.entities.tokenID = String(largeID);
  s.entities.recipient = actor(e.run, "recipient").address;
  await e.call(s, "mint20", s.entities.erc20, "ToolERC20", "mint", [owner, 10n ** 30n]);
  for (const id of [largeID, largeID + 1n, largeID + 2n])
    await e.call(s, "mint721-" + id, s.entities.erc721, "ToolERC721", "mint", [
      owner,
      id,
      s.entities.metadataInitial,
    ]);
  for (const id of [largeID, largeID + 1n])
    await e.call(s, "mint1155-" + id, s.entities.erc1155, "ToolERC1155", "mint", [owner, id, 100n]);
  await e.call(s, "uri1155", s.entities.erc1155, "ToolERC1155", "setURI", [
    s.entities.metadataInitial,
  ]);
  await e.verify(s, ["erc20", "erc721", "erc1155"]);
  await tokenChecks(e, s);
  e.check(s, "NFT", "/api/v1/nfts/" + s.entities.erc721 + "/" + largeID, {
    "data.token_id": String(largeID),
    "data.owner": s.name === "wallet" ? e.run.state.to : owner,
  });
  metadataCheck(e, s, "available", "Preview Tool NFT");
}
async function ens(e, s) {
  s.entities.name ??= "tool-" + e.run.state.id.toLowerCase() + ".test";
  const name = s.entities.name;
  s.registration ??= {
    label: name.split(".")[0],
    owner: e.account.address,
    duration: "31536000",
    secret: toHex(crypto.randomBytes(32)),
    resolver: contracts.PublicResolver.address,
    data: [
      encodeFunctionData({
        abi: contracts.PublicResolver.abi,
        functionName: "setAddr",
        args: [namehash(name), BigInt(manifest.coinType), e.account.address],
      }),
    ],
    reverseRecord: 2,
    referrer: zeroHash,
  };
  e.run.save();
  const registration = { ...s.registration, duration: BigInt(s.registration.duration) };
  const commitment = await readGenesis(e, "PreviewRegistrarController", "makeCommitment", [
    registration,
  ]);
  await genesisCall(e, s, "commit", "PreviewRegistrarController", "commit", [commitment]);
  if (!s.steps.register) {
    const committed = await readGenesis(e, "PreviewRegistrarController", "commitments", [
      commitment,
    ]);
    await e.wait(
      () => e.rpc.getBlock(),
      (b) => b.timestamp >= committed + 3n,
      "ENS minimum commitment age",
    );
    const current = (await e.rpc.getBlock()).timestamp;
    if (current >= committed + 86400n)
      throw Error("ENS commitment expired; create a new run explicitly");
  }
  const price = await readGenesis(e, "PreviewRegistrarController", "rentPrice", [
    registration.label,
    registration.duration,
  ]);
  await genesisCall(e, s, "register", "PreviewRegistrarController", "register", [registration], {
    value: price.base + price.premium,
  });
  s.entities.owner = e.account.address;
  s.entities.controller = e.account.address;
  s.entities.resolved = e.account.address;
  e.check(s, "name", "/api/v1/names/" + name, {
    "data.name": name,
    "data.state": "indexed",
    "data.registration_state": "registered",
    "data.registrant.value": e.account.address,
    "data.controller.value": e.account.address,
    "data.resolved_address.value": e.account.address,
  });
  e.check(s, "history", "/api/v1/names/" + name + "/history", {});
}
async function aa(e, s) {
  const ctx = await connect({
    ...e.run.state.endpoints,
    keyFile: e.run.dir + "/private/owner.key",
  });
  s.salt ??= String(
    BigInt("0x" + crypto.createHash("sha256").update(e.run.state.id).digest("hex").slice(0, 16)),
  );
  s.entities.smartAccount = await smartAddress(ctx, BigInt(s.salt));
  e.run.save();
  await e.tx(s, "fund", { to: s.entities.smartAccount, value: parseEther("2") });
  await e.userop(s, "create-account", async () => {
    const p = await prepareOperation(ctx, { salt: BigInt(s.salt) });
    return { hash: p.hash, operation: rpcOperation(p.operation) };
  });
}
async function proxies(e, s) {
  const kind = s.name.slice(6);
  if (kind === "safe") {
    const singleton = await e.deploy(s, "singleton", "Safe");
    const factory = await e.deploy(s, "factory", "SafeProxyFactory");
    await e.call(s, "create-proxy", factory, "SafeProxyFactory", "createProxyWithNonce", [
      singleton,
      "0x",
      141n,
    ]);
    const receipt = await e.rpc.getTransactionReceipt({ hash: s.steps["create-proxy"].hash });
    for (const log of receipt.logs) {
      try {
        const event = decodeEventLog({ abi: e.artifacts.SafeProxyFactory.abi, ...log });
        if (event.eventName === "ProxyCreation") s.entities.proxy = event.args.proxy;
      } catch {
        /* unrelated event */
      }
    }
    if (!s.entities.proxy) throw Error("Safe ProxyCreation missing");
    e.check(s, "Safe detection", "/api/v1/contracts/" + s.entities.proxy + "/proxy", {
      "data.proxy_detection.primary.family": "safe",
      "data.proxy_detection.primary.official_singleton": false,
      "data.proxy_detection.primary.canonical_proxy_shell": true,
      "data.proxy_detection.primary.implementation": s.entities.singleton,
    });
    return;
  }
  if (kind === "diamond") {
    const facet = await e.deploy(s, "facet", "ToolDiamondFacet");
    await e.deploy(s, "facet2", "ToolDiamondFacetV2");
    await e.deploy(s, "proxy", "ToolDiamond", [facet]);
    await e.verify(s, ["facet", "facet2", "proxy"]);
    e.check(s, "Diamond detection", "/api/v1/contracts/" + s.entities.proxy + "/proxy", {
      "data.proxy_detection.primary.family": "erc2535",
    });
    return;
  }
  if (kind === "cwia") {
    const factory = await e.deploy(s, "factory", "MyAccountFactory");
    const implementation = await e.read(factory, "MyAccountFactory", "implementation");
    s.entities.implementation = implementation;
    s.deployments.implementation = {
      ...s.deployments.implementation,
      address: implementation,
      artifact: "MyAccount",
      identity: e.artifacts.MyAccount.identity,
    };
    await e.call(s, "create-proxy", factory, "MyAccountFactory", "create", [
      e.account.address,
      largeID,
      toHex("hello,world"),
    ]);
    s.entities.proxy = await e.read(factory, "MyAccountFactory", "account");
    s.entities.immutableArgs = encodePacked(
      ["address", "uint256", "uint16", "bytes"],
      [e.account.address, largeID, 11, toHex("hello,world")],
    );
    await e.verify(s, ["factory", "implementation"], [[s.entities.proxy, implementation]]);
    setProxyCheck(e, s, implementation);
    e.check(s, "CWIA arguments", "/api/v1/contracts/" + s.entities.proxy + "/proxy", {
      "data.immutable_args": s.entities.immutableArgs,
      "data.immutable_args_decoding.status": "decoded",
      "data.immutable_args_decoding.arguments.1.value": String(largeID),
      "data.immutable_args_decoding.arguments.3.value": toHex("hello,world"),
    });
    return;
  }
  const impl = await e.deploy(
    s,
    "implementation",
    kind === "erc1967" ? "ToolERC1967Implementation" : "Implementation",
  );
  const initialize = (v) => data(e, "Implementation", "initialize", [e.account.address, v]);
  if (["erc1167", "immutable"].includes(kind)) {
    const factory = await e.deploy(s, "factory", "CloneFactory");
    const immutable = kind === "immutable";
    const args = encodePacked(["address", "uint256"], [e.account.address, largeID]);
    await e.call(
      s,
      "create-proxy",
      factory,
      "CloneFactory",
      immutable ? "deployWithImmutableArgs" : "deployStandard",
      immutable ? [impl, args] : [impl],
    );
    s.entities.proxy = await e.read(
      factory,
      "CloneFactory",
      immutable ? "immutableArgsClone" : "standardClone",
    );
    if (immutable) s.entities.immutableArgs = args;
    await e.call(s, "initialize", s.entities.proxy, "Implementation", "initialize", [
      e.account.address,
      11n,
    ]);
  } else if (kind === "beacon") {
    const beacon = await e.deploy(s, "beacon", "UpgradeableBeacon", [impl, e.account.address]);
    await e.deploy(s, "proxy", "BeaconProxy", [beacon, initialize(31n)]);
    await e.deploy(s, "proxy2", "BeaconProxy", [beacon, initialize(32n)]);
  } else if (kind === "transparent") {
    await e.deploy(s, "proxy", "TransparentUpgradeableProxy", [
      impl,
      e.account.address,
      initialize(11n),
    ]);
    const slot = await e.rpc.getStorageAt({
      address: s.entities.proxy,
      slot: adminSlot,
      blockHash: s.steps.proxy.receipt.blockHash,
    });
    s.entities.admin = "0x" + slot.slice(-40);
    s.deployments.admin = {
      ...s.deployments.admin,
      address: s.entities.admin,
      artifact: "ProxyAdmin",
      identity: e.artifacts.ProxyAdmin.identity,
    };
  } else await e.deploy(s, "proxy", "ERC1967Proxy", [impl, initialize(11n)]);
  if (!["erc1167", "immutable"].includes(kind))
    await e.deploy(
      s,
      "implementationV2",
      kind === "erc1967" ? "ToolERC1967ImplementationV2" : "ImplementationV2",
    );
  if (kind === "uups") await e.deploy(s, "badImplementation", "BadUUIDImplementation");
  await e.verify(
    s,
    Object.keys(s.deployments),
    [s.entities.proxy, s.entities.proxy2].filter(Boolean).map((address) => [address, impl]),
  );
  setProxyCheck(e, s, impl);
}
async function eip7702(e, s) {
  await e.deploy(s, "delegate", "ToolCalls");
  await e.deploy(s, "delegate2", "ToolCallsV2");
  await e.verify(s, ["delegate", "delegate2"]);
  s.entities.authority = actor(e.run, "authority").address;
  await e.tx(s, "authority-fund", { to: s.entities.authority, value: parseEther("1") });
  await authorize(e, s, "delegate-initial", s.entities.delegate);
}
async function authorize(e, s, id, delegate, { skip = false, revert = false } = {}) {
  const authority = actor(e.run, "authority");
  let authorization = s.steps[id]?.authorization;
  if (!s.steps[id]) {
    const nonce = await e.rpc.getTransactionCount({
      address: authority.address,
      blockTag: "pending",
    });
    authorization = await e.wallet(authority).signAuthorization({
      contractAddress: delegate,
      chainId: 48815,
      nonce: nonce + (skip ? 1 : 0),
      executor: e.account.address,
    });
  }
  await e.tx(
    s,
    id,
    {
      type: "eip7702",
      to: authority.address,
      authorizationList: authorization ? [authorization] : [],
      data:
        delegate === zeroAddress
          ? "0x"
          : data(e, "ToolCalls", revert ? "fail" : "setValue", revert ? [] : [largeID]),
    },
    { expected: revert ? "reverted" : "success" },
  );
  if (!skip) s.entities.currentDelegate = delegate;
  const code = await e.rpc.getCode({
    address: authority.address,
    blockHash: s.steps[id].receipt.blockHash,
  });
  const expectedCode =
    s.entities.currentDelegate === zeroAddress
      ? "0x"
      : "0xef0100" + s.entities.currentDelegate.slice(2);
  if ((code ?? "0x").toLowerCase() !== expectedCode.toLowerCase())
    throw Error("Delegation code differs at transaction block");
  e.check(s, "delegation", "/api/v1/addresses/" + authority.address + "/delegation", {
    "data.status": s.entities.currentDelegate === zeroAddress ? "not_delegated" : "delegated",
    ...(s.entities.currentDelegate === zeroAddress
      ? {}
      : { "data.delegate": s.entities.currentDelegate }),
  });
  if (!skip)
    e.check(s, "delegation history", "/api/v1/addresses/" + authority.address + "/delegations", {
      "data.0.authority": authority.address,
      "data.0.transaction_hash": s.steps[id].hash,
    });
  e.check(
    s,
    id + " authorizations",
    "/api/v1/transactions/" + s.steps[id].hash + "/authorizations",
    { "data.state": "complete", "data.items.0.application_status": skip ? "skipped" : "applied" },
  );
}
async function contractSamples(e, s) {
  await e.deploy(s, "verified", "ToolCalls");
  await e.deploy(s, "sameCode", "ToolCalls");
  await e.deploy(s, "unverified", "ToolUnverified");
  await e.deploy(s, "proxyReuse", "ERC1967Proxy", [
    s.entities.sameCode,
    data(e, "ToolCalls", "setValue", [11n]),
  ]);
  await e.verify(s, ["verified"]);
  e.check(s, "read-only artifact reuse", "/api/v1/contracts/" + s.entities.proxyReuse + "/proxy", {
    "data.implementation.address": s.entities.sameCode,
    "data.implementation.verification_state": "unverified",
    "data.implementation.artifact_resolution": "code_hash",
  });
}
async function wallet(e, s) {
  if (!e.run.state.to) throw Error("wallet requires --to ADDRESS");
  s.entities.wallet = e.run.state.to;
  await e.tx(s, "eth", { to: e.run.state.to, value: parseEther("2") });
  // Local USD owner is the genesis developer, independent of the run owner.
  const { mnemonicToAccount } = await import("viem/accounts");
  const developer = mnemonicToAccount(
    "test test test test test test test test test test test junk",
  );
  const unlock = e.lockSender(developer.address);
  try {
    await genesisCall(
      e,
      s,
      "local-usd",
      "LocalBillingToken",
      "transfer",
      [e.run.state.to, 1000n * 10n ** 6n],
      { account: developer },
    );
  } finally {
    e.unlockSender(unlock, s.steps["local-usd"]);
  }
  await tokens(e, s);
  await e.call(s, "wallet-erc20", s.entities.erc20, "ToolERC20", "transfer", [
    e.run.state.to,
    1000n * 10n ** 18n,
  ]);
  await e.call(s, "wallet-erc721", s.entities.erc721, "ToolERC721", "transferFrom", [
    e.account.address,
    e.run.state.to,
    largeID,
  ]);
  await e.call(s, "wallet-erc1155", s.entities.erc1155, "ToolERC1155", "safeTransferFrom", [
    e.account.address,
    e.run.state.to,
    largeID,
    10n,
    "0x",
  ]);
  await tokenChecks(e, s);
}
export async function setup(e, s) {
  if (s.name.startsWith("proxy.")) await proxies(e, s);
  else
    switch (s.name) {
      case "transactions":
        await transactions(e, s);
        break;
      case "blob":
        for (const count of [1, 2])
          await e.tx(s, "blob-" + count, {}, { prepared: () => e.prepareBlob(count) });
        break;
      case "tokens":
        await tokens(e, s);
        break;
      case "ens":
        await ens(e, s);
        break;
      case "aa":
        await aa(e, s);
        break;
      case "contracts":
        await contractSamples(e, s);
        break;
      case "eip7702":
        await eip7702(e, s);
        break;
      case "wallet":
        await wallet(e, s);
        break;
      default:
        throw Error("Unknown scenario");
    }
  s.setupComplete = true;
  s.status = "mined";
  e.run.save();
}
export async function action(e, s, name, id) {
  const entity = s.entities;
  if (name === "activity") {
    await e.tx(s, id, {
      to: s.name === "wallet" ? e.run.state.to : (entity.recipient ?? e.account.address),
      value: 10000000000000001n,
    });
    return;
  }
  if (s.name.startsWith("proxy.")) {
    if (name === "unauthorized") {
      const other = actor(e.run, "recipient");
      await e.tx(s, id + "-fund", { to: other.address, value: parseEther("0.1") });
      await e.call(s, id, entity.proxy, "MyAccount", "setStored", [1n], {
        account: other,
        expected: "reverted",
      });
      return;
    }
    if (name === "write") {
      const a =
        s.name === "proxy.cwia"
          ? "MyAccount"
          : s.name === "proxy.diamond"
            ? "ToolDiamondFacet"
            : "Implementation";
      await e.call(s, id, entity.proxy, a, a === "MyAccount" ? "setStored" : "setValue", [largeID]);
      const value = await e.read(entity.proxy, a, a === "MyAccount" ? "stored" : "value");
      const expected =
        s.name === "proxy.diamond" && entity.diamondReplaced ? largeID + 1000n : largeID;
      if (value !== expected) throw Error("Proxy storage write did not match");
      entity.expectedValue = String(value);
      return;
    }
    if (["add", "replace", "remove"].includes(name)) {
      const selector = toFunctionSelector(name === "replace" ? "value()" : "double(uint256)");
      await e.call(s, id, entity.proxy, "ToolDiamond", "diamondCut", [
        [
          {
            facetAddress: name === "remove" ? zeroAddress : entity.facet2,
            action: { add: 0, replace: 1, remove: 2 }[name],
            functionSelectors: [selector],
          },
        ],
        zeroAddress,
        "0x",
      ]);
      const actual = await e.read(entity.proxy, "ToolDiamond", "facetAddress", [selector]);
      if (actual.toLowerCase() !== (name === "remove" ? zeroAddress : entity.facet2).toLowerCase())
        throw Error("Diamond selector route did not change");
      if (
        name === "add" &&
        (await e.read(entity.proxy, "ToolDiamondFacetV2", "double", [7n])) !== 14n
      )
        throw Error("Diamond facet invocation failed");
      if (name === "replace") {
        const expected =
          BigInt(entity.expectedValue ?? "0") + (entity.diamondReplaced ? 0n : 1000n);
        const value = await e.read(entity.proxy, "ToolDiamondFacetV2", "value");
        if (value !== expected) throw Error("Replacement facet returned an unexpected value");
        entity.diamondReplaced = true;
        entity.expectedValue = String(value);
      }
      return;
    }
    if (name === "bad-upgrade" && !s.steps[id]) {
      s.beforeFailedUpgrade = await e.rpc.getStorageAt({ address: entity.proxy, slot: implSlot });
      e.run.save();
    }
    s.upgradeValues ??= {};
    if (!s.upgradeValues[id]) {
      s.upgradeValues[id] = [];
      for (const address of [entity.proxy, entity.proxy2].filter(Boolean))
        s.upgradeValues[id].push([
          address,
          String(await e.read(address, "Implementation", "value")),
        ]);
      e.run.save();
    }
    const target = name === "bad-upgrade" ? entity.badImplementation : entity.implementationV2;
    const options = name === "bad-upgrade" ? { expected: "reverted" } : {};
    if (s.name === "proxy.beacon")
      await e.call(s, id, entity.beacon, "UpgradeableBeacon", "upgradeTo", [target], options);
    else if (s.name === "proxy.transparent")
      await e.call(
        s,
        id,
        entity.admin,
        "ProxyAdmin",
        "upgradeAndCall",
        [entity.proxy, target, "0x"],
        options,
      );
    else
      await e.call(
        s,
        id,
        entity.proxy,
        "Implementation",
        "upgradeToAndCall",
        [target, "0x"],
        options,
      );
    for (const [address, value] of s.upgradeValues[id]) {
      if (String(await e.read(address, "Implementation", "value")) !== value)
        throw Error("Upgrade changed proxy storage");
    }
    if (name !== "bad-upgrade") {
      setProxyCheck(e, s, target);
      await e.verify(
        s,
        [],
        [entity.proxy, entity.proxy2].filter(Boolean).map((address) => [address, target]),
      );
    } else {
      const slot = await e.rpc.getStorageAt({
        address: entity.proxy,
        slot: implSlot,
        blockHash: s.steps[id].receipt.blockHash,
      });
      if (slot.toLowerCase() !== s.beforeFailedUpgrade.toLowerCase())
        throw Error("Failed upgrade changed implementation");
    }
    return;
  }
  if (s.name === "eip7702") {
    if (name === "execute") await e.call(s, id, entity.authority, "ToolCalls", "setValue", [123n]);
    else
      await authorize(
        e,
        s,
        id,
        name === "clear" ? zeroAddress : name === "revert" ? entity.delegate : entity.delegate2,
        { skip: name === "skip", revert: name === "revert" },
      );
    return;
  }
  if (s.name === "aa") {
    const ctx = await connect({
      ...e.run.state.endpoints,
      keyFile: e.run.dir + "/private/owner.key",
    });
    const rejection =
      name === "bad-signature" ? [-32507] : name === "insufficient-prefund" ? [-32000] : [];
    await e.userop(
      s,
      id,
      async () => {
        const p = await prepareOperation(ctx, {
          salt: BigInt(s.salt) + (name === "insufficient-prefund" ? 1000000n : 0n),
        });
        if (name === "revert")
          p.operation.callData = encodeFunctionData({
            abi: parseAbi(["function execute(address dest,uint256 value,bytes func)"]),
            functionName: "execute",
            args: [contracts.PreviewRegistrarController.address, 0n, "0xdeadbeef"],
          });
        p.hash = getUserOperationHash({
          chainId: 48815,
          entryPointAddress: contracts.EntryPoint.address,
          entryPointVersion: "0.9",
          userOperation: p.operation,
        });
        p.operation.signature = await (
          name === "bad-signature" ? actor(e.run, "recipient") : e.account
        ).sign({ hash: p.hash });
        return { hash: p.hash, operation: rpcOperation(p.operation) };
      },
      name === "revert" ? "reverted" : "success",
      rejection,
    );
    return;
  }
  if (s.name === "contracts") {
    await e.verify(s, ["unverified", "sameCode"]);
    e.check(s, "read-only artifact reuse", "/api/v1/contracts/" + entity.proxyReuse + "/proxy", {
      "data.implementation.address": entity.sameCode,
      "data.implementation.verification_state": "verified",
      "data.implementation.artifact_resolution": "exact_address",
    });
    return;
  }
  if (s.name === "tokens") {
    const recipient = entity.recipient;
    if (name === "transfer") {
      await e.call(s, id + "-20", entity.erc20, "ToolERC20", "transfer", [
        recipient,
        1000000000000000001n,
      ]);
      await e.call(s, id + "-721", entity.erc721, "ToolERC721", "transferFrom", [
        e.account.address,
        recipient,
        largeID,
      ]);
      await e.call(s, id + "-1155", entity.erc1155, "ToolERC1155", "safeBatchTransferFrom", [
        e.account.address,
        recipient,
        [largeID, largeID + 1n],
        [2n, 3n],
        "0x",
      ]);
    } else if (name === "approve") {
      await e.call(s, id + "-20", entity.erc20, "ToolERC20", "approve", [recipient, largeID]);
      await e.call(s, id + "-721", entity.erc721, "ToolERC721", "setApprovalForAll", [
        recipient,
        true,
      ]);
      await e.call(s, id + "-1155", entity.erc1155, "ToolERC1155", "setApprovalForAll", [
        recipient,
        true,
      ]);
    } else if (name === "burn") {
      await e.call(s, id + "-20", entity.erc20, "ToolERC20", "burn", [10n]);
      await e.call(s, id + "-721", entity.erc721, "ToolERC721", "burn", [largeID + 2n]);
      await e.call(s, id + "-1155", entity.erc1155, "ToolERC1155", "burn", [largeID, 1n]);
    } else
      await e.call(s, id, entity.erc721, "ToolERC721", "setURI", [
        largeID,
        name === "metadata-update" ? entity.metadataUpdated : entity.metadataInvalid,
      ]);
    await tokenChecks(e, s);
    if (name === "transfer")
      e.check(s, "NFT", "/api/v1/nfts/" + entity.erc721 + "/" + largeID, {
        "data.token_id": String(largeID),
        "data.owner": recipient,
      });
    if (name === "metadata-update") metadataCheck(e, s, "available", "Preview Tool NFT updated");
    if (name === "metadata-invalid") metadataCheck(e, s, "unsafe");
    if (name.startsWith("metadata-")) {
      const check = s.checks.find((c) => c.label === "NFT metadata");
      check.expected["data.observation.block_hash"] = s.steps[id].receipt.blockHash;
      await e.wait(
        async () => {
          const r = await request(e.run.state.endpoints.app, check.route);
          return r.status === 200 && matches(r.body, check.expected);
        },
        Boolean,
        "metadata action publication",
      );
      entity.metadataResults ??= [];
      if (!entity.metadataResults.some((r) => r.action === id))
        entity.metadataResults.push({ action: id, ...check.expected });
      e.run.save();
    }
    return;
  }
  if (s.name === "ens") {
    const next = actor(e.run, "ens-next");
    const token = BigInt(keccak256(toHex(entity.name.split(".")[0])));
    if (name === "renew") {
      const p = await readGenesis(e, "PreviewRegistrarController", "rentPrice", [
        entity.name.split(".")[0],
        2419200n,
      ]);
      await genesisCall(
        e,
        s,
        id,
        "PreviewRegistrarController",
        "renew",
        [entity.name.split(".")[0], 2419200n, zeroHash],
        { value: p.base },
      );
    } else if (name === "transfer") {
      await e.tx(s, id + "-fund", { to: next.address, value: parseEther("1") });
      await genesisCall(
        e,
        s,
        id,
        "BaseRegistrarImplementation",
        "transferFrom",
        [entity.owner, next.address, token],
        { account: entity.owner === next.address ? next : e.account },
      );
      entity.owner = next.address;
    } else if (name === "reclaim") {
      await genesisCall(e, s, id, "BaseRegistrarImplementation", "reclaim", [token, next.address], {
        account: next,
      });
      entity.controller = next.address;
    } else {
      const account = entity.controller === next.address ? next : e.account;
      if (name === "set-primary")
        await genesisCall(e, s, id, "DefaultReverseRegistrar", "setName", [entity.name], {
          account,
        });
      else
        await genesisCall(
          e,
          s,
          id,
          "PublicResolver",
          "setAddr",
          [
            namehash(entity.name),
            BigInt(manifest.coinType),
            name === "mismatch" ? actor(e.run, "recipient").address : account.address,
          ],
          { account },
        );
    }
    if (name === "set-address" || name === "mismatch")
      entity.resolved =
        name === "mismatch"
          ? actor(e.run, "recipient").address
          : (entity.controller ?? e.account.address);
    e.check(s, "name", "/api/v1/names/" + entity.name, {
      "data.name": entity.name,
      "data.state": "indexed",
      "data.registration_state": "registered",
      "data.registrant.value": entity.owner,
      "data.controller.value": entity.controller ?? e.account.address,
      "data.resolved_address.value": entity.resolved ?? e.account.address,
    });
    return;
  }
  throw Error("Unsupported action");
}
