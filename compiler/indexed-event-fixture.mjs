// Called by go run ./e2e/cmd/eventfixture. The pinned compiler emits real EVM logs.
import fs from "node:fs";

import solc from "solc";
const source = fs.readFileSync("internal/enrich/testdata/indexed-events/Events.sol", "utf8");
const output = JSON.parse(
  solc.compile(
    JSON.stringify({
      language: "Solidity",
      sources: { "Events.sol": { content: source } },
      settings: {
        evmVersion: "cancun",
        optimizer: { enabled: true, runs: 200 },
        outputSelection: { "*": { "*": ["abi", "evm.deployedBytecode.object"] } },
      },
    }),
  ),
);
if (output.errors?.some((error) => error.severity === "error"))
  throw new Error(JSON.stringify(output.errors));
const contract = output.contracts["Events.sol"].Events;
const pair = ["abc", "-123", "0xf00d", ["1", "65535"]];
process.stdout.write(
  JSON.stringify({
    compiler: solc.version(),
    abi: contract.abi,
    bytecode: contract.evm.deployedBytecode.object,
    cases: [
      { name: "Text", values: ["", "0x"] },
      { name: "Text", values: [" 你好 🌍 ", "0x000102ff"] },
      {
        name: "Arrays",
        values: [
          ["1", "65535"],
          ["-128", "127"],
          ["0x01", "0x0203"],
        ],
      },
      { name: "Tuple", values: [pair] },
      {
        name: "Nested",
        values: [
          [pair, ["", "0", "0x", []]],
          [["1", "65535"], []],
        ],
      },
      {
        name: "Example",
        values: [
          "0x0000000000000000000000000000000000000011",
          "0x0000000000000000000000000000000000000022",
        ],
      },
      { name: "Hidden", values: ["", "0", "0x", "false"] },
    ],
  }),
);
