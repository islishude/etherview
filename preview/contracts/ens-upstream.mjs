import { execFileSync } from "node:child_process";
// Explicit source refresh only. Never install or execute ENS npm package scripts.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
const version = "1.7.0";
const revision = "9b034936a42f462fc04bc0a929a419ede5e18d59";
const roots = {
  ENSRegistry: "registry",
  BaseRegistrarImplementation: "ethregistrar",
  ETHRegistrarController: "ethregistrar",
  ReverseRegistrar: "reverseRegistrar",
  DefaultReverseRegistrar: "reverseRegistrar",
  DefaultReverseResolver: "reverseResolver",
  PublicResolver: "resolvers",
  UniversalResolver: "universalResolver",
  GatewayProvider: "ccipRead",
};
const directory = fs.mkdtempSync(path.join(os.tmpdir(), "etherview-ens-source-"));
try {
  const git = (...args) =>
    execFileSync("git", ["-C", directory, ...args], { encoding: "utf8", timeout: 120000 });
  git("init", "--template=");
  git("fetch", "--depth=1", "https://github.com/ensdomains/ens-contracts.git", revision);
  if (git("rev-parse", "FETCH_HEAD").trim() !== revision)
    throw Error("ENS source revision mismatch");
  const sources = {};
  function read(file) {
    if (sources[file]) return;
    const content = git("show", `${revision}:${file}`);
    sources[file] = { content };
    for (const match of content.matchAll(/^\s*import\s+[\s\S]*?["']([^"']+)["']\s*;/gm)) {
      if (match[1].startsWith("."))
        read(path.posix.normalize(path.posix.join(path.posix.dirname(file), match[1])));
    }
  }
  for (const [name, folder] of Object.entries(roots)) read(`contracts/${folder}/${name}.sol`);
  const value = {
    version,
    revision,
    source: `https://github.com/ensdomains/ens-contracts/tree/${revision}`,
    roots,
    sources: Object.fromEntries(Object.entries(sources).sort()),
    license: git("show", `${revision}:LICENSE.txt`),
  };
  fs.writeFileSync(
    new URL("upstream/ens-sources.json", import.meta.url),
    JSON.stringify(value, null, 2) + "\n",
  );
} finally {
  fs.rmSync(directory, { recursive: true, force: true });
}
