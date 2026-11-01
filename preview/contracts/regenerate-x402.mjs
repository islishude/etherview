// Explicit upstream regeneration; never called at application startup.
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
const dir = fs.mkdtempSync(path.join(os.tmpdir(), "etherview-x402-contracts-"));
const run = (cmd, args, cwd) => execFileSync(cmd, args, { cwd, stdio: "pipe" });
const sources = [
  {
    name: "Permit2",
    repo: "Uniswap/permit2",
    commit: "cc56ad0f3439c502c246fc5cfcc3db92bb8b7219",
    root: "",
    file: "Permit2.sol",
    contract: "Permit2",
    immutableNames: ["cachedDomainSeparator", "cachedChainId"],
  },
  {
    name: "X402ExactPermit2Proxy",
    repo: "x402-foundation/x402",
    commit: "71eb9a55e081e7b81ba3046d0bd17c3eb9c7bf81",
    root: "contracts/evm",
    file: "x402ExactPermit2Proxy.sol",
    contract: "x402ExactPermit2Proxy",
    immutableNames: ["permit2"],
  },
];
try {
  if (!run("forge", ["--version"]).toString().includes("1.8.7"))
    throw Error("forge 1.8.7 required");
  const result = {};
  for (const source of sources) {
    const checkout = path.join(dir, source.name);
    fs.mkdirSync(checkout);
    run("git", ["init", "-q"], checkout);
    run(
      "git",
      ["fetch", "--depth", "1", `https://github.com/${source.repo}.git`, source.commit],
      checkout,
    );
    run("git", ["checkout", "--detach", "FETCH_HEAD"], checkout);
    run("git", ["submodule", "update", "--init", "--recursive", "--depth", "1"], checkout);
    const root = path.join(checkout, source.root);
    run("forge", ["build", "--skip", "test", "--skip", "script"], root);
    const artifact = JSON.parse(
      fs.readFileSync(path.join(root, "out", source.file, `${source.contract}.json`)),
    );
    const settings = structuredClone(artifact.metadata.settings);
    delete settings.compilationTarget;
    settings.remappings = settings.remappings.filter((r) => !r.startsWith("/"));
    settings.outputSelection = {
      "*": { "*": ["abi", "evm.bytecode", "evm.deployedBytecode", "metadata"] },
    };
    const verification = {
      language: "solidity",
      compiler_version: artifact.metadata.compiler.version,
      input_kind: "standard_json",
      input: {
        language: "Solidity",
        sources: Object.fromEntries(
          Object.keys(artifact.metadata.sources).map((p) => [
            p,
            { content: fs.readFileSync(path.join(root, p), "utf8") },
          ]),
        ),
        settings,
      },
    };
    const verificationFile = new URL(`../verification/${source.contract}.json`, import.meta.url);
    if (process.argv.includes("--check")) {
      if (
        JSON.stringify(JSON.parse(fs.readFileSync(verificationFile))) !==
        JSON.stringify(verification)
      )
        throw Error("verification source drift");
    } else fs.writeFileSync(verificationFile, JSON.stringify(verification, null, 2) + "\n");
    const refs = artifact.deployedBytecode.immutableReferences;
    const keys = Object.keys(refs).sort((a, b) => Number(a) - Number(b));
    if (keys.length !== source.immutableNames.length) throw Error("immutable layout changed");
    result[source.name] = {
      repository: `https://github.com/${source.repo}`,
      commit: source.commit,
      compiler: artifact.metadata.compiler.version,
      abi: artifact.abi,
      bytecode: artifact.deployedBytecode.object,
      immutables: Object.fromEntries(
        keys.map((key, index) => [source.immutableNames[index], refs[key]]),
      ),
    };
  }
  const output = JSON.stringify(result, null, 2) + "\n";
  const target = new URL("upstream/x402-standard.json", import.meta.url);
  if (process.argv.includes("--check")) {
    if (JSON.stringify(JSON.parse(fs.readFileSync(target))) !== JSON.stringify(result))
      throw Error("standard contract artifact drift");
  } else fs.writeFileSync(target, output);
} finally {
  fs.rmSync(dir, { recursive: true, force: true });
}
