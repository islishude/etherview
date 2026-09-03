# P30 — Contract Platform & Runtime Operations

Status: `done`

This is the canonical plan for contract verification, contract intelligence,
and the shared runtime/operations platform. Its current work items use the P30
prefix. P70/P73 release and live-payment blockers remain outside this completed
plan.

## Outcome

Etherview provides fail-closed contract verification and contract intelligence
over exact chain, address, runtime, block, and generation identities. It supports
the maintained Solidity/Yul and pinned Geas verification paths, authenticated
artifact reuse, standard/Safe/Diamond/CWIA proxy evidence, trace-bound ABI and
failure decoding, EIP-7702 execution identity, factory-derived verification,
and the PostgreSQL-authoritative monolith/split runtime, deployment, telemetry,
and disposable accelerator boundaries.

## Responsibilities

### Verification and compiler platform

P30-T01–P30-T17 and P30-T63–P30-T90 cover durable verification
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

Allowed item states are `todo`, `in_progress`, `blocked`, `done`, `dropped`.

## Acceptance

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

None. P70 release, reference-capacity, and P73 live-testnet evidence remain
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
