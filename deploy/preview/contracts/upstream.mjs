// Extract only the two required simulation artifacts from the exact Alto build.
import fs from "node:fs";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
const revision = "3413697d806785a5b57f7aefea1967ffd4fe618e";
const script = `const fs=require('fs');if(fs.readFileSync('/app/.git/HEAD','utf8').trim()!==${JSON.stringify(revision)})throw Error('Alto revision mismatch');const result={};for(const name of ['PimlicoSimulations','EntryPointSimulations09']){const folder=name==='PimlicoSimulations'?'PimlicoSimulations':'EntryPointSimulations';const a=JSON.parse(fs.readFileSync('/app/src/contracts/'+folder+'.sol/'+name+'.json'));result[name]={abi:a.abi,bytecode:a.bytecode.object};}process.stdout.write(JSON.stringify({artifacts:result,constants:fs.readFileSync("/app/test/e2e/deploy-contracts/constants.ts","utf8")}));`;
const extracted = JSON.parse(
  execFileSync(
    process.env.DOCKER ?? "docker",
    [
      "run",
      "--rm",
      "--network=none",
      "--entrypoint",
      "node",
      "etherview-preview-bundler:local",
      "-e",
      script,
    ],
    { maxBuffer: 16 << 20, encoding: "utf8" },
  ),
);
const value =
  JSON.stringify(
    {
      source: `https://github.com/pimlicolabs/alto/tree/${revision}`,
      anvilBuild: "1.8.6",
      artifacts: extracted.artifacts,
    },
    null,
    2,
  ) + "\n";
const file = new URL("upstream/alto-simulations.json", import.meta.url);
if (process.argv.includes("--check")) {
  if (fs.readFileSync(file, "utf8") !== value) throw Error("Alto simulation artifacts are stale");
} else fs.writeFileSync(file, value);

const calls = {};
for (const key of ["ENTRY_POINT_V09_CREATECALL", "SIMPLE_ACCOUNT_FACTORY_V09_CREATECALL"]) {
  const match = extracted.constants.match(new RegExp(key + ': Hex =\\s*"(0x[0-9a-fA-F]+)"'));
  if (!match) throw Error(`Missing upstream creation input ${key}`);
  calls[key] = match[1];
}
const canonical =
  JSON.stringify(
    {
      source: `https://raw.githubusercontent.com/pimlicolabs/alto/${revision}/test/e2e/deploy-contracts/constants.ts`,
      sourceSha256: createHash("sha256").update(extracted.constants).digest("hex"),
      calls,
    },
    null,
    2,
  ) + "\n";
const canonicalFile = new URL("upstream/account-abstraction-v09.json", import.meta.url);
if (process.argv.includes("--check")) {
  if (fs.readFileSync(canonicalFile, "utf8") !== canonical)
    throw Error("Canonical AA construction inputs are stale");
} else fs.writeFileSync(canonicalFile, canonical);
