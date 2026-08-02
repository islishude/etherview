import assert from "node:assert/strict";
import test from "node:test";
import { Transaction, Wallet } from "ethers";
import { createTransactionSender } from "./transaction-sender.mjs";

const TEST_PRIVATE_KEY = Wallet.createRandom().privateKey;
const TEST_TO = "0x0000000000000000000000000000000000000001";

test("uses eth_sendTransaction when no private key is configured", async () => {
  const calls = [];
  const rpc = async (method, params = []) => {
    calls.push({ method, params });
    return "0xunlocked";
  };
  const from = new Wallet(TEST_PRIVATE_KEY).address;
  const send = createTransactionSender({ rpc, from });

  assert.equal(await send({ to: TEST_TO, data: "0x1234" }), "0xunlocked");
  assert.deepEqual(calls, [
    {
      method: "eth_sendTransaction",
      params: [{ from, gas: "0xe4e1c0", to: TEST_TO, data: "0x1234" }],
    },
  ]);
});

test("locally signs and submits an EIP-155 raw transaction", async () => {
  let submittedRaw;
  const rpc = async (method, params = []) => {
    switch (method) {
      case "eth_chainId":
        return "0x7a69";
      case "eth_getTransactionCount":
        assert.deepEqual(params.slice(1), ["pending"]);
        return "0x2";
      case "eth_gasPrice":
        return "0x1";
      case "eth_sendRawTransaction":
        [submittedRaw] = params;
        return "0xraw";
      default:
        throw new Error(`unexpected RPC method: ${method}`);
    }
  };
  const wallet = new Wallet(TEST_PRIVATE_KEY);
  const send = createTransactionSender({
    rpc,
    from: wallet.address,
    privateKey: TEST_PRIVATE_KEY,
  });

  assert.equal(
    await send({ to: TEST_TO, data: "0x1234", value: "0x5" }),
    "0xraw",
  );
  const transaction = Transaction.from(submittedRaw);
  assert.equal(transaction.from, wallet.address);
  assert.equal(transaction.to, TEST_TO);
  assert.equal(transaction.chainId, 31337n);
  assert.equal(transaction.nonce, 2);
  assert.equal(transaction.gasLimit, 15_000_000n);
  assert.equal(transaction.gasPrice, 1_000_000_000n);
  assert.equal(transaction.value, 5n);
  assert.equal(transaction.data, "0x1234");
});

test("rejects a signing key for a different owner", () => {
  assert.throws(
    () =>
      createTransactionSender({
        rpc: async () => undefined,
        from: TEST_TO,
        privateKey: TEST_PRIVATE_KEY,
      }),
    /does not match the transaction owner/,
  );
});
