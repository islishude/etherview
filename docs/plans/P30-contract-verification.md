# P30 — Contract Platform & Runtime Operations

Status: `done`

This is the canonical plan for contract verification, contract intelligence,
and the shared runtime/operations platform. P70/P73 release and live-payment blockers remain outside this plan.

## Outcome

Etherview provides fail-closed contract verification and contract intelligence
over exact chain, address, runtime, block, and generation identities. It supports
the maintained Solidity/Yul, pinned Geas and pinned Vyper verification paths, authenticated
artifact reuse, standard/Safe/Diamond/CWIA proxy evidence, trace-bound ABI and
failure decoding, EIP-7702 execution identity, factory-derived verification,
and the PostgreSQL-authoritative monolith/split runtime, deployment, telemetry,
and disposable accelerator boundaries.

## References

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
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P30-T10 | done | P10, P20 | Dual compilation, automatic candidates, transformations, batch, blueprint, and method lookup | compiler and matcher fixtures |
| P30-T11 | done | P30-T10 | Native asynchronous REST, Etherscan adaptation, and explicit Sourcify workflows | API and integration tests |
| P30-T14 | done | P30-T11 | Architecture-neutral compiler catalog discovery and exact compiler provenance | catalog/provenance regressions |
| P30-T15 | done | P20-T13, P30-T14 | Bind verified OpenZeppelin 5.6.1 proxy, implementation, and management artifacts to exact immutable runtime identities and invalidate stale interaction bindings | verifier, PostgreSQL, immutable-source, upgrade/reorg, and Hardhat fixture tests |
| P30-T16 | done | P30-T14, P30-T66 | Allow authenticated canonical Genesis predeploys to use runtime-only native and Etherscan-compatible address verification without fabricating creation evidence | target-resolution, publication-fence, PostgreSQL, and compatibility regressions |
| P30-T17 | done | P30-T16, P40-T17 | Persist verified function selector indexes atomically for newly successful verification results | migration, parser, publication, invalid/empty ABI, and PostgreSQL tests |
| P30-T18 | done | P30-T14, P40 | Writer-authoritative exact/same-code artifact resolution across native and Etherscan reads | repository, API, compatibility, migration, and generation tests |
| P30-T19 | done | P30-T18, P50 | Standard proxy implementation interaction with fresh identity fencing and verified-only management | proxy API, wallet, frontend, browser, and Hardhat tests |
| P30-T20 | done | P30-T18, P20, P40, P50 | Persisted-first, bounded read-time ABI decoding for transaction logs | codec, catalog, API, frontend, reorg, and PostgreSQL tests |
| P30-T24 | done | P20-T13 | Shared block-pinned detection context, detector interface, resolver, structured outcomes, and memoized RPC accounting | detector/resolver unit and fuzz tests; existing OZ suite |
| P30-T25 | done | P30-T24 | Generated Safe runtime/singleton/factory manifests plus bulk and deep Safe detectors, including slot 0 and `masterCopy()` consistency | generated-manifest check; positive, negative, adversarial, and fixed-block integration fixtures |
| P30-T26 | done | P30-T25 | Structured API/UI persistence, metrics, runbook and bounded backfill (rollout controls superseded by P30-T137) | OpenAPI generation; integration/race/browser/runtime gates; rollout-control review |
| P30-T27 | done | P30-T26 | Add a factory-created SafeProxy 1.4.1 production-path E2E through Trace discovery, generation-fenced persistence, and the public read boundary | pinned artifact checks; Compose rendering; monolith/split Hardhat production E2E; common gates |
| P30-T28 | done | P20, P40, P50, P30-T19, P30-T20 | `trace@2` log attribution, complete call ABI projection, generated API, compact Web disclosure, migration, rollout, and regression closure | codec, trace, PostgreSQL, generation, browser, runtime, Hardhat, and common gates |
| P30-T31 | done | P30-T28, P30-T48 | Expose canonical transaction failure decoding and render custom plus Solidity builtin errors in the transaction Overview | codec, catalog, generated API, Web, PostgreSQL, browser, runtime, and common gates |
| P30-T33 | done | P00 | Shared component lifecycle, role graph, readiness, graceful shutdown | lifecycle/parity tests |
| P30-T34 | done | P20 | PostgreSQL job/outbox plus optional NATS, Redis, and S3 adapters | outage/fallback tests |
| P30-T35 | done | P00, P10, P40, P50 | Multi-stage non-root image and monolith/distributed Compose profiles | production Compose Go E2E |
| P30-T36 | done | P30-T33, P30-T34 | Helm role deployments, HPA, migration job, secrets, network policy | Helm lint/render tests |
| P30-T37 | done | P10, P20, P40 | Structured logs, OpenTelemetry, Prometheus metrics, alerts, admin/repair | observability tests |
| P30-T38 | done | P10, P20, P40, P50 | Backfill tuning, HA/failover, cache/rate policy, reference capacity profile | soak/load tests |
| P30-T42 | done | P30-T34, P30-T36 | Add AWS default credential discovery and API-role-scoped workload identity for the S3 trace cache | credential-chain, refresh, redaction, Compose, Helm, and common gates |
| P30-T43 | done | P20, P30-T14, P40, P50, P30-T28, P30-T42 | `state_diff@2`, `trace@3`, and `abi@3`; exact EIP-7702 and constructor persistence/projection; generated APIs; delegated-account Web interaction; migration, rollout, ADR, and regression closure | codec, state diff, trace, PostgreSQL, generation, browser, integration, runtime, Hardhat, and common gates |
| P30-T48 | done | P30-T43 | Expose exact transaction-time calldata decoding and render EIP-7702 delegation, redelegation, and clearing semantics in the transaction Overview | catalog, API, generation, Web, integration, browser, runtime, and common gates |
| P30-T58 | done | P30-T48, P30-T17, P40-T18, P30-T15, P50-T57, P70-T29, P50-T62 | Materialize transaction-scoped effective EIP-7702 execution identities in `abi@4`, preserving raw evidence while fixing same-transaction delegation calls and same-block redelegation isolation | ABI/store/Catalog/API/Web regressions, PostgreSQL reorg and replay, real runtime E2E, Preview, and common gates |
| P30-T59 | done | P20, P30-T14, P40, P50, P30-T25, P30-T31 | Replace the singular implementation assumption with additive selector-scoped Diamond targets in persistence, query, OpenAPI, generated clients, and compatibility projections | migration, query, OpenAPI generation, model unit and integration tests |
| P30-T60 | done | P30-T59 | Fixed-block bounded ERC-2535 detector with Loupe fallback, cross-validation, immutable functions, facet code checks, candidate gates, and independent status/completeness/validation | detector unit/fuzz tests, hostile-return limits, exact-block RPC fixtures |
| P30-T61 | done | P30-T60 | Raw DiamondCut indexing, ordered selector intervals, reorg retention, replay, snapshot reconciliation, and standard DiamondCut presence | PostgreSQL integration/race, add/replace/remove, reorg and same-transaction ordering tests |
| P30-T62 | done | P30-T61 | Historical selector-bound ABI decoding, selector-filtered Diamond interaction, collision handling, facet/current/history API, bilingual UI, and monolith/split acceptance | generated clients, Vitest, Playwright, runtime E2E, common gates |
| P30-T64 | done | P30-T14, P40, P50, P30-T42 | Pinned v0.3.3 helper subprocess, virtual source filesystem, deterministic exact compilation, runtime identity, and compiler routing | compiler unit/security/process tests, license and image checks |
| P30-T65 | done | P30-T64 | Language-aware queue claiming, lease-bound Geas provenance, exact runtime/optional creation matching, canonical publication, and per-family availability | worker, repository, PostgreSQL integration and race tests |
| P30-T66 | done | P30-T65 | Native address API, fixed compiler listing, Etherscan reads, bilingual Web submission and plain-text source workspace | generated clients, Go/Web/API/browser tests |
| P30-T68 | done | P20-T13, P30-T24 | Exact legacy LibCWIA runtime parser, authoritative `proxy@2` projection, independent evidence detector, and persistent `cwia` mechanism | parser/detector unit and fuzz tests; PostgreSQL constraints; `make source-check` |
| P30-T69 | done | P30-T68 | ABI and proxy-verification binding support plus generated native/Etherscan API contracts for raw CWIA arguments | query, verification, API, generation, integration, and reorg tests |
| P30-T73 | done | P30-T69 | Replace the provisional NatSpec schema with bounded canonical Solady CWIA Solidity-AST derivation, dynamic bytes/array decoding, code-hash read projection, and exact-binding write fencing | verifier/query/API/Web tests; production Hardhat/browser/runtime gates; common gates |
| P30-T77 | done | P30-T73 | Resolve exact legacy CWIA event, Trace, Method, selector, and calldata ABI from an exact-address implementation artifact first or a same-chain same-code artifact without granting verification or write authority | focused query/ABI tests; PostgreSQL integration/race; production Hardhat monolith/split; common gates |
| P30-T79 | done | P30-T17 | Fresh-schema authenticated compilation-unit and bounded candidate persistence on successful address verification | codec, digest/provenance, idempotency, migration, and PostgreSQL round-trip tests |
| P30-T80 | done | P30-T79 | Historical canonical CREATE/CREATE2 scanner, creation/runtime unique matcher, durable attempts, and internal derived publication | Factory-to-child Solidity fixtures and PostgreSQL integration tests |
| P30-T81 | done | P30-T80 | Historical parent code-epoch resolution, publication-time canonical recheck, stale handling, and retry idempotency | reorg, reattach, code replacement, and duplicate-work tests |
| P30-T82 | done | P30-T81 | Trace-stage-completion forward enqueue and transitive asynchronous propagation without ingestion-path matching | future-child, nested-factory, retry, and monolith/split tests |
| P30-T83 | done | P30-T82 | Generated provenance/children API, bilingual Web presentation, bounded configuration, metrics, admin backfill, and operations guidance | generated API, Web, observability, browser, deployment, and common gates |
| P30-T86 | done | P30-T83 | Generation-bound trace/proxy forward events, fork-aware scan rewind, canonical attempt writes, and success-reset pagination budgets | state-machine unit and PostgreSQL reorg/pagination regressions; generation/source/plan checks |
| P30-T87 | done | P30-T86 | Heartbeat-fenced scan/event leases plus one prepared match and short canonical publication transaction | lease-contention, slow-match/reorg, stale-publication, integration-race, and runtime parity tests |
| P30-T88 | done | P30-T87 | Exact-epoch parent/child provenance with additive direct-verification creation provenance and unchanged wire shape | A-to-B-to-A, FQN conflict, direct-child, code-hash, API, Web, and generation checks |
| P30-T97 | done | P30-T88 | Native API, Etherscan vyper-json and bilingual Web verification | generated contracts, API, Web and browser tests |
| P30-T115 | done | P30-T42, P30-T36 | Replace MinIO with RustFS and AWS SDK S3 transport, with isolated real-service acceptance | S3 unit/race, RustFS, deployment, security/license, docs and plan gates |
| P30-T127 | done | P30-T88, P30-T97 | Implement dual Pyodide runtime selection and authenticated release tooling | Full fresh-process matrices, authenticated runtime routing and native production parity |
| P30-T128 | done | P30-T88, P30-T97 | Application-locked PyPI assembly and immutable recipe provenance in place of hosted packages and signed catalogs | Package equivalence, isolation, PostgreSQL and native AMD64/ARM64 application acceptance |
| P30-T130 | done | P30-T128 | Retire Vyper 0.2.0 and generate reviewed PyPI update PRs daily | Updater integrity, full supported-version matrices and native production acceptance |

| P30-T136 | done | P30-T128, P30-T130 | Parallel native Vyper fixtures, Vyper application and Hardhat acceptance over one image per architecture | Independent parity/count regressions, identity-bound aggregation, two native CI runs and failed-job rerun evidence |

| P30-T137 | done | P30-T24, P30-T26, P30-T62, P30-T77 | Unify proxy detection, remove generation feature flags and shadow comparison, publish an unversioned detection contract while preserving exact binding and authorization | Baseline differential fixtures, RPC/error regressions, PostgreSQL/reorg, browser, production topology and generated/schema/governance gates |

## Acceptance

- [x] Compiler/helper identity, exact runtime matching and canonical publication are authenticated.
- [x] Solidity/Yul, Vyper and Geas retain separate bounded execution contracts.
- [x] Vyper uses application-locked archives and explicit fresh-process runtime routing.
- [x] Safe, Diamond, CWIA, EIP-7702 and factory-derived provenance remain distinct.
- [x] ABI reads and wallet writes enforce their exact historical/current identity fences.
- [x] Shared components preserve monolith/split readiness, shutdown and persistence parity.
- [x] Optional accelerators preserve PostgreSQL authority and bounded failure behavior.
- [x] Native architecture and production topology evidence exists for the delivered compiler changes.

## Current Blockers

None. P70/P73 retain independent release blockers.

## Evidence

P30-T137 passed seven immutable pre-change observation fixtures, focused
detector/authorization/RPC race regressions, the full PostgreSQL integration
race suite, all 50 browser tests, and fresh-schema acceptance. The current
production image passed runtime, local x402, Hardhat and Foundry acceptance in
both monolith and split roles. Generation, source, lint, Go/Web/race, security,
license, deployment, docs and plan gates passed; the local license scanner
required an explicit current `GOROOT`. These are local macOS ARM64/Docker
results, not new native Linux matrix, live-chain or production rollout evidence.
ADR-0032 owns the fresh-baseline cutover and separate deployment review.

P30-T136 passed local helper/aggregation regressions, application count/parity race
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


Retained completed milestones: P30-T10, P30-T11, P30-T14, P30-T15, P30-T16, P30-T17,
P30-T18, P30-T19, P30-T20, P30-T24, P30-T25, P30-T26, P30-T27, P30-T28, P30-T31,
P30-T33, P30-T34, P30-T35, P30-T36, P30-T37, P30-T38, P30-T42, P30-T43, P30-T48,
P30-T58, P30-T59, P30-T60, P30-T61, P30-T62, P30-T64, P30-T65, P30-T66, P30-T68,
P30-T69, P30-T73, P30-T77, P30-T79, P30-T80, P30-T81, P30-T82, P30-T83, P30-T86,
P30-T87, P30-T88, P30-T97, P30-T115, P30-T127, P30-T128, P30-T130.

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

[Detailed completion record](https://github.com/islishude/etherview/blob/e665a0b9b5dcee02e6d90d3a2224a76031cbda3d/docs/plans/P30-contract-verification.md#evidence).
These are historical results, not tests rerun by this plan cleanup.
