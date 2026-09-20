# Development Guide

This guide owns the repository's engineering workflow and change-routing
rules. It complements the compact `AGENTS.md` entry point without duplicating
the detailed architecture, testing, or operations manuals.

## Source-of-truth map

| Concern | Authoritative source |
| --- | --- |
| Current scope, dependencies, status, and evidence | [Root plan](../PLAN.md), [plan catalog](plans/index.md), and [child plans](plans/) |
| Current system behavior and component boundaries | [Architecture overview](architecture/overview.md) |
| Accepted consequential decisions and invariants | [ADR catalog](decisions/index.md) and accepted ADR files |
| Runnable commands, test scope, restricted hosts, and evidence | [Makefile](../Makefile) and [testing guide](testing.md) |
| Deployment, telemetry, recovery, and administration | [Operations runbook](operations.md) |
| Public HTTP contract | [OpenAPI source](../api/openapi.yaml) |
| Historical native pgx acceptance | [Native pgx acceptance record](#native-pgx-acceptance-record) |
| Production executable SQL | [sqlc query sources](../internal/db/queries/) |

Accepted ADRs are mandatory. Update or add one when a public API, persistent
contract, security boundary, external-service boundary, or monolith/split
runtime decision changes. Put task status and evidence in plans rather than in
`AGENTS.md`, and keep design detail out of plans once an ADR or architecture
document owns it.

## Change workflow

1. Read the root plan, the owning child plan, its dependencies, and every
   linked ADR or testing rule relevant to the change.
2. Select one dependency-ready `todo` work item and mark it `in_progress`
   before changing implementation. Synchronize the root plan when the child
   plan's overall status changes.
3. Inspect staged, unstaged, and untracked files. Preserve all pre-existing
   work and avoid opportunistic edits outside the claimed scope.
4. Implement the behavior and focused regressions together. Keep monolith and
   split-role behavior aligned when the change crosses process boundaries.
5. Run targeted checks first, then the applicable maintained gates. Follow
   `docs/testing.md` when a browser, Docker, cache, or service boundary is
   involved.
6. Update acceptance state and record concise commands and results in the child
   plan. Review all staged, unstaged, and untracked files again before
   reporting completion.

Never delete or reuse a work-item ID. Mark abandoned work `dropped` with a
reason or `superseded` with its replacement. A `blocked` item names the blocker
and the condition that clears it. Long-lived code TODOs must cite a plan item.

## Implementation policy

- Do not preserve backward compatibility during active development. Migrations
  target the current fresh-database schema; do not add startup data backfills,
  legacy-schema adapters, or compatibility readiness states unless explicitly
  requested.
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

| Change touches | Required action |
| --- | --- |
| Public API or public numeric fields | Start in `api/openapi.yaml`, preserve string encoding beyond JavaScript's safe integer range, regenerate Go and TypeScript contracts, and run `make generate-check`. Consult [ADR-0003](decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md). |
| Database queries, execution boundaries, or package direction | Start executable SQL in `internal/db/queries/`; only the migration runner and validated partition-DDL module may own raw production SQL in Go. Public readers use the neutral public-query, ABI, proxy, and stage contract packages and never import HTTP transport or enrichment worker hubs. Run `make source-check`, plus generation checks when sqlc sources change. |
| Runtime components or roles | Update the owning typed builder and the independent production component manifest. Verify `roles=all`, split-role registration, readiness, shutdown, and parity. Consult [ADR-0001](decisions/ADR-0001-modular-roles-and-postgresql-truth.md). |
| Chain facts, RPC reads, or enrichment | Preserve chain plus block-hash identity, orphan facts, one-endpoint exact-state reads, canonical rechecks, and generation/lease-fenced publication. Never infer coverage from a disconnected height or fall back to `latest`. Use go-ethereum exported protocol types as specified by [ADR-0022](decisions/ADR-0022-go-ethereum-type-and-raw-rpc-ownership.md). |
| RPC, metadata, compiler, facilitator, or other external calls | Copy bounded inputs and close database snapshots before the call, then recheck canonicality or ownership before commit. Enforce size, time, work, redirect, and network limits; return stable typed errors and redact hostile nested details. |
| Optional infrastructure | Keep PostgreSQL authoritative and readiness-correct. Redis, NATS, and object storage remain disposable accelerators with explicit bounded fallback behavior. Consult [ADR-0015](decisions/ADR-0015-disposable-runtime-accelerators.md). |
| Browser, authentication, or billing | Keep secrets out of logs, URLs, ConfigMaps, the SPA, and image layers. Use the generated same-origin API client, keep wallet RPC inside the injected-provider allowlist, and preserve the separate API-key, SIWE-user, and x402-top-up payer identities. Consult [ADR-0013](decisions/ADR-0013-embedded-spa-serving-and-browser-security.md), [ADR-0020](decisions/ADR-0020-siwe-user-sessions.md), [ADR-0035](decisions/ADR-0035-user-owned-scoped-api-keys.md), and [ADR-0044](decisions/ADR-0044-prepaid-api-billing-and-x402-topups.md). |
| Verification, proxy, Diamond, EIP-7702, CWIA, or Geas behavior | Read the mechanism's accepted ADR and the relevant architecture section before editing. Preserve exact code/block provenance, current binding fences, compiler/helper identity, and fail-closed publication semantics; do not collapse distinct mechanisms into aliases. |
| SPA structure | Keep core pages and both locale trees split by domain, use only the generated explorer client outside the injected wallet module, and retain the explicit Oxlint limits and Oxfmt gate in `web-lint` rather than adding blanket suppressions. |
| Deployment or operator behavior | Update `docs/operations.md` and the relevant Compose/Helm contracts. Keep secrets role-scoped and use repository Compose/Buildx wrappers and supported overrides. |

## Database access

Follow [ADR-0050](decisions/ADR-0050-native-pgx-and-typed-queries.md): native
pgxpool and pgx transactions, generated sqlc pgx/v5 methods, and exact numeric
values. Preserve reader routing and snapshot/lease boundaries. Transaction
cancellation requires explicit bounded rollback; uncertain session-lock
ownership requires physical connection disposal. Do not introduce stdlib
bridges, exported-query scans, or compatibility adapters. Name query arguments
and computed result columns in SQL. Use `:execrows` when callers enforce an
affected-row condition and `:exec` when only execution errors matter.

## Frontend tooling

Use `npm --prefix web run format` after editing hand-written frontend files.
`make web-lint` runs TypeScript, Oxlint, and a read-only Oxfmt check through the
same npm scripts used by CI. Keep generated API types under the API generator;
do not format them manually. The [testing guide](testing.md) owns exact limits,
formatting scope, and tool-policy regression coverage.

## Completion checklist

- Add regressions for malformed inputs and, where relevant, reorgs,
  canonicality, optional-capability loss, numeric bounds, concurrency, and
  security-sensitive parsing.
- Run the smallest focused checks before broader gates. The Makefile is command
  truth; `docs/testing.md` defines each gate's scope and evidence rules.
- Run `make generate-check` after OpenAPI, SQL, generated-client, or embedded
  SPA changes; `make source-check` after database execution-boundary changes;
  run `make docs-check` after maintained documentation or executable
  deployment/runtime-surface changes; and run `make plan-check` after plan,
  ADR-link, or governance changes.
- Do not substitute local mocks or a weaker browser/container mode for a
  required production, integration, or operator gate. Record what actually ran
  and leave unmet external evidence explicit.
- A work item becomes `done` only after its targeted checks and applicable
  gates pass and the child plan contains concise current evidence.

## Native pgx acceptance record

Historical evidence recorded on 2026-09-20. These results apply to the revisions
and local working trees named below, not automatically to later changes.
[P68](plans/P68-runtime-architecture-hardening.md) owns current task status.

These checks were collected in the isolated `codex/pgx-native` working tree,
based on `a805559556025c3f88699a2fb5bc51d137be4d0a`. The user preserved the
previous database/sql attempt in a stash and requested integration of the
native implementation into local `main`. The stash is retained separately.
The implementation is published as [PR 92](https://github.com/islishude/etherview/pull/92)
at commit `aebb61fee2783e3f6bb8810a81296b05c142bce6`.

### Implementation boundary

- Production, CLI and test database access use pgx/v5, pgxpool and pgtype.
  Generated queries use pgx/v5 with private statements and typed parameters and
  results. Anonymous generated parameters/results have been removed; 97 queries
  whose callers discard affected counts now use `:exec`, while conditional writes
  retain `:execrows`. Parameter permutations were checked against all 607 SQL
  statements and their native parameter/result types before PostgreSQL regression.
  Business SQL execution remains only inside generated code. Migrations
  and validated partition DDL retain their existing explicit raw boundaries.
- Read/write routing, chain locks, transaction isolation, lease fencing and
  ambiguous-commit confirmation remain intact. Cancellation uses bounded native
  rollback; uncertain advisory-lock sessions are removed and physically closed.
  Pool teardown shares the supervisor deadline, including stuck borrowers.
- Exact numeric/NULL projections and native arrays/UUID/JSONB are validated.
  API-key, canonical-coverage, Genesis candidate and holder-candidate scans use
  bounded keyset pages; candidate pages share a snapshot that closes before RPC.
- Worker lease loss is task-local. Home HTTP/SSE includes decimal event versions
  with a two-second fence. Browser queries carry typed event metadata and recover
  one EventSource after CLOSED, including expired cursors and UserOperation reorgs.
- No schema migration, startup backfill or compatibility adapter was added.

### Passing evidence

- `go build ./...`, `make lint-go` (zero issues), source, docs and plan checks.
- `make compose-check helm-check` passes rendered deployment and Helm contracts;
  their original local results did not establish production-image acceptance.
- `make generate-check` through the common gate; OpenAPI and sqlc regeneration
  remains reproducible. Web lint and 373 Vitest cases plus asset-policy tests pass.
- `make test-e2e`: all 29 real Chrome tests pass, including server-generated
  expired-cursor HTTP 400, cursor-free recovery, single EventSource ownership,
  UserOperation head refresh and removal of orphaned detail.
- Focused worker/home/component race tests pass. They cover renewal/completion/
  failure lease loss, continued work and peer liveness, fatal error preservation,
  cancellation, home version 10→11, replica switching, concurrent waiters and a
  late obsolete frontend response. Accepted HTTP snapshots also raise the
  QueryClient version floor; fresh clients remain isolated.
- `make test-integration`: all six PostgreSQL 18 packages pass. The core
  integration package completes in 225.143 seconds. `make test-integration-race`
  passes the same six packages, with the core package in 229.401 seconds.
  Owned PostgreSQL projects and volumes were removed after each run.
- Production E2E test sources compile with `go test -run '^$'
  -tags=runtimee2e,hardhat3e2e,foundrye2e,previewmetadatae2e,hardhat3verify ./e2e/...`.
  This is compile-only evidence.
- `make check` passes generation, Go/Web lint, Go/Web unit tests, Go race,
  security and license checks, then stops at the external Docker syntax-image
  authentication boundary below. No vulnerabilities in called symbols or leaked
  secrets were reported by those checks. After the final query-contract and
  browser version-floor updates, `make generate-check lint test test-race` and
  `make security-check license-check` pass again. The external Docker prerequisite
  was blocked at that time; this was not a successful aggregate `make check`
  result. The PR evidence below subsequently covers those CI gates.

### Core read/write benchmark

Host: macOS 27.0, native arm64, Go 1.27.1, owned PostgreSQL 18. Baseline and native
measurements ran serially with the same fixture and `-benchtime 1s`. The table
shows one baseline sample and the range of two final native samples:

`go run ./cmd/testintegration -root . -packages ./internal/integration -benchmark '^BenchmarkCorePersistenceAndProjection$' -benchtime 1s`

The earlier diagnostic benchmark overlapping an integration run is excluded.
These are short local samples, not statistical significance claims or
P70 reference-capacity evidence. Typical fixtures have 20 transactions/one log;
large fixtures have 200 transactions/two logs per transaction.

| Case | Baseline → native ms/op | Baseline → native bytes/op | Baseline → native allocs/op |
|---|---:|---:|---:|
| typical/idempotent_core_commit | 8.007 → 7.754–7.758 | 1,498,508 → 1,478,811–1,485,492 | 11,663 → 11,572–11,584 |
| typical/block_page | 0.816 → 0.728–0.745 | 14,713 → 6,458–6,459 | 181 → 123 |
| typical/transaction_page | 18.860 → 17.914–19.015 | 837,128 → 751,537–752,908 | 13,178 → 12,706–12,718 |
| large/idempotent_core_commit | 52.085 → 51.441–52.475 | 14,399,008 → 14,599,800–14,600,065 | 132,296 → 132,241–132,252 |
| large/block_page | 0.877 → 0.811–0.840 | 14,711 → 6,474 | 181 → 123 |
| large/transaction_page | 20.838 → 20.360–23.415 | 4,607,222 → 4,384,576–4,386,665 | 77,485 → 75,384–75,400 |

Block reads reduce allocations materially. Transaction reads reduce bytes and
allocations. Large-write time stays close to this baseline; large-write bytes
rise roughly 1.4 percent. One large transaction-page sample is approximately
12 percent slower than the baseline, while the confirmation sample is slightly
faster. These variable short samples do not establish a latency improvement or
a regression; longer repeated capacity measurements remain separate. Both
results are retained, and no acceptance threshold was relaxed.

### PR CI evidence and historical Preview blocker

[CI run 35474508539](https://github.com/islishude/etherview/actions/runs/35474508539)
completed successfully for PR head `aebb61fee2783e3f6bb8810a81296b05c142bce6`.
All 11 checks pass: deterministic generation/lint/unit/race, PostgreSQL
integration, embedded browser flows, security/licenses, container/Compose/Helm,
and native amd64/arm64 Vyper, Hardhat and Foundry matrices. The
[deployment job](https://github.com/islishude/etherview/actions/runs/35474508539/job/105981319796)
explicitly succeeds at the production-image schema lifecycle and runtime
Compose topology-parity steps. The successful CI deployment check clears the
previous Docker validation blocker for the common gates; this does not assert
that the literal aggregate `make check` command ran remotely.

The follow-up local `make check` now passes completely, including Docker,
Compose and Helm validation.

The earlier local Buildx failures (Docker Hub frontend/token TLS timeout and
connection reset) remain historical evidence. Their schema/runtime and
Hardhat/Foundry acceptance gaps are now covered by the successful PR run.

Preview Metadata is not part of `.github/workflows/ci.yml`. Its previous
public-IPFS gate failed as recorded below. The user approved P68-T25 to replace
that external dependency with local real-Kubo acceptance, retaining exact
content, version-update and restart-persistence requirements. P70/P73 external
release gates remain independent.

The follow-up `make test-preview-metadata` successfully builds the current
native arm64 production image
`sha256:764e23d149f8944ee59159173acb065b89686da302e5750ac7db230b0dc908c0`.
The full Preview test then fails after 37.66 seconds at the initial metadata
version. The worker records HTTP-phase `temporary_fetch_error` on attempts
one through four and `attempts_exhausted` on attempt five. The failure diagnostics
are retained as `etherview-preview-metadata-587905476/compose-logs.txt` and
`compose-ps.txt` in the local test artifact directory; owned Compose resources
are torn down.

A direct GET of the exact documented fixture returns HTTP 429 with
`Retry-After: 2114` and `Sunset: Mon, 21 Sep 2026 00:00:00 GMT`. Its response
announces a switch to a Service Worker gateway and links the
[gateway-change notice](https://gatewaychanges.ipfs.io/). This does not meet the
then-current server-side one-attempt public-IPFS contract. At that time the
clearing condition was endpoint recovery or a reviewed contract replacement.
No gateway, fixture, timeout, retry limit, or content assertion changed in that
historical follow-up. P68-T25 now implements the user-approved replacement.

### Local Kubo replacement acceptance (P68-T25 / P68-T24)

The user approved replacing public gateway availability with real local Kubo
acceptance. Daily Preview uses Kubo v0.43.1 with network retrieval, behind a
trusted HTTPS proxy; acceptance runs `daemon --offline` and
`Gateway.NoFetch=true`. The existing 205-byte fixture, CID, content digest,
contract bytecode, single-attempt requirements, version history and worker
restart checks remain intact. ADR-0005 records the scoped development boundary.

On 2026-09-20 the final exact `make test-preview-metadata` passes on native arm64
(`TestPreviewLocalNFTMetadata`, 46.95s). It builds and exercises `cmd/ipfs`
against real Kubo: repeated wrapped imports reproduce the original CID;
empty, binary and 1-MiB files roundtrip exactly; missing content fails offline;
Kubo restart preserves content. TLS requires the trusted CA and the HTTPS
proxy rejects management paths. All exposed acceptance ports are random and
loopback-only. Two on-chain metadata versions succeed with one attempt each,
exact content and canonical provenance, plus unchanged restart persistence.

The retained `etherview-preview-metadata-767907377/report.json` records:

- application image `sha256:d905f800d077725da0b40148b0c9adcf955ca168a94e5bd34bfcc24cdb9cd51c`;
- Kubo image `sha256:b293923d66e490e70ced64df42ea7a6cf7eac2740e3fb29101df18070fa7be48`;
- gateway proxy image `sha256:30f1c0d78e0ad60901648be663a710bdadf19e4c10ac6782c235200619158284`;
- CID `bafybeibnsoufr2renqzsh347nrx54wcubt5lgkeivez63xvivplfwhtpym`, size 205,
  SHA-256 `a87d3d327d1a2c7f839000c080e07cd152b49ddf653f1a5afa5144eeec103d8d`;
- `gateway_mode=local-offline`, `cli_roundtrip=true`, `offline_no_fetch=true`,
  two source versions/two attempts and both `policy_bypassed=true` observations
  on addresses belonging to the owned gateway.

The report identifies base commit `aebb61fee2783e3f6bb8810a81296b05c142bce6`
and `dirty=true`; it covers the local working-tree changes, not a new CI commit.
Its directory also retains Compose state/logs; owned containers and volumes
were removed. Earlier exploratory attempts found Docker's internal-network
port-publication limitation and ephemeral-port changes on restart. The final
harness retains a project network, enforces daemon-level offline/NoFetch policy,
and resolves host ports again after restart.

`go test -race ./cmd/ipfs ./internal/config ./internal/metadata`, `make lint-go`,
`make compose-check docs-check plan-check`, and `make security-check license-check`
pass. Additional CLI coverage proves early response headers do not truncate
uploads, stream errors do not publish partial files, and existing output remains
untouched. A separate isolated daily project passes exact `make start-preview`,
`make recreate-preview` and `make stop-preview`: NoFetch stays false, an additional
CID stays pinned and downloads identically across recreation, and stop removes
its data volume. Its report remains in `/tmp/etherview-ipfs-lifecycle.yGcl3Q`.
This tests the online configuration/lifecycle, not external P2P availability.

P68-T25 and P68-T24 are done using these checks and the earlier aggregate
acceptance evidence. Remote CI was not rerun for this change. P70/P73 live
payment and reference-capacity gates remain independent.
