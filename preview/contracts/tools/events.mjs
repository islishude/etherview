import assert from "node:assert/strict";

import { encodeFunctionData, keccak256, toHex } from "viem";

export const positionedSignature = "Positioned(bytes32,bytes32,bytes32,uint256)";
export const eventTopics = [
  keccak256(toHex(positionedSignature)),
  ...[11n, 22n, 33n].map((n) => toHex(n, { size: 32 })),
];
const route = (address, resource, filter = {}) =>
  `/api/v1/addresses/${address}/${resource}?${new URLSearchParams(filter)}`;

function checks(e, s) {
  const { verified, unverified, proxy } = s.entities;
  e.check(s, "event log page size", route(verified, "logs"), { "data.length": 25 });
  e.check(s, "unverified raw log", route(unverified, "logs"), {
    "data.0.log.address": unverified,
    "data.0.log.topics.length": 4,
    "data.0.transaction_hash": s.steps["raw-events"].hash,
  });
  for (const [i, topic] of eventTopics.entries())
    e.check(s, `exact topic${i}`, route(verified, "logs", { [`topic${i}`]: topic }), {
      data: { $contains: { "log.address": verified, [`log.topics.${i}`]: topic } },
    });
  if (e.options["no-verify"]) return;
  for (const signature of ["Changed(uint256)", "Changed(bytes32)", "Hidden(bytes)"])
    e.check(
      s,
      `historical name ${signature}`,
      route(verified, "log-events", { name: signature.split("(")[0] }),
      {
        data: { $contains: { signature } },
      },
    );
  e.check(s, "overloaded names", route(verified, "log-events", { name: "Changed" }), {
    "data.length": 2,
  });
  e.check(s, "anonymous signature", route(verified, "logs", { event_signature: "Hidden(bytes)" }), {
    data: { $contains: { "log.decoding.signature": "Hidden(bytes)", "log.topics.length": 0 } },
  });
  e.check(
    s,
    "proxy historical signature",
    route(proxy, "logs", { event_signature: "Changed(uint256)" }),
    {
      "data.length": 3,
      "data.0.log.address": proxy,
      "data.0.log.decoding.signature": "Changed(uint256)",
      "data.0.transaction_hash": s.steps["proxy-events"].hash,
    },
  );
  e.check(s, "unverified event directory", route(unverified, "log-events"), { "data.length": 0 });
}

export async function setupEvents(e, s) {
  const implementation = await e.deploy(s, "verified", "ToolEvents");
  await e.deploy(s, "nextImplementation", "ToolEventsV2");
  await e.deploy(s, "unverified", "ToolRawEvents");
  const initialize = encodeFunctionData({
    abi: e.artifacts.ToolEvents.abi,
    functionName: "initialize",
    args: [e.account.address, 0n],
  });
  await e.deploy(s, "proxy", "ERC1967Proxy", [implementation, initialize]);
  await e.verify(
    s,
    ["verified", "nextImplementation", "proxy"],
    [[s.entities.proxy, implementation]],
  );
  await e.call(s, "verified-events", implementation, "ToolEvents", "emitSamples", [30n]);
  await e.call(s, "proxy-events", s.entities.proxy, "ToolEvents", "emitSamples", [3n]);
  await e.call(s, "raw-events", s.entities.unverified, "ToolRawEvents", "emitRaw");
  checks(e, s);
}

export async function actionEvents(e, s, name, id) {
  if (name === "emit") {
    await e.call(s, id, s.entities.verified, "ToolEvents", "emitSamples", [30n]);
  } else if (name === "upgrade") {
    await e.call(s, id, s.entities.proxy, "ToolEvents", "upgradeToAndCall", [
      s.entities.nextImplementation,
      "0x",
    ]);
    await e.verify(s, [], [[s.entities.proxy, s.entities.nextImplementation]]);
    await e.call(s, id + "-events", s.entities.proxy, "ToolEventsV2", "emitAfterUpgrade");
    if (!e.options["no-verify"])
      e.check(
        s,
        "proxy new signature",
        route(s.entities.proxy, "logs", { event_signature: "AfterUpgrade(address,uint256)" }),
        {
          data: {
            $contains: {
              "log.address": s.entities.proxy,
              "log.decoding.signature": "AfterUpgrade(address,uint256)",
            },
          },
        },
      );
  } else throw Error("Unknown event action");
  checks(e, s);
}

export function assertEventPages(first, second, address) {
  assert.equal(first.data.length, 25);
  assert.ok(first.meta.next_cursor);
  assert.ok(second.data.length > 0);
  const rows = [...first.data, ...second.data];
  assert.equal(
    new Set(rows.map((r) => `${r.block_hash}:${r.log.log_index}`)).size,
    rows.length,
    "duplicate event across pages",
  );
  for (let i = 0; i < rows.length; i++) {
    assert.equal(rows[i].log.address.toLowerCase(), address.toLowerCase());
    if (i === 0) continue;
    const previous = [
      rows[i - 1].block_number,
      rows[i - 1].transaction_index,
      rows[i - 1].log.log_index,
    ].map(BigInt);
    const current = [rows[i].block_number, rows[i].transaction_index, rows[i].log.log_index].map(
      BigInt,
    );
    const position = previous.findIndex((v, j) => v !== current[j]);
    assert.ok(
      position >= 0 && previous[position] > current[position],
      "event pages are not in descending order",
    );
  }
}

export function registerEventRanges(e, s) {
  if (s.name !== "events") return;
  const block = s.steps["verified-events"]?.receipt?.blockNumber;
  if (!block) return;
  const range = { from_block: block, to_block: block };
  e.check(
    s,
    "single-block log range",
    route(s.entities.verified, "logs", { ...range, limit: 100 }),
    { "data.length": 33, "data.0.block_number": block },
  );
  for (const [i, topic] of eventTopics.entries())
    e.check(
      s,
      `single-block topic${i}`,
      route(s.entities.verified, "logs", { ...range, [`topic${i}`]: topic }),
      { "data.length": 1, "data.0.block_number": block },
    );
  const before = s.steps.verified.receipt.blockNumber;
  e.check(
    s,
    "empty earlier range",
    route(s.entities.verified, "logs", { from_block: before, to_block: before }),
    { "data.length": 0 },
  );
  if (s.checks.some((c) => c.label === "overloaded names")) {
    e.check(
      s,
      "range-scoped overload directory",
      route(s.entities.verified, "log-events", { ...range, name: "Ch" }),
      { "data.length": 2 },
    );
    e.check(
      s,
      "empty earlier directory",
      route(s.entities.verified, "log-events", { from_block: before, to_block: before }),
      { "data.length": 0 },
    );
    const upgrade = Object.entries(s.steps).find(([id]) => id.endsWith("upgrade-events"))?.[1]
      ?.receipt?.blockNumber;
    if (upgrade) {
      e.check(
        s,
        "post-upgrade directory",
        route(s.entities.proxy, "log-events", { from_block: upgrade, to_block: upgrade }),
        { "data.length": 1, "data.0.signature": "AfterUpgrade(address,uint256)" },
      );
      e.check(
        s,
        "old name excluded after upgrade",
        route(s.entities.proxy, "log-events", {
          from_block: upgrade,
          to_block: upgrade,
          name: "Changed",
        }),
        { "data.length": 0 },
      );
    }
  }
}

// Recheck cursors from fresh read-only snapshots on every check/resume, never
// persist a cursor that later publication can legitimately invalidate.
export async function checkEventPages(e, s, request) {
  if (s.name !== "events") return;
  const address = s.entities.verified;
  const filters = [
    {},
    ...(s.checks.some((c) => c.label === "overloaded names")
      ? [{ event_signature: "Changed(uint256)" }]
      : []),
  ];
  for (const filter of filters) {
    const label = filter.event_signature ? "signature keyset pagination" : "raw keyset pagination";
    await e.wait(
      async () => {
        const first = await request(e.run.state.endpoints.app, route(address, "logs", filter));
        if ([409, 429, 503].includes(first.status)) return false;
        assert.equal(first.status, 200);
        assert.ok(first.body.meta.next_cursor, "fixture must have multiple event pages");
        const second = await request(
          e.run.state.endpoints.app,
          route(address, "logs", { ...filter, cursor: first.body.meta.next_cursor }),
        );
        if (
          [409, 429, 503].includes(second.status) ||
          (second.status === 400 && second.body.error?.code === "invalid_cursor")
        )
          return false;
        assert.equal(second.status, 200);
        assertEventPages(first.body, second.body, address);
        return true;
      },
      Boolean,
      label,
    );
    e.check(s, label, route(address, "logs", filter), { "data.length": 25 });
    s.checks.find((c) => c.label === label).status = "passed";
    e.run.save();
  }
}
