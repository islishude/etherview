# Etherview Implementation Plan

Status: `blocked`

## Goal

Build a production-oriented, single-chain-configurable Ethereum execution-layer
explorer in Go. The React SPA is embedded in the Go binary. The same components
run as a monolith or as independently scalable roles, with PostgreSQL as the
only mandatory external service.

Consensus enrichment and blob archives are optional extensions tracked in P79.
Validator browsing, MEV accounting and L2-specific batch semantics are not core v1 scope.

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
| P70 | [Release](docs/plans/P70-release.md) | blocked | P10, P20, P30, P40, P50, P64, P65, P67, P68, P73, P74, P75, P77, P78 | Security, conformance, performance, E2E, documentation, and v1 release |
| P73 | [Prepaid API Billing](docs/plans/P73-prepaid-api-billing.md) | blocked | P30, P40, P65 | x402 account top-ups and PostgreSQL prepaid credit for bounded Etherscan V2 reads |
| P74 | [Etherscan V2 Read Expansion](docs/plans/P74-etherscan-v2-read-expansion.md) | done | P20, P40, P65 | Authoritative withdrawals, holdings, funding, block counts, and advanced compatibility filters |
| P75 | [Runtime and Performance Hardening](docs/plans/P75-runtime-performance-hardening.md) | done | P10, P30, P68, P73, P74 | Bounded traversal/backfill, efficient persistence/projections, compatibility reads, and lean SPA delivery |
| P76 | [ERC-4337 UserOperation Browsing](docs/plans/P76-erc4337-user-operations.md) | done | P10, P20, P30, P40, P50, P68, P75 | Canonical EntryPoint v0.6-v0.9 UserOperation indexing, APIs, search, and bilingual Web browsing |
| P77 | [Authoritative ERC-20 Token Holders](docs/plans/P77-authoritative-erc20-token-holders.md) | done | P20, P30, P40, P50, P68, P74, P75 | Genesis-covered, exact-state-reconciled ERC-20 holder snapshots, APIs, compatibility, and Web browsing |
| P78 | [Watchlist, Notifications and CSV](docs/plans/P78-watchlist-notifications-csv.md) | done | P10, P20, P40, P50, P65 | Private watches, durable in-app notifications and bounded address exports |
| P79 | [Consensus and Blob Archives](docs/plans/P79-consensus-and-blobs.md) | done | P10, P20, P40, P50 | Optional Beacon enrichment, consensus-first slots and immutable blob archives |

Allowed plan states are `planned`, `in_progress`, `blocked`, `done`, and
`superseded`.

## Current Work

P73-T08 remains blocked on live EIP-3009/Permit2 payment and writer/chain
reconciliation. P70-T04 remains blocked on the operator-provisioned reference
capacity environment. P70-T01, P70-T02, P70-T03 and P70-T05 await P73 closure;
P70-T06 is dependency-gated. Their child plans own clearing conditions.

Completed plans retain feature milestones and concise historical acceptance.
Routine fixes, dependency bumps, CI repairs and documentation edits do not
reopen them or require new plan items.

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

Use the [development guide](docs/development.md#planning-threshold) to decide
whether a change needs planning. Update existing items in place; synchronize
this index only when scope, dependencies or overall status changes. Record
verification once at its owning change or milestone, not in a root changelog.
