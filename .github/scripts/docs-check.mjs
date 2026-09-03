import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = process.cwd();
const read = (file) => readFileSync(resolve(root, file), "utf8");
const failures = [];

function check(condition, message) {
  if (!condition) failures.push(message);
}

function contains(file, fragment) {
  const normalized = (value) => value.replace(/\s+/gu, " ").trim();
  check(
    normalized(read(file)).includes(normalized(fragment)),
    `${file} is missing: ${fragment}`,
  );
}

function forbid(file, fragment) {
  check(!read(file).includes(fragment), `${file} contains stale text: ${fragment}`);
}

const maintained = [
  "README.md",
  "deploy/README.md",
  "deploy/helm/etherview/README.md",
  "docs/architecture/overview.md",
  "docs/architecture/etherscan-v2-compatibility.md",
  "docs/operations.md",
  "docs/testing.md",
  "docs/development.md",
];

for (const file of maintained) {
  forbid(file, "--profile verification");
  forbid(file, "x402testnet");
  forbid(file, "roles=verify");
  forbid(file, "Reth");
}

const makefile = read("Makefile");
const deployment = read("deploy/README.md");
check(
  makefile.includes("recreate-preview: preview-cert-check preview-genesis-runtime docker-build"),
  "Makefile recreate-preview must require the read-only Preview Genesis prerequisite",
);
contains(
  "docs/operations.md",
  "`make recreate-preview` reuses the existing runtime copy without refreshing its timestamp",
);
contains(
  "docs/operations.md",
  "it never creates or modifies a Genesis file",
);
contains(
  "deploy/README.md",
  "Public verification is configured in the mounted YAML through",
);
forbid("docs/operations.md", "creating it once if missing");

const compose = read("compose.yaml");
const profiles = new Set(
  [...compose.matchAll(/profiles:\s*\[([^\]]*)\]/g)].flatMap((match) =>
    match[1]
      .split(",")
      .map((value) => value.trim().replace(/^['"]|['"]$/g, ""))
      .filter(Boolean),
  ),
);
for (const profile of ["monolith", "distributed", "accelerators"]) {
  check(profiles.has(profile), `compose.yaml is missing profile ${profile}`);
}
check(!profiles.has("verification"), "compose.yaml unexpectedly defines a verification profile");
check(!deployment.includes("--profile verification"), "deployment guide advertises a removed profile");

const dockerfile = read("Dockerfile");
const nodeVersions = new Set(
  [...dockerfile.matchAll(/^FROM node:([0-9]+\.[0-9]+\.[0-9]+)-slim AS /gm)].map(
    (match) => match[1],
  ),
);
check(nodeVersions.size === 1, "Dockerfile Node builder versions must agree");
const nodeVersion = [...nodeVersions][0];
const runtimeCheck = read(".github/scripts/solcjs-runtime-image-check.mjs");
const runtimeNodeVersion = runtimeCheck.match(/manifest\.node_version, "v([^"]+)"/u)?.[1];
const wrapperVersion = runtimeCheck.match(/manifest\.wrapper_package, "([^"]+)"/u)?.[1];
check(nodeVersion !== undefined, "Dockerfile Node builder version is not discoverable");
check(nodeVersion === runtimeNodeVersion, "Dockerfile and runtime manifest Node versions drift");
for (const file of [
  "README.md",
  "deploy/README.md",
  "deploy/helm/etherview/README.md",
  "docs/testing.md",
]) {
  contains(file, `Node ${nodeVersion}`);
}
forbid("docs/decisions/ADR-0031-api-owned-solc-js-executor.md", "Node 26.5.0 binary");
contains("docs/architecture/overview.md", `Node ${nodeVersion} SEA`);
contains("docs/architecture/overview.md", `\`${wrapperVersion}\` wrapper protocol`);

const stageSource = read("internal/stagecontract/stage.go");
const stages = [...stageSource.matchAll(/Name:\s*"([a-z0-9_-]+)",\s*Version:\s*(\d+)/gu)].map(
  (match) => ({ name: match[1], version: Number(match[2]) }),
);
check(stages.length > 0, "current stage identities are not discoverable");
const stagePattern = stages.map((stage) => stage.name).join("|");
contains("docs/operations.md", `reindex --stage ${stagePattern}`);
const abiStage = stages.find((stage) => stage.name === "abi");
check(abiStage !== undefined, "current ABI stage is missing");
contains(
  "docs/operations.md",
  `--reason "publish ABI v${abiStage?.version} after trace v3 and proxy v2 are complete"`,
);
contains("docs/operations.md", "`verification.worker_count` independently controls");
forbid("docs/operations.md", "runtime.worker_count` controls durable enrichment, trace, verification");

const handlerSource = read("internal/etherscan/handler.go");
const supportedStart = handlerSource.indexOf("var supported =");
const supportedEnd = handlerSource.indexOf("\ntype actionSpec", supportedStart);
check(supportedStart >= 0 && supportedEnd > supportedStart, "Etherscan action map is not discoverable");
const supported = handlerSource.slice(supportedStart, supportedEnd);
const sourceActions = new Set();
for (const moduleMatch of supported.matchAll(/^\t"([^"]+)": \{\n([\s\S]*?)^\t\},/gmu)) {
  for (const actionMatch of moduleMatch[2].matchAll(/^\t\t"([^"]+)":/gmu)) {
    sourceActions.add(`${moduleMatch[1]}.${actionMatch[1]}`);
  }
}
const matrix = read("docs/architecture/etherscan-v2-compatibility.md");
const sourceModules = new Set([...sourceActions].map((action) => action.split(".")[0]));
const documentedActions = new Set();
let currentModule = "";
for (const line of matrix.split("\n")) {
  if (/^## /u.test(line) && !line.startsWith("### ")) currentModule = "";
  const heading = line.match(/^### `([^`]+)`$/u);
  if (heading) currentModule = heading[1];
  const row = line.match(/^\| `([^`]+)` \|/u);
  if (row && sourceModules.has(currentModule)) {
    documentedActions.add(`${currentModule}.${row[1]}`);
  }
}
check(sourceActions.size > 0, "Etherscan action map contains no actions");
check(
  sourceActions.size === documentedActions.size &&
    [...sourceActions].every((action) => documentedActions.has(action)),
  "Etherscan action map and compatibility matrix drift",
);

if (failures.length > 0) {
  for (const failure of failures) console.error(`docs-check: ${failure}`);
  process.exit(1);
}

console.log(
  `docs-check: ok (${maintained.length} maintained documents, ${sourceActions.size} Etherscan actions)`,
);
