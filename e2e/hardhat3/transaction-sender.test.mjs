import assert from "node:assert/strict";
import test from "node:test";
import { parseTransaction, recoverTransactionAddress } from "viem";
import { generatePrivateKey, privateKeyToAccount } from "viem/accounts";
import { createTransactionSender } from "./transaction-sender.mjs";

const TEST_PRIVATE_KEY = generatePrivateKey();
const TEST_TO = "0x0000000000000000000000000000000000000001";

test("uses eth_sendTransaction when no private key is configured", async () => {
  const calls = [];
  const rpc = async (method, params = []) => {
    calls.push({ method, params });
    return "0xunlocked";
  };
  const from = privateKeyToAccount(TEST_PRIVATE_KEY).address;
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
  const wallet = privateKeyToAccount(TEST_PRIVATE_KEY);
  const send = createTransactionSender({
    rpc,
    from: wallet.address,
    privateKey: TEST_PRIVATE_KEY,
  });

  assert.equal(await send({ to: TEST_TO, data: "0x1234", value: "0x5" }), "0xraw");
  const transaction = parseTransaction(submittedRaw);
  assert.equal(
    await recoverTransactionAddress({ serializedTransaction: submittedRaw }),
    wallet.address,
  );
  assert.equal(transaction.type, "legacy");
  assert.equal(transaction.to, TEST_TO);
  assert.equal(transaction.chainId, 31337);
  assert.equal(transaction.nonce, 2);
  assert.equal(transaction.gas, 15_000_000n);
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

test("signs contract creation with exact large values and the suggested gas price", async () => {
  const account = privateKeyToAccount(TEST_PRIVATE_KEY);
  let raw;
  const send = createTransactionSender({
    from: account.address,
    privateKey: TEST_PRIVATE_KEY,
    gasLimit: "0x5208",
    rpc: async (method, params) => {
      switch (method) {
        case "eth_chainId":
          return "0x1";
        case "eth_getTransactionCount":
          return "0x0";
        case "eth_gasPrice":
          return "0x77359400";
        case "eth_sendRawTransaction":
          [raw] = params;
          return "0xcreation";
        default:
          throw new Error(`unexpected RPC method: ${method}`);
      }
    },
  });
  const value = (1n << 200n) + 42n;
  assert.equal(await send({ data: "0x60006000", value: `0x${value.toString(16)}` }), "0xcreation");
  const transaction = parseTransaction(raw);
  assert.equal(transaction.to, undefined);
  assert.equal(transaction.nonce ?? 0, 0);
  assert.equal(transaction.gas, 21_000n);
  assert.equal(transaction.gasPrice, 2_000_000_000n);
  assert.equal(transaction.value, value);
  assert.equal(transaction.data, "0x60006000");
  assert.equal(await recoverTransactionAddress({ serializedTransaction: raw }), account.address);
});

test("redacts invalid signing keys without attaching their source error", () => {
  const secret = "invalid-private-key-must-not-be-logged";
  assert.throws(
    () => createTransactionSender({ rpc: async () => {}, from: TEST_TO, privateKey: secret }),
    (error) => {
      assert.equal(error.message, "ETHERVIEW_HARDHAT3_PRIVATE_KEY must be a valid private key");
      assert.equal(error.cause, undefined);
      assert.ok(!error.stack.includes(secret));
      return true;
    },
  );
});

for (const privateKey of [undefined, TEST_PRIVATE_KEY]) {
  test(`rejects an overridden sender before RPC (${privateKey ? "signed" : "unlocked"})`, async () => {
    const send = createTransactionSender({
      rpc: async () => assert.fail("must not call RPC"),
      from: privateKeyToAccount(TEST_PRIVATE_KEY).address,
      privateKey,
    });
    await assert.rejects(send({ from: TEST_TO }), /transaction from must match/);
  });
}

for (const field of ["eth_chainId", "eth_getTransactionCount"]) {
  test(`rejects unsafe numeric ${field} before submission`, async () => {
    const send = createTransactionSender({
      from: privateKeyToAccount(TEST_PRIVATE_KEY).address,
      privateKey: TEST_PRIVATE_KEY,
      rpc: async (method) => {
        assert.notEqual(method, "eth_sendRawTransaction");
        return method === field ? "0x20000000000000" : "0x1";
      },
    });
    await assert.rejects(send({ to: TEST_TO }), /safe integer|range|cannot be represented/i);
  });
}
