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
| P20 | [Enrichment](docs/plans/P20-enrichment.md) | in_progress | P10 | Tokens, NFTs, ABI/proxy decoding, traces, balances, and statistics |
| P30 | [Contract Verification](docs/plans/P30-contract-verification.md) | planned | P10, P20 | Sandboxed Solidity/Vyper verification, Sourcify, and metadata safety |
| P40 | [API](docs/plans/P40-api.md) | in_progress | P10; incremental P20/P30 | Native REST, search, API keys, SSE, and Etherscan V2 compatibility |
| P50 | [Web](docs/plans/P50-web.md) | in_progress | P40; incremental P20/P30 | Bilingual embedded SPA and injected-wallet contract interaction |
| P60 | [Runtime & Operations](docs/plans/P60-runtime-operations.md) | in_progress | P00; spans P10–P50 | Monolith/split runtime, Compose, Helm, observability, optional adapters |
| P70 | [Release](docs/plans/P70-release.md) | planned | P10–P60 | Security, conformance, performance, E2E, documentation, and v1 release |

Allowed plan states are `planned`, `in_progress`, `blocked`, `done`, and
`superseded`.

## Phase Results

- P00 is complete: the repository now has enforced plan governance, exact
  toolchains, a runnable role-aware CLI, embedded migrations and generated
  contracts, and a binary-served SPA. Reviewable commands and results remain in
  [P00 evidence](docs/plans/P00-foundation.md#evidence).
- P10 is complete: core history has durable coverage and leases, sticky RPC
  ingestion, canonical/orphan retention, finality-safe reorg handling, derived
  rollback journals, and identity-bound repair/reindex. Reviewable commands and
  results remain in [P10 evidence](docs/plans/P10-indexing.md#evidence).
- P20, P40, P50, and P60 are in progress on independent dependency-ready
  foundation items. P30 and P70 remain planned. Implemented supporting slices are
  not promoted until every owning work item satisfies its full acceptance
  boundary.

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
