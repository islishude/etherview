# P30 — Contract Platform & Runtime Operations

Status: `done`

This is the canonical plan for contract verification, contract intelligence,
and the shared runtime/operations platform. P60/P70 release and live-payment blockers remain outside this plan.

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

- [ADR-0056](../decisions/ADR-0056-bytecode-db-source-discovery.md)
- [ADR-0052](../decisions/ADR-0052-amsterdam-explorer-support.md)
- [ADR-0027](../decisions/ADR-0027-process-native-api-tls.md)
- [Architecture](../architecture/overview.md)
- [ADR catalog](../decisions/index.md)
- [Proxy baseline review](../reviews/P58-oz-proxy-baseline.md)
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
| P30-T10  | done   | P10, P20                                                      | Dual compilation, automatic candidates, transformations, batch/blueprint/method lookup, asynchronous REST/Etherscan/Sourcify workflows, and lease-safe verification workers                                             | compiler/matcher fixtures, API/PostgreSQL, heartbeat/lease-loss/fatal-error race and production parity tests                                                            |
| P30-T14  | done   | P30-T10                                                       | API-owned architecture-neutral solc-js executor, compiler catalog, checksum-addressed shared cache, Node SEA packaging, and exact provenance                                                                            | catalog/provenance, cache concurrency/restart, image, and production compiler regressions                                                                               |
| P30-T15  | done   | P20-T13, P30-T14                                              | Exact/same-code verified artifacts, atomic selector indexes, immutable proxy/implementation/management binding, and identity-fenced standard proxy interaction                                                          | publication/ABI, writer-query, upgrade/reorg, authorization/wallet, PostgreSQL, browser and Hardhat tests                                                               |
| P30-T16  | done   | P30-T14, P30-T64                                              | Allow authenticated canonical Genesis predeploys to use runtime-only native and Etherscan-compatible address verification without fabricating creation evidence                                                         | target-resolution, publication-fence, PostgreSQL, and compatibility regressions                                                                                         |
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
| P30-T138 | done   | P20-T19, P20-T20, P40-T22, P50-T75, P30-T35, P30-T36, P30-T34 | Real Lighthouse PoS Preview, role-scoped durable archive deployment, explicit initialization, and outage/recovery parity with documented deferred writer IAM enforcement                                                | Preview consensus/metadata, RustFS, schema/runtime, finality, permissions, restart, and common gates                                                                    |
| P30-T139 | done   | P30-T10, P30-T14, P30-T15, P30-T97                            | Bounded remote eth-bytecode-db discovery, durable scheduling and locally verified source publication with provenance and role parity                                                                                    | Protocol/PG race, creation fallback/Yul, source provenance, common/browser/schema/runtime gates and production Solidity/Vyper topology parity                           |
| P30-T140 | done   | P30-T10                                                       | Optional Etherscan credentials for Sourcify imports, authenticated encryption before durable enqueue, credential-safe request identity and bilingual Web inputs                                                         | API, redaction/tamper/deduplication, PostgreSQL worker and browser regressions                                                                                          |
| P30-T141 | done   | P30-T10, P30-T140                                             | Sourcify-to-Etherview source import through canonical local verification, explicit post-publication import and accessible verification choices                                                                          | API authorization, hostile-source and PostgreSQL publication regressions, bilingual browser and select visual checks                                                    |
| P30-T142 | done   | P30-T138                                                      | Caddy Preview HTTPS migration for app, IPFS, Geth and Beacon on *.etherview.localhost, including browser origins and streaming                                                                                          | Real Caddy boundary regressions, browser authentication, Preview metadata/consensus, runtime/RustFS and common gates                                                    |
| P30-T143 | done   | P20-T22, P20-T17, P30-T138                                    | Reproducible Preview genesis ERC-4337 v0.9 and .test ENS contracts, Alto Bundler and management CLI                                                                                                                     | Reproducible genesis, real PoS safe-mode Bundler and ENS CLI/API/browser, split/all restart and existing Preview gates                                                  |
| P30-T144 | done   | P30-T143                                                      | Replace Preview Alto with pinned Rundler v0.9 support, remove Alto-only genesis helpers and patch                                                                                                                       | Reproducible genesis, safe validation, standard RPC, real PoS submission/rejection/restart in split/all and existing Preview gates                                      |
| P30-T145 | done   | P30-T143, P30-T14                                             | Verify every Preview genesis contract through the normal local verifier during Compose startup                                                                                                                          | Complete source inventory, bounded failure and restart regressions, real Preview publication for every non-empty allocation                                             |

## Acceptance

- [x] Reproducible real-constructor allocations match Geth block zero and
      authenticated genesis import. Input fingerprints reject stale or tampered
      bundles while preserving existing system and x402 allocations.
- [x] Safe-mode v0.9 Bundler submission and local .test ENS registration, renewal,
      transfer/reclaim and record management pass through the CLI, API and browser
      in split/all layouts, including failure paths and container recreation.

- [x] Existing Sourcify sources import from an explicit source chain without
      re-upload through ordinary local compilation and canonical/lease-fenced
      publication on the deployment's chain. Direct and post-publication imports
      keep local and external outcomes separate; independent source/metadata
      uploads, bilingual service cards and select controls pass keyboard, theme
      and mobile browser checks.
- [x] Optional Etherscan credentials remain encrypted across durable Sourcify
      submission/restart, never enter responses or logs, and remain separate
      from Etherview authentication in the bilingual Web flow.
- [x] Compiler/helper identity, exact runtime matching and canonical publication are authenticated.
- [x] Solidity/Yul, Vyper and Geas retain separate bounded execution contracts.
- [x] Vyper uses application-locked archives and explicit fresh-process runtime routing.
- [x] Safe, Diamond, CWIA, EIP-7702 and factory-derived provenance remain distinct.
- [x] ABI reads and wallet writes enforce their exact historical/current identity fences.
- [x] Shared components preserve monolith/split readiness, shutdown and persistence parity.
- [x] Optional accelerators preserve PostgreSQL authority and bounded failure behavior.
- [x] Native architecture and production topology evidence exists for the delivered compiler changes.
- [x] Go-runtime and writer/reader pool telemetry is bounded and supports
      reproducible performance baselines without changing readiness authority.
- [x] API archive credentials are read-only and enrich credentials are read/write;
      initialization runs explicitly outside core role dependencies. Optional
      endpoint/object-store outages do not block core recreation or readiness.
- [x] Real Geth/Lighthouse Preview preserves network/object identity and passes
      locally signed blob transactions, independent Beacon/S3/API readback,
      recovery, finalized-epoch advancement, and all/split publication.
- [x] Public-network retention, production backup/restore, and deferred storage-side
      writer IAM enforcement are not inferred from local Preview acceptance.
- [x] Runtime builders and capability modules fail closed on incomplete dependencies.
- [x] SSE, shutdown and worker leases preserve bounded cancellation and recovery.
- [x] Local Kubo, streaming IPFS CLI and the independent HTTPS gateway pass acceptance.
- [x] Applicable schema, production topology and native architecture gates completed.
- [x] Domain hardening in P10, P30, P40, P50, and the completed P60 billing
      milestones has aggregate generation, source, security, race, integration,
      browser, deployment, and monolith/split
      production acceptance; live-payment and reference-capacity gates remain separate.
- [x] UserOperation runtime fixtures opt in explicitly; unrelated Hardhat/Foundry
      overlays remain feature-off with feature-derived readiness. The testing
      guide owns the shared-overlay defaults, cross-suite sequence, timeout,
      and failure-signature rules.
- [x] PostgreSQL integration retains a bounded timeout above observed cold-CI
      duration without skipped/weakened tests; a fresh remediation-revision CI
      run passed integration and native AMD64/ARM64 Hardhat/Foundry matrices.
- [x] UserOperation, holder, and private-tool feature acceptance includes schema,
      generation, integration/race, browser, and all/split production parity;
      local evidence does not close live-payment or reference-capacity gates.
- [x] Hardhat and Foundry verification E2E explicitly disable ENS and do not
      require or impersonate the ENS Mainnet RPC fixture.
- [x] ENS resolver configuration, maintenance and metrics belong only to API/all
      roles. Fixture acceptance retains identical monolith/split semantics and
      does not imply live Ethereum Mainnet or CCIP gateway availability.
- [x] Diamond detection cross-validates Loupe and immutable functions, checks
      facet code and reports status/completeness/validation independently.
      Ordered add/replace/remove history retains reorg facts; selector-filtered
      interaction and collision handling preserve exact historical ABI identity.
- [x] CWIA arguments derive from bounded canonical Solidity AST, including
      dynamic bytes/arrays. Exact-address then same-chain same-code ABI reads
      do not grant verification or write authority; writes require exact binding.
- [x] Factory matches are unique, bind exact parent/child code epochs and keep
      matching outside ingestion and publication transactions. Generation/lease
      fences, canonical rechecks, retry idempotency and fork-aware rewind guard
      historical and transitive forward discovery without losing provenance.
- [x] Worker lease loss remains task-local while fatal compiler errors retain
      their classification. Local Kubo metadata acceptance checks two canonical
      versions, one attempt each and restart persistence; its gateway/CA isolation
      preserves strict production SSRF rules and proves no public-IPFS availability.

- [x] Optional process-native API TLS validates its certificate/key pair before
      binding, supports TLS 1.2/1.3 and HTTP/2, and never falls back to HTTP on
      invalid material. Secrets remain API/all-scoped; rotation uses restart.
- [x] Native application healthchecks and Compose `--wait` own Preview readiness;
      explicit lifecycle setup remains required. Configurable JSON/text log levels
      and reporter-fenced rate-limited progress/outcome logs retain bounded fields.
- [x] Hardhat provider submission/status polling and durable proxy verification
      pass production paths; independent Foundry acceptance retains both layouts,
      exact compiler provenance and native AMD64/ARM64 coverage.

## Current Blockers

No remaining delivery blockers. The following deferred deployment limitation
remains explicit and is not cleared by CI.

Preview storage-enforced conditional-write IAM permissions are deferred by
operator decision until RustFS 1.0.5 is evaluated. The deployed RustFS 1.0.4
still does not resolve this IAM limitation; application conditional writes and
API read-only access remain active. The
[runbook](../../preview/docs/operations.md#preview-archive-iam-limitation) owns the issue details.
Clearing this requires a fixed release containing upstream
[PR #8615](https://github.com/rustfs/rustfs/pull/8615) and real-container rejection
of unconditional writes and overwrites, with unchanged stored bytes after denial.
The 1.0.5 target is an evaluation checkpoint, not a guarantee of a fix.
This is a retained deployment limitation
of P30-T138; its completed local acceptance does not establish writer IAM enforcement.
P60/P70 release blockers remain independent.

## Evidence

P30-T144 completed on 2026-10-28. Rundler 0.11.0 replaces Alto and its
simulation allocations. Local real-PoS Preview acceptance covered genesis,
safe validation, standard RPC, rejection/restart, ENS, billing and split/all
parity. [CI run 40681596433](https://github.com/islishude/etherview/actions/runs/40681596433)
passed all 31 jobs at `562b42fcd0cbf2e1446bb000e48254403ff080e1`, including
security/licenses, container/Compose/Helm, production schema/runtime, Preview,
and native AMD64/ARM64 production acceptance. These remote results close the
remaining common, deployment and runtime gates previously blocked by local
DNS and registry access. They do not cover subsequent uncommitted changes or
clear the deferred RustFS IAM limitation and independent P60/P70 release gates.

P30-T143 passed locally on macOS ARM64 on 2026-10-28.
`make preview-contracts-check` reproduced the allocations and covered controlled
commitment/expiry/grace-period failures. `make test-preview-contracts-prebuilt`
verified real Geth/Lighthouse genesis state, authenticated import, safe-mode
Alto v0.9 deployment/call/revert and rejection paths, local ENS CLI operations,
search/primary-name publication, browser display and persistent split/all parity.
Reports retain allocation/source digests and transaction/UserOperation hashes.
The full common check, PostgreSQL race, schema/runtime and 149 browser tests
passed, together with Caddy and Preview metadata/consensus/x402 regressions.
Tests used independent projects and temporary volumes; daily Preview was not
reset. Remote CI and P60/P70 release gates are not established by this evidence.

P30-T142 passed locally on macOS ARM64 on 2026-10-26: `make test-preview-gateway`
against the official Caddy 2.11.7 image, both Preview E2E prebuilt targets after
building the application and pulling Preview images, `make check`, `make test-e2e`
(148 browser tests), `make test-runtime-e2e`, and `make test-rustfs`.
Caddy regressions cover TLS, raw IPFS paths/bodies, cancellation and timeouts,
CORS and replacement origins, streaming, failed writes without retries, upstream
isolation, certificate startup failures and bounded shutdown. Real Preview
acceptance covers HTTPS transactions, IPFS, finalized Beacon data, all/split
parity, and browser SIWE/CSRF/Secure cookies, wallet URLs, CORS, WSS and SSE.
Tests use isolated name resolution and disposable loopback ports; this does not
claim a daily Preview migration, system hosts changes or a remote CI run.

P30-T141 passed on 2026-10-25 against the worktree based on `d6da23b` on local
macOS ARM64: Go package and focused race tests, PostgreSQL 18 local publication,
compiler mismatch and reorg regressions, 659 Web tests, 129 browser tests, and
generation, OpenAPI, lint, docs, plan and deployment checks. Local HTTPS and
compiler fixtures prove import boundaries; a public Sourcify GET checked response
shape only. No live public publication/import or remote CI acceptance is implied.

P30-T140 passed on 2026-10-25 against the worktree based on `d6da23b` on local
macOS ARM64: Go package tests and focused race tests, PostgreSQL 18 encrypted
queue/restart/deduplication regression, 647 Web tests, 127 browser tests, and
generation, OpenAPI, lint, docs, plan and deployment checks. External calls used
local HTTPS fixtures; this does not establish live Sourcify/Etherscan service
availability or remote CI acceptance.

P30-T33/P30-T35/P30-T37 retain process TLS, certificate deployment, native
healthcheck/Compose lifecycle and bounded logging acceptance. Historical
Genesis, analytics and compiler production paths passed their feature gates.
Amsterdam headers/gas/native transfers passed local Go/race, PostgreSQL/race,
generation, browser, schema, runtime/x402, Hardhat, Foundry and real-Geth Preview
acceptance in both layouts. Protocol-log projection additionally passed
publication/corruption/reorg and bilingual browser regressions. These delivery
records do not establish a new native CI run, live network or production rollout.

P30-T138 passed `make check`, `make source-check`, `make test-integration-race`,
`make test-e2e`, `make test-rustfs`, `make test-schema-e2e`, and
`make test-runtime-e2e` at its delivery revision. Production monolith and split
roles preserved core readiness through optional endpoint outages, restart,
and dependency recovery.
Geth v1.17.8 / Lighthouse v8.3.0 passed real private PoS/finalized-epoch
advancement, locally signed blob transactions, independent Beacon/S3/API byte
and KZG/hash checks, read-only denial, outages, retained network/object identity,
and both publication layouts. `make test-preview-metadata`,
`make test-preview-consensus`, `make test-rustfs`, `make test-runtime-e2e`, and
`make check` passed locally; the license scanner required an explicit current
`GOROOT` without relaxing any gate. These historical local protocol/container
results do not prove public historical retention, production backup/restore,
or storage-enforced writer conditional permissions. Operators must verify
retention and database/archive recovery together; the IAM clearing condition
remains above.

P30-T24 passed seven immutable pre-change observation fixtures, focused
detector/authorization/RPC race regressions, the full PostgreSQL integration
race suite, all 50 browser tests, and fresh-schema acceptance. The delivery revision
production image passed runtime, local x402, Hardhat and Foundry acceptance in
both monolith and split roles. Generation, source, lint, Go/Web/race, security,
license, deployment, docs and plan gates passed; the local license scanner
required an explicit current `GOROOT`. These are local macOS ARM64/Docker
results, not new native Linux matrix, live-chain or production rollout evidence.
ADR-0032 owns the fresh-baseline cutover and separate deployment review.

P30-T97 passed local helper/aggregation regressions, application count/parity race
tests, provider compatibility, tagged E2E lint, source/docs/plan/deployment checks
and both local application suites. Native Linux AMD64/ARM64 acceptance passed in
[run 38539515561 attempt 2](https://github.com/islishude/etherview/actions/runs/38539515561/attempts/2)
and [attempt 3](https://github.com/islishude/etherview/actions/runs/38539515561/attempts/3),
including all 25 versions / 352 fixture inputs per architecture and both
application topologies. Attempt 2 reused attempt-1 image/package artifact IDs
after cancellation and failed-only rerun; both final bundles passed the existing
application acceptance tool. PR #148 records timings: recovery 13m50s / 122.27
runner-minutes, full rerun 16m43s / 125.60 versus baseline 18m40s / 119.18.
Recovery excludes reused jobs; these samples do not establish a stable speedup.

P30-T139 passed protocol and PostgreSQL race regressions, `make check`, browser,
fresh-schema and runtime gates, plus the Hardhat/Vyper production suites. The
final discovery image passed controlled HTTPS source import and real local
Solidity/Vyper compilation in both topologies; Yul creation fallback and
cross-address/chain isolation passed PostgreSQL regressions. These are local
ARM64/Docker results. Public search from this host returned HTTP 403 on
2026-10-25, so public-source coverage and new remote CI acceptance remain unproven.

Retained completed milestones: P30-T10, P30-T14, P30-T15, P30-T16, P30-T20, P30-T24,
P30-T33, P30-T34, P30-T35, P30-T36, P30-T37, P30-T43, P30-T59, P30-T64, P30-T68,
P30-T79, P30-T97, P30-T138, P30-T139.

Contract matching, compiler provenance, ABI/proxy/trace decoding and
canonical publication passed focused unit/fuzz/race and PostgreSQL regressions.
Hardhat, Foundry, embedded-browser and production monolith/split gates covered
Safe, Diamond, Geas, CWIA, EIP-7702 and factory-derived verification at their
respective delivery revisions. Lifecycle, telemetry, deployment and optional
accelerator outage/fallback checks passed; RustFS transport passed the real
service gate and S3 credential/redirect regressions locally.

Dual-runtime acceptance passed all 13 checks in
[run 38326160375](https://github.com/islishude/etherview/actions/runs/38326160375)
at `81cd91396f18f8000646f700e6a1f2a7334428ee`, including both native Linux
architectures and monolith/split Hardhat. Application-locked assembly then
passed [run 38331342142](https://github.com/islishude/etherview/actions/runs/38331342142)
at `9d37872cf043b16ac1c7934204a68a85d0f8b9c6`; both architecture records passed
the application acceptance validator. These historical runs covered 26
versions before Vyper 0.2.0 retirement.

The subsequent supported-version retirement and reviewed updater passed all
13 checks in [run 38422689050](https://github.com/islishude/etherview/actions/runs/38422689050)
at `ed2d01cc3276acc8b7aa6052876a3dbaedeb7c04`, including native matrices,
Hardhat/Foundry, PostgreSQL, browser, security/license and Preview acceptance.
Later maintenance is not covered by that run. No production rollout is claimed.
The current distribution and supported-version policy are owned by ADR-0054;
retired native helpers, hosted catalogs and custom advisory approval work are
not outstanding delivery tasks.

Go-runtime and writer/reader pool metric contracts passed focused checks and
common gates. Capacity tuning remains backed by domain benchmarks; local
measurements do not replace the operator-provisioned P70-T04 capacity report.

[Detailed completion record](https://github.com/islishude/etherview/blob/e665a0b9b5dcee02e6d90d3a2224a76031cbda3d/docs/plans/P30-contract-verification.md#evidence).
These are historical results, not tests rerun by this plan cleanup.

### Production acceptance boundaries

P30-T35 retains aggregate acceptance for UserOperation, holder, and
private-tool boundaries. EntryPoint v0.6-v0.9 decoder/configuration,
PostgreSQL normal/race, generation, and `make check` passed. Fresh-schema and
production runtime tests exercised a real Anvil packed v0.9 transaction through
both worker/API layouts with identical durable/public results; unrelated
feature-off Hardhat/Foundry fixtures retained independent acceptance. The
recorded overlay/timeout remediation passed fresh CI integration and both
architectures of Hardhat/Foundry; this is historical, not a new CI run.

Holder checks passed schema/runtime, generation, integration/race, browser,
Hardhat, Foundry, deployment, security/license, and common gates. Transient
compiler download failures required unchanged retries and do not establish
upstream availability. Private watch/notification/export acceptance passed
local `make check`, integration/race, browser, schema, and production runtime
on 2026-09-26. P70-T04 still requires high-cardinality holder reads concurrent
with reconciliation in the operator-provisioned reference environment.

SQL/source ownership, typed runtime builders, HTTP shutdown/SSE, worker
lease recovery and version-fenced home state passed focused normal/race,
generation, PostgreSQL and browser gates. Native pgx read/write, cancellation,
session-lock disposal and topology acceptance passed; the development guide
retains the bounded benchmark and native pgx acceptance summary.

[PR 92 CI run 35474508539](https://github.com/islishude/etherview/actions/runs/35474508539)
at `aebb61fee2783e3f6bb8810a81296b05c142bce6` passed all 11 jobs, including
production schema/runtime and native AMD64/ARM64 Hardhat/Foundry. Later local
Kubo acceptance replaced the failed public-gateway prerequisite: the exact
`make test-preview-metadata` passed two canonical metadata versions, one
attempt each, gateway/worker restart and immutable content checks. That local
replacement does not establish public-IPFS availability.

Neutral contracts and production/test decomposition passed import-graph,
source/lint, focused, and topology checks. Aggregate bounded-runtime acceptance
passed `make check`, explicit integration/race and browser suites, schema,
runtime/x402, Hardhat, and Foundry production gates at its delivery revision.
This covers domain hardening, not P60-T17 live payments or P70-T04 capacity;
short local benchmarks establish regression coverage only.

### Preview genesis verification acceptance

P30-T145 passed on 2026-10-28 against local Geth/Lighthouse and the production
API: all 24 code-bearing genesis allocations have exact-address runtime
publications; a repeated run and API restart reuse all 24. The source inventory
includes constructor-created children and fixed system/utility/application
inputs, with no synthetic creation evidence. Input generation checks, 15 helper
regressions, Compose rendering, Preview lint, repository formatting, docs and
plan checks passed. This is local Preview acceptance, not a production rollout
or additional remote CI evidence.
