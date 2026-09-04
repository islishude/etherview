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
| P30 | [Contract Platform & Runtime Operations](docs/plans/P30-contract-verification.md) | done | P00, P10, P20 | Consolidated verification, contract intelligence, runtime, deployment, telemetry, and optional accelerators |
| P40 | [API](docs/plans/P40-api.md) | done | P10; incremental P20/P30 | Native REST, search, API keys, SSE, and Etherscan V2 compatibility |
| P50 | [Web](docs/plans/P50-web.md) | done | P40; incremental P20/P30 | Bilingual embedded SPA and injected-wallet contract interaction |
| P64 | [NFT Metadata Web](docs/plans/P64-nft-metadata-web.md) | done | P20, P30, P40, P50 | Canonical NFT metadata projection, standard-event refresh, and guarded external-image navigation |
| P65 | [User Authentication](docs/plans/P65-user-auth.md) | done | P40, P50 | SIWE wallet login, revocable sessions, profiles, administration, and scoped user API keys |
| P67 | [ENS Primary Names](docs/plans/P67-ens-primary-names.md) | done | P20, P30, P40, P50 | Snapshot-stable official and custom ENS forward resolution plus verified primary-name display |
| P68 | [Runtime and Architecture Hardening](docs/plans/P68-runtime-architecture-hardening.md) | done | P00, P30, P40, P50 | Explicit SQL, runtime, HTTP, Web, and quality boundaries |
| P70 | [Release](docs/plans/P70-release.md) | blocked | P10, P20, P30, P40, P50, P64, P65, P67, P68, P73, P74, P75, P77 | Security, conformance, performance, E2E, documentation, and v1 release |
| P73 | [Prepaid API Billing](docs/plans/P73-prepaid-api-billing.md) | blocked | P30, P40, P65 | x402 account top-ups and PostgreSQL prepaid credit for bounded Etherscan V2 reads |
| P74 | [Etherscan V2 Read Expansion](docs/plans/P74-etherscan-v2-read-expansion.md) | done | P20, P40, P65 | Authoritative withdrawals, holdings, funding, block counts, and advanced compatibility filters |
| P75 | [Runtime and Performance Hardening](docs/plans/P75-runtime-performance-hardening.md) | done | P10, P30, P68, P73, P74 | Bounded traversal/backfill, efficient persistence/projections, compatibility reads, and lean SPA delivery |
| P76 | [ERC-4337 UserOperation Browsing](docs/plans/P76-erc4337-user-operations.md) | done | P10, P20, P30, P40, P50, P68, P75 | Canonical EntryPoint v0.6-v0.9 UserOperation indexing, APIs, search, and bilingual Web browsing |
| P77 | [Authoritative ERC-20 Token Holders](docs/plans/P77-authoritative-erc20-token-holders.md) | done | P20, P30, P40, P50, P68, P74, P75 | Genesis-covered, exact-state-reconciled ERC-20 holder snapshots, APIs, compatibility, and Web browsing |

Allowed plan states are `planned`, `in_progress`, `blocked`, `done`, and
`superseded`.

## Phase Results

- P00, P10, and P20 are complete; foundation, canonical chain history,
  enrichment, reorg retention, and lease-fenced publication are documented in
  their child-plan evidence.
- P30 is complete as the single contract-platform and runtime-operations plan.
  Its current work items preserve distinct verification, proxy, ABI, Trace,
  EIP-7702, Geas, CWIA, derived-verification, deployment, and runtime evidence in
  [P30 evidence](docs/plans/P30-contract-verification.md#evidence).
- P40 and P50 are complete; native API, compatibility, embedded SPA, wallet,
  browser, and generated-contract evidence remains in their child plans.
- P64, P65, P67, P68, P74, P75, and P76 are complete with their current
  PostgreSQL, browser, runtime, deployment, and common-gate evidence.
- P77 is complete with exact-state holder reconciliation, generated APIs,
  compatibility billing, bilingual Web browsing, PostgreSQL/race, production
  topology, security, license, and common-gate evidence. P70 remains blocked by
  P73-T08 live payment reconciliation and P70-T04 reference-capacity evidence.
  Local or synthetic evidence does not close either external gate.

## Global Release Gates

- [ ] Every plan required by P70 is `done` or explicitly `superseded` with reviewable evidence.
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
