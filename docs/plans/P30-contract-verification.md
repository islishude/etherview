# P30 — Contract Platform & Runtime Operations

Status: `done`

This is the canonical plan for contract verification, contract intelligence,
and the shared runtime/operations platform. Its current work items use the P30
prefix. P70/P73 release and live-payment blockers remain outside this plan.

## Outcome

Etherview provides fail-closed contract verification and contract intelligence
over exact chain, address, runtime, block, and generation identities. It supports
the maintained Solidity/Yul, pinned Geas and pinned Vyper verification paths, authenticated
artifact reuse, standard/Safe/Diamond/CWIA proxy evidence, trace-bound ABI and
failure decoding, EIP-7702 execution identity, factory-derived verification,
and the PostgreSQL-authoritative monolith/split runtime, deployment, telemetry,
and disposable accelerator boundaries.

## Responsibilities

### Verification and compiler platform

P30-T01–P30-T17, P30-T63–P30-T90 and P30-T95–P30-T99 cover durable verification
requests, compiler provenance, exact matching, Sourcify consent, Geas,
authenticated compilation units, CREATE/CREATE2 derivation, canonical
publication, and generation-safe replay.

### Contract identity and ABI intelligence

P30-T18–P30-T62 and P30-T68–P30-T77 cover exact artifact reuse, proxy
detection, Safe and Diamond history, trace-bound logs and failures, EIP-7702
transaction-time execution identity, CWIA immutable arguments, and interaction
fences. These mechanisms retain distinct evidence and authorization contracts.

### Runtime and operations

P30-T33–P30-T42 cover shared lifecycle, role parity, PostgreSQL authority,
optional NATS/Redis/S3 accelerators, image and Compose/Helm deployment,
readiness, telemetry, bounded worker/load policy, repair tooling, and
credential-scoped operational boundaries.

## References

- [Architecture](../architecture/overview.md)
- [ADR catalog](../decisions/index.md)
- [Proxy baseline review](../reviews/P58-oz-proxy-baseline.md)
- [Proxy shadow rollout template](../reviews/proxy-detection-v2-shadow-rollout-template.md)
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
- [ADR-0047](../decisions/ADR-0047-pinned-vyper-executor.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P30-T01 | done | P10, P20 | Verification job/source/compiler/result schema and service boundary | repository tests |
| P30-T02 | done | P30-T01 | Allowlisted compiler manifests, checksum cache, resource-limited sandbox | tamper/limit tests |
| P30-T03 | done | P30-T02 | Solidity/Yul Standard JSON and multi-file exact/metadata-only matching | compiler fixture tests |
| P30-T04 | done | P30-T01 | Sourcify v2 lookup/import and consent-gated submission | mocked API tests |
| P30-T05 | done | P20 | Safe HTTPS/IPFS NFT metadata and media proxy | SSRF/content tests |
| P30-T06 | done | P20 | Configurable name-service resolver and operator labels | resolver/CLI tests |
| P30-T07 | done | P30-T01, P30-T02 | Fail-closed compiler cleanup and immutable publication constraints | cleanup and PostgreSQL regressions |
| P30-T08 | done | P30-T07 | v2 ADR, destructive verification schema, and governance reset | migration and plan checks |
| P30-T09 | done | P30-T08 | Durable dynamic compiler catalog, checksum cache, and generic sandbox runner | catalog and sandbox security tests |
| P30-T10 | done | P30-T09 | Dual compilation, automatic candidates, transformations, batch, blueprint, and method lookup | compiler and matcher fixtures |
| P30-T11 | done | P30-T10 | Native asynchronous REST, Etherscan adaptation, and explicit Sourcify workflows | API and integration tests |
| P30-T12 | done | P30-T11 | Verification UI, deployment configuration, egress, and operations guidance | browser and deployment tests |
| P30-T13 | done | P30-T12 | Common, race, PostgreSQL, security, license, and parity closure | applicable repository gates |
| P30-T14 | done | P30-T13 | Architecture-neutral compiler catalog discovery and exact compiler provenance | catalog/provenance regressions |
| P30-T15 | done | P20-T13, P30-T14 | Bind verified OpenZeppelin 5.6.1 proxy, implementation, and management artifacts to exact immutable runtime identities and invalidate stale interaction bindings | verifier, PostgreSQL, immutable-source, upgrade/reorg, and Hardhat fixture tests |
| P30-T16 | done | P30-T14, P30-T67 | Allow authenticated canonical Genesis predeploys to use runtime-only native and Etherscan-compatible address verification without fabricating creation evidence | target-resolution, publication-fence, PostgreSQL, and compatibility regressions |
| P30-T17 | done | P30-T16, P40-T17 | Persist verified function selector indexes atomically for newly successful verification results | migration, parser, publication, invalid/empty ABI, and PostgreSQL tests |
| P30-T18 | done | P30-T14, P40 | Writer-authoritative exact/same-code artifact resolution across native and Etherscan reads | repository, API, compatibility, migration, and generation tests |
| P30-T19 | done | P30-T18, P50 | Standard proxy implementation interaction with fresh identity fencing and verified-only management | proxy API, wallet, frontend, browser, and Hardhat tests |
| P30-T20 | done | P30-T18, P20, P40, P50 | Persisted-first, bounded read-time ABI decoding for transaction logs | codec, catalog, API, frontend, reorg, and PostgreSQL tests |
| P30-T21 | done | P30-T19, P30-T20 | Architecture, acceptance, common-gate, integration, browser, and production proxy closure | ADR, plan, generation, integration, E2E, Hardhat, and common gates |
| P30-T22 | done | P30-T18 | Null-safe contract artifact rendering and Preview regression coverage | focused Vitest, production build, Preview/browser check, plan check |
| P30-T23 | done | P20-T13 | OZ 5.6.1 call graph, behavior audit, fixed regression inventory, and explicit change baseline | focused proxy tests; `make plan-check` |
| P30-T24 | done | P30-T23 | Shared block-pinned detection context, detector interface, resolver, structured outcomes, and memoized RPC accounting | detector/resolver unit and fuzz tests; existing OZ suite |
| P30-T25 | done | P30-T24 | Generated Safe runtime/singleton/factory manifests plus bulk and deep Safe detectors, including slot 0 and `masterCopy()` consistency | generated-manifest check; positive, negative, adversarial, and fixed-block integration fixtures |
| P30-T26 | done | P30-T25 | Additive API/UI persistence, shadow-mode diffing, metrics, feature flag, runbook, rollback, and bounded backfill | OpenAPI generation; integration/race/browser/runtime gates; rollout-control review |
| P30-T27 | done | P30-T26 | Add a factory-created SafeProxy 1.4.1 production-path E2E through Trace discovery, generation-fenced persistence, and the public read boundary | pinned artifact checks; Compose rendering; monolith/split Hardhat production E2E; common gates |
| P30-T28 | done | P20, P40, P50, P30-T21 | `trace@2` log attribution, complete call ABI projection, generated API, compact Web disclosure, migration, rollout, and regression closure | codec, trace, PostgreSQL, generation, browser, runtime, Hardhat, and common gates |
| P30-T29 | done | P30-T28 | Decode creation-block logs after later same-address verification by exact runtime-code reuse without extending address verification provenance | codec, PostgreSQL, Preview transaction, plan, and common gates |
| P30-T30 | done | P30-T17, P40-T19, P30-T28 | Decode direct Trace calls from exact verified address-range selectors when completed state-diff evidence omits an unchanged target | focused catalog, PostgreSQL, Preview transaction, browser, and common gates |
| P30-T31 | done | P30-T28, P30-T48 | Expose canonical transaction failure decoding and render custom plus Solidity builtin errors in the transaction Overview | codec, catalog, generated API, Web, PostgreSQL, browser, runtime, and common gates |
| P30-T32 | done | P30-T31 | Render Solidity builtin `Error(string)` and `Panic(uint256)` as concise error text without the structured ABI argument table | focused Web, browser, plan, and common gates |
| P30-T33 | done | P00 | Shared component lifecycle, role graph, readiness, graceful shutdown | lifecycle/parity tests |
| P30-T34 | done | P20 | PostgreSQL job/outbox plus optional NATS, Redis, and S3 adapters | outage/fallback tests |
| P30-T35 | done | P00, P10, P40, P50 | Multi-stage non-root image and monolith/distributed Compose profiles | production Compose Go E2E |
| P30-T36 | done | P30-T33, P30-T34 | Helm role deployments, HPA, migration job, secrets, network policy | Helm lint/render tests |
| P30-T37 | done | P10, P20, P30-T01, P30-T02, P30-T05, P40 | Structured logs, OpenTelemetry, Prometheus metrics, alerts, admin/repair | observability tests |
| P30-T38 | done | P10, P20, P30-T07, P40, P50 | Backfill tuning, HA/failover, cache/rate policy, reference capacity profile | soak/load tests |
| P30-T39 | done | P30-T37 | Add exact NFT identity and redacted network diagnostics to durable metadata transition logs | metadata, netpolicy, observability, Preview, and common gates |
| P30-T40 | done | P30-T37, P30-T39 | Add exact bounded operational context to worker, lifecycle, request, RPC, and optional-adapter logs | focused race, PostgreSQL integration, runtime topology, and common gates |
| P30-T41 | done | P30-T39, P30-T40 | Preserve a closed, redacted metadata failure reason and the final retry code when attempts are exhausted | metadata and observability regressions plus common gates |
| P30-T42 | done | P30-T34, P30-T36 | Add AWS default credential discovery and API-role-scoped workload identity for the S3 trace cache | credential-chain, refresh, redaction, Compose, Helm, and common gates |
| P30-T43 | done | P20, P30-T14, P40, P50, P30-T28, P30-T42 | `state_diff@2`, `trace@3`, and `abi@3`; exact EIP-7702 and constructor persistence/projection; generated APIs; delegated-account Web interaction; migration, rollout, ADR, and regression closure | codec, state diff, trace, PostgreSQL, generation, browser, integration, runtime, Hardhat, and common gates |
| P30-T44 | done | P30-T43 | Repair delegated-account Web layout, add delegated-address browser regressions, and close Preview production verification | focused Web tests, production browser, Preview, and common gates |
| P30-T45 | done | P30-T44 | Fix Preview delegation-history SQL and allow delegated reads across canonical-tip advancement while retaining exact write fences | focused Go and Web tests; Preview API/browser verification |
| P30-T46 | done | P30-T45 | Rebuild the delegated-account page as a contract-style hash-tab workbench with lazy history and delegated deep-link regressions | focused Web tests, production browser, and common gates |
| P30-T47 | done | P30-T46 | Restore transaction Authorizations tab query-parameter routing and add a click regression | focused CorePages frontend tests, lint, build, generation, and plan checks |
| P30-T48 | done | P30-T47 | Expose exact transaction-time calldata decoding and render EIP-7702 delegation, redelegation, and clearing semantics in the transaction Overview | catalog, API, generation, Web, integration, browser, runtime, and common gates |
| P30-T49 | done | P30-T48 | Preserve EIP-7702 log execution-address attribution when the delegate code hash is unavailable from prestateTracer, then resolve ABI only through the exact historical code identity | trace/ABI/catalog regressions, live Preview replay, and common gates |
| P30-T50 | done | P30-T49 | Decode ABI `receive` and `fallback` entry points for trace and transaction calldata instead of reporting empty or selectorless calls as unknown functions | ABI/Catalog/API/Web regressions, live Preview verification, and common gates |
| P30-T51 | done | P30-T50 | Report calls with exact empty execution code, including ordinary native transfers to EOAs, as ABI decoding not applicable instead of an unknown function selector | Catalog/API/Web regressions, live Preview verification, and common gates |
| P30-T52 | done | P30-T51 | Keep canonical delegation history discoverable from an address after its current EIP-7702 delegation is cleared | state/query, generated API, Web, browser, integration, and common gates |
| P30-T53 | done | P30-T52 | Order canonical delegation history by its numeric chain position and replace cleared-address Code content with a lazy current-status surface | Catalog, PostgreSQL, generated API, Web, browser, Preview, and common gates |
| P30-T54 | done | P30-T53 | Remove the redundant History action from the cleared-address Status surface while retaining the dedicated History tab | Web, browser, generation, and common gates |
| P30-T55 | done | P30-T54 | Add production-browser and real Prague/Anvil EIP-7702 transaction E2E coverage across authorization outcomes, transaction-time execution identity, clearing, and reorg canonicality | browser, runtime, topology-parity, and common gates |
| P30-T56 | done | P30-T55, P40-T19, P50-T59 | Recover exact top-level execution identity when diff-only prestate evidence omits an unchanged target, including Native Transfer list and transaction-action projection | state-diff, query, Catalog, Web, PostgreSQL, browser, Preview, and common gates |
| P30-T57 | done | P30-T56 | Restore exact transaction-time ABI component metadata and bounded recursive Decoded calldata rendering without current-state fallback | ABI/Catalog/API contracts, Web transformation and DOM, PostgreSQL, browser, runtime, and common gates |
| P30-T58 | done | P30-T57, P40-T19, P50-T63 | Materialize transaction-scoped effective EIP-7702 execution identities in `abi@4`, preserving raw evidence while fixing same-transaction delegation calls and same-block redelegation isolation | ABI/store/Catalog/API/Web regressions, PostgreSQL reorg and replay, real runtime E2E, Preview, and common gates |
| P30-T59 | done | P20, P30-T14, P40, P50, P30-T25, P30-T32 | Replace the singular implementation assumption with additive selector-scoped Diamond targets in persistence, query, OpenAPI, generated clients, and compatibility projections | migration, query, OpenAPI generation, model unit and integration tests |
| P30-T60 | done | P30-T59 | Fixed-block bounded ERC-2535 detector with Loupe fallback, cross-validation, immutable functions, facet code checks, candidate gates, and independent status/completeness/validation | detector unit/fuzz tests, hostile-return limits, exact-block RPC fixtures |
| P30-T61 | done | P30-T60 | Raw DiamondCut indexing, ordered selector intervals, reorg retention, replay, snapshot reconciliation, and standard DiamondCut presence | PostgreSQL integration/race, add/replace/remove, reorg and same-transaction ordering tests |
| P30-T62 | done | P30-T61 | Historical selector-bound ABI decoding, selector-filtered Diamond interaction, collision handling, facet/current/history API, bilingual UI, and monolith/split acceptance | generated clients, Vitest, Playwright, runtime E2E, common gates |
| P30-T63 | done | P30-T14, P40, P50, P30-T42 | Accepted ADR, generated public contract, migration `0044`, and typed Geas request/provenance model | OpenAPI generation, migration and request-model tests, plan check |
| P30-T64 | done | P30-T63 | Pinned v0.3.3 helper subprocess, virtual source filesystem, deterministic exact compilation, runtime identity, and compiler routing | compiler unit/security/process tests, license and image checks |
| P30-T65 | done | P30-T64 | Language-aware queue claiming, lease-bound Geas provenance, exact runtime/optional creation matching, canonical publication, and per-family availability | worker, repository, PostgreSQL integration and race tests |
| P30-T66 | done | P30-T65 | Native address API, fixed compiler listing, Etherscan reads, bilingual Web submission and plain-text source workspace | generated clients, Go/Web/API/browser tests |
| P30-T67 | done | P30-T66 | Pinned sys-asm fixture and monolith/six-role production verification acceptance, documentation, and common-gate closure | Foundry production E2E, schema/runtime E2E, common gates |
| P30-T68 | done | P20-T13, P30-T24 | Exact legacy LibCWIA runtime parser, authoritative `proxy@2` projection, independent V2 detector, and persistent `cwia` mechanism | parser/detector unit and fuzz tests; PostgreSQL constraints; `make source-check` |
| P30-T69 | done | P30-T68 | ABI and proxy-verification binding support plus generated native/Etherscan API contracts for raw CWIA arguments | query, verification, API, generation, integration, and reorg tests |
| P30-T70 | done | P30-T69 | Verified NatSpec schema parsing, packed scalar decoding, bilingual Web presentation, and schema-digest write fence | schema/parser unit tests; Vitest; embedded Chromium E2E |
| P30-T71 | done | P30-T70 | Pinned Solady Hardhat fixture, monolith/six-role parity, operations/testing documentation, and aggregate gates | schema/runtime/Hardhat E2E; security; common gates |
| P30-T72 | done | P30-T71 | Preview regression fix: expose code-hash-authenticated CWIA implementation reads before proxy verification while preserving exact-binding/schema write gates and reason-specific status text | focused target/page tests; embedded Chromium E2E; live Preview inspection |
| P30-T73 | done | P30-T72 | Replace the provisional NatSpec schema with bounded canonical Solady CWIA Solidity-AST derivation, dynamic bytes/array decoding, code-hash read projection, and exact-binding write fencing | verifier/query/API/Web tests; production Hardhat/browser/runtime gates; common gates |
| P30-T74 | done | P30-T73 | Separate exact implementation verification from code-hash artifact availability and replace immutable-argument facts with an accessible Name/Type/Offset/Data table | query/API/write-fence tests; bilingual browser/Preview regressions; common gates |
| P30-T75 | done | P30-T74 | Keep published proxy interaction tabs stable when a fresh operation fence observes a transient latest-stage unavailable snapshot, while failing that operation closed and preserving real target-change refreshes | target/form/page tests; real-Chromium transient-stage regression; live Preview reproduction |
| P30-T76 | done | P30-T75 | Hide the direct verified-artifact submission surface on recognized CWIA shell addresses while retaining implementation artifact source/ABI and proxy interaction | page/unit/browser regressions; live Preview inspection |
| P30-T77 | done | P30-T76 | Resolve exact legacy CWIA event, Trace, Method, selector, and calldata ABI from an exact-address implementation artifact first or a same-chain same-code artifact without granting verification or write authority | focused query/ABI tests; PostgreSQL integration/race; production Hardhat monolith/split; common gates |
| P30-T78 | done | P30-T17 | Reusable candidate matcher and one submitted/derived publication transaction with unchanged ordinary verification behavior | verifier unit and PostgreSQL publication regressions |
| P30-T79 | done | P30-T78 | Fresh-schema authenticated compilation-unit and bounded candidate persistence on successful address verification | codec, digest/provenance, idempotency, migration, and PostgreSQL round-trip tests |
| P30-T80 | done | P30-T79 | Historical canonical CREATE/CREATE2 scanner, creation/runtime unique matcher, durable attempts, and internal derived publication | Factory-to-child Solidity fixtures and PostgreSQL integration tests |
| P30-T81 | done | P30-T80 | Historical parent code-epoch resolution, publication-time canonical recheck, stale handling, and retry idempotency | reorg, reattach, code replacement, and duplicate-work tests |
| P30-T82 | done | P30-T81 | Trace-stage-completion forward enqueue and transitive asynchronous propagation without ingestion-path matching | future-child, nested-factory, retry, and monolith/split tests |
| P30-T83 | done | P30-T82 | Generated provenance/children API, bilingual Web presentation, bounded configuration, metrics, admin backfill, and operations guidance | generated API, Web, observability, browser, deployment, and common gates |
| P30-T84 | done | P30-T83 | Enable dry-run, backfill publication, and forward propagation in the local Preview Compose configuration only | Preview Compose/config regression and common configuration gates |
| P30-T85 | done | P30-T84 | Start late-verification scans at the exact canonical creator-code epoch and prove constructor-created children through Hardhat and Foundry production topologies | epoch/backfill PostgreSQL regressions, Hardhat/Foundry monolith/split E2E, Preview transaction acceptance, and common gates |
| P30-T86 | done | P30-T85 | Generation-bound trace/proxy forward events, fork-aware scan rewind, canonical attempt writes, and success-reset pagination budgets | state-machine unit and PostgreSQL reorg/pagination regressions; generation/source/plan checks |
| P30-T87 | done | P30-T86 | Heartbeat-fenced scan/event leases plus one prepared match and short canonical publication transaction | lease-contention, slow-match/reorg, stale-publication, integration-race, and runtime parity tests |
| P30-T88 | done | P30-T87 | Exact-epoch parent/child provenance with additive direct-verification creation provenance and unchanged wire shape | A-to-B-to-A, FQN conflict, direct-child, code-hash, API, Web, and generation checks |
| P30-T89 | done | P30-T88 | Split derived-adjacent Go/Web presentation modules and enforce lower production/test structural ceilings | Go/Web lint, unit, browser, and source-boundary checks |
| P30-T90 | done | P30-T89 | Complete release, topology, migration, documentation, and operator evidence without implementation changes | common, PostgreSQL/race, schema/runtime, Hardhat, Foundry, browser, deployment, and diff gates |
| P30-T95 | done | P30-T90 | Pinned Python Vyper 0.4.3 executor, runtime identity, isolation and resource limits | helper build, real compiler, permission/resource and provenance tests |
| P30-T96 | done | P30-T95 | Vyper input normalization, matcher, fresh-schema provenance and publication | compiler fixtures, malformed input, PostgreSQL and reorg regressions |
| P30-T97 | done | P30-T96 | Native API, Etherscan vyper-json and bilingual Web verification | generated contracts, API, Web and browser tests |
| P30-T98 | done | P30-T95 | Production helper packaging, role parity, deployment and licenses | image, configuration, Compose, Helm and license checks |
| P30-T99 | done | P30-T96, P30-T97, P30-T98 | Vyper production verification acceptance and maintained documentation | common gates, native AMD64/ARM64 monolith/split verification E2E |

| P30-T100 | done | P30-T99 | Serialize Compose stdout/stderr capture and streaming to prevent concurrent buffer corruption | real-process output and failure regressions under the race detector; Foundry E2E; docs/plan checks |

| P30-T107 | done | P30-T100 | Signed dynamic Vyper runtime catalogs, immutable provenance and authenticated cache | signature, persistence, download, cache and subprocess regressions |
| P30-T108 | done | P30-T107 | All non-withdrawn stable Vyper build locks and version-aware input/output and matching | official compiler differential matrix on native AMD64/ARM64 |
| P30-T109 | done | P30-T108 | Vyper capability API, generated client and bilingual Web | API, Etherscan and browser regressions |
| P30-T110 | done | P30-T109 | Runtime release pipeline, deployment parity and complete acceptance | common gates, PostgreSQL, and native AMD64/ARM64 monolith/split production E2E |

| P30-T111 | done | P30-T100 | Fix Vyper release archive padding and preserve cancelled legacy jobs during migration | Python producer/Go extractor regressions, gzip integrity and padding bounds, PostgreSQL migration preservation and maintained gates |

| P30-T112 | done | P30-T111 | Restore signed Vyper catalog environment loading at the production startup boundary | Config loading for API/all, rejection of invalid trust settings, and Hardhat startup regression checks |

| P30-T113 | done | P30-T112 | Align strict Hardhat persistence totals with the expanded Vyper version/protocol matrix | Replay CI totals, reject missing/duplicate jobs and results, preserve provenance and topology parity checks |

Allowed item states are `todo`, `in_progress`, `blocked`, `done`, `dropped`.

## Acceptance

- [x] P30-T107–P30-T113: 26 non-withdrawn stable Vyper releases, signed dynamic
      runtimes, API/Web and provenance/migration regressions pass all 11 checks
      in [PR #63 CI run 34694346465](https://github.com/islishude/etherview/actions/runs/34694346465)
      at `dc2d689c0edda6173a77e257889b60e13fa8c9c5`, including both native
      Linux architectures and monolith/split production acceptance.

- [x] P30-T95–P30-T99: Vyper 0.4.3 source verification passes pinned helper,
      exact matching, native/Etherscan/Web, and native AMD64/ARM64 production
      acceptance without restoring Pyodide or an independent runner.

- [x] Verification is bound to exact chain, address, runtime code, code hash,
      block identity, request digest, compiler identity, and canonicality.
- [x] Solidity/Yul verification uses bounded canonical inputs, dual compilation,
      exact candidate matching, authenticated compiler provenance, and current
      API-owned solc-js execution rules.
- [x] Sourcify remains explicit, consent-gated, bounded, and unable to replace
      local canonical publication evidence.
- [x] Geas uses only its pinned helper, bounded inline files, exact runtime and
      optional creation matching, and empty-ABI semantics.
- [x] Artifact reuse never creates independent address verification, and proxy
      or management writes require a fresh exact binding.
- [x] Standard, Safe, Diamond, and CWIA observations retain distinct mechanisms,
      evidence, conflicts, histories, code identities, and fail-closed states.
- [x] Trace logs, calls, returns, failures, and ABI selectors remain bound to
      exact execution identity while raw bytes remain available.
- [x] EIP-7702 authorization, delegation history, transaction-time execution
      identity, constructor decoding, clearing, and reorg behavior remain exact.
- [x] Factory-derived verification uses authenticated compilation units, exact
      CREATE/CREATE2 provenance, canonical rechecks, bounded scans, and
      generation-safe publication.
- [x] Runtime lifecycle, role manifests, readiness, shutdown, deployment,
      telemetry, optional accelerators, credentials, and repair tooling preserve
      PostgreSQL authority and monolith/split parity.
- [x] All current work items retain their verification boundaries and do not
      claim P70 capacity or P73 live-payment closure.
- [x] Generated API, PostgreSQL, browser, race, deployment, Hardhat, Foundry,
      and common-gate evidence remains attributable through the current P30 work
      items.

## Current Blockers

None. P30-T99's native AMD64/ARM64 acceptance is recorded below for the exact
PR #56 commit tested by CI. Subsequent local review corrections retain their
separate validation boundary. P70 capacity and P73 live-testnet evidence remain
owned by their current plans.

## Evidence

- The completed contract and runtime work was organized without changing
  runtime,
  database, API, generated-client, migration, or deployment behavior.
- Current P30 work items retain their verification boundaries.
- Artifact and Web regressions passed their generated
  contract, PostgreSQL, browser, Preview, and production-build checks.
- Proxy work passed fixed-block detector, PostgreSQL/race,
  browser, schema, runtime, and Hardhat topology checks; public V2 remains
  default-off pending deployment-specific shadow review.
- Trace/failure work passed codec, PostgreSQL, browser,
  runtime, Hardhat, and common gates while retaining raw trace/log bytes.
- Runtime work passed lifecycle, integration/race, Compose,
  Helm, deployment, telemetry, and short-load checks; optional accelerators
  remain disposable and PostgreSQL remains authoritative.
- EIP-7702 work passed PostgreSQL, browser, runtime, Hardhat,
  reorg, and common gates for exact authorization and transaction-time identity.
- Diamond work passed integration/race, browser, Hardhat, and
  common gates for selector-scoped history and interaction.
- Geas work passed helper, image, schema, runtime, Foundry, and
  common gates for the pinned sys-asm fixture.
- CWIA work passed parser, PostgreSQL/race, browser, Preview,
  Hardhat, and common gates without enabling public proxy V2.
- Derived-verification work passed PostgreSQL/race,
  generation, source, browser, deployment, Hardhat, Foundry, and common gates.
- P30-T01: completed; verification boundary: repository tests.
- P30-T02: completed; verification boundary: tamper/limit tests.
- P30-T03: completed; verification boundary: compiler fixture tests.
- P30-T04: completed; verification boundary: mocked API tests.
- P30-T05: completed; verification boundary: SSRF/content tests.
- P30-T06: completed; verification boundary: resolver/CLI tests.
- P30-T07: completed; verification boundary: cleanup and PostgreSQL regressions.
- P30-T08: completed; verification boundary: migration and plan checks.
- P30-T09: completed; verification boundary: catalog and sandbox security tests.
- P30-T10: completed; verification boundary: compiler and matcher fixtures.
- P30-T11: completed; verification boundary: API and integration tests.
- P30-T12: completed; verification boundary: browser and deployment tests.
- P30-T13: completed; verification boundary: applicable repository gates.
+ P30-T14: completed; verification boundary: catalog/provenance regressions.
- P30-T15: completed; verification boundary: verifier, PostgreSQL, immutable-source, upgrade/reorg, and Hardhat fixture tests.
- P30-T16: completed; verification boundary: target-resolution, publication-fence, PostgreSQL, and compatibility regressions.
- P30-T17: completed; verification boundary: migration, parser, publication, invalid/empty ABI, and PostgreSQL tests.
- P30-T18: completed; verification boundary: repository, API, compatibility, migration, and generation tests.
- P30-T19: completed; verification boundary: proxy API, wallet, frontend, browser, and Hardhat tests.
- P30-T20: completed; verification boundary: codec, catalog, API, frontend, reorg, and PostgreSQL tests.
- P30-T21: completed; verification boundary: ADR, plan, generation, integration, E2E, Hardhat, and common gates.
- P30-T22: completed; verification boundary: focused Vitest, production build, Preview/browser check, plan check.
- P30-T23: completed; verification boundary: focused proxy tests; `make plan-check`.
- P30-T24: completed; verification boundary: detector/resolver unit and fuzz tests; existing OZ suite.
- P30-T25: completed; verification boundary: generated-manifest check; positive, negative, adversarial, and fixed-block integration fixtures.
- P30-T26: completed; verification boundary: OpenAPI generation; integration/race/browser/runtime gates; rollout-control review.
- P30-T27: completed; verification boundary: pinned artifact checks; Compose rendering; monolith/split Hardhat production E2E; common gates.
- P30-T28: completed; verification boundary: codec, trace, PostgreSQL, generation, browser, runtime, Hardhat, and common gates.
- P30-T29: completed; verification boundary: codec, PostgreSQL, Preview transaction, plan, and common gates.
- P30-T30: completed; verification boundary: focused catalog, PostgreSQL, Preview transaction, browser, and common gates.
- P30-T31: completed; verification boundary: codec, catalog, generated API, Web, PostgreSQL, browser, runtime, and common gates.
- P30-T32: completed; verification boundary: focused Web, browser, plan, and common gates.
- P30-T33: completed; verification boundary: lifecycle/parity tests.
- P30-T34: completed; verification boundary: outage/fallback tests.
- P30-T35: completed; verification boundary: production Compose Go E2E.
- P30-T36: completed; verification boundary: Helm lint/render tests.
- P30-T37: completed; verification boundary: observability tests.
- P30-T38: completed; verification boundary: soak/load tests.
- P30-T39: completed; verification boundary: metadata, netpolicy, observability, Preview, and common gates.
- P30-T40: completed; verification boundary: focused race, PostgreSQL integration, runtime topology, and common gates.
- P30-T41: completed; verification boundary: metadata and observability regressions plus common gates.
- P30-T42: completed; verification boundary: credential-chain, refresh, redaction, Compose, Helm, and common gates.
- P30-T43: completed; verification boundary: codec, state diff, trace, PostgreSQL, generation, browser, integration, runtime, Hardhat, and common gates.
- P30-T44: completed; verification boundary: focused Web tests, production browser, Preview, and common gates.
- P30-T45: completed; verification boundary: focused Go and Web tests; Preview API/browser verification.
- P30-T46: completed; verification boundary: focused Web tests, production browser, and common gates.
- P30-T47: completed; verification boundary: focused CorePages frontend tests, lint, build, generation, and plan checks.
- P30-T48: completed; verification boundary: catalog, API, generation, Web, integration, browser, runtime, and common gates.
- P30-T49: completed; verification boundary: trace/ABI/catalog regressions, live Preview replay, and common gates.
- P30-T50: completed; verification boundary: ABI/Catalog/API/Web regressions, live Preview verification, and common gates.
- P30-T51: completed; verification boundary: Catalog/API/Web regressions, live Preview verification, and common gates.
- P30-T52: completed; verification boundary: state/query, generated API, Web, browser, integration, and common gates.
- P30-T53: completed; verification boundary: Catalog, PostgreSQL, generated API, Web, browser, Preview, and common gates.
- P30-T54: completed; verification boundary: Web, browser, generation, and common gates.
- P30-T55: completed; verification boundary: browser, runtime, topology-parity, and common gates.
- P30-T56: completed; verification boundary: state-diff, query, Catalog, Web, PostgreSQL, browser, Preview, and common gates.
- P30-T57: completed; verification boundary: ABI/Catalog/API contracts, Web transformation and DOM, PostgreSQL, browser, runtime, and common gates.
- P30-T58: completed; verification boundary: ABI/store/Catalog/API/Web regressions, PostgreSQL reorg and replay, real runtime E2E, Preview, and common gates.
- P30-T59: completed; verification boundary: migration, query, OpenAPI generation, model unit and integration tests.
- P30-T60: completed; verification boundary: detector unit/fuzz tests, hostile-return limits, exact-block RPC fixtures.
- P30-T61: completed; verification boundary: PostgreSQL integration/race, add/replace/remove, reorg and same-transaction ordering tests.
- P30-T62: completed; verification boundary: generated clients, Vitest, Playwright, runtime E2E, common gates.
- P30-T63: completed; verification boundary: OpenAPI generation, migration and request-model tests, plan check.
- P30-T64: completed; verification boundary: compiler unit/security/process tests, license and image checks.
- P30-T65: completed; verification boundary: worker, repository, PostgreSQL integration and race tests.
- P30-T66: completed; verification boundary: generated clients, Go/Web/API/browser tests.
- P30-T67: completed; verification boundary: Foundry production E2E, schema/runtime E2E, common gates.
- P30-T68: completed; verification boundary: parser/detector unit and fuzz tests; PostgreSQL constraints; `make source-check`.
- P30-T69: completed; verification boundary: query, verification, API, generation, integration, and reorg tests.
- P30-T70: completed; verification boundary: schema/parser unit tests; Vitest; embedded Chromium E2E.
- P30-T71: completed; verification boundary: schema/runtime/Hardhat E2E; security; common gates.
- P30-T72: completed; verification boundary: focused target/page tests; embedded Chromium E2E; live Preview inspection.
- P30-T73: completed; verification boundary: verifier/query/API/Web tests; production Hardhat/browser/runtime gates; common gates.
- P30-T74: completed; verification boundary: query/API/write-fence tests; bilingual browser/Preview regressions; common gates.
- P30-T75: completed; verification boundary: target/form/page tests; real-Chromium transient-stage regression; live Preview reproduction.
- P30-T76: completed; verification boundary: page/unit/browser regressions; live Preview inspection.
- P30-T77: completed; verification boundary: focused query/ABI tests; PostgreSQL integration/race; production Hardhat monolith/split; common gates.
- P30-T78: completed; verification boundary: verifier unit and PostgreSQL publication regressions.
- P30-T79: completed; verification boundary: codec, digest/provenance, idempotency, migration, and PostgreSQL round-trip tests.
- P30-T80: completed; verification boundary: Factory-to-child Solidity fixtures and PostgreSQL integration tests.
- P30-T81: completed; verification boundary: reorg, reattach, code replacement, and duplicate-work tests.
- P30-T82: completed; verification boundary: future-child, nested-factory, retry, and monolith/split tests.
- P30-T83: completed; verification boundary: generated API, Web, observability, browser, deployment, and common gates.
- P30-T84: completed; verification boundary: Preview Compose/config regression and common configuration gates.
- P30-T85: completed; verification boundary: epoch/backfill PostgreSQL regressions, Hardhat/Foundry monolith/split E2E, Preview transaction acceptance, and common gates.
- P30-T86: completed; verification boundary: state-machine unit and PostgreSQL reorg/pagination regressions; generation/source/plan checks.
- P30-T87: completed; verification boundary: lease-contention, slow-match/reorg, stale-publication, integration-race, and runtime parity tests.
- P30-T88: completed; verification boundary: A-to-B-to-A, FQN conflict, direct-child, code-hash, API, Web, and generation checks.
- P30-T89: completed; verification boundary: Go/Web lint, unit, browser, and source-boundary checks.
- P30-T90: completed; verification boundary: common, PostgreSQL/race, schema/runtime, Hardhat, Foundry, browser, deployment, and diff gates.

### P30-T95 — Pinned Python executor

- Built CPython 3.13.15/PyInstaller 6.22.2/Vyper 0.4.3 from the hash-locked
  dependency set. macOS ARM64 helper self-test and
  `VYPER_EXECUTOR_TEST_PATH=/tmp/etherview-vyper-runtime-v3/etherview-vyper
  go test ./internal/verify -run '^TestVyper' -count=1` pass, including real
  compilation, determinism, version/provenance rejection and cancellation.
- The pinned Python 3.13.15 Linux ARM64 builder also builds the helper and
  passes access-denial self-tests with Linux resource limits enabled. This is
  builder evidence only; production image closure, resource exhaustion and
  both native architecture E2E gates remain P30-T98/P30-T99.
- `make plan-check docs-check` passed.

### P30-T96–P30-T97 — Verification and public surfaces

- Vyper-specific Standard JSON/multipart normalization, original/perturbed
  compilation, CBOR footer checks and recursive immutable layout are covered
  by official-compiler fixtures, including imported module immutables.
- PostgreSQL 18 applies migration 0065 successfully.
  `go run ./cmd/testintegration -root . -packages ./internal/integration
  -run 'TestVyperDurable'` passes durable publication, family availability,
  immutable compiler identity, reorg publication rejection and exclusion from
  factory-derived compilation units. Existing canonical publication regressions
  also passed against the new schema.
- Native, Etherscan, generated contract, config, app and verifier package tests
  pass. The Web suite passes 369 tests; the new Chromium Vyper submission test
  passes with exact target and native optimization mode. Go/Web lint,
  `make generate-check`, `make docs-check`, and `make plan-check` pass.
- Real production-chain, complete runtime and multi-architecture acceptance
  remain in P30-T98/P30-T99; these local tests do not substitute for them.

### P30-T98 — Production runtime

- Linux ARM64 production image builds the pinned helper, validates its complete
  ELF dependency closure against the final distroless root and runs self-test
  as UID/GID 65532. `make docker-image-check` passes read-only, no-network,
  no-capability helper execution and excludes general Python/package CLIs.
- Compose and Helm carry the API-owned Vyper path without a platform override
  or new service. Configuration, role assembly, startup identity and compiler
  family metrics are aligned; deployment and license gates pass.
- `make test-hardhat3-e2e` passes both production monolith and split-role
  topologies on native Linux ARM64. Each deploys two real immutable contracts,
  proves native and Etherscan verification, chain execution, source/ABI reads,
  full creation/partial runtime evidence and exact pinned Vyper provenance.
  Solidity/Yul, proxy, Safe, Diamond and derived-verification regressions also
  pass. P30-T99 retains the separate native AMD64 acceptance requirement.

### P30-T99 — Acceptance boundary (2026-09-08)

- Implementation is complete through P30-T98. Final `make check` passes
  generation, source boundaries, Go unit/race, 370 Web tests, lint, security,
  licenses and deployment rendering. `make test-integration-race` passes the
  complete PostgreSQL 18 suite; `make test-e2e` passes all 27 Chromium tests.
- Final `make test-hardhat3-e2e` passes both native Linux ARM64 production
  topologies (246.78 seconds), including Vyper native/Etherscan submission,
  immutable execution, full creation/partial runtime evidence, source/ABI reads
  and independent compiler-family provenance counts.
- `make test-foundry-e2e`, `make test-schema-e2e`, and
  `make test-runtime-e2e` pass, including both Foundry/Geas topologies and
  monolith/split runtime and local x402 regressions. These are isolated Anvil
  and PostgreSQL tests, not production-chain or live-payment acceptance.
- `make docker-image-check` confirms the entire Vyper tree is non-writable,
  its helper runs with no network/capabilities, and no general Python/package
  CLI is shipped. Checked ARM64 image ID:
  `sha256:3562037e10b6119665d0841657e07459ebb80a3735fa8cbb63b8f58f14a1bd06`.
- Final targeted `go test -race ./internal/verify -run '^TestVyper' -count=1`
  and `make lint-go` pass after adding active compiler-timeout/process-cleanup
  coverage. The helper can compile again after a timed-out process is removed.
  Pinned fixtures additionally cover metadata off, modules and module
  immutables, built-in/inline/JSON interfaces and non-Solidity target filenames.
- Python dependency auditing scans the complete locked set. Two incorrectly
  unbounded advisory records are corrected only for the exact official 0.4.3
  wheel using the maintainer's patched-version statements; raw audit output
  remains in the ignored audit cache. The [compiler README](../../compiler/vyper/README.md)
  records both references and the narrow correction policy.
- The native AMD64 evidence missing from this local run is now supplied by
  the PR #56 CI closure recorded below.

### P30-T98 — Builder tag review follow-up

- Removed the Python builder OCI digest pin as requested; Docker now uses
  `python:3.13.15-slim-trixie`. Updated the compiler README and ADR-0047.
- Deployment, documentation and plan checks pass, as does `git diff --check`.
  This image-reference-only change did not rerun production image E2E.

### P30-T95/P30-T96 — Review corrections

- Distinguish layout leaves by a string-valued `type`, allowing an imported
  module to contain an immutable named `type`. Require explicit non-null
  offsets, and retain strict length/alignment/overlap checks. The official
  compiler fixture `module_type` proves compilation and transformation-aware
  matching for this case.
- Runtime schema v2 requires server-owned input/output byte limits on compile
  invocations. Go and Python enforce the same effective configuration. Python
  reads incrementally, so a large configured ceiling does not preallocate that
  amount of memory. Update the complete helper runtime and drain bound jobs
  before this protocol upgrade; old manifests fail startup validation.
- `go test -race ./internal/verify -run '^TestVyper' -count=1` passes configured
  limits above 5 MiB, exact-boundary acceptance, oversize rejection, invalid CLI
  limits, module layout regressions, timeout and cleanup checks.
- `make check` and the focused PostgreSQL `TestVyperDurablePublicationAndReorgFence`
  integration test pass. An initial dependency-audit service failure was retained
  as a failed gate; the unchanged audit and complete check passed on rerun.
- The newly built Linux ARM64 production image passes `make docker-image-check`.
  Its helper compiles a 5,244,233-byte input with a 16 MiB limit and a small input
  with a 1 GiB ceiling under the unchanged 512 MiB address-space limit.
  `make test-hardhat3-e2e-prebuilt` then passes both native production topologies
  (237.14 seconds), using that rebuilt and inspected image.
- Documentation, source and plan checks and `git diff --check` pass. These
  subsequent local corrections are not included in the PR #56 CI commit below;
  their native AMD64 CI validation must follow submission of the changes.

### P30-T99 — CI closure (2026-09-08)

- [PR #56 CI run 34178834069](https://github.com/islishude/etherview/actions/runs/34178834069)
  passes all nine checks for `4b10534d01d05a9a26876673c3467b767ad885fa`.
  This closes P30-T99 and the P30 release dependency for that tested revision.
- Native production-image Hardhat 3 verification passes on
  [AMD64](https://github.com/islishude/etherview/actions/runs/34178834069/job/101913548293)
  and [ARM64](https://github.com/islishude/etherview/actions/runs/34178834069/job/101913548148),
  covering monolith/split Vyper submission, compilation, publication and
  source/ABI reads alongside Solidity/Yul regressions. Both native Foundry
  jobs pass the Geas regressions.
- Generation/lint/unit tests, PostgreSQL integration, embedded SPA browser E2E,
  security/licenses and Container/Compose/Helm checks also pass. This CI evidence
  does not include the subsequent uncommitted review corrections above and does
  not close P70 capacity or P73 live-payment acceptance.

### P30-T90 — PostgreSQL CI heartbeat regression (2026-09-08)

- [CI run 34191818745](https://github.com/islishude/etherview/actions/runs/34191818745/job/101951263813)
  at `ead40648f764794a5f2281b37d21d926c7207cb7` fails only PostgreSQL integration:
  `TestFactoryVerificationBackfillsUniquelyMatchedCreatedContract` loses its
  test-only 300 ms scan lease on page five. The other eight CI jobs pass,
  including both native architectures' production verification suites.
- Replace per-trace sleeps with a context-bounded observer handshake that holds
  matching until a successful database heartbeat. Use the normal 30-second
  scan lease; retain all 500 durable outcomes, six-page completion, zero
  consecutive failures and observed-renewal assertions. Production renewal,
  ownership checks and publication SQL are unchanged.
- The owned PostgreSQL focused regression passes with `-race` (15.014 seconds):
  `go run ./cmd/testintegration -root . -packages ./internal/integration -run
  '^TestFactoryVerificationBackfillsUniquelyMatchedCreatedContract$' -race`.
  `go test -race ./internal/derivedverify -count=1`, `make lint-go`,
  `make docs-check` and `make plan-check` pass, including heartbeat loss and
  finalized-lease regression coverage. The complete `make test-integration`
  rerun passes all six packages against owned PostgreSQL 18. P30-T90 returns to
  done; this local correction has not been submitted to CI.


### P30-T100 — Compose output capture race (2026-09-09)

- PR #61 job `102321283132` in run `34305519245` panicked in
  `bytes.Buffer.grow` through `io.MultiWriter` during Forge verification.
  `OSExecutor.Run` gave stdout and stderr distinct writers sharing one
  unsynchronized buffer. A real-process regression reproduced data races and
  the same slice-bounds panic before the fix; this is a harness concurrency
  defect, not evidence of a gRPC behavior regression.
- One per-command mutex now serializes capture and streaming together, also
  protecting callers that use the same sink for both streams. Tests assert
  complete capture, separate-stream routing, shared-stream equality, and
  retained output plus exit status on command failure.
- `go test -race ./internal/testcompose ./cmd/testintegration
  ./cmd/testschemae2e -count=1` passes. The concurrent-output regression also
  passes five repetitions under `-race`; the package passes with main's
  original dependency files supplied through a temporary `-modfile`.
  `golangci-lint run ./internal/testcompose/...` reports zero issues.
- `make test-foundry-e2e` rebuilt current production/client images and passes
  monolith and distributed Forge/Geas verification on local ARM64 (96 seconds).
  The working tree included separately updated dependencies, including gRPC
  1.83.2. This is local evidence; the linked native AMD64 CI job has not been
  rerun with this fix.
- `make docs-check plan-check` and `git diff --check` pass. No public,
  persistent, compiler, or topology contract changes were needed.

### P30-T107–P30-T110 — Dynamic Vyper runtime catalogs (2026-09-12)

- Implementation covers 26 non-withdrawn stable releases, with official-source
  packaging for 0.2.0; signed Ed25519 catalogs, immutable runtime provenance,
  authenticated archive/cache installation, historical compiler adapters,
  capability API/Web controls and native release/production E2E workflows.
- IDs T101–T106, ADR-0048 and migration 0066 already belong to the separate
  `simplify-contract-verify-runtime` branch. This work uses T107–T110,
  ADR-0049 and migration 0067 to avoid sharing those identifiers.
- Local macOS ARM64: all 26 frozen helpers pass 362 original/perturbed reference
  executions covering constructors, supported immutables, interfaces, missing
  imports, optimization modes, metadata omission and malformed sources. The Go
  matrix verifies startup, real execution, exact matching and identity changes.
  These are not Linux production or native AMD64 evidence.
- Targeted Go tests for verify/httpapi/etherscan/config/app pass. Vyper/catalog
  race regressions, cold download/checksum/cache-repair tests, signed-catalog
  publisher tests, Web's 370 tests and the real Chromium Vyper submission pass.
- PostgreSQL Vyper publication/reorg, catalog expiry/freshness, bound retry without
  a fresh catalog, immutable rebinding rejection and catalog/history migration
  regressions pass. The broad integration run found one obsolete assertion
  forbidding all Vyper catalog rows after the full migration chain; that assertion
  was updated for the current schema and its targeted regression passes.
- Generation, source, docs, plan, Go vet/lint and Web lint checks pass. A full
  `make check` passed through unit/race/security/license checks but stopped at
  Docker deployment validation. Retrying `make deployment-check` encounters the
  same Docker Hub OAuth connection reset; the gate remains open.
- Remaining acceptance: native Linux AMD64 and ARM64 runtime matrices, production
  image/deployment checks and both production topologies using the collected
  native artifacts. CI now builds both architecture sets before those tests and
  writes descriptor-bound acceptance only after topology parity passes. No
  production catalog can be signed without both acceptance records. Remote
  publication remains a separate explicitly authorized release operation.

At this local-only checkpoint the items remained open; local/mock and macOS
results did not close native production acceptance. The subsequent remote CI
closure below supplies the missing evidence.

### P30-T111 — Release archive padding and cancelled history fixes (2026-09-12)

- The extractor accepts the Python producer's final 10 KiB USTAR record padding
  (up to 19 complete zero blocks), while rejecting nonzero bytes, partial blocks
  and excessive padding. Reading through gzip EOF still rejects corrupt CRCs
  and truncated trailers. The release packer is shared with the Python-to-Go
  round-trip regression instead of reproducing it with Go's different tar writer.
- Migration 0067 retains cancelled v1 Vyper jobs, both unbound and previously
  lease-bound. The PostgreSQL regression creates these states under schema 0065,
  applies the actual migration chain, compares complete job snapshots and checks
  that cancelled jobs cannot be claimed again. No rows are rewritten or rebound.
- Both regressions failed before their fixes: valid producer archives were
  rejected, and the migration failed with constraint violation 23514.
- `go test -race ./internal/verify -run 'TestVyperArchive|TestVyperRuntimeCache'
  -count=1` passes, including producer interoperability, padding bounds,
  gzip corruption/truncation and authenticated cache repair.
- `go run ./cmd/testintegration -root . -packages './internal/integration'
  -run '^TestVyper|TestSolcJSExecutorMigrationDeletesVyperAndPreservesSolidity$'`
  passes against the owned PostgreSQL 18 project, including cancellation
  preservation and the existing publication, reorg and provenance regressions.
- All 26 existing macOS ARM64 release archives additionally pass extraction,
  authenticated manifest validation and helper self-test through a temporary Go
  test overlay (72.071s). This does not close P30-T110's native Linux gates.
- `make generate-check source-check docs-check plan-check lint-go` and
  `git diff --check` pass. P30-T107–P30-T110 retain their existing acceptance state.

### P30-T112 — Hardhat CI startup configuration failure (2026-09-12)

- Run 34687982706 at 506d3de failed in both native Hardhat jobs during monolith
  startup, before verification: the retained Compose logs report
  `ETHERVIEW_VERIFICATION_VYPER_CATALOG_URL is no longer supported`.
  The new environment reader was unreachable because the same variable remained
  in the retired-setting denylist. Remove only that obsolete rejection.
- A regression exercises the actual `LoadForRoles` path for `all` and `api`,
  including configured and explicitly empty catalog settings. It failed with
  the identical CI error before the fix. Missing/malformed public keys,
  non-HTTPS URLs and unlisted origins remain rejected after the fix; genuinely
  retired compiler environment variables remain rejected as well.
- `go test -race ./internal/config ./internal/app` and
  `make source-check docs-check plan-check lint-go` pass.
- The cited run's native Vyper runtime matrices and other CI jobs passed; this
  is not a compiler-matrix or package-timeout failure. The run predates the
  uncommitted T111 archive/migration fixes, which are preserved in this worktree.
- Local `make docker-build` was attempted for a production E2E replay but failed
  fetching the Docker Hub frontend OAuth token (connection reset). Full native
  Hardhat E2E and a new remote CI run remain unverified; T110 stays open.

### P30-T113 — Hardhat persistence totals after Vyper expansion (2026-09-12)

- Run 34689671531 at 5d26cec reaches the final monolith snapshot on both AMD64
  and ARM64. All six Vyper families pass native/Etherscan verification and cache
  owner replacement; the snapshot has 21 address jobs, 23 compiler results and
  12 Vyper jobs/results with valid provenance. The old 11/13 total assertions
  still assumed only two Vyper jobs, so both checks fail despite complete data.
- Version and protocol matrices now supply one Vyper job count to execution,
  provenance SQL and strict snapshot checks. Expected totals remain exact:
  nine Solidity addresses plus Vyper, and eleven non-Vyper results plus Vyper.
  All existing proxy, Yul, derived, Safe/Diamond and topology-parity checks remain.
- The regression replays the observed CI totals, rejects the obsolete totals,
  rejects missing/duplicate counts in every category and covers another matrix
  size. It failed against the original checks and passes with the correction.
  `test-hardhat3-e2e-prebuilt` now includes this regression alongside the full
  production test, so the lightweight count test is not omitted by its filter.
- `go test -race -tags='runtimee2e hardhat3e2e' ./e2e/runtime
  -run '^TestHardhat3VerificationCounts$' -count=1`, tagged golangci-lint and
  `make source-check docs-check plan-check lint-go` pass.
- A local production rebuild again fails fetching the Docker Hub frontend OAuth
  token (connection reset). Full production E2E and a new remote CI run remain
  unverified; this count fix does not close P30-T110's native topology gate.

### P30-T107–P30-T113 — Remote acceptance closure (2026-09-12)

- [PR #63](https://github.com/islishude/etherview/pull/63) head and the local
  code commit are `dc2d689c0edda6173a77e257889b60e13fa8c9c5`.
  [CI run 34694346465](https://github.com/islishude/etherview/actions/runs/34694346465)
  is a successful pull-request run with all 11 checks successful.
- Native Vyper runtime matrices pass on Linux AMD64 and ARM64. Both native
  Hardhat jobs pass monolith/distributed verification and topology parity;
  their `vyper-acceptance-amd64` and `vyper-acceptance-arm64` artifacts each
  contain 26 descriptor digests with `monolith=true` and `split=true`.
- Generation/lint/unit/race, PostgreSQL integration, native AMD64/ARM64 Foundry,
  embedded SPA browser E2E, security/licenses and Container/Compose/Helm gates
  also pass at that commit. This supersedes the earlier incomplete acceptance
  checkpoints and closes T107–T110; T111–T113 fixes are included in the same run.
- P30 returns to done. This evidence is CI acceptance, not production deployment
  or publication of a signed compiler catalog. P70/P73 external release blockers
  remain outside this work. Documentation-only closure is checked locally with
  `make docs-check plan-check` and `git diff --check`.
