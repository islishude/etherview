import assert from "node:assert/strict";

const implementation = process.env.ETHERVIEW_HARDHAT3_PROXY_IMPLEMENTATION;
assert.ok(implementation, "ETHERVIEW_HARDHAT3_PROXY_IMPLEMENTATION is required");

export default [implementation, "0x"];
