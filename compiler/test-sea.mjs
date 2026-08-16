import { spawnSync } from "node:child_process";
import { cpSync, mkdirSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";

const [targetRootArgument, artifactArgument] = process.argv.slice(2);
if (!targetRootArgument || !artifactArgument) {
  throw new Error("usage: node test-sea.mjs <target-root> <soljson>");
}

const targetRoot = resolve(targetRootArgument);
const executor = "/opt/etherview/solcjs/etherview-solcjs";
const artifact = "/tmp/compiler \"quoted\" \\ fixture/soljson.js";
const targetArtifact = join(targetRoot, artifact.slice(1));
mkdirSync(dirname(targetArtifact), { recursive: true, mode: 0o755 });
cpSync(resolve(artifactArgument), targetArtifact);

const input = JSON.stringify({
  language: "Solidity",
  sources: {
    "Contract.sol": {
      content:
        "contract Contract { function answer() external pure returns (uint256) { return 42; } }",
    },
  },
  settings: {
    outputSelection: { "*": { "*": ["abi", "evm.bytecode.object"] } },
  },
});
writeFileSync(join(targetRoot, "tmp", "compiler-input.json"), input);

const environment = {
  HOME: "/nonexistent",
  TMPDIR: "/tmp",
  LD_LIBRARY_PATH: "/opt/etherview/solcjs/lib",
  LANG: "C",
  LC_ALL: "C",
};
function run(args, standardInput = "") {
  return spawnSync(
    "/usr/sbin/chroot",
    ["--userspec=65532:65532", targetRoot, executor, ...args],
    {
      encoding: "utf8",
      env: environment,
      input: standardInput,
      maxBuffer: 16 << 20,
    },
  );
}

const selfTest = run(["--self-test"]);
if (
  selfTest.status !== 0 ||
  selfTest.stdout !==
    '{"schema":"etherview-solcjs-sea-self-test-v1","sea":true,"node_version":"v26.7.0","wrapper_package":"solc@0.8.36","exec_argv":["--permission","--disable-sigusr1","--no-addons","--no-global-search-paths","--max-old-space-size=384"],"permissions":"restricted","write_denied":true}'
) {
  throw new Error(
    `SEA self-test failed: status=${selfTest.status} signal=${selfTest.signal} error=${selfTest.error} stdout=${selfTest.stdout} stderr=${selfTest.stderr}`,
  );
}
if (run(["--self-test", "unexpected"]).status === 0) {
  throw new Error("SEA self-test accepted an extra protocol argument");
}

for (const permission of [
  "--allow-net",
  "--allow-child-process",
  "--allow-fs-write=*",
]) {
  const widenedSelfTest = run([`--node-options=${permission}`, "--self-test"]);
  if (widenedSelfTest.status === 0) {
    throw new Error(`SEA self-test accepted widened permission ${permission}`);
  }
}

const nodeOptions = `--node-options=--allow-fs-read=${JSON.stringify(artifact)}`;
const compilation = run(
  [nodeOptions, "--compile", artifact, "0.8.36+commit.8a079791"],
  input,
);
if (compilation.status !== 0) {
  throw new Error(`SEA compilation failed: ${compilation.stderr}`);
}
const output = JSON.parse(compilation.stdout);
if (!output.contracts?.["Contract.sol"]?.Contract) {
  throw new Error("SEA compilation did not return the expected contract");
}
if (
  run(
    [nodeOptions, "--compile", artifact, "0.8.36+commit.8a079791", "unexpected"],
    input,
  ).status === 0
) {
  throw new Error("SEA compilation accepted an extra protocol argument");
}
for (const permission of [
  "--allow-net",
  "--allow-child-process",
  "--allow-fs-write=*",
]) {
  const widenedOptions =
    `--node-options=${permission} --allow-fs-read=${JSON.stringify(artifact)}`;
  if (
    run(
      [widenedOptions, "--compile", artifact, "0.8.36+commit.8a079791"],
      input,
    ).status === 0
  ) {
    throw new Error(`SEA compilation accepted widened permission ${permission}`);
  }
}

const mismatch = run(
  [nodeOptions, "--compile", artifact, "0.8.36+commit.deadbeef"],
  input,
);
if (mismatch.status === 0) {
  throw new Error("SEA accepted a mismatched compiler version");
}

const importInput = JSON.stringify({
  language: "Solidity",
  sources: { "Contract.sol": { urls: ["file:///etc/passwd"] } },
  settings: { outputSelection: { "*": { "*": ["abi"] } } },
});
const importResult = run(
  [nodeOptions, "--compile", artifact, "0.8.36+commit.8a079791"],
  importInput,
);
if (
  importResult.status !== 0 ||
  !importResult.stdout.includes("File import callback not supported")
) {
  throw new Error("SEA unexpectedly resolved a compiler import");
}
