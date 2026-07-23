# Etherview Implementation Plan

Status: `in_progress`

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
| P30 | [Contract Verification](docs/plans/P30-contract-verification.md) | done | P10, P20 | Sandboxed Solidity/Vyper verification, Sourcify, and metadata safety |
| P40 | [API](docs/plans/P40-api.md) | done | P10; incremental P20/P30 | Native REST, search, API keys, SSE, and Etherscan V2 compatibility |
| P50 | [Web](docs/plans/P50-web.md) | done | P40; incremental P20/P30 | Bilingual embedded SPA and injected-wallet contract interaction |
| P60 | [Runtime & Operations](docs/plans/P60-runtime-operations.md) | in_progress | P00; spans P10–P50 | Monolith/split runtime, Compose, Helm, observability, optional adapters |
| P70 | [Release](docs/plans/P70-release.md) | planned | P10–P60 | Security, conformance, performance, E2E, documentation, and v1 release |

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
- P20 is complete: block-scoped ABI/proxy, token, trace, search, adapter, and
  statistics enrichment now uses exact-state and lease-fenced publication
  contracts. Reviewable commands and results remain in
  [P20 evidence](docs/plans/P20-enrichment.md#evidence).
- P30 is complete: verification publication is immutable and exact-result
  backed, compiler execution and cleanup fail closed, and Solidity, Vyper,
  Sourcify, metadata, and name-resolution boundaries pass their targeted,
  PostgreSQL, race, and common repository gates. Reviewable commands and
  results remain in
  [P30 evidence](docs/plans/P30-contract-verification.md#evidence).
- P40 is complete: the native spec-first API, stable cursors, authenticated
  capability surfaces, durable event replay, and the explicit Etherscan V2
  subset now pass their contract, race, security, and PostgreSQL coverage
  boundaries.
- P50 is complete: core and capability explorer pages, exact verification-job
  and published-artifact reads, EIP-6963 wallet discovery, session-fenced
  contract calls, and the binary-embedded SPA pass generated-client,
  bilingual, responsive, security-header, browser, and WCAG coverage. P60
  remains in progress and P70 remains planned; their release gates are not
  promoted by P50 completion.

## Global Release Gates

- [ ] Every P00–P60 plan is `done` with reviewable evidence.
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
