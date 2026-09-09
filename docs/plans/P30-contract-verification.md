# P30 — Contract Platform & Runtime Operations

Status: `in_progress`

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
- [ADR-0048](../decisions/ADR-0048-wazero-compiler-executor.md)
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
| P30-T100 | done | P30-T99 | Isolated wazero feasibility probe for official Solidity/Yul and CPython WASI Vyper; no production executor change | real compiler comparisons, dependency diagnostics, and governance gates |
| P30-T101 | done | P30-T100 | Pin full solc migration baseline and accepted WASM executor decision | catalog digest inventory and governance gates |
| P30-T102 | done | P30-T101 | Bounded static soljson extraction and complete Solidity/Yul Emscripten adapter | full baseline differential, malformed and resource tests |
| P30-T103 | done | P30-T101 | Dedicated bounded Go WASM subprocess protocol and runtime manifest | identity, I/O, timeout, cancellation and process cleanup tests |
| P30-T104 | done | P30-T102, P30-T103 | Source-built CPython WASI with Go Keccak and pinned Vyper bundle | native differential fixtures, host ABI and reproducible build checks |
| P30-T105 | done | P30-T104 | Atomic provenance/schema/config integration for unified WASM execution | generated API, PostgreSQL preflight and worker regression gates |
| P30-T106 | in_progress | P30-T104 | Unified production packaging, native architecture acceptance and old runtime removal | all compiler baselines, AMD64/ARM64 production E2E, performance and common gates |

Allowed item states are `todo`, `in_progress`, `blocked`, `done`, `dropped`.

## Acceptance

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

### P30-T100 — wazero feasibility

- Isolated diagnostic implementation and reproduction commands are in
  [compiler/wazero-probe](../../compiler/wazero-probe/README.md), with a separate
  Go module pinned to wazero v1.12.0. Production execution, provenance, database,
  API, deployment and existing accepted ADRs remain unchanged.
- The experiment uses official catalog SHA-256-verified Solidity 0.8.30 and
  0.8.36 soljson artifacts, their npm equivalents, and the pinned third-party
  CPython 3.13.15 / WASI SDK 24 archive. Extracted WASM hashes agree between npm
  and official-catalog artifacts for both Solidity versions. Extraction still
  uses Node during preparation; actual candidate compilation runs in wazero.
- Local macOS ARM64 / Go 1.27.1 results are retained in
  [evidence.json](../../compiler/wazero-probe/evidence.json): 51 diagnostic cases,
  with 37 complete JSON reference matches, 12 explicit Solidity/Yul error-path
  mismatches, and two dependency-load diagnostics. All five existing Solidity
  0.8.30 fixtures match, as do normal Solidity and Yul cases for both compiler
  versions from npm and the official catalog. A CGO-disabled binary also
  successfully compiles official Solidity 0.8.30.
- Unmodified Vyper cannot import PyCryptodome because CPython WASI lacks
  `_ctypes`. With the explicitly diagnostic pure-Python Keccak replacement,
  all 22 original/modified Vyper fixture inputs and two invalid/missing-import
  inputs match native CPython outputs. Vyper sources are unchanged; immutables
  and cbor2 use their upstream pure-Python fallbacks. The hash implementation
  passes 56 differential vectors against pinned PyCryptodome. This is not an
  approved production hash implementation or full dependency compatibility claim.
- Solidity syntax errors reach unimplemented `___cxa_allocate_exception`;
  missing imports remain rejected but differ from the official wrapper's error
  text. The probe's null callback and incomplete Emscripten exception support
  therefore cannot replace production yet. Supporting more historical versions,
  exact callback/errors, and Node-free extraction requires separate work.
- WASI checks reject `/etc/passwd` access and writes to the exposed read-only
  package tree, and a 600 MiB allocation raises `MemoryError` under a 512 MiB
  linear-memory cap. An entered infinite Python loop is cancelled with a
  5-second context deadline (6.712 seconds observed including teardown).
  No total RSS limit, exhaustive sandbox audit, Linux image/architecture parity,
  workload benchmark, or production publication acceptance is claimed.
- Commands completed: `python3 compiler/wazero-probe/prepare.py
  /tmp/etherview-wazero-probe`; `go build` and `go vet ./...` inside the isolated
  module; `check.py` and `boundaries.py` with the executable, work directory and
  repository arguments documented in the README; Node/Python syntax checks;
  `make docs-check plan-check`; `git diff --check`. The matrix's successful exit
  asserts the scoped feasibility checks, not the unsupported production paths.
  The item is complete as an evaluation; no production migration is authorized
  or implied by its status.

### P30-T101 — WASM migration baseline

- ADR-0048 accepts the approved staged migration and gated unified cutover.
- `compiler/wasm/solc-baseline.json` pins 105 official build entries, from
  0.3.6 through 0.8.36, including the catalog prereleases. Snapshot SHA-256:
  `0ee86d7e0a30f0d90593ff64dfb56d192c514c8e33feebeb54446be55b12e5ad`. Unique versions and complete SHA-256 fields validate.
- Plancheck now accepts three-digit work-item identifiers with a cross-width
  range regression. P30-T100 is in the actual work-item table, not an orphan row.
- `go test ./internal/plancheck` and `make docs-check plan-check` pass.
  Artifact execution and whole-catalog differential acceptance remain T102.

### P30-T102 — Solidity/Yul implementation progress

- All 105 baseline artifacts are locally SHA-256 verified. Static extraction,
  external memory/table initialization, original dynCall and WASM indirect-call
  bridges, three exception layouts, callback denial and legacy JSON translation
  are implemented without executing JavaScript or rewriting compiler bytes.
- Full normal, invalid-source and Yul matrices plus targeted reruns of the ten
  imported-table versions cover 105/105 entries per mode. Existing version
  mismatch/broken-reference behavior remains rejected. Five real Solidity
  fixtures and representative missing-import cases pass. Guest loop cancellation
  and linear-memory limits pass. These are local macOS ARM64 results only.
- Direct, race-instrumented JIT construction exceeds the diagnostic one-minute
  context; the race gate is not claimed passing. T103 builds the already-planned
  subprocess boundary so real compiler acceptance uses the production process
  shape, with race checks retained on host logic and parent orchestration.
  T102/T103 can progress from the pinned T101 baseline independently; T104 waits
  for both. No production timeout increase or runtime cutover is made.

### P30-T102–P30-T103 — compiler core and subprocess contract

- The 105-entry normal, invalid-source and Yul matrices pass with targeted reruns
  of the ten imported-table builds after their callback/destructor correction.
  Representative missing imports (including imported tables), all five existing
  Solidity fixtures, authentication, malformed input, unknown imports, output
  bounds, guest cancellation and linear-memory checks pass.
- Real compilers now run through the actual CGO-disabled dedicated subprocess in
  tests. Small guest/host tests and parent I/O remain race instrumented. This
  preserves the production process boundary without increasing the one-minute
  diagnostic or two-minute production timeout. `go test -race
  ./internal/wasmcompiler ./internal/plancheck -count=1` passes (82.210s / 2.107s).
- The lightweight compilerbundle package shares the canonical full-tree manifest
  and bounded runner without linking the VM into API/all. File tampering,
  unlisted/symlink files, runtime identity changes, secret-free environment,
  input/output limits, timeout and descendant cleanup tests pass; ordinary and
  race package runs pass (3.877s / 8.299s).
- The helper checks its linked wazero version and applies FD/core limits only
  inside the subprocess. Its self-test executes a guest memory-limit probe.
  Scoped lint and governance gates pass. Full production manifest assembly,
  Vyper, database/config wiring and native architecture acceptance remain T104–T106.

### P30-T104 — source-built Vyper WASI runtime

- CPython 3.13.15 is built from checksum-pinned source with WASI SDK 24 and a
  static `_etherview_keccak` module calling Go's x/crypto Keccak-256. Official
  Vyper wheel members remain unchanged; pure-Python cbor2/immutables are used.
  No PyCryptodome, PyInstaller or native Python extension is in the runtime.
- All 22 compiler fixtures and native-error comparisons pass. 56 randomized
  native PyCryptodome/C-bridge vectors, a known vector, ABI memory bounds and
  overlap checks pass. Version mismatch and output limit fail closed.
- `build.py` reconstructs source/toolchain trees from authenticated archives;
  only archives are reused. Fixed epoch/hash seed and checked-hash Python
  bytecode produce identical manifests in two clean local builds. Packaging
  bytecode reduced observed local fixture invocations from about 6s to 2s;
  this is not a controlled production performance claim.
- Source-built runtime self-tests and readonly guest filesystem checks pass;
  `TestVyperWasmFixtures` passes in 45.668s and error comparisons in 6.537s on
  macOS ARM64. Native Linux architecture/image acceptance remains T106.

### P30-T105 — persistence and runtime integration

- API/all construct the unified WASM backends; Geas is unchanged. New work binds
  `solc_wasm_v1` / `etherview_wazero_v1` / `wasm_subprocess_v1`; Vyper retains its
  wheel identity. Historical terminal identities remain readable and reusable
  without new old-executor bindings or provenance rewriting.
- Migration 0066 refuses queued/running old bindings, preserves terminal rows,
  and admits new bindings only under the existing lease fence. Existing public
  admission configuration provides the drain workflow. Retired native executor
  YAML/environment paths fail configuration validation; Compose/Helm are aligned.
- Focused migration/reclaim/Vyper publication tests pass (4.250s), the complete
  internal/integration package passes (214.105s), config/app/verify tests pass,
  and the real parent WASM identity regression passes. `make source-check`,
  `make docs-check plan-check`, and Compose rendering pass. No OpenAPI or query
  generation changes are needed because the HTTP shape and SQL query inputs
  remain unchanged. Production image/native architecture gates remain T106.


### P30-T106 — candidate packaging and acceptance in progress

- CI run 34299874572 at 56527a21 passes both native compiler-baseline matrices
  and Foundry gates, but Hardhat reaches Go's implicit ten-minute package
  timeout during the second topology. The completed monolith takes about 397s
  on AMD64 and 345s on ARM64. The test already has a 30-minute context and CI a
  45-minute job budget. The user approved explicitly aligning the Go runner to
  30 minutes; production compiler deadlines and all acceptance cases remain
  unchanged. This supersedes the earlier ten-minute aggregate gate requirement
  for Hardhat only, not the recorded runtime performance cost. Native rerun
  results remain pending; T106 is not complete.
  Validation: the Make dry-run command retains the complete test selector/tags
  and emits `-timeout=30m0s`; `make docs-check plan-check` and `git diff --check`
  pass. The runner-only change does not require a new production image build.

- The candidate production image contains the dedicated static Go helper and
  source-built CPython WASI bundle; Node SEA and native Python/PyInstaller are
  absent. API/all do not link wazero. Local Linux ARM64 image checks, self-tests
  and monolith/split Hardhat E2E pass (439.53s, within the existing ten-minute
  gate); Foundry/Geas E2E pass (96.10s). The production compile timeout remains
  two minutes. Native Linux AMD64 acceptance is still outstanding.
- Static extraction now separates the authenticated base64 payload before
  parsing the small JavaScript wrapper. Bounded code generation uses at most
  four wazero workers inside each fresh helper. No compiler pool, native-code
  cache or verification worker-count change is introduced. This brought the
  Hardhat gate within its existing limit; it does not establish a speedup over
  the old runtime.
- `make check`, `make generate-check`, Compose/deployment checks and image
  checks pass locally. Source/toolchain expansion now uses temporary owned build
  trees; only verified archives are cached. Public CPython fixture keys are no
  longer left in the workspace, and gitleaks passes without an exclusion.
  Real SMT-query JSON matches the reference, including requested solver input;
  the focused differential passes normally and under the host race harness.
- Native AMD64/ARM64 CI jobs are prepared but have not run for this candidate.
  Retained old development
  executors and the original T100 prototype are not shipped in the candidate
  image; their gated cleanup and full production acceptance are not complete.
  Do not deploy or mark T106 done based on the local evidence alone.

- Final local full-catalog rerun passes 105/105 entries for each of normal,
  invalid, missing-import and Yul input (207.152s / 197.293s / 196.514s /
  198.191s). Extraction/host/golden checks pass (20.712s). Commands and scoped
  results are retained in [core evidence](../../compiler/wasm/core-evidence.json).
- [Performance samples](../../compiler/wasm/performance-local.json) compare the
  actual original Node SEA/native Vyper executors with the candidate on the same
  macOS ARM64 host, inputs and per-input limits. Single-worker median invocation
  times are Solidity 0.365s → 3.304s and Vyper 0.106s → 0.966s. Median per-child
  peak RSS is Solidity 274 MiB → 783 MiB and Vyper 37 MiB → 300 MiB. Two-worker
  batches of six inputs take Solidity 1.133s → 10.826s and Vyper 0.339s → 2.981s;
  every output remains JSON-equal. These small-input measurements include fresh
  startup/JIT, show a substantial cost, and are not production capacity results.
  The 512 MiB guest linear-memory cap does not bound helper RSS. Operators must
  budget CPU/RSS from measured workload and existing worker counts; no timeout
  relaxation or automatic fallback is used to hide the overhead.
