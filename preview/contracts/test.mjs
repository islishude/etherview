import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import net from "node:net";
import { spawn } from "node:child_process";
import {
  encodeAbiParameters,
  namehash,
  zeroHash,
  keccak256,
  toHex,
  decodeAbiParameters,
  parseAbiParameters,
} from "viem";
import {
  connect,
  contracts,
  manifest,
  quote,
  commitName,
  registerName,
  manageName,
  resolveName,
  readState,
  localName,
} from "./client.mjs";
const genesis = JSON.parse(fs.readFileSync(new URL("../genesis.json", import.meta.url)));
test("manifest and allocation bind real genesis code and funded isolated identities", () => {
  const alloc = JSON.parse(fs.readFileSync(new URL("allocation.json", import.meta.url)));
  for (const [address, account] of Object.entries(alloc))
    assert.deepEqual(genesis.alloc[address.slice(2)], account);
  for (const item of Object.values(contracts))
    assert.equal(keccak256(alloc[item.address.toLowerCase()].code), item.codeHash);
  assert.equal(
    contracts.EntryPoint.address.toLowerCase(),
    "0x433709009b8330fda32311df1c2afa402ed8d009",
  );
  assert.equal(genesis.alloc.f39fd6e51aad88f6f4ce6ab8827279cfffb92266.nonce, undefined);
  for (const address of [
    "322619fcccc741880ee7071cbe4e877fb919ecf8",
    "8f3acfd2738a732aa1e0367480559f13e8def69c",
    "ca20cad9ae12f7e5855e07e16c174dedf53d4333",
    "bcd4042de499d14e55001ccbb24a551f3b954096",
  ])
    assert.equal(genesis.alloc[address], undefined, "obsolete Alto allocation");

  assert.equal(genesis.alloc["70997970c51812dc3a010c7d01b50e0d17dc79c8"].nonce, "0x1");
  assert.ok(BigInt(alloc[manifest.devAccounts.ensOwner.address.toLowerCase()].nonce) > 0n);
  assert.throws(() => localName("name.eth"));
  assert.throws(() => localName("x.test"));
  assert.equal(localName("HELLO.test"), "hello.test");
});
test(
  "real genesis ENS registration, ownership, reverse matching and expiry",
  { timeout: 120000 },
  async (t) => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "etherview-ens-contracts-"));
    t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
    const server = net.createServer();
    await new Promise((r) => server.listen(0, "127.0.0.1", r));
    const port = server.address().port;
    await new Promise((r) => server.close(r));
    const child = spawn(
      process.env.ANVIL || "anvil",
      [
        "--host",
        "127.0.0.1",
        "--port",
        String(port),
        "--accounts",
        "0",
        "--chain-id",
        "48815",
        "--hardfork",
        "cancun",
        "--init",
        path.resolve(import.meta.dirname, "../genesis.json"),
        "--silent",
      ],
      { stdio: "ignore" },
    );
    t.after(async () => {
      child.kill("SIGTERM");
      if (child.exitCode === null) await new Promise((r) => child.once("exit", r));
    });
    const rpc = `http://127.0.0.1:${port}`;
    let ctx;
    for (let i = 0; i < 100; i++) {
      try {
        ctx = await connect({ rpc });
        break;
      } catch {
        await new Promise((r) => setTimeout(r, 100));
      }
    }
    assert.ok(ctx, "Anvil did not start");
    const mine = async (seconds) => {
      await ctx.rpc.request({ method: "evm_increaseTime", params: [seconds] });
      await ctx.rpc.request({ method: "evm_mine", params: [] });
    };
    const state = path.join(dir, "name.json");
    const name = "alice.test";
    assert.equal((await quote(ctx, name)).available, true);
    await commitName(ctx, name, state, 2419200n);
    assert.equal(fs.statSync(state).mode & 0o777, 0o600);
    await assert.rejects(() => commitName(ctx, name, state));
    await assert.rejects(() => registerName(ctx, state), /too new/);
    const saved = readState(state);
    const registration = { ...saved.registration, duration: BigInt(saved.registration.duration) };
    await assert.rejects(() =>
      ctx.write("PreviewRegistrarController", "register", [registration], 2419200n * 1000000000n),
    );
    await mine(3);
    await assert.rejects(() =>
      ctx.write("PreviewRegistrarController", "register", [registration], 0n),
    );
    await registerName(ctx, state);
    assert.equal((await quote(ctx, name)).available, false);
    const duplicate = path.join(dir, "duplicate.json");
    await commitName(ctx, name, duplicate, 2419200n);
    await mine(3);
    await assert.rejects(() => registerName(ctx, duplicate));
    const resolved = await resolveName(ctx, name);
    assert.equal(
      decodeAbiParameters(parseAbiParameters("bytes"), resolved[0])[0].toLowerCase(),
      ctx.account.address.toLowerCase(),
    );
    const reverse = await ctx.read("UniversalResolver", "reverse", [
      ctx.account.address,
      BigInt(manifest.coinType),
    ]);
    assert.equal(reverse[0], name);
    const expires = await ctx.read("BaseRegistrarImplementation", "nameExpires", [
      BigInt(keccak256(toHex("alice"))),
    ]);
    await manageName(ctx, "renew", name, { duration: 2419200 });
    assert.equal(
      await ctx.read("BaseRegistrarImplementation", "nameExpires", [
        BigInt(keccak256(toHex("alice"))),
      ]),
      expires + 2419200n,
    );
    const next = await connect({ rpc, index: 19 });
    await assert.rejects(() => manageName(next, "set-address", name));
    await manageName(ctx, "transfer", name, { to: next.account.address });
    await manageName(next, "reclaim", name);
    await assert.rejects(() => manageName(ctx, "set-address", name));
    await manageName(next, "set-address", name);
    await manageName(next, "set-primary", name);
    await assert.rejects(() =>
      ctx.read("UniversalResolver", "reverse", [ctx.account.address, BigInt(manifest.coinType)]),
    );
    assert.equal(
      (
        await next.read("UniversalResolver", "reverse", [
          next.account.address,
          BigInt(manifest.coinType),
        ])
      )[0],
      name,
    );
    const expired = path.join(dir, "expired.json");
    await commitName(ctx, "expired.test", expired);
    await mine(86401);
    await assert.rejects(() => registerName(ctx, expired), /expired/);
    const expiredRegistration = readState(expired).registration;
    await assert.rejects(() =>
      ctx.write(
        "PreviewRegistrarController",
        "register",
        [{ ...expiredRegistration, duration: BigInt(expiredRegistration.duration) }],
        31536000n * 1000000000n,
      ),
    );
    await mine(56 * 86400);
    assert.equal(
      (await quote(ctx, name)).available,
      false,
      "expired names remain reserved during the grace period",
    );
    await mine(90 * 86400);
    assert.equal((await quote(ctx, name)).available, true);
  },
);

test("Preview x402 code, constructor storage and settlement funds exist at genesis", () => {
  const token = genesis.alloc["8464135c8f25da09e49bc8782676a84730c318bc"];
  assert.ok(token.code.length > 2);
  assert.equal(token.nonce, "0x1");
  const storage = token.storage;
  const slot = (n) => "0x" + BigInt(n).toString(16).padStart(64, "0");
  assert.equal(BigInt(storage[slot(2)]), 1000000000000000000n);
  const balanceSlot = keccak256(
    encodeAbiParameters(
      [{ type: "address" }, { type: "uint256" }],
      ["0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266", 3n],
    ),
  );
  assert.equal(BigInt(storage[balanceSlot]), 1000000000000000000n);
  assert.equal(Buffer.from(storage[slot(0)].slice(2, 20), "hex").toString(), "Local USD");
  assert.equal(
    BigInt(genesis.alloc["70997970c51812dc3a010c7d01b50e0d17dc79c8"].balance),
    10n ** 19n,
  );
});
