import crypto from "node:crypto";
import fs from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";

import solc from "solc";

import { saveState } from "../client.mjs";

const require = createRequire(import.meta.url);
export const root = path.resolve(import.meta.dirname, "../../..");
export const digest = (value) => crypto.createHash("sha256").update(value).digest("hex");
export function compile(directory) {
  if (!solc.version().startsWith("0.8.30+")) throw Error("Unexpected fixture compiler");
  const sources = {};
  const add = (name, file) => {
    sources[name] = { content: fs.readFileSync(file, "utf8") };
  };
  add("Fixtures.sol", new URL("Fixtures.sol", import.meta.url));
  for (const file of [
    "Implementation.sol",
    "CloneFactory.sol",
    "MyAccount.sol",
    "MyAccountFactory.sol",
  ])
    add(file, path.join(root, "e2e/hardhat3/contracts", file));
  const settings = {
    optimizer: { enabled: true, runs: 200 },
    evmVersion: "cancun",
    outputSelection: {
      "*": { "*": ["abi", "evm.bytecode.object", "evm.deployedBytecode.object"] },
    },
  };
  const input = { language: "Solidity", sources, settings };
  const output = JSON.parse(
    solc.compile(JSON.stringify(input), {
      import: (name) => {
        try {
          const content = fs.readFileSync(require.resolve(name), "utf8");
          sources[name] = { content };
          return { contents: content };
        } catch {
          return { error: "Missing locked import: " + name };
        }
      },
    }),
  );
  const errors = output.errors?.filter((e) => e.severity === "error") ?? [];
  if (errors.length)
    throw Error("Fixture compilation failed: " + errors.map((e) => e.formattedMessage).join("\n"));
  const identity = digest(JSON.stringify({ compiler: solc.version(), input }));
  const artifacts = {};
  for (const [file, contracts] of Object.entries(output.contracts))
    for (const [name, value] of Object.entries(contracts)) {
      if (value.evm.bytecode.object)
        artifacts[name] = {
          name,
          file,
          abi: value.abi,
          bytecode: "0x" + value.evm.bytecode.object,
          input,
          compiler: solc.version().replace(/\.Emscripten.*$/, ""),
          identity,
        };
    }
  // Safe's canonical 1.4.1 bytecode was built with 0.7.6. Never recompile it with 0.8.x.
  for (const name of ["Safe", "SafeProxyFactory", "SafeProxy"]) {
    const folder = name === "Safe" ? "" : "proxies/";
    const artifact = require(
      "@safe-global/safe-contracts/build/artifacts/contracts/" +
        folder +
        name +
        ".sol/" +
        name +
        ".json",
    );
    artifacts[name] = {
      name,
      abi: artifact.abi,
      bytecode: artifact.bytecode,
      upstream: "@safe-global/safe-contracts@1.4.1",
      identity: digest(artifact.bytecode),
    };
  }
  if (directory) {
    fs.mkdirSync(directory, { recursive: true });
    saveState(path.join(directory, identity + ".json"), {
      identity,
      compiler: solc.version(),
      input,
      artifacts,
    });
  }
  return artifacts;
}
