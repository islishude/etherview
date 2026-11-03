import assert from "node:assert/strict";
import { spawn, execFileSync } from "node:child_process";
import { once } from "node:events";
import fs from "node:fs";
import { createRequire } from "node:module";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
const require = createRequire(new URL("../contracts/package.json", import.meta.url));
const { createPublicClient, createWalletClient, http, defineChain } = require("viem");
const { mnemonicToAccount } = require("viem/accounts");
const manifest = JSON.parse(
  fs.readFileSync(new URL("../contracts/manifest.json", import.meta.url)),
);
const rethImage = "ghcr.io/paradigmxyz/reth:v2.9.0";
const chain = defineChain({
  id: 31337,
  name: "Preview payment",
  nativeCurrency: { name: "Ether", symbol: "ETH", decimals: 18 },
  rpcUrls: { default: { http: [] } },
});

test(
  "Reth persists both token balances and spent authorization across restart and rejects changed seeds",
  { timeout: 120000 },
  async () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "etherview-reth-"));
    const seed = path.join(dir, "seed.json");
    fs.copyFileSync(new URL("genesis.json", import.meta.url), seed);
    const listener = net.createServer();
    await new Promise((resolve) => listener.listen(0, "127.0.0.1", resolve));
    const port = listener.address().port;
    await new Promise((resolve) => listener.close(resolve));
    await new Promise((resolve) => listener.listen(0, "127.0.0.1", resolve));
    const wsPort = listener.address().port;
    await new Promise((resolve) => listener.close(resolve));
    const transport = http(`http://127.0.0.1:${port}`, { retryCount: 0, timeout: 1000 });
    const client = createPublicClient({ chain, transport, pollingInterval: 200 });
    const payer = mnemonicToAccount("test test test test test test test test test test test junk");
    const recipient = mnemonicToAccount(
      "test test test test test test test test test test test junk",
      { addressIndex: 2 },
    ).address;
    const wallet = createWalletClient({ chain, transport, account: payer });
    const docker = globalThis.process.env.DOCKER || "docker";
    const container = `etherview-reth-${globalThis.process.pid}-${port}`;
    let process;
    let diagnostics = "";
    const start = () => {
      diagnostics = "";
      process = spawn(
        docker,
        [
          "run",
          "--rm",
          "--name",
          container,
          "--user",
          `${globalThis.process.getuid()}:${globalThis.process.getgid()}`,
          "-e",
          "HOME=/data",
          "-p",
          `127.0.0.1:${port}:8545`,
          "-p",
          `127.0.0.1:${wsPort}:8546`,
          "-v",
          `${dir}:/data`,
          "-v",
          `${fileURLToPath(new URL("./", import.meta.url))}:/fixture:ro`,
          "-e",
          "RETH_SEED_FILE=/data/seed.json",
          "--entrypoint",
          "sh",
          rethImage,
          "/fixture/entrypoint.sh",
        ],
        { stdio: ["ignore", "pipe", "pipe"] },
      );
      process.stdout.on("data", (chunk) => {
        diagnostics = (diagnostics + chunk.toString()).slice(-8192);
      });
      process.stderr.on("data", (chunk) => {
        diagnostics = (diagnostics + chunk.toString()).slice(-8192);
      });
      return process;
    };
    const ready = async () => {
      for (let attempt = 0; attempt < 400; attempt++) {
        if (process.exitCode !== null) throw Error(`Reth exited before ready: ${diagnostics}`);
        try {
          if (
            (await client.getChainId()) === 31337 &&
            (await client.getBlock()).timestamp >= BigInt(Math.floor(Date.now() / 1000) - 10)
          )
            return;
        } catch {}
        await new Promise((resolve) => setTimeout(resolve, 50));
      }
      throw Error("Reth not ready");
    };
    const stop = async () => {
      if (process && process.exitCode === null && !process.signalCode) {
        const done = once(process, "exit");
        execFileSync(docker, ["stop", "--time", "15", container], { stdio: "ignore" });
        await done;
      }
    };
    try {
      start();
      await ready();
      const socket = new WebSocket(`ws://127.0.0.1:${wsPort}`);
      try {
        await once(socket, "open", { signal: AbortSignal.timeout(5000) });
        const message = once(socket, "message", { signal: AbortSignal.timeout(5000) });
        socket.send(JSON.stringify({ jsonrpc: "2.0", id: 1, method: "eth_chainId", params: [] }));
        assert.equal(JSON.parse((await message)[0].data).result, "0x7a69");
      } finally {
        socket.close();
      }
      const first = await client.request({
        method: "eth_getBlockByNumber",
        params: ["latest", false],
      });
      assert.match(first.blockAccessListHash, /^0x[0-9a-f]{64}$/);
      assert.match(first.slotNumber, /^0x[0-9a-f]+$/);
      const genesis = JSON.parse(fs.readFileSync(seed));
      for (const [address, account] of Object.entries(genesis.alloc)) {
        if (!address.startsWith("0000")) continue;
        assert.equal(
          (await client.getCode({ address: `0x${address}` })).toLowerCase(),
          account.code.toLowerCase(),
        );
      }
      await new Promise((resolve) => setTimeout(resolve, 6500));
      const next = await client.getBlock();
      assert.ok(next.number > BigInt(first.number), "three-second timer must mine empty blocks");
      assert.ok(next.timestamp > BigInt(first.timestamp));
      execFileSync(docker, ["exec", container, "bash", "/fixture/healthcheck.sh"]);
      const nonce = "0x" + "01".repeat(32);
      const authorizationTypes = {
        TransferWithAuthorization: [
          { name: "from", type: "address" },
          { name: "to", type: "address" },
          { name: "value", type: "uint256" },
          { name: "validAfter", type: "uint256" },
          { name: "validBefore", type: "uint256" },
          { name: "nonce", type: "bytes32" },
        ],
      };
      for (const [name, decimals, domainName] of [
        ["LocalBillingToken", 6, "Local USD"],
        ["LocalBillingToken18", 18, "Local USD 18"],
      ]) {
        const { address, abi } = manifest.contracts[name];
        assert.equal(
          await client.readContract({ address, abi, functionName: "decimals" }),
          decimals,
        );
        const value = 10n ** BigInt(decimals);
        const message = {
          from: payer.address,
          to: recipient,
          value,
          validAfter: 0n,
          validBefore: 9999999999n,
          nonce,
        };
        const signature = await payer.signTypedData({
          domain: { name: domainName, version: "1", chainId: 31337, verifyingContract: address },
          types: authorizationTypes,
          primaryType: "TransferWithAuthorization",
          message,
        });
        const hash = await wallet.writeContract({
          address,
          abi,
          functionName: "transferWithAuthorization",
          args: [
            payer.address,
            recipient,
            value,
            0n,
            9999999999n,
            nonce,
            Number.parseInt(signature.slice(130), 16),
            signature.slice(0, 66),
            "0x" + signature.slice(66, 130),
          ],
        });
        assert.equal((await client.waitForTransactionReceipt({ hash })).status, "success");
        const permit2 = "0x000000000022D473030F116dDEE9F6B43aC78BA3";
        const approval = await wallet.writeContract({
          address,
          abi,
          functionName: "approve",
          args: [permit2, value],
        });
        assert.equal(
          (await client.waitForTransactionReceipt({ hash: approval })).status,
          "success",
        );
        const permitSignature = await payer.signTypedData({
          domain: { name: "Permit2", chainId: 31337, verifyingContract: permit2 },
          types: {
            TokenPermissions: [
              { name: "token", type: "address" },
              { name: "amount", type: "uint256" },
            ],
            Witness: [
              { name: "to", type: "address" },
              { name: "validAfter", type: "uint256" },
            ],
            PermitWitnessTransferFrom: [
              { name: "permitted", type: "TokenPermissions" },
              { name: "spender", type: "address" },
              { name: "nonce", type: "uint256" },
              { name: "deadline", type: "uint256" },
              { name: "witness", type: "Witness" },
            ],
          },
          primaryType: "PermitWitnessTransferFrom",
          message: {
            permitted: { token: address, amount: value },
            spender: "0x402085c248EeA27D92E8b30b2C58ed07f9E20001",
            nonce: BigInt(decimals),
            deadline: 9999999999n,
            witness: { to: recipient, validAfter: 0n },
          },
        });
        const settlement = await wallet.writeContract({
          address: manifest.contracts.X402ExactPermit2Proxy.address,
          abi: manifest.contracts.X402ExactPermit2Proxy.abi,
          functionName: "settle",
          args: [
            {
              permitted: { token: address, amount: value },
              nonce: BigInt(decimals),
              deadline: 9999999999n,
            },
            payer.address,
            { to: recipient, validAfter: 0n },
            permitSignature,
          ],
        });
        assert.equal(
          (await client.waitForTransactionReceipt({ hash: settlement })).status,
          "success",
        );
        assert.equal(
          await client.readContract({
            address,
            abi,
            functionName: "allowance",
            args: [payer.address, permit2],
          }),
          0n,
        );
      }
      const nativeBalance = await client.getBalance({ address: payer.address });
      const savedHead = await client.getBlock();
      await stop();
      start();
      await ready();
      assert.equal(await client.getBalance({ address: payer.address }), nativeBalance);
      assert.equal((await client.getBlock({ blockNumber: savedHead.number })).hash, savedHead.hash);
      await new Promise((resolve) => setTimeout(resolve, 6500));
      assert.ok(
        (await client.getBlock()).number > savedHead.number,
        "mining must resume after restart",
      );
      execFileSync(docker, ["exec", container, "bash", "/fixture/healthcheck.sh"]);
      for (const [name, decimals] of [
        ["LocalBillingToken", 6],
        ["LocalBillingToken18", 18],
      ]) {
        const { address, abi } = manifest.contracts[name];
        assert.equal(
          await client.readContract({ address, abi, functionName: "balanceOf", args: [recipient] }),
          2n * 10n ** BigInt(decimals),
        );
        assert.equal(
          await client.readContract({
            address,
            abi,
            functionName: "authorizationState",
            args: [payer.address, nonce],
          }),
          true,
        );
      }
      assert.equal(
        await client.readContract({
          address: manifest.contracts.Permit2.address,
          abi: manifest.contracts.Permit2.abi,
          functionName: "nonceBitmap",
          args: [payer.address, 0n],
        }),
        (1n << 6n) | (1n << 18n),
      );
      await stop();
      const originalSeed = fs.readFileSync(seed);
      fs.appendFileSync(seed, "\n");
      const failed = start();
      const [code] = await once(failed, "exit");
      assert.equal(code, 1);
      assert.match(diagnostics, /reth_genesis_mismatch/);
      fs.writeFileSync(seed, originalSeed);
      fs.renameSync(path.join(dir, "chain"), path.join(dir, "saved-chain"));
      const missing = start();
      assert.equal((await once(missing, "exit"))[0], 1);
      assert.match(diagnostics, /reth_state_missing/);
      fs.renameSync(path.join(dir, "saved-chain"), path.join(dir, "chain"));
      fs.unlinkSync(path.join(dir, "genesis.sha256"));
      const orphan = start();
      assert.equal((await once(orphan, "exit"))[0], 1);
      assert.match(diagnostics, /reth_identity_missing/);
    } finally {
      await stop();
      fs.rmSync(dir, { recursive: true, force: true });
    }
  },
);
