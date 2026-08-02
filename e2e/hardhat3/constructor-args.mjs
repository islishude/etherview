import assert from "node:assert/strict";

const encoded = process.env.ETHERVIEW_HARDHAT3_CONSTRUCTOR_ARGS;
assert.ok(encoded, "ETHERVIEW_HARDHAT3_CONSTRUCTOR_ARGS is required");

const values = JSON.parse(encoded);
assert.ok(Array.isArray(values), "constructor arguments must be a JSON array");

export default values;
