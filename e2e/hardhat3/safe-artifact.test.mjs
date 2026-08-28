import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { keccak256 } from "ethers";

const SAFE_VERSION = "1.4.1";
const SAFE_PROXY_RUNTIME_HASH =
  "0xd7d408ebcd99b2b70be43e20253d6d92a8ea8fab29bd3be7f55b10032331fb4c";

async function packageJSON(path) {
  return JSON.parse(await readFile(new URL(path, import.meta.url)));
}

test("pins the official Safe 1.4.1 deployment artifacts", async () => {
  const metadata = await packageJSON(
    "./node_modules/@safe-global/safe-contracts/package.json",
  );
  assert.equal(metadata.version, SAFE_VERSION);

  const safe = await packageJSON(
    "./node_modules/@safe-global/safe-contracts/build/artifacts/contracts/Safe.sol/Safe.json",
  );
  const factory = await packageJSON(
    "./node_modules/@safe-global/safe-contracts/build/artifacts/contracts/proxies/SafeProxyFactory.sol/SafeProxyFactory.json",
  );
  const proxy = await packageJSON(
    "./node_modules/@safe-global/safe-contracts/build/artifacts/contracts/proxies/SafeProxy.sol/SafeProxy.json",
  );

  assert.equal(safe.contractName, "Safe");
  assert.ok(
    safe.abi.some((item) => item.type === "function" && item.name === "setup"),
  );
  assert.equal(factory.contractName, "SafeProxyFactory");
  const create = factory.abi.find(
    (item) => item.type === "function" && item.name === "createProxyWithNonce",
  );
  assert.deepEqual(
    create?.inputs.map((input) => input.type),
    ["address", "bytes", "uint256"],
  );
  const event = factory.abi.find(
    (item) => item.type === "event" && item.name === "ProxyCreation",
  );
  assert.deepEqual(
    event?.inputs.map((input) => ({ type: input.type, indexed: input.indexed })),
    [
      { type: "address", indexed: true },
      { type: "address", indexed: false },
    ],
  );
  assert.equal(proxy.contractName, "SafeProxy");
  assert.deepEqual(
    proxy.abi.find((item) => item.type === "constructor")?.inputs.map(
      (input) => input.type,
    ),
    ["address"],
  );
  assert.equal((proxy.deployedBytecode.length - 2) / 2, 171);
  assert.equal(keccak256(proxy.deployedBytecode), SAFE_PROXY_RUNTIME_HASH);
});
