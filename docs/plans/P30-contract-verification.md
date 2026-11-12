# P30 — Contract Platform & Runtime Operations

Status: `done`

This is the canonical plan for contract verification, contract intelligence,
and the shared runtime/operations platform. Final release gates belong to P70.

## Outcome

Etherview provides fail-closed contract verification and contract intelligence
over exact chain, address, runtime, block, and generation identities. It supports
the maintained Solidity/Yul, pinned Geas and pinned Vyper verification paths, authenticated
artifact reuse, standard/Safe/Diamond/CWIA proxy evidence, trace-bound ABI and
failure decoding, EIP-7702 execution identity, factory-derived verification,
and the PostgreSQL-authoritative monolith/split runtime, deployment, telemetry,
and disposable accelerator boundaries. Optional eth-bytecode-db discovery uses
external source candidates only after local compilation and verification.

Runtime operations also owns the real Lighthouse PoS Preview and role-scoped
durable blob archive deployment. Optional Beacon/archive failures remain isolated
from execution readiness; PostgreSQL owns manifests while the separate archive
bucket durably stores bodies. Explicit Preview lifecycle commands provision the
archive outside application startup dependencies. Local Kubo, the independent
HTTPS gateway and streaming IPFS CLI provide reproducible metadata acceptance.
Generated genesis contracts, a separately scoped Bundler and management CLI
provide ERC-4337 v0.9 and local .test ENS without external ENS sources. Bundler
availability does not gate core readiness.

## References

- [ADR-0058](../decisions/ADR-0058-background-ens-synchronization-and-database-only-reads.md)

- [ADR-0056](../decisions/ADR-0056-bytecode-db-source-discovery.md)
- [ADR-0052](../decisions/ADR-0052-amsterdam-explorer-support.md)
- [ADR-0027](../decisions/ADR-0027-process-native-api-tls.md)
- [Architecture](../architecture/overview.md)
- [ADR catalog](../decisions/index.md)
- [Proxy deployment review template](../reviews/proxy-detection-deployment-review-template.md)
- [ADR-0001](../decisions/ADR-0001-modular-roles-and-postgresql-truth.md)
- [ADR-0002](../decisions/ADR-0002-identity-bound-repair-and-explicit-reindex.md)
- [ADR-0003](../decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md)
- [ADR-0004](../decisions/ADR-0004-durable-runtime-status-and-events.md)
- [ADR-0005](../decisions/ADR-0005-safe-nft-metadata-and-media-boundary.md)
- [ADR-0007](../decisions/ADR-0007-block-scoped-derived-canonicality-journals.md)
- [ADR-0009](../decisions/ADR-0009-block-bound-abi-provenance.md)
- [ADR-0010](../decisions/ADR-0010-block-pinned-proxy-stage-and-abi-dependency.md)
- [ADR-0011](../decisions/ADR-0011-snapshot-search-stats-and-bounded-adapters.md)
- [ADR-0012](../decisions/ADR-0012-lease-fenced-derived-publication.md)
- [ADR-0013](../decisions/ADR-0013-embedded-spa-serving-and-browser-security.md)
- [ADR-0015](../decisions/ADR-0015-disposable-runtime-accelerators.md)
- [ADR-0018](../decisions/ADR-0018-api-read-replica-routing.md)
- [ADR-0019](../decisions/ADR-0019-authenticated-genesis-state-import.md)
- [ADR-0022](../decisions/ADR-0022-go-ethereum-type-and-raw-rpc-ownership.md)
- [ADR-0023](../decisions/ADR-0023-exact-transaction-state-differences.md)
- [ADR-0024](../decisions/ADR-0024-verifier-v2-workflow.md)
- [ADR-0028](../decisions/ADR-0028-proxy-verification-and-hardhat-e2e.md)
- [ADR-0031](../decisions/ADR-0031-api-owned-solc-js-executor.md)
- [ADR-0032](../decisions/ADR-0032-evidence-based-proxy-detection.md)
- [ADR-0033](../decisions/ADR-0033-trace-bound-log-attribution-and-call-decoding.md)
- [ADR-0034](../decisions/ADR-0034-eip7702-execution-identity-and-constructor-decoding.md)
- [ADR-0037](../decisions/ADR-0037-persistent-solcjs-artifact-cache.md)
- [ADR-0038](../decisions/ADR-0038-selector-scoped-erc2535-diamond-identity.md)
- [ADR-0039](../decisions/ADR-0039-pinned-geas-verification-executor.md)
- [ADR-0040](../decisions/ADR-0040-sea-packaged-solcjs-executor.md)
- [ADR-0042](../decisions/ADR-0042-solady-legacy-cwia-identity.md)
- [ADR-0043](../decisions/ADR-0043-factory-derived-verification-provenance.md)
- [ADR-0054](../decisions/ADR-0054-vyper-application-lock.md)
- [ADR-0053](../decisions/ADR-0053-vyper-wasm-distribution.md)
- [ADR-0055](../decisions/ADR-0055-consensus-and-blob-archives.md)
- [Testing](../testing.md)

## Work Items

| ID       | Status | Depends on                                                    | Deliverable                                                                                                                                                                                                             | Verification                                                                                                                                                            |
| -------- | ------ | ------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| P30-T157 | done   | P30-T138                                                      | Reth/Lighthouse primary Preview chain and real BAL all/split acceptance                                                                                                                                                 | Reth/Lighthouse finality/restart, BAL, contracts/tools/x402, gateway, fresh schema, runtime and browser acceptance                                                      |
| P30-T10  | done   | P10, P20                                                      | Dual compilation, automatic candidates, transformations, batch/blueprint/method lookup, asynchronous REST/Etherscan/Sourcify workflows, and lease-safe verification workers                                             | compiler/matcher fixtures, API/PostgreSQL, heartbeat/lease-loss/fatal-error race and production parity tests                                                            |
| P30-T14  | done   | P30-T10                                                       | API-owned architecture-neutral solc-js executor, compiler catalog, checksum-addressed shared cache, Node SEA packaging, and exact provenance                                                                            | catalog/provenance, cache concurrency/restart, image, and production compiler regressions                                                                               |
| P30-T15  | done   | P20-T13, P30-T14, P30-T64                                     | Exact/same-code artifacts and selectors, canonical Genesis runtime-only verification, immutable proxy/implementation binding and identity-fenced interaction                                                            | Publication/ABI, Genesis provenance, wallet authorization, PostgreSQL/reorg, browser and Hardhat tests                                                                  |
| P30-T20  | done   | P20-T05, P20-T13, P30-T15                                     | Persisted-first bounded log/call ABI decoding, trace-bound attribution, canonical failure decoding and Solidity/custom errors with generated APIs and Web disclosure                                                    | codec/budget, catalog, trace, generation, PostgreSQL/race, browser and production parity tests                                                                          |
| P30-T24  | done   | P20-T13                                                       | Unified block-pinned proxy detector/resolver, generated Safe manifests and exact evidence, memoized bounded RPC, public projections, metrics/runbook and bounded backfill without generation flags or shadow comparison | baseline differential, unit/fuzz, adversarial exact-block RPC, Safe factory discovery, authorization/reorg, PostgreSQL/race, browser and production gates               |
| P30-T33  | done   | P00                                                           | Typed role/subsystem builders, independent executable component manifest, shared lifecycle/readiness, bounded HTTP/pool shutdown, optional native API TLS and fail-closed startup                                       | configuration/graph, HTTP/1.1/HTTP/2 cancellation, stuck-borrower, deployment and monolith/split parity tests                                                           |
| P30-T34  | done   | P20, P30-T33                                                  | PostgreSQL job/outbox authority, optional NATS/Redis/S3 accelerators, AWS SDK credential discovery and role-scoped workload identity, and isolated RustFS service acceptance                                            | outage/fallback, credential refresh/redaction/redirect, S3 unit/race, real RustFS and deployment tests                                                                  |
| P30-T35  | done   | P00, P10, P40, P50                                            | Non-root production images, native healthchecks and Compose topologies, streaming single-file IPFS CLI, and local Kubo Preview with an HTTPS gateway                                                                    | image/schema/runtime, CLI/gateway unit/race, TLS isolation, offline roundtrip, exact metadata/restart and native architecture gates                                     |
| P30-T36  | done   | P30-T33, P30-T34                                              | Helm role deployments, HPA, migration job, secrets, network policy                                                                                                                                                      | Helm lint/render tests                                                                                                                                                  |
| P30-T37  | done   | P10, P20, P40                                                 | Configurable structured logs, fenced progress/outcome reporting, OpenTelemetry, bounded Go-runtime/native writer-reader pool metrics, alerts, admin/repair and domain tuning baselines                                  | metric contracts, outage/failover and bounded benchmarks; reference capacity remains P70-T04                                                                            |
| P30-T43  | done   | P20-T11, P20-T13, P30-T15, P30-T20                            | Exact transaction-time EIP-7702 execution identity, state/trace/ABI and constructor provenance, delegated-account interaction and calldata semantics                                                                    | same-transaction delegation, same-block redelegation, clearing, immutable raw evidence, reorg/replay, PostgreSQL, browser and real runtime tests                        |
| P30-T59  | done   | P30-T24, P30-T15, P30-T20                                     | Selector-scoped Diamond detection, Loupe validation, ordered DiamondCut history, reconciliation, historical ABI, collision-safe facet interaction and API/Web projections                                               | bounded hostile RPC/fuzz, add/replace/remove/reorg ordering, PostgreSQL/race, generation, browser and monolith/split tests                                              |
| P30-T64  | done   | P30-T14                                                       | Pinned Geas v0.3.3 virtual-filesystem helper, exact runtime/optional creation matching, language-aware lease-fenced queue/provenance and native/Etherscan/Web workflows                                                 | compiler/process/security, availability, PostgreSQL/race, generation, browser, license and production image tests                                                       |
| P30-T68  | done   | P30-T24, P30-T15                                              | Legacy LibCWIA exact parser/evidence, immutable argument persistence, bounded Solady Solidity-AST decoding and exact-address/same-code ABI reads with separate write authority                                          | parser/fuzz, PostgreSQL/reorg, query/verifier, dynamic bytes/arrays, generation, browser and production Hardhat tests                                                   |
| P30-T79  | done   | P30-T15, P30-T20                                              | Authenticated compilation units and candidates, historical CREATE/CREATE2 unique creation/runtime matching, transitive forward discovery, exact-epoch provenance, bounded backfill and generated API/Web                | generation/heartbeat fences, short publication transactions, fork rewind, retry/pagination, A-to-B-to-A and direct-child provenance, PostgreSQL/race and topology tests |
| P30-T97  | done   | P30-T14, P30-T79                                              | Native/Etherscan/Web Vyper verification, dual Pyodide runtime routing, application-locked PyPI archives/recipe provenance, supported-version policy and reviewed updater                                                | package equivalence/isolation, fresh-process version matrices, PostgreSQL, identity-bound aggregation, native AMD64/ARM64 and monolith/split acceptance                 |
| P30-T138 | done   | P20-T19, P20-T20, P40-T22, P50-T75, P30-T35, P30-T36, P30-T34 | Real Lighthouse PoS Preview, Caddy HTTPS for app/IPFS/RPC/Beacon, explicit archive initialization and role-scoped durable archive deployment                                                                            | TLS/origin/streaming, finality/readback/restart/outage, schema/runtime and all/split acceptance; writer IAM remains deferred                                            |
| P30-T139 | done   | P30-T10, P30-T14, P30-T15, P30-T97                            | Bounded remote eth-bytecode-db discovery, durable scheduling and locally verified source publication with provenance and role parity                                                                                    | Protocol/PG race, creation fallback/Yul, source provenance, common/browser/schema/runtime gates and production Solidity/Vyper topology parity                           |
| P30-T141 | done   | P30-T10                                                       | Sourcify source import through local verification, independent post-publication import, encrypted optional Etherscan credentials and bilingual browser workflows                                                        | Authorization, credential identity/redaction/restart, hostile source/publication, PostgreSQL and browser regressions                                                    |
| P30-T144 | done   | P20-T17, P30-T138, P30-T14                                    | Reproducible Preview ERC-4337/local ENS genesis, pinned Rundler, management CLI and ordinary-verifier publication of all genesis contracts                                                                              | Allocation/source integrity, runtime-only provenance, real PoS submission/rejection/restart and split/all parity                                                        |
| P30-T150 | done   | P20-T27, P40-T27, P50-T81                                     | ENS under enrich with current-chain configuration and shared deployment/lifecycle; real-contract production-image local acceptance                                                                                      | Fresh schema, credential isolation, autonomous discovery/publication, reorg/restart/outage and all/split browser gates                                                  |
| P30-T151 | done   | P30-T150                                                      | Representative Mainnet core-to-publication acceptance and independent reconciliation                                                                                                                                    | Pinned image/manifest/blocks, event and imported candidate provenance, independent RPC comparisons                                                                      |
| P30-T152 | done   | P30-T144, P30-T150                                            | Preview manual scenarios, resumable transaction journal, reports, proxy families and EIP-7702 lifecycle controls                                                                                                        | Tool regressions, isolated split/all Preview CLI and browser acceptance                                                                                                 |
| P30-T153 | done   | P30-T10, P30-T141                                             | Independent verification role, optional NATS wake and PostgreSQL claims, fully asynchronous Sourcify import                                                                                                             | Multi-instance lease/recovery, immutable import input, all/split parity and production deployment gates                                                                 |
| P30-T154 | done   | P30-T153                                                      | Session-owned polling and publication for live sync, maintenance, NFT source discovery, mempool and compiler catalogs; safe concurrent compiler-cache startup                                                           | PostgreSQL contention, lost-session takeover, stale-write and Docker cold-start regressions, common and production parity gates                                         |
| P30-T155 | done   | P30-T154                                                      | Database-wide deadlock gates for managed integration and both runtime layouts, bounded diagnostics and cross-domain concurrency regressions                                                                             | Detector self-tests, PostgreSQL/race, runtime parity and generated/source/documentation gates                                                                           |
| P30-T156 | done   | P30-T10, P30-T153                                             | Compact verification inputs, durable admission budgets, lightweight status and bounded input retention                                                                                                                  | Input boundaries, multi-replica quotas, retention, compiler and all/split acceptance                                                                                    |

## Acceptance

- [x] Primary Preview uses pinned Reth/Lighthouse PoS with header-gated BAL;
      native balances, contracts, safe Bundler validation, payments and role
      parity pass using isolated network material and volumes.

- [x] Compact verification inputs, principal/global admission, source-free status
      and bounded retention pass large-input, concurrent PostgreSQL/race and
      real Hardhat/Foundry/Vyper all/split acceptance.

- [x] Managed PostgreSQL integration and both runtime layouts reject new database
      deadlocks even when retries succeed, retain bounded diagnostics and reject
      incomplete shutdown; cross-domain concurrency and detector self-tests pass.

- [x] Multi-instance observation and publication reject stale owners after session
      loss; concurrent NFT admission and fresh compiler-cache startup pass real
      PostgreSQL race and production all/split regressions.
- [x] Compiler/helper identity, dual compilation, exact matching, immutable
      provenance and canonical lease-fenced publication pass each language's gates.
- [x] Vyper uses application-locked archives and fresh dual-Pyodide SEA execution;
      native architecture and all/split acceptance remain independently required.
- [x] Safe, Diamond, CWIA, EIP-7702 and factory-derived provenance remain distinct;
      ABI reads and wallet writes enforce their own identity and authority fences.
- [x] Sourcify/discovery sources become local evidence only after local verification;
      credentials are encrypted and local/external outcomes remain separate.
- [x] Shared builders and the independent manifest preserve role parity,
      fail-closed startup, readiness, bounded cancellation/shutdown and persistence.
- [x] Optional accelerators retain PostgreSQL authority and bounded fallback;
      role-scoped deployment, native API TLS, telemetry and repair controls pass.
- [x] Caddy, Kubo, real Lighthouse and durable archive Preview preserve origin,
      network/object identity, finality, outage/restart and all/split behavior.
- [x] Preview genesis contracts, Rundler and verifier publication are reproducible;
      authenticated runtime-only verification fabricates no creation evidence.
- [x] Manual scenario tools retain bounded lifecycle actions and durable recovery;
      provider delivery and human visual acceptance remain separately scoped.
- [x] ENS runtime under enrich passed local production tests and independent
      representative Mainnet reconciliation; full directory coverage is not claimed.
- [x] Schema, deployment, feature-aware E2E overlays and native acceptance passed
      at delivery. Local results do not establish public retention, production
      backup/restore, deferred writer IAM or final release capacity/security.

## Current Blockers

The current-chain ENS feature and representative-publication milestones are
complete with [new acceptance evidence](../reviews/ens-current-chain-acceptance.md).
Full directory coverage remains separately reported deployment progress; P70
retains its release security, capacity, recovery and other existing gates.

The following pre-existing deferred deployment limitation remains explicit
and is not cleared by CI.

Preview storage-enforced conditional-write IAM permissions are deferred by
operator decision pending policy evaluation against the pinned RustFS 1.0.5
image. The image upgrade does not enable writer-policy enforcement; application
conditional writes and API read-only access remain active. The
[runbook](../../preview/docs/operations.md#preview-archive-iam-limitation) owns the issue details.
Clearing this requires a fixed release containing upstream
[PR #8615](https://github.com/rustfs/rustfs/pull/8615) and real-container rejection
of unconditional writes and overwrites, with unchanged stored bytes after denial.
The 1.0.5 pin is an evaluation baseline, not evidence of IAM enforcement.
This is a retained deployment limitation
of P30-T138; its completed local acceptance does not establish writer IAM enforcement.
P70 release gates remain independent.

## Evidence

P30-T157 completed locally on 2026-11-12 in the working tree based on `ddacb5a5`.
Reth v2.9.0/Lighthouse v8.3.2 passed metadata, full consensus/finality/restart,
contracts, tools and x402 prebuilt gates, plus gateway, fresh-schema and runtime
acceptance. `make preview-network-check preview-contracts-check`,
`make test-rustfs` and all 270 `make test-e2e` browser cases passed. Rundler
retains safe validation with zero private-chain priority floor and bounded
factory-deployment gas. Tests used isolated macOS/Docker resources; existing
operator volumes were preserved and public-network/remote-CI claims remain separate.

P30-T156 passed 3 MiB Standard JSON and 1.5 MiB multipart reconstruction,
concurrent deduplication/quota/release, unknown-input reservation and binding,
lease takeover, bounded retention, source-free successful status and native
result compatibility. Complete PostgreSQL integration/race, schema/runtime,
Hardhat, Foundry, Vyper and browser gates passed locally. The
[review completion report](../reviews/review-fixes-2026-11-11.md) retains commands,
configuration defaults and measured-source evidence; P70's named reference
environment and 500 RPS / 30-minute acceptance remain separate blockers.

P30-T155 local acceptance completed on 2026-11-06 in the worktree based on
`66fd3974`. `make test-integration`, the [focused PostgreSQL race command](../testing/integration.md#database-wide-deadlock-gate),
and `make test-runtime-e2e-prebuilt` passed with the locally rebuilt production
image. Shared consumers passed `make test-hardhat3-e2e-prebuilt`,
`make test-foundry-e2e-prebuilt` and `make test-vyper-e2e-prebuilt` in both layouts.
`make lint-go`, `make generate-check`, `make docs-check`, `make plan-check`,
`make compose-check` and `make test-workflow-scripts` passed. Real deadlock,
synthetic abort, statistics reset, bounded borrower cleanup and swallowed-error
runner regressions establish the gate independently of business success.
This covers executed schedules, not exhaustive lock-order proof or P70 soak.

P30-T154 local acceptance passed on 2026-11-03 in the worktree based on
`4197eb3adb047770ba64a38e3a1343c11b52f4e0`. Real PostgreSQL 18 race regressions
cover contention, connection termination, takeover, rejected stale writes,
identical NFT admission, session cleanup and snapshot boundaries. Three fresh
compiler-cache volumes each passed eight concurrent container creations.
`make check`, `make test-integration-race`, and the schema/runtime, Preview
metadata and Hardhat prebuilt targets passed; Hardhat used
`VERIFICATION_PARALLEL=2`. Production split tests use two sync, enrich, API,
verification, metadata and maintenance replicas. The final local Linux ARM64
image `etherview:multiinstance-fix` is
`sha256:176bf78e9fd67ae408395e51b5051e8279fb48bb2ec4c039c24db80606702f45`.
This macOS-hosted working-tree acceptance does not establish native Linux,
AMD64, clean-release, capacity or security closure in P70.

P30-T153 local acceptance passed: independent API admission, eight concurrent
PostgreSQL claimers, immutable input races/restart, canonical and stale-lease
fences, and production topologies with two API and two verification instances.
Real NATS delivery/reconnect, outage polling and killed-worker takeover passed.
`make check`, generation/lint/docs/plan/deployment gates, PostgreSQL integration
race, schema and runtime E2E, 234 browser cases, Hardhat/Vyper and affected
Preview contract/metadata/x402 suites passed. These local production-image
results do not establish native release, capacity or security closure in P70.

P30-T10/P30-T14/P30-T15/P30-T20 passed matching, compiler/Genesis
provenance, ABI/proxy/trace decoding and canonical publication unit/fuzz/race and PostgreSQL regressions at delivery.
P30-T43/P30-T59/P30-T64/P30-T68/P30-T79 passed Hardhat, Foundry, browser
and production all/split gates for EIP-7702, Diamond, Geas, CWIA and
factory-derived verification. P30-T24's unified detector
also passed immutable baseline comparisons and exact-block adversarial tests;
ADR-0032 still requires independent deployment review.

P30-T141 passed encrypted credentials, queue/restart/deduplication, hostile
source import, compiler mismatch/reorg and bilingual browser checks on the
worktree based on `d6da23b`. Local HTTPS/compiler fixtures and a public response
shape check do not establish live Sourcify publication or service availability.
P30-T139 passed controlled HTTPS discovery and real Solidity/Vyper compilation
in both local ARM64/Docker topologies. Public search returned HTTP 403 on
2026-10-25; public coverage was not established.

P30-T97's application-locked dual-runtime acceptance passed native Linux
AMD64/ARM64 compiler matrices and both production layouts in
[run 38539515561 attempt 2](https://github.com/islishude/etherview/actions/runs/38539515561/attempts/2)
and [attempt 3](https://github.com/islishude/etherview/actions/runs/38539515561/attempts/3).
The records cover 25 versions / 352 inputs per architecture and bind reused
artifacts where applicable. ADR-0054 owns the current supported-version and
release policy; these historical runs do not cover later changes.

### Production acceptance boundaries

P30-T33/P30-T34/P30-T35/P30-T36/P30-T37 passed component/startup/readiness,
HTTP/SSE cancellation, bounded shutdown, pgx locks/transactions, telemetry,
Compose/Helm, accelerator outage/fallback and role-scoped credentials checks.
Schema/runtime and native architecture acceptance also covered UserOperations,
holders, private tools, Genesis and Amsterdam projections. Local Kubo tests
proved two canonical metadata versions and restart persistence. These checks
do not prove public IPFS availability or P70-T04 reference capacity.

P30-T138 passed Caddy TLS/origins/streaming, local RustFS, full schema/runtime,
metadata and real Geth/Lighthouse consensus gates. Private PoS acceptance
verified blob/KZG readback, finality advancement, API read-only denial,
network/object retention and all/split outage/restart recovery. Local results
prove neither public retention nor production database/archive backup/restore.
Storage-side writer IAM remains deferred under Current Blockers.

P30-T144 passed reproducible genesis, Rundler safe validation, standard RPC,
submission/rejection/restart, ENS and billing in real PoS split/all Preview.
[CI run 40681596433](https://github.com/islishude/etherview/actions/runs/40681596433)
passed all 31 jobs at `562b42fcd0cbf2e1446bb000e48254403ff080e1`, including
native AMD64/ARM64 acceptance. Local genesis verification on 2026-10-28 published
all 24 code-bearing allocations through the ordinary verifier, reused them after
restart and fabricated no creation evidence. Neither result clears writer IAM.

P30-T152 passed 24 tool regressions and isolated production runs: each layout
published 17 initial scenarios and completed 38 lifecycle actions. Bilingual
desktop/mobile checks covered manual refresh and initial/final state. See
[tool acceptance](../../preview/docs/tools.md#acceptance) for environment and
limits. Wallet-provider actions, live payments and final human visual review
remain separate manual evidence.

### Current-chain ENS acceptance

P30-T150 passed shared enrich configuration/component parity, secret scoping,
real-contract discovery/migration/reorg/restart/outage, PostgreSQL race,
`make check`, schema/runtime, Preview contracts and 155 browser tests locally.
P30-T151's [report](../reviews/ens-current-chain-acceptance.md) pins image,
manifest and Mainnet block 26299443: 766 independently matched stored logs and
seven imported v1/v2/CCIP/primary samples passed publication, database-only reads,
restart and independent reconciliation. Event-discovered live samples are zero.
Full directory coverage and P70 release security/capacity/soak remain separate.
