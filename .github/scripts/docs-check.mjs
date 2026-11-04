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
  check(normalized(read(file)).includes(normalized(fragment)), `${file} is missing: ${fragment}`);
}

function forbid(file, fragment) {
  check(!read(file).includes(fragment), `${file} contains stale text: ${fragment}`);
}

const maintained = [
  "README.md",
  "deploy/README.md",
  "deploy/helm/etherview/README.md",
  "docs/architecture/overview.md",
  "docs/architecture/runtime.md",
  "docs/architecture/storage.md",
  "docs/architecture/indexing.md",
  "docs/architecture/publication.md",
  "docs/architecture/contract-intelligence.md",
  "docs/architecture/verification.md",
  "docs/architecture/api.md",
  "docs/architecture/web.md",
  "docs/architecture/ens.md",
  "docs/architecture/tokens.md",
  "docs/architecture/billing.md",
  "docs/architecture/consensus.md",
  "docs/architecture/etherscan-v2-compatibility.md",
  "docs/operations.md",
  "docs/operations/accounts.md",
  "docs/operations/billing.md",
  "docs/operations/capacity.md",
  "docs/operations/chain-data.md",
  "docs/operations/deployment.md",
  "docs/operations/maintenance.md",
  "docs/operations/observability.md",
  "docs/operations/recovery.md",
  "docs/operations/trace-cache.md",
  "docs/operations/verification.md",
  "docs/testing.md",
  "docs/development.md",
  "docs/development/implementation.md",
  "docs/development/tooling.md",
  "docs/development/dependencies.md",
  "docs/reviews/native-pgx-acceptance.md",
  "docs/testing/ci.md",
  "docs/testing/quality.md",
  "docs/testing/browser.md",
  "docs/testing/integration.md",
  "docs/testing/runtime.md",
  "docs/testing/verification-clients.md",
  "docs/testing/compilers.md",
  "docs/testing/contracts.md",
  "docs/testing/chain-data.md",
  "docs/testing/billing.md",
  "docs/testing/performance.md",
  "preview/docs/operations.md",
  "preview/docs/contracts.md",
  "preview/docs/architecture.md",
  "preview/docs/testing.md",
  "preview/docs/setup.md",
  "preview/docs/README.md",
];

for (const file of maintained) {
  forbid(file, "--profile verification");
  forbid(file, "x402testnet");
  forbid(file, "roles=verify");
}

// Reth is now the payment-only Preview chain; Geth remains the indexed chain.
contains("preview/docs/architecture.md", "Reth v2.9.0");
contains("preview/docs/architecture.md", "Geth chain 48815");
contains("preview/docs/operations.md", "--dev --dev.block-time 3s");
forbid("preview/Caddyfile", "anvil.etherview.localhost");
const makefile = read("Makefile");
const deployment = read("deploy/README.md");
check(
  makefile.includes(
    "recreate-preview: preview-env preview-cert-check preview-network-runtime docker-build",
  ),
  "Makefile recreate-preview must require the read-only Preview network prerequisite",
);
contains(
  "preview/docs/operations.md",
  "`make recreate-preview` reuses the existing network bundle without refreshing its timestamp",
);
contains("preview/docs/operations.md", "it never creates or modifies a Genesis file");
contains("deploy/README.md", "Public verification is configured in the mounted YAML through");
forbid("docs/operations/deployment.md", "creating it once if missing");

const compose = read("deploy/compose.yaml");
check(/^name: etherview$/m.test(compose), "deployment project identity changed");
check(/^name: etherview-preview$/m.test(read("compose.yaml")), "Preview project identity changed");
for (const file of ["README.md", "deploy/README.md", "docs/operations/deployment.md"]) {
  contains(file, "--project-directory . -f deploy/compose.yaml --env-file .env.deploy");
}
const profiles = new Set(
  [...compose.matchAll(/profiles:\s*\[([^\]]*)\]/g)].flatMap((match) =>
    match[1]
      .split(",")
      .map((value) => value.trim().replace(/^['"]|['"]$/g, ""))
      .filter(Boolean),
  ),
);
for (const profile of ["monolith", "distributed", "accelerators"]) {
  check(profiles.has(profile), `deploy/compose.yaml is missing profile ${profile}`);
}
check(
  !profiles.has("verification"),
  "deploy/compose.yaml unexpectedly defines a verification profile",
);
check(
  !deployment.includes("--profile verification"),
  "deployment guide advertises a removed profile",
);

const dockerfile = read("Dockerfile");
const nodeVersions = new Set(
  [...dockerfile.matchAll(/^FROM node:([0-9]+\.[0-9]+\.[0-9]+)(?:-slim)? AS /gm)].map(
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
  "docs/testing/quality.md",
]) {
  contains(file, `Node ${nodeVersion}`);
}
forbid("docs/decisions/ADR-0031-api-owned-solc-js-executor.md", "Node 26.5.0 binary");
contains("docs/architecture/verification.md", `Node ${nodeVersion} SEA`);
contains("docs/architecture/verification.md", `\`${wrapperVersion}\` wrapper protocol`);

const stageSource = read("internal/stagecontract/stage.go");
const stages = [...stageSource.matchAll(/Name:\s*"([a-z0-9_-]+)",\s*Version:\s*(\d+)/gu)].map(
  (match) => ({ name: match[1], version: Number(match[2]) }),
);
check(stages.length > 0, "current stage identities are not discoverable");
const stagePattern = stages.map((stage) => stage.name).join("|");
contains("docs/operations/recovery.md", `reindex --stage ${stagePattern}`);
const traceStage = stages.find((stage) => stage.name === "trace");
const abiStage = stages.find((stage) => stage.name === "abi");
check(abiStage !== undefined, "current ABI stage is missing");
contains(
  "docs/operations/recovery.md",
  `--reason "publish ABI v${abiStage?.version} after trace v${traceStage?.version} and proxy v2 are complete"`,
);
contains("docs/operations/capacity.md", "`verification.worker_count` independently controls");
forbid(
  "docs/operations/capacity.md",
  "runtime.worker_count` controls durable enrichment, trace, verification",
);

// Operator instructions must follow the verification role and catalog pricing.
contains("docs/operations/verification.md", "on every `all`/`verification`");
contains("docs/operations/verification.md", "`ETHERVIEW_DERIVED_VERIFY_ENABLED=true`");
forbid("docs/operations/verification.md", "`DERIVED_VERIFY_");
forbid("docs/operations/verification.md", "every `all`/`api`");
forbid("docs/operations/billing.md", "empty operation-price map");
forbid("preview/docs/setup.md", "API-only compiler-download");

const handlerSource = read("internal/etherscan/handler.go");
const supportedStart = handlerSource.indexOf("var supported =");
const supportedEnd = handlerSource.indexOf("\ntype actionSpec", supportedStart);
check(
  supportedStart >= 0 && supportedEnd > supportedStart,
  "Etherscan action map is not discoverable",
);
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
  const row = line.match(/^\|\s*`([^`]+)`\s*\|/u);
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
