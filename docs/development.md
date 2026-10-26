# Development Guide

This guide owns the repository's engineering workflow and change-routing
rules. It complements the compact `AGENTS.md` entry point without duplicating
the detailed architecture, testing, or operations manuals.

## Source-of-truth map

| Concern                                                       | Authoritative source                                                               |
| ------------------------------------------------------------- | ---------------------------------------------------------------------------------- |
| Milestone scope, dependencies, status, and acceptance         | [Root plan](../PLAN.md), [plan catalog](plans/index.md), and [child plans](plans/) |
| Routine change rationale and validation                       | PR description or completion report; Git history                                   |
| Current system behavior and component boundaries              | [Architecture overview](architecture/overview.md)                                  |
| Accepted consequential decisions and invariants               | [ADR catalog](decisions/index.md) and accepted ADR files                           |
| Runnable commands, test scope, restricted hosts, and evidence | [Makefile](../Makefile) and [testing guide](testing.md)                            |
| Deployment, telemetry, recovery, and administration           | [Operations runbook](operations.md)                                                |
| Public HTTP contract                                          | [OpenAPI source](../api/openapi.yaml)                                              |
| Historical native pgx acceptance                              | [Native pgx acceptance record](#native-pgx-acceptance-record)                      |
| Production executable SQL                                     | [sqlc query sources](../internal/db/queries/)                                      |

Accepted ADRs are mandatory. Update or add one when a public API, persistent
contract, security boundary, external-service boundary, or monolith/split
runtime decision changes. Keep milestone status out of `AGENTS.md`, and keep
design detail out of plans once an ADR or architecture document owns it.

## Planning threshold

Create or extend a plan only for a substantial capability, a consequential
architecture/persistent-contract migration, or multi-stage work whose
dependencies, rollout or acceptance need to survive the current change.
An explicit user request for a tracked plan also qualifies. Use one milestone
per independently verifiable outcome, not one item per file, test, review
finding, command or commit. Reuse the owning milestone for its implementation,
regressions and acceptance instead of splitting each activity into new items.

Small bug fixes, behavior-preserving refactors, styling/copy changes, routine
dependency updates, CI/test repairs and documentation maintenance normally do
not need a plan item. Record their rationale, tests and limitations in the PR
or completion report. Size alone is not the criterion: a small change that
alters a consequential boundary still follows the relevant ADR and planning
rules. Do not reopen a completed plan for ordinary maintenance.

Plans are a roadmap and acceptance summary, not a changelog. Update existing
scope, status, blockers and evidence in place. After completion, retain a short
capability summary, meaningful acceptance evidence and its limits. Remove
abandoned/superseded approaches and routine records once incoming dependencies,
code TODOs and documentation links have been resolved. Git owns detailed
history; do not move the same ledger into a new archive document.

Never remove unresolved feature work, release gates or failures merely to
reduce document size. While an abandoned item still has active consumers,
mark it `dropped` with a reason or `superseded` with its replacement. A `blocked`
item names its blocker and clearing condition. Never renumber or reuse IDs;
the [catalog watermarks](plans/index.md#identifier-allocation) reserve removed
numbers. Long-lived code TODOs should link to an actionable issue or existing
milestone; they do not automatically justify creating a plan item.

## Change workflow

1. Inspect staged, unstaged, and untracked files. Preserve all pre-existing
   work and keep changes within the requested scope.
2. Read the architecture, ADRs and testing rules relevant to the change.
   Consult the root and owning child plan when planned scope, dependencies,
   acceptance or blockers are affected.
3. Apply the planning threshold. For planned work, claim a dependency-ready
   existing `todo` item as `in_progress` before implementation; add an item only
   for a qualifying outcome not already covered. For routine work, proceed
   without editing plans.
4. Implement the behavior and focused regressions together. Keep monolith and
   split-role behavior aligned when the change crosses process boundaries.
5. Run targeted checks first, then the applicable maintained gates. Follow
   `docs/testing.md` when a browser, Docker, cache, or service boundary is
   involved.
6. Record concise commands, results and limitations in the PR or completion
   report. For planned work, update the owning milestone's acceptance once;
   synchronize the root only for scope, dependency or overall-state changes.
   Review staged, unstaged and untracked files again before reporting completion.

## Implementation policy

- Do not preserve backward compatibility during active development. The current
  fresh-database baseline is organized by domain in the numbered SQL files under
  `internal/store/migrations/`, from `0001_core.sql` through
  `0017_schema_bindings.sql`. Each file has its own version and checksum; the
  runner applies all pending files in one transaction under an advisory lock.
  Databases created with the earlier development chain or single-file baseline
  must be recreated as described in the
  [runbook](operations.md#fresh-database-initialization). Do not add startup
  data backfills, legacy-schema adapters, or compatibility readiness states
  unless explicitly requested.
  Keep this baseline declarative: put columns, defaults, identity and constraints
  in `CREATE TABLE`, use `PARTITION OF` for initial partitions, and declare named
  child indexes before recursive parent indexes. Retain only documented ALTER
  exceptions required for dependency completion or exact catalog semantics;
  `TestBaselineDeclaresFinalObjects` checks their complete statements across
  every file. Keep domain-owned sequences, tables, initial partitions, indexes,
  functions and triggers together; cross-domain views and function schema
  bindings come last. Preserve prerequisite unique indexes before referencing
  foreign keys and define CHECK functions before their tables. Deferred PL/pgSQL
  body resolution remains transaction-local to the complete baseline. When
  reorganizing the baseline, compare before/after PostgreSQL 18 schema dumps
  (excluding dump restriction tokens) and retain object names and behavior.
- Choose the simplest implementation that fully meets current requirements.
  Prefer established, maintained libraries over custom replacements.
- Make architectural decisions for the long term. Do not introduce a stopgap
  that is intended to be replaced later.
- Generated artifacts are outputs, not editing surfaces. Change their
  authoritative source and regenerate them.
- Keep tracked templates immutable when runtime-specific artifacts can be
  generated in ignored workspace paths.

## Boundary checklist

Use this table to locate the complete rules before changing a boundary. The
linked documents, not this summary, define the exact contract.

| Change touches                                                 | Required action                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| -------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Public API or public numeric fields                            | Start in `api/openapi.yaml`, preserve string encoding beyond JavaScript's safe integer range, regenerate Go and TypeScript contracts, and run `make generate-check`. Consult [ADR-0003](decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md).                                                                                                                                                                                                                                                                              |
| Database queries, execution boundaries, or package direction   | Start executable SQL in `internal/db/queries/`; only the migration runner and validated partition-DDL module may own raw production SQL in Go. Public readers use the neutral public-query, ABI, proxy, and stage contract packages and never import HTTP transport or enrichment worker hubs. Run `make lint-go` (including SQL ownership checks), plus generation checks when sqlc sources change.                                                                                                                                         |
| Runtime components or roles                                    | Update the owning typed builder and the independent production component manifest. Verify `roles=all`, split-role registration, readiness, shutdown, and parity. Consult [ADR-0001](decisions/ADR-0001-modular-roles-and-postgresql-truth.md).                                                                                                                                                                                                                                                                                               |
| Chain facts, RPC reads, or enrichment                          | Preserve chain plus block-hash identity, orphan facts, one-endpoint exact-state reads, canonical rechecks, and generation/lease-fenced publication. Never infer coverage from a disconnected height or fall back to `latest`. Use go-ethereum exported protocol types as specified by [ADR-0022](decisions/ADR-0022-go-ethereum-type-and-raw-rpc-ownership.md).                                                                                                                                                                              |
| RPC, metadata, compiler, facilitator, or other external calls  | Copy bounded inputs and close database snapshots before the call, then recheck canonicality or ownership before commit. Enforce size, time, work, redirect, and network limits; return stable typed errors and redact hostile nested details.                                                                                                                                                                                                                                                                                                |
| Optional infrastructure                                        | Keep PostgreSQL authoritative and readiness-correct. Redis, NATS, and trace object caches remain disposable accelerators with bounded fallback. Optional blob bodies use a separate durable archive with PostgreSQL publication authority. Consult [ADR-0015](decisions/ADR-0015-disposable-runtime-accelerators.md) and [ADR-0055](decisions/ADR-0055-consensus-and-blob-archives.md).                                                                                                                                                      |
| Browser, authentication, or billing                            | Keep secrets out of logs, URLs, ConfigMaps, the SPA, and image layers. Use the generated same-origin API client, keep wallet RPC inside the injected-provider allowlist, and preserve the separate API-key, SIWE-user, and x402-top-up payer identities. Consult [ADR-0013](decisions/ADR-0013-embedded-spa-serving-and-browser-security.md), [ADR-0020](decisions/ADR-0020-siwe-user-sessions.md), [ADR-0035](decisions/ADR-0035-user-owned-scoped-api-keys.md), and [ADR-0044](decisions/ADR-0044-prepaid-api-billing-and-x402-topups.md). |
| Verification, proxy, Diamond, EIP-7702, CWIA, or Geas behavior | Read the mechanism's accepted ADR and the relevant architecture section before editing. Preserve exact code/block provenance, current binding fences, compiler/helper identity, and fail-closed publication semantics; do not collapse distinct mechanisms into aliases.                                                                                                                                                                                                                                                                     |
| SPA structure                                                  | Keep core pages and both locale trees split by domain, use only the generated explorer client outside the injected wallet module, and retain the explicit Oxlint limits and Oxfmt gate in `web-lint` rather than adding blanket suppressions.                                                                                                                                                                                                                                                                                                |
| Deployment or operator behavior                                | Update `docs/operations.md` and the relevant Compose/Helm contracts. Keep secrets role-scoped and use repository Compose/Buildx wrappers and supported overrides.                                                                                                                                                                                                                                                                                                                                                                            |

## Database access

Follow [ADR-0050](decisions/ADR-0050-native-pgx-and-typed-queries.md): native
pgxpool and pgx transactions, generated sqlc pgx/v5 methods, and exact numeric
values. Preserve reader routing and snapshot/lease boundaries. Transaction
cancellation requires explicit bounded rollback; uncertain session-lock
ownership requires physical connection disposal. Do not introduce stdlib
bridges, exported-query scans, or compatibility adapters. Name query arguments
and computed result columns in SQL. Use `:execrows` when callers enforce an
affected-row condition and `:exec` when only execution errors matter.

## Formatting and frontend tooling

Use `make format` to format repository JavaScript, YAML, Markdown, and the
existing hand-written frontend scope. Use `make format-check` for a read-only
check; `make lint` and CI include it. Both commands install the locked root pnpm
workspace. `pnpm-workspace.yaml` owns the workspace list, install policy and
dependency overrides; `api`, `web`, `compiler`, and `e2e/hardhat3` share one root
`pnpm-lock.yaml`. Install pnpm 12.15.0 (pinned by `packageManager`), then run
`pnpm install --frozen-lockfile` (or `make pnpm-install`) at the repository root.
When switching an existing checkout from npm, remove the generated root and
workspace `node_modules` directories before installing. Dependency changes use
`pnpm add` in the owning workspace and commit the updated shared lockfile.
Workspace manifests retain their runtime constraints: the compiler requires
exactly Node 26.12.1 and solc 0.8.37, while the Hardhat fixture keeps solc 0.8.30.

The workspace policy enforces engines and disables dependency lifecycle scripts.
Automatic peer installation is disabled, so the artifact-only Safe fixture's
unused Ethers v5 peer is not installed. Required peers are explicit workspace
dependencies, including the frontend test DOM library. API generation uses
TypeScript 6.0.3 for Hey API, while the Web type-checker remains on TypeScript 7.0.2. Docker uses the `node:26.12.1` image's curl to download the
[official standalone installer](https://pnpm.io/installation#using-a-standalone-script),
with `PNPM_VERSION=12.15.0`, an explicit shell and `PNPM_HOME/bin` on `PATH`, then
uses the same workspace policy with frozen, filtered installs. CI pins
`pnpm/action-setup@v6.1.0`. Dedicated compiler
CI jobs use `pnpm --filter ./compiler install --frozen-lockfile`; a full
`pnpm install --frozen-lockfile` installs all workspaces. Resolve package files
from the owning module instead of assuming a hoisted `node_modules` directory.

After installation, `pnpm run lint`, `pnpm run format`, and `pnpm run format:check`
provide the JavaScript/frontend gates directly. Use
`pnpm --dir web run format` for frontend-only edits.
`make web-lint` runs TypeScript, Oxlint, and a read-only Oxfmt check through the
same pnpm scripts used by CI. Keep generated API types under the API generator;
do not format them manually. `api/openapi-ts.config.ts` generates the Hey API
Fetch SDK, domain types and bundled runtime under `web/src/api/heyapi/` from the
API workspace. After changing `api/openapi.yaml`, run `make web-generate` for
the SDK alone, or `make generate` for all generated artifacts and the embedded
SPA. Ordinary `make web-build` and Docker Web builds consume the committed SDK
without running the OpenAPI generator.
Pass `apiClient` explicitly to operation functions, retain domain
aliases, and keep specialized EventSource/raw-response handling at the existing
boundary. `make generate-check` compares the complete regenerated directory.
The [testing guide](testing.md) owns exact limits,
formatting scope, and tool-policy regression coverage.

### Push worker build

The push-only Service Worker lives in `web/src/workers/push-sw.ts`. Its separate
TypeScript project uses the `WebWorker` library without page DOM globals and
runs as part of the ordinary frontend type check. Shared imports must also be
safe in a worker environment.

Import the entry with Vite's `?worker&url` query and register it as a module
Service Worker. Vite owns TypeScript transformation, shared imports, development
serving and the separate production bundle; no custom worker compiler or public
copy is needed. Worker entries are emitted at the distribution root so their
scope can cover the application without production header overrides. The Vite
development server supplies `Service-Worker-Allowed: /` for source URLs.
Production assets still pass through normal manifest preparation and embedding.
The worker has no fetch handler, offline cache, or notification history.

### Stylesheet ownership

The [stylesheet guide](../web/src/styles/README.md) owns module responsibilities
and cascade order. Keep common layout in existing Tailwind utilities and
`DesignPrimitives`, complex page rules in their owning CSS module, and shared
theme values in foundation tokens. Colocate self-contained responsive rules
with their owner; retain cross-module overrides in the final responsive module.
Preserve selectors, media conditions, and declaration order during structural
moves, and verify computed layout at the affected breakpoints. Do not add a
preprocessor or bulk-convert existing components for a stylesheet split.

## Completion checklist

- Add regressions for malformed inputs and, where relevant, reorgs,
  canonicality, optional-capability loss, numeric bounds, concurrency, and
  security-sensitive parsing.
- Run the smallest focused checks before broader gates. The Makefile is command
  truth; `docs/testing.md` defines each gate's scope and evidence rules.
- Run `make openapi-check` after OpenAPI changes.
  Run `make generate-check` after OpenAPI, SQL, generated-client, or embedded
  SPA changes; `make lint-go` after database execution or package-boundary changes;
  run `make docs-check` after maintained documentation or executable
  deployment/runtime-surface changes; and run `make plan-check` after plan,
  ADR-link, or governance changes.
- Do not substitute local mocks or a weaker browser/container mode for a
  required production, integration, or operator gate. Record what actually ran
  and leave unmet external evidence explicit.
- A planned item becomes `done` only after its targeted checks and applicable
  gates pass and its child plan contains concise current evidence. Routine
  changes use the same applicable checks without manufacturing plan entries.

## Native pgx acceptance record

This section summarizes the native pgx migration's acceptance coverage and
local benchmark observations recorded on 2026-09-20. These are historical
results, not a substitute for validating later changes. Use
[ADR-0050](decisions/ADR-0050-native-pgx-and-typed-queries.md) for the database
contract, the [testing guide](testing.md) for current gate requirements, and
[P00](plans/P00-foundation.md) for database/package acceptance and
[P30](plans/P30-contract-verification.md#production-acceptance-boundaries) for
production-image evidence.

### Required implementation boundaries

- Production, CLI and test database access share pgx/v5, pgxpool and pgtype.
  Business SQL originates in `internal/db/queries/` and executes through typed
  sqlc methods with private statements. Query arguments and computed result
  columns have explicit names; writes use `:execrows` only when callers enforce
  an affected-row condition. Migrations and validated partition DDL retain
  their explicit raw-SQL boundaries.
- Reader routing, transaction isolation, chain locks, lease fencing and
  ambiguous-commit confirmation remain intact. Cancellation explicitly rolls
  back with bounded cleanup. Uncertain advisory-lock sessions are removed from
  the pool and physically closed. Pool shutdown respects the supervisor deadline,
  including when a borrower is stuck.
- Numeric values remain exact; nullable projections preserve absence. Native
  arrays, UUIDs and JSONB retain their typed encodings. Large scans use bounded
  keyset pages under the required snapshot or chain lock, and snapshots close
  before external calls.
- The migration adds no schema changes, startup backfills or compatibility
  adapters. Monolith and split-role deployments use the same persistence and
  runtime components.

### Validation coverage

The recorded acceptance includes the following passing checks. When changing
these boundaries, rerun the applicable gates against the changed tree and
record the command, environment, result and any unmet requirement in the PR or
completion report, and in the owning milestone when this is planned work.

| Area                                     | Checks and recorded coverage                                                                                                                                                                                              |
| ---------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Generated contracts and source ownership | `make generate-check` and `make source-check`: reproducible generation and enforced SQL execution boundaries.                                                                                                             |
| Common quality gates                     | `make check`: generation, lint, unit/race, security/license and deployment validation. The completed local run passed the full aggregate gate.                                                                            |
| PostgreSQL behavior                      | `make test-integration` and `make test-integration-race`: all six PostgreSQL 18 packages passed, covering typed values, query semantics and persistence invariants.                                                       |
| Worker and home-feed concurrency         | Focused race tests covered task-local lease loss, peer liveness, fatal errors, cancellation, home event-version fencing, replica switching and concurrent waiters.                                                        |
| Embedded browser behavior                | `make test-e2e`: 29 Chrome tests passed, including expired-cursor recovery, single EventSource ownership, version-floor handling and UserOperation reorg updates.                                                         |
| Production schema and runtime            | Production-image schema lifecycle and monolith/split topology parity passed; native Linux AMD64/ARM64 Vyper, Hardhat and Foundry matrices also passed. See the testing guide for each suite's commands and prerequisites. |

Compilation alone does not establish runtime acceptance, and rendered Compose
or Helm validation does not establish production-image behavior. Earlier local
Docker prerequisite failures were followed by successful production checks;
a partially completed aggregate command must still be recorded as incomplete.

Preview metadata has its own `make test-preview-metadata` gate. Its accepted
local Kubo replacement verifies exact content, two canonical metadata versions,
one attempt per version and restart persistence. The prior public-gateway
failure and replacement evidence belong to P30-T35 in
[P30](plans/P30-contract-verification.md#production-acceptance-boundaries).
The current CI runtime workflow runs this gate; it does not establish
public-IPFS availability. Live payment and reference-capacity acceptance remain
separate release requirements.

### Core read/write benchmark

Host: macOS 27.0, native arm64, Go 1.27.1, PostgreSQL 18. The pre-migration
baseline and native pgx measurements ran serially with the same fixture and
`-benchtime 1s`. The table shows one baseline sample and the range of two native
samples. An earlier diagnostic run overlapping an integration test is excluded.

```sh
go run ./cmd/testintegration -root . -packages ./internal/integration -benchmark '^BenchmarkCorePersistenceAndProjection$' -benchtime 1s
```

Typical fixtures contain 20 transactions and one log per transaction; large
fixtures contain 200 transactions and two logs per transaction.

| Case                           | Baseline → native ms/op |         Baseline → native bytes/op | Baseline → native allocs/op |
| ------------------------------ | ----------------------: | ---------------------------------: | --------------------------: |
| typical/idempotent_core_commit |     8.007 → 7.754–7.758 |    1,498,508 → 1,478,811–1,485,492 |      11,663 → 11,572–11,584 |
| typical/block_page             |     0.816 → 0.728–0.745 |               14,713 → 6,458–6,459 |                   181 → 123 |
| typical/transaction_page       |  18.860 → 17.914–19.015 |          837,128 → 751,537–752,908 |      13,178 → 12,706–12,718 |
| large/idempotent_core_commit   |  52.085 → 51.441–52.475 | 14,399,008 → 14,599,800–14,600,065 |   132,296 → 132,241–132,252 |
| large/block_page               |     0.877 → 0.811–0.840 |                     14,711 → 6,474 |                   181 → 123 |
| large/transaction_page         |  20.838 → 20.360–23.415 |    4,607,222 → 4,384,576–4,386,665 |      77,485 → 75,384–75,400 |

Block reads used fewer allocations; transaction reads used fewer bytes and
allocations. Large-write time stayed close to the baseline while bytes rose
about 1.4 percent. One large transaction-page sample was about 12 percent slower
than baseline and the other was slightly faster. These short samples establish
neither a latency improvement nor a regression and do not satisfy the P70
reference-capacity gate. Performance acceptance requires separate repeated
measurements with the dataset, hardware, duration and load documented.
