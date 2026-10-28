import assert from "node:assert/strict";

import { hexToNumber } from "viem";
import { privateKeyToAccount } from "viem/accounts";

const DEFAULT_GAS_LIMIT = "0xe4e1c0";
const LOCAL_SIGNING_GAS_PRICE_FLOOR = 1_000_000_000n;

function localAccount(privateKey) {
  if (!privateKey) return undefined;

  try {
    return privateKeyToAccount(privateKey);
  } catch {
    // Do not attach the source error: malformed key errors must never expose
    // the configured secret in CI or Preview diagnostics.
    throw new Error("ETHERVIEW_HARDHAT3_PRIVATE_KEY must be a valid private key");
  }
}

/**
 * Return a transaction sender that preserves the unlocked-account RPC path by
 * default and uses local raw signing only when a private key is configured.
 */
export function createTransactionSender({ rpc, from, privateKey, gasLimit = DEFAULT_GAS_LIMIT }) {
  assert.equal(typeof rpc, "function", "rpc must be a function");
  const account = localAccount(privateKey);
  if (account) {
    assert.equal(
      account.address.toLowerCase(),
      from.toLowerCase(),
      "ETHERVIEW_HARDHAT3_PRIVATE_KEY does not match the transaction owner",
    );
  }

  return async function sendTransaction(transaction) {
    const request = { from, gas: gasLimit, ...transaction };
    assert.equal(
      request.from.toLowerCase(),
      from.toLowerCase(),
      "transaction from must match the configured owner",
    );

    if (!account) {
      return rpc("eth_sendTransaction", [request]);
    }

    const [chainID, nonce, gasPrice] = await Promise.all([
      rpc("eth_chainId"),
      rpc("eth_getTransactionCount", [from, "pending"]),
      rpc("eth_gasPrice"),
    ]);
    const suggestedGasPrice = BigInt(gasPrice);
    const rawTransaction = await account.signTransaction({
      type: "legacy",
      chainId: hexToNumber(chainID),
      nonce: hexToNumber(nonce),
      gas: BigInt(request.gas),
      // An empty dev chain may suggest one wei even though its transaction
      // pool still enforces the normal minimum priority fee.
      gasPrice:
        suggestedGasPrice > LOCAL_SIGNING_GAS_PRICE_FLOOR
          ? suggestedGasPrice
          : LOCAL_SIGNING_GAS_PRICE_FLOOR,
      to: request.to,
      data: request.data ?? "0x",
      value: BigInt(request.value ?? "0x0"),
    });
    return rpc("eth_sendRawTransaction", [rawTransaction]);
  };
}
