import { parseArgs } from "node:util";

import { parseEther } from "viem";

import {
  connect,
  manifest,
  stringify,
  quote,
  commitName,
  registerName,
  manageName,
  resolveName,
  smartAddress,
  fundAccount,
  prepareOperation,
  sendOperation,
  operationReceipt,
} from "./client.mjs";
const { values, positionals } = parseArgs({
  allowPositionals: true,
  options: Object.fromEntries(
    ["rpc", "bundler", "index", "key-file", "state", "duration", "to", "value", "data", "salt"].map(
      (k) => [k, { type: "string" }],
    ),
  ),
});
const [domain, action, name] = positionals;
try {
  if (domain === "addresses") {
    process.stdout.write(stringify(manifest));
  } else {
    const ctx = await connect({ ...values, keyFile: values["key-file"] });
    let result;
    if (domain === "ens") {
      switch (action) {
        case "quote":
          result = await quote(ctx, name, BigInt(values.duration ?? 31536000));
          break;
        case "commit":
          if (!values.state) throw new Error("An explicit --state path is required");
          result = await commitName(ctx, name, values.state, BigInt(values.duration ?? 31536000));
          break;
        case "register":
          if (!values.state) throw new Error("An explicit --state path is required");
          result = await registerName(ctx, values.state);
          break;
        case "resolve":
          result = await resolveName(ctx, name);
          break;
        default:
          result = await manageName(ctx, action, name, values);
      }
    } else if (domain === "aa") {
      const salt = BigInt(values.salt ?? 0);
      const options = {
        salt,
        to: values.to,
        value: parseEther(values.value ?? "0"),
        data: values.data ?? "0x",
      };
      switch (action) {
        case "address":
          result = { address: await smartAddress(ctx, salt) };
          break;
        case "fund":
          result = await fundAccount(ctx, salt, parseEther(values.value ?? "1"));
          break;
        case "estimate":
          result = await prepareOperation(ctx, options);
          break;
        case "send":
          if (!values.state) throw new Error("An explicit --state path is required");
          result = await sendOperation(ctx, values.state, options);
          break;
        case "receipt":
          if (!values.state) throw new Error("An explicit --state path is required");
          result = await operationReceipt(ctx, values.state);
          break;
        default:
          throw new Error("Unknown AA operation");
      }
    } else
      throw new Error(
        "Use addresses, ens quote|commit|register|renew|transfer|reclaim|set-address|set-primary|resolve, or aa address|fund|estimate|send|receipt",
      );
    process.stdout.write(stringify(result));
  }
} catch (error) {
  // SDK nested diagnostics can contain URLs, calldata or registration secrets.
  console.error(
    error.shortMessage
      ? "Preview RPC or contract operation failed; inspect the saved transaction or operation hash."
      : error.message,
  );
  process.exitCode = 1;
}
