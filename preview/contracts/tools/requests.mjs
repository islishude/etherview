import { createHash } from "node:crypto";
import fs from "node:fs";

import { concatHex, encodeFunctionData, keccak256, toHex } from "viem";

import { contracts } from "../client.mjs";

const genesis = JSON.parse(fs.readFileSync(new URL("../../genesis.json", import.meta.url)));
export const requestTitles = {
  "0x00": "Validator deposit",
  "0x01": "Validator withdrawal / exit",
  "0x02": "Consolidation",
  "0x03": "Builder deposit",
  "0x04": "Builder exit",
};
export const requestContracts = {
  "0x00": "0x00000000219ab540356cBB839Cbe05303d7705Fa",
  "0x01": "0x00000961ef480eb55e80d19ad83579a64c007002",
  "0x02": "0x0000bbddc7ce488642fb579f8b00f3a590007251",
  "0x03": "0x0000bff46984e3725691fa540a8c7589300d8282",
  "0x04": "0x000064d678505ad48f8ccb093bc65613800e8282",
};
export async function requestsAvailability(rpc, features) {
  if (!features.consensus) return "Requests require consensus enrichment";
  const block = await rpc.getBlock({ blockNumber: 0n });
  for (const address of Object.values(requestContracts)) {
    const expected = Object.entries(genesis.alloc).find(
      ([key]) => key.toLowerCase() === address.slice(2).toLowerCase(),
    )?.[1].code;
    const code = await rpc.request({
      method: "eth_getCode",
      params: [address, { blockHash: block.hash, requireCanonical: true }],
    });
    if (!expected || code === "0x" || keccak256(code) !== keccak256(expected))
      return "Preview requests genesis mismatch; explicitly reinitialize using preview/docs/operations.md (existing network is never reset by this tool)";
  }
  return "available";
}
export function requestSamples(runID, owner) {
  const bytes = (label, size) =>
    "0x" +
    createHash("sha512")
      .update(runID + ":" + label)
      .digest("hex")
      .repeat(2)
      .slice(0, size * 2);
  const pubkey = (label) => bytes(label, 48);
  const credentials = "0x01" + "00".repeat(11) + owner.slice(2).toLowerCase();
  const signature = bytes("signature", 96);
  return [
    {
      request_type: "0x00",
      pubkey: pubkey("deposit"),
      withdrawal_credentials: credentials,
      amount: "1000000000",
      signature,
    },
    {
      request_type: "0x01",
      source_address: owner,
      validator_pubkey: pubkey("withdrawal"),
      amount: "0",
    },
    {
      request_type: "0x02",
      source_address: owner,
      source_pubkey: pubkey("source"),
      target_pubkey: pubkey("target"),
    },
    {
      request_type: "0x03",
      pubkey: pubkey("builder"),
      withdrawal_credentials: credentials,
      amount: "1000000000",
      signature,
    },
    { request_type: "0x04", source_address: owner, pubkey: pubkey("builder") },
  ];
}
export function requestInput(sample, fee = 0n) {
  if (fee < 0n || fee > 10n ** 15n) throw Error("Preview request fee exceeds demonstration budget");
  let data;
  let value = fee;
  switch (sample.request_type) {
    case "0x00":
      data = encodeFunctionData({
        abi: contracts.PreviewDeposit.abi,
        functionName: "deposit",
        args: [sample.pubkey, sample.withdrawal_credentials, sample.signature],
      });
      value = BigInt(sample.amount) * 10n ** 9n;
      break;
    case "0x01":
      data = concatHex([sample.validator_pubkey, toHex(BigInt(sample.amount), { size: 8 })]);
      break;
    case "0x02":
      data = concatHex([sample.source_pubkey, sample.target_pubkey]);
      break;
    case "0x03":
      data = concatHex([
        sample.pubkey,
        sample.withdrawal_credentials,
        toHex(BigInt(sample.amount), { size: 8 }),
        sample.signature,
      ]);
      value += BigInt(sample.amount) * 10n ** 9n;
      break;
    case "0x04":
      data = sample.pubkey;
      break;
    default:
      throw Error("Unknown request type");
  }
  return { to: requestContracts[sample.request_type], data, value };
}
export async function setupRequests(e, s) {
  s.entities.requests ??= requestSamples(e.run.state.id, e.account.address);
  e.run.save();
  for (const sample of s.entities.requests) {
    const id = "request-" + sample.request_type;
    // A journaled transaction must be reconciled without requoting or resubmitting.
    if (s.steps[id]) {
      await e.tx(s, id, {});
      continue;
    }
    let fee = 0n;
    if (sample.request_type !== "0x00") {
      const block = await e.rpc.getBlock();
      const quote = await e.rpc.request({
        method: "eth_call",
        params: [
          { to: requestContracts[sample.request_type], data: "0x" },
          { blockHash: block.hash, requireCanonical: true },
        ],
      });
      if (!/^0x[0-9a-fA-F]{64}$/.test(quote)) throw Error("Invalid request fee quote");
      // Small headroom for fee changes between the pinned read and inclusion.
      fee = BigInt(quote) * 2n + 1n;
    }
    await e.tx(s, id, requestInput(sample, fee));
  }
}
const matches = (item, sample) =>
  Object.entries(sample).every(
    ([key, value]) => String(item[key]).toLowerCase() === String(value).toLowerCase(),
  );
export async function checkRequests(e, s, request) {
  if (s.name !== "requests") return;
  for (const sample of s.entities.requests ?? []) {
    const step = s.steps["request-" + sample.request_type];
    if (!step?.receipt) throw Error("Requests setup is unfinished; use resume");
    let number = BigInt(step.receipt.blockNumber);
    const last = number + 63n;
    let parent;
    await e.wait(
      async () => {
        if (number > last) throw Error("Request was not published within 64 blocks of submission");
        const head = await e.rpc.getBlock();
        if (number > head.number) return false;
        const block = await e.rpc.getBlock({ blockNumber: number });
        if (
          (parent && block.parentHash !== parent) ||
          (number === BigInt(step.receipt.blockNumber) && block.hash !== step.receipt.blockHash)
        )
          throw Error("Request scan chain changed; reconcile with check");
        let cursor;
        for (let page = 0; page < 164; page++) {
          const query = new URLSearchParams({
            request_type: sample.request_type,
            limit: "50",
            ...(cursor ? { cursor } : {}),
          });
          const route = "/api/v1/blocks/" + block.hash + "/requests?" + query;
          const result = await request(e.run.state.endpoints.app, route);
          if (
            [404, 409, 429, 503].includes(result.status) ||
            (cursor && result.status === 400 && result.body.error?.code === "invalid_cursor")
          )
            return false;
          if (result.status !== 200) throw Error("Request publication HTTP " + result.status);
          if (result.body.data?.state !== "complete") return false;
          const item = result.body.data.items.find((item) => matches(item, sample));
          if (item) {
            s.entities.requestBlocks ??= {};
            s.entities.requestBlocks[sample.request_type] = block.hash;
            e.check(s, "request " + sample.request_type, route, {
              "data.state": "complete",
              "data.items": { $contains: sample },
            });
            s.checks.find((c) => c.label === "request " + sample.request_type).status = "passed";
            e.run.save();
            return true;
          }
          const next = result.body.meta?.next_cursor;
          if (!next) {
            parent = block.hash;
            number++;
            return false;
          }
          if (next === cursor) throw Error("Repeated requests cursor");
          cursor = next;
        }
        throw Error("Request pagination exceeded protocol bound");
      },
      Boolean,
      "request " + sample.request_type + " publication",
    );
  }
}
