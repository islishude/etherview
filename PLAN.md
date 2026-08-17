# Etherview Implementation Plan

Status: `blocked`

## Goal

Build a production-oriented, single-chain-configurable Ethereum execution-layer
explorer in Go. The React SPA is embedded in the Go binary. The same components
run as a monolith or as independently scalable roles, with PostgreSQL as the
only mandatory external service.

Consensus-layer browsing, archived blob bodies, MEV accounting, and L2-specific
batch semantics are not core v1 scope.

## Plan Index

| ID | Plan | Status | Depends on | Outcome |
|---|---|---|---|---|
| P00 | [Foundation](docs/plans/P00-foundation.md) | done | — | Governance, toolchain, config, CLI, migrations, CI, and embedded SPA skeleton |
| P10 | [Indexing](docs/plans/P10-indexing.md) | done | P00 | Full-history core indexing, canonicality, finality, reorgs, and repair |
| P20 | [Enrichment](docs/plans/P20-enrichment.md) | done | P10 | Tokens, NFTs, ABI/proxy decoding, traces, balances, and statistics |
| P30 | [Contract Verification](docs/plans/P30-contract-verification.md) | done | P10, P20 | Verification foundation; the current surface is Solidity/Yul with architecture-neutral solc-js |
| P40 | [API](docs/plans/P40-api.md) | done | P10; incremental P20/P30 | Native REST, search, API keys, SSE, and Etherscan V2 compatibility |
| P50 | [Web](docs/plans/P50-web.md) | done | P40; incremental P20/P30 | Bilingual embedded SPA and injected-wallet contract interaction |
| P55 | [OpenZeppelin Proxy Interaction](docs/plans/P55-openzeppelin-proxy-interaction.md) | superseded | P20, P30, P40, P50 | Superseded coordination record for the cross-phase OpenZeppelin proxy chain |
| P56 | [Contract Artifact Reuse and ABI UX](docs/plans/P56-contract-artifact-reuse-and-abi-ux.md) | done | P20, P30, P40, P50 | Same-code artifacts, standard proxy interaction, and decoded transaction logs |
| P57 | [Web Contract Artifact Nullability](docs/plans/P57-web-contract-artifact-nullability.md) | done | P56 | Preview contract pages tolerate nullable verification artifact fields |
| P58 | [Evidence-based Proxy Detection](docs/plans/P58-evidence-based-proxy-detection.md) | in_progress | P20, P30, P40, P50, P60 | Stable OZ 5.x detection, composable detectors, and block-pinned Safe Proxy recognition |
| P59 | [Trace-bound ABI Decoding](docs/plans/P59-trace-bound-abi-decoding.md) | done | P20, P40, P50, P56 | Exact trace-frame log attribution and decoded calls, returns, and reverts |
| P60 | [Runtime & Operations](docs/plans/P60-runtime-operations.md) | done | P00; spans P10–P50 | Monolith/split runtime, Compose, Helm, observability, optional adapters |
| P61 | [EIP-7702 Delegated Accounts](docs/plans/P61-eip7702-delegated-accounts.md) | done | P20, P30, P40, P50, P59, P60 | Exact authorization, execution-code, constructor, API, and delegated-account interaction semantics |
| P62 | [ERC-2535 Diamond Proxies](docs/plans/P62-erc2535-diamond-proxies.md) | done | P20, P30, P40, P50, P58, P59, P60 | Selector-scoped facet detection, history, ABI resolution, and interaction |
| P63 | [Geas Contract Verification](docs/plans/P63-geas-contract-verification.md) | done | P30, P40, P50, P60 | Pinned Geas v0.3.3 address verification for multi-file sys-asm contracts |
| P64 | [NFT Metadata Web](docs/plans/P64-nft-metadata-web.md) | done | P20, P30, P40, P50, P60 | Canonical NFT metadata projection and guarded external-image navigation |
| P65 | [User Authentication](docs/plans/P65-user-auth.md) | done | P40, P50 | SIWE wallet login, revocable sessions, profiles, administration, and scoped user API keys with a tabbed `/account` workspace |
| P66 | [x402 API Billing](docs/plans/P66-x402-billing.md) | blocked | P40, P60; optional P65 | Accountless exact-EVM per-request payment and durable reconciliation |
| P70 | [Release](docs/plans/P70-release.md) | blocked | P10–P66 | Security, conformance, performance, E2E, documentation, and v1 release |

Allowed plan states are `planned`, `in_progress`, `blocked`, `done`, and
`superseded`.

## Phase Results

- P00 is complete: the repository has enforced plan governance, minimum-version
  Go/Node/npm checks that support compatible newer stable releases, a runnable
  role-aware CLI, embedded migrations and generated contracts, and a
  binary-served SPA. Restricted-host guidance now distinguishes writable
  language-tool caches from browser and Docker boundaries that require the
  unchanged repository target to run with approved host access. Reviewable
  commands and results remain in
  [P00 evidence](docs/plans/P00-foundation.md#evidence).
- P10 is complete: core history has durable coverage and leases, sticky RPC
  ingestion, canonical/orphan retention, finality-safe reorg handling, derived
  rollback journals, and identity-bound repair/reindex. Reviewable commands and
  results remain in [P10 evidence](docs/plans/P10-indexing.md#evidence).
- P20 is complete: block-scoped ABI/proxy, token, trace, state-difference,
  search, adapter, and statistics enrichment uses exact-state and lease-fenced
  publication contracts. OpenZeppelin 5.6.1-aware `proxy@2` and dependent
  `abi@2` add shared Beacon observations, exact Clone evidence, canonical
  upgrade/initialization facts, and generation-safe replay. Trace and StateDiff
  now bind one strict `debug_traceBlockByHash` response per non-empty block,
  with no per-transaction debug fallback or partial publication. Reviewable
  commands and results remain in [P20
  evidence](docs/plans/P20-enrichment.md#evidence).
- P30 is complete: P30-T16 adds authenticated runtime-only verification
  for canonical Genesis predeploys across native and Etherscan-compatible address
  submission without weakening ordinary creation-fact requirements. Its
  historical verifier-v2 foundation remains superseded at
  the runner and language boundaries by P70-T29; the maintained surface is
  bounded automatic Solidity/Yul matching with official
  architecture-neutral solc-js, asynchronous REST and explicit Sourcify
  workflows, and canonical-runtime-gated full/partial publication. P30-T15
  adds source-authenticated OpenZeppelin 5.6.1 interaction bindings fenced to
  the exact canonical proxy generation, runtime immutables, implementation and
  management code identities, shared Beacon/UUPS generation, code epoch, and
  continuous interaction coverage. P30-T17 atomically persists bounded
  verified function-selector sets for every newly successful verification;
  active-development migrations deliberately do not retain a legacy backfill
  or compatibility readiness path. Reviewable commands and results remain in
  [P30 evidence](docs/plans/P30-contract-verification.md#evidence).
- P40's existing native spec-first API, stable cursors, authenticated
  capability surfaces, durable event replay, and the explicit Etherscan V2
  subset pass their contract, race, security, and PostgreSQL coverage
  boundaries, including transaction-scoped logs, token transfers, trace
  identity, state-change resources, and snapshot-stable address activity
  across transactions, internal calls, ERC-20 transfers, and NFT transfers.
  P40-T09 adds a writer-authoritative complete home snapshot and centralized
  SSE fanout without changing the durable `/events` replay protocol. P40-T10
  adds snapshot-bound address origins, exact ERC-20 balances, and isolated
  public wallet-chain configuration. P40-T11 completes writer-authoritative
  proxy detail, canonical upgrade and initialization histories, stale-cursor
  fencing, exact verified interaction bindings, and anonymous free verified
  artifact reads, including real OpenZeppelin 5.6.1 monolith/split coverage.
  P40-T13 adds exact-inclusion successful internal ETH transfers plus
  block-correct nullable ERC-20 decimals to transaction and address transfer
  resources. P40-T14 exposes exact-block execution and authenticated receipt
  Blob base-fee facts on transaction resources. P40-T15 adds strict,
  endpoint-scoped consecutive-snapshot replacement evidence and one
  writer-authoritative included/pending/replaced transaction-detail contract,
  with PostgreSQL and monolith/split production-runtime coverage. P40-T16 adds
  snapshot-stable canonical address withdrawal history ordered and paginated
  directly on numeric withdrawal indexes, with exact block-identity cursors.
  P40-T17 projects global-list methods from exact canonical published
  execution identity and ABI results, with deterministic creation, native
  transfer, selector, and short-calldata fallbacks. P40-T18 shares that
  projection with address transactions and resolves verified selector entries,
  same-code history, proxy routes, and Diamond routes without loading complete
  ABI documents on list reads. P40-T19 also resolves exact verified-address
  selectors when a completed state diff omits an unchanged direct-call target,
  covering both transaction lists and the transaction calldata resource.
- P50's established surface is complete: its core and capability explorer pages, exact
  verification-job and published-artifact reads, EIP-6963 wallet discovery,
  session-fenced
  contract calls, and the binary-embedded SPA pass generated-client,
  bilingual, responsive, security-header, browser, and WCAG coverage. The
  transaction detail surface now adds five deep-linkable lazy subresource tabs,
  deterministic action summaries, and reorg identity fencing. Preview
  new-head wake plus bounded first-page refresh keeps newly indexed blocks and
  transactions visible without disturbing historical cursor pages. P50-T08
  keeps transaction copy controls visible and exposes the validated receipt
  contract address for successful top-level creations. P50-T09 removes the
  home hero, localizes live recent-block age, and unifies the header brand mark
  with the browser favicon. P50-T10 adds address activity tabs with lazy,
  independent pagination and a contract-only entry to the existing contract
  page while keeping state identity fields out of the address summary.
  P50-T11 replaces the home page's three REST polling loops with one atomic
  same-origin full-snapshot EventSource and no HTTP fallback. P50-T12 adds the
  de-duplicated address header, QR/copy controls, origin and ERC-20 holdings,
  configured native labels, and account-independent add-network flow. P50-T13
  consolidates that flow into the existing wallet menu and removes its
  duplicate footer surface. P50-T14 completes anonymous verified-ABI-driven
  contract and as-proxy implementation forms, exact ProxyAdmin and Beacon
  management targets, binding-fenced writes, upgrade and initialization
  histories, and real OpenZeppelin 5.6.1 seven-role Preview acceptance.
  P50-T15 corrects small-integer ABI result formatting for Viem's `number`
  representation, including ERC-20 `decimals()` and nested/revert values, and
  rejects types that the selected codec cannot encode or decode. Its focused,
  ordinary, generation, production-build, live Preview, and security checks
  pass after the locked transitive `nanoid` update clears the web audit.
  P50-T49 surfaces successful internal ETH transfers directly in the
  transaction Overview without eagerly loading Trace, and renders ERC-20
  transfer quantities with exact integer decimal expansion across transaction,
  address, and token event tables.
  P50-T50 moves that internal-transfer surface into its own deep-linkable,
  lazy-loaded tab immediately before Token transfers and removes it from the
  Overview.
  P50-T16 completes the verified-code surface with a strictly read-only
  multi-file CodeMirror workspace, explicit compiler-setting summaries, and
  copyable disclosure-first ABI, transformation, and artifact details across
  bilingual themes and responsive embedded-browser flows. P50-T53 consolidates
  transaction Gas usage with an exact percentage and exposes execution plus
  Blob fee settings across Overview and the Blob tab. P50-T54 classifies
  transaction actions from exact transaction-time direct, delegated, or empty
  execution-code evidence and fails closed while that evidence is unavailable.
  P50-T55 implements retention-bounded pending/replaced detail previews and a
  shared iconized transaction-status system, covered by bilingual responsive
  browser and post-migration monolith/split production-runtime tests. P50-T56
  adds lazy address withdrawal history after Internal Transactions and shares
  exact Gwei-to-Ether rendering between address and block withdrawal tables.
  P50-T57 adds the bilingual compact Method column only to the global
  transaction list, with full-signature disclosure and responsive overflow
  coverage. P50-T58 implements ascending semantic compiler-version ordering
  through the catalog API and verification page; its targeted, PostgreSQL,
  browser, race, security, license, and deployment gates pass. P50-T59 reuses
  the compact bilingual Method cell in address Transactions only, with
  full-signature disclosure and responsive/accessibility coverage. P50-T60
  restores the shared localized Direction/方向 heading instead of exposing the
  `table.direction` resource key. P50-T61 established consistent status and
  finality presentation for Address Transactions. P50-T63 moves Finality into
  an independent final column, aligns the transaction amount heading with the
  global table, and leaves other address activity tables unchanged.
- P59 is complete. P59-T05 keeps Solidity builtin failures concise while
  preserving structured custom-error arguments. P59-T04 exposes the canonical
  root failure as a dedicated
  API resource and renders bounded, provenance-aware custom and Solidity
  builtin error decoding in transaction Overview. P59-T03 extends the exact
  verified-address fallback used by calldata reads to direct Trace calls whose
  unchanged target is omitted by completed state-diff evidence, while indirect
  and unresolved delegated execution stay fail-closed.
  `trace@2` binds validated callTracer logs to exact execution
  frames while retaining emitter identity, decodes call inputs, successful
  outputs, and direct reverts with candidate-bound provenance, and keeps
  trace-cache payloads free of ABI projections. Migration `0039`, bounded
  trace reindex and dependent proxy/ABI replay, generated API clients, and the
  bilingual disclosure UI pass PostgreSQL, browser, monolith/split runtime,
  real Hardhat proxy, and common-gate evidence. Reviewable commands and results
  remain in [P59 evidence](docs/plans/P59-trace-bound-abi-decoding.md#evidence).
  P59-T02 additionally lets creation-block logs consume a later same-address,
  exact-runtime artifact as `code_hash`/`high` without backdating verified
  address provenance; the reported Preview transaction now decodes all four
  exact-trace-attributed logs.
- P61 is complete: P61-T16 adds an `abi@4` transaction-scoped effective
  execution-identity projection without changing raw `state_diff@3` evidence.
  Existing migrations `0040` and `0047` plus `state_diff@3` preserve and replay
  type-4 authorization outcomes into canonical delegation and transaction-time
  execution-code facts, including unchanged top-level targets recovered from
  one same-endpoint, exact-block complete-prestate request. `trace@3` exposes
  raw execution evidence while `abi@4` materializes transaction-scoped EIP-7702
  execution identity and verified CREATE/CREATE2 constructor decoding without changing
  raw context, emitter, initcode, input, or output. Generated authorization and
  delegation APIs plus the bilingual delegated-EOA interaction surface use a
  writer-authoritative binding fence before every write. Cleared delegations
  remain discoverable through an exact canonical-history address signal that
  opens History without eager binding or artifact reads. P61-T11 restores
  newest-first numeric delegation-history ordering and replaces cleared Code
  content with a lazy writer-authoritative Status surface. P61-T12 removes its
  redundant History action while retaining the dedicated History tab. P61-T13
  adds embedded-production browser coverage and real signed Prague/Anvil
  type-4 transaction coverage across authorization outcomes, transaction-time
  delegate identity, clearing, and canonical reorg retention. P61-T14 restores
  Native Transfer list and transaction-action projection when diff-only
  prestate evidence omits an unchanged empty target. PostgreSQL,
  browser, monolith/six-role runtime, real Hardhat verification, and common-gate
  results remain in [P61 evidence](docs/plans/P61-eip7702-delegated-accounts.md#evidence).
- P62 is complete: migration `0043` replaces the singular implementation
  assumption with exact selector-scoped Diamond facets and immutable targets.
  Bounded fixed-block Loupe detection, ordered reorg-safe DiamondCut history,
  historical selector-bound ABI resolution, collision-preserving interaction,
  generated APIs, and bilingual facets/history surfaces pass PostgreSQL
  ordinary/race, Chromium, real Hardhat monolith/six-role, and common-gate
  evidence. Reviewable commands and results remain in
  [P62 evidence](docs/plans/P62-erc2535-diamond-proxies.md#evidence).
- P63 is complete: native address verification accepts bounded multi-file Geas
  v0.3.3 sources through a checksum- and executable-bound helper, exact
  runtime/optional creation matching, PostgreSQL lease fencing, bilingual Web
  submission, and empty-ABI artifact reads. The pinned sys-asm EIP-7002
  constructor/runtime verifies in monolith and six-role production topologies;
  standalone and Etherscan submission surfaces remain Solidity-only. Reviewable
  commands and results remain in
  [P63 evidence](docs/plans/P63-geas-contract-verification.md#evidence).
- P64 is complete: the newest exact canonical ERC-721/ERC-1155 metadata
  observation is exposed through a bounded native API and rendered as inert
  text, ordered traits, and a confirmation-gated external image link on the NFT
  detail route. The existing authenticated media proxy and its SSRF/content
  boundary remain unchanged. Reviewable commands and results remain in
  [P64 evidence](docs/plans/P64-nft-metadata-web.md#evidence).
- P60 is complete: the hardened non-root image, PostgreSQL-only monolith and
  split-role deployments, replica failover, bounded capacity controls,
  disposable accelerators, telemetry, migration safety, and operator tooling
  pass their targeted runtime, integration, race, Helm, and short-load
  evidence. P60-T07 additionally binds every durable metadata transition log
  to its exact NFT/block identity and bounded network diagnosis. P60
  completion does not promote P70's security, conformance, long-soak,
  artifact, or release gates.
  P60-T08 adds exact bounded worker/job/block/stage summaries, transaction
  identity only for deterministically failing trace items, correlated
  lifecycle/HTTP/RPC events, and stable degraded-adapter diagnostics across
  both production topology shapes without weakening existing redaction.
  P60-T09 classifies metadata DNS, connection, TLS, request, policy, and
  unknown transport failures into closed reasons and retains the preceding
  retry code when an attempt budget is exhausted, without restoring raw nested
  errors.
- P65's SIWE session, wallet, profile, and administrator work is complete.
  P65-T09 adds scoped user-owned API keys and the tabbed `/account` workspace;
  PostgreSQL, browser, schema/runtime, Hardhat, Foundry, security, race,
  deployment, and aggregate gates pass for monolith and six-role deployments.
- P66 is blocked: the additive ledger, reviewed v2 exact-EVM adapter,
  replay-fenced capture and settlement middleware, free and administrative
  APIs, operator reconciliation, optional payer attribution, and embedded
  account/administrator views plus operational/deployment closure are
  complete. The payment protocol remains accountless; the explicit opt-in Base
  Sepolia transaction and reconciliation gate remains open.
- P70 is blocked: P70-T07 completes the optional API read pool with
  writer-authoritative routing, fail-closed readiness, deployment wiring, and
  capacity guidance. P70-T09 has implemented direct reviewed go-ethereum
  ownership for recognized Ethereum protocol semantics under raw-first RPC,
  transport, persistence, and public-contract adapters; its focused,
  PostgreSQL, generation, security, license, Helm, Compose, ordinary, and race
  gates plus Docker image/runtime and aggregate common-gate verification pass.
  P70-T16's execution analytics close with a deterministic competing-hash
  production Compose reorg; P70-T18's address/network release validation passes
  the embedded browser and managed PostgreSQL suites; P70-T19 replaces manual
  PostgreSQL setup and shell-heavy runtime smoke with Go-owned integration,
  production-schema, and monolith/six-role runtime E2E targets. P70-T20
  completes optional process-native TLS on API listeners, a Preview-local
  mkcert workflow, role-scoped Helm certificate delivery, and production
  runtime validation; P70-T23's native-Linux runtime rerun now closes the TLS
  fixture permission boundary. P70-T04 still requires the named reference
  capacity environment. P70-T29 has replaced the superseded remote runner with
  the host-native API-owned Node/solc-js executor and removed the obsolete
  language surface. Its ARM64 Preview, production-image, migration, runtime,
  real Hardhat monolith/split, and final native AMD64/ARM64 CI evidence passes.
  P70-T30 makes the independent Hardhat fixture compile networklessly and keeps
  its retained container-written diagnostics readable by the CI host.
  P70-T27's complete real Hardhat proxy gate now passes in both topologies.
  P70-T31 adds an independent Foundry source-verification production gate with
  a manifest-digest-pinned client, offline Solidity 0.8.30 preflight, strict
  `/v2/api?chainid=1` submission, duplicate-job prevention, and normalized
  monolith/split provenance parity. Its native AMD64 and ARM64 CI matrix passed
  on PR #20's exact implementation head, completing P70-T31. P70-T32
  established an explicit operator-selected runtime location without adding a
  runtime mount or weakening manifest identity. P70-T40 now replaces the
  separate Node/wrapper/runtime tree with one Node 26.7.0 SEA, one executor
  path, and an automatically discovered, attributed, and revalidated
  target-rootfs ELF closure; focused, deployment, production-image, common,
  runtime, Hardhat, and Foundry monolith/split gates pass locally on ARM64,
  while the maintained native AMD64/ARM64 CI matrices exercise the same image
  and real compiler paths. P70-T38 retains the authenticated
  solc-js artifact cache across application replacement without changing
  compiler trust or catalog-freshness semantics; cache concurrency, Compose,
  Helm, image, real Hardhat/Foundry, runtime, and common gates pass locally on
  ARM64. P70-T15 now completes Preview public verification and one exact
  public-IPFS NFT metadata fetch, including scoped Docker fake-IP diagnostics,
  one-attempt PostgreSQL persistence, and metadata-worker restart stability.
  P66 live-payment evidence still gates conformance, security, release CI,
  documentation, and artifact work.

## Global Release Gates

- [ ] Every P00–P66 plan is `done` with reviewable evidence.
- [ ] Genesis-to-head ingestion is gap-free, restart-safe, and reorg-safe.
- [ ] Monolith and split-role modes pass the same behavioral acceptance suite.
- [ ] Optional RPC capabilities and optional infrastructure fail explicitly and
      never corrupt core readiness.
- [ ] API, migrations, embedded SPA, security, and operational documentation
      gates pass.
- [ ] Reference capacity test sustains 500 read requests/second for 30 minutes,
      common-query p95 below 500 ms, error rate below 0.1%, and core lag no more
      than two blocks under a healthy upstream.

## Update Rules

Follow `AGENTS.md`. Child work items are updated in place. When a child plan
changes overall state, update the corresponding row above in the same change.
