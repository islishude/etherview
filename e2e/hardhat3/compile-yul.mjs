import { createRequire } from "node:module";
import { writeFileSync } from "node:fs";

const require = createRequire(import.meta.url);
const solc = require("solc");
const outputPath =
  process.env.ETHERVIEW_HARDHAT3_YUL_SUBMISSION_FILE ??
  "/e2e-output/yul-submission.json";

const input = {
  language: "Yul",
  sources: {
    "Counter.yul": {
      content: `object "Counter" {
  code {
    datacopy(0, dataoffset("Counter_deployed"), datasize("Counter_deployed"))
    return(0, datasize("Counter_deployed"))
  }
  object "Counter_deployed" {
    code {
      mstore(0, 42)
      return(0, 32)
    }
  }
}`,
    },
  },
  settings: {
    evmVersion: "shanghai",
    optimizer: { enabled: false },
    outputSelection: {
      "*": {
        "*": ["evm.bytecode.object", "evm.deployedBytecode.object"],
      },
    },
  },
};

const output = JSON.parse(solc.compile(JSON.stringify(input)));
const errors = (output.errors ?? []).filter(
  (diagnostic) => diagnostic.severity === "error",
);
if (errors.length > 0) {
  throw new Error(`Yul fixture compilation failed: ${JSON.stringify(errors)}`);
}
const contract = output.contracts?.["Counter.yul"]?.Counter;
if (
  typeof contract?.evm?.bytecode?.object !== "string" ||
  contract.evm.bytecode.object.length === 0 ||
  typeof contract?.evm?.deployedBytecode?.object !== "string" ||
  contract.evm.deployedBytecode.object.length === 0
) {
  throw new Error("Yul fixture compiler output is incomplete");
}
const compilerVersion = solc
  .version()
  .replace(/^v/, "")
  .replace(/\.Emscripten\.clang$/, "");
if (compilerVersion !== "0.8.30+commit.73712a01") {
  throw new Error(`unexpected Yul fixture compiler ${compilerVersion}`);
}

writeFileSync(
  outputPath,
  `${JSON.stringify({
    language: "yul",
    compiler_version: compilerVersion,
    input,
    bytecodes: {
      creation_bytecode: `0x${contract.evm.bytecode.object}`,
      runtime_bytecode: `0x${contract.evm.deployedBytecode.object}`,
    },
    contract_name_hint: "Counter.yul:Counter",
  })}\n`,
  // This contains only the fixed public fixture and is retained for CI
  // diagnostics. The container runs as root, so the host uploader needs read.
  { encoding: "utf8", mode: 0o644 },
);
