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
| P30 | [Contract Verification](docs/plans/P30-contract-verification.md) | done | P10, P20 | Blockscout-style Solidity/Yul/Vyper verification, dynamic compilers, and metadata safety |
| P40 | [API](docs/plans/P40-api.md) | done | P10; incremental P20/P30 | Native REST, search, API keys, SSE, and Etherscan V2 compatibility |
| P50 | [Web](docs/plans/P50-web.md) | done | P40; incremental P20/P30 | Bilingual embedded SPA and injected-wallet contract interaction |
| P60 | [Runtime & Operations](docs/plans/P60-runtime-operations.md) | done | P00; spans P10–P50 | Monolith/split runtime, Compose, Helm, observability, optional adapters |
| P65 | [User Authentication](docs/plans/P65-user-auth.md) | done | P40, P50 | SIWE wallet login, revocable sessions, profiles, and administration |
| P66 | [x402 API Billing](docs/plans/P66-x402-billing.md) | blocked | P40, P60; optional P65 | Accountless exact-EVM per-request payment and durable reconciliation |
| P70 | [Release](docs/plans/P70-release.md) | blocked | P10–P66 | Security, conformance, performance, E2E, documentation, and v1 release |

Allowed plan states are `planned`, `in_progress`, `blocked`, `done`, and
`superseded`.

## Phase Results

- P00 is complete: the repository has enforced plan governance, minimum-version
  Go/Node/npm checks that support compatible newer stable releases, a runnable
  role-aware CLI, embedded migrations and generated contracts, and a
  binary-served SPA. Reviewable commands and results remain in
  [P00 evidence](docs/plans/P00-foundation.md#evidence).
- P10 is complete: core history has durable coverage and leases, sticky RPC
  ingestion, canonical/orphan retention, finality-safe reorg handling, derived
  rollback journals, and identity-bound repair/reindex. Reviewable commands and
  results remain in [P10 evidence](docs/plans/P10-indexing.md#evidence).
- P20 is complete: block-scoped ABI/proxy, token, trace, state-difference,
  search, adapter, and statistics enrichment uses exact-state and
  lease-fenced publication contracts. Reviewable commands and results remain in
  [P20 evidence](docs/plans/P20-enrichment.md#evidence).
- P30 is complete: the destructive verifier-v2 cutover provides bounded
  automatic Solidity/Yul/Vyper candidate matching, a durable dynamic compiler
  catalog and digest-pinned generic runner, native asynchronous REST and
  explicit Sourcify workflows, and canonical-runtime-gated full/partial
  publication. Reviewable commands and results remain in
  [P30 evidence](docs/plans/P30-contract-verification.md#evidence), including
  runner-platform-aware compiler artifact discovery and provenance.
- P40 is complete: the native spec-first API, stable cursors, authenticated
  capability surfaces, durable event replay, and the explicit Etherscan V2
  subset pass their contract, race, security, and PostgreSQL coverage
  boundaries, including transaction-scoped logs, token transfers, trace
  identity, state-change resources, and snapshot-stable address activity
  across transactions, internal calls, ERC-20 transfers, and NFT transfers.
  P40-T09 adds a writer-authoritative complete home snapshot and centralized
  SSE fanout without changing the durable `/events` replay protocol. P40-T10
  adds snapshot-bound address origins, exact ERC-20 balances, and isolated
  public wallet-chain configuration.
- P50 is complete: core and capability explorer pages, exact verification-job
  and published-artifact reads, EIP-6963 wallet discovery, session-fenced
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
  duplicate footer surface.
- P60 is complete: the hardened non-root image, PostgreSQL-only monolith and
  split-role deployments, replica failover, bounded capacity controls,
  disposable accelerators, telemetry, migration safety, and operator tooling
  pass their targeted runtime, integration, race, Helm, and short-load
  evidence. P60 completion does not promote P70's security, conformance,
  long-soak, artifact, or release gates.
- P65 is complete: a SIWE action can now select and authorize an injected
  wallet without a separate connection step while retaining
  writer-authoritative SIWE challenges, Cookie/Origin/CSRF sessions,
  user/operator administration, bounded wallet signing, embedded
  account/admin UX, role-scoped deployment Secrets, and operational/security
  closure pass unit, race, PostgreSQL, browser, Helm/Compose, image, license,
  and security evidence.
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
  production-schema, and monolith/seven-role runtime E2E targets. P70-T20
  completes optional process-native TLS on API listeners, a Preview-local
  mkcert workflow, role-scoped Helm certificate delivery, and production
  runtime validation. P70-T04 still requires the named reference capacity
  environment, and P70-T15 has restored Preview catalog publication through
  its explicitly unsafe local fake-IP exception but still requires real
  Solidity/Vyper execution and NFT metadata evidence. P66 live-payment evidence
  still gates conformance, security, release CI, documentation, and artifact
  work.

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
