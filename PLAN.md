# Etherview Implementation Plan

Status: `blocked`

## Goal

Build a production-oriented, single-chain-configurable Ethereum execution-layer
explorer in Go. The React SPA is embedded in the Go binary. The same components
run as a monolith or as independently scalable roles, with PostgreSQL as the
only mandatory external service.

Consensus enrichment, execution requests and blob archives are optional extensions owned by P20,
with APIs in P40, browsing in P50, and deployment/Preview in P30. Preview writer
IAM enforcement remains deferred until RustFS 1.0.4 is evaluated; P30 owns the
clearing condition.
Validator browsing, MEV accounting and L2-specific batch semantics are not core v1 scope.

## Plan Index

| ID  | Plan                                                                              | Status  | Depends on                   | Outcome                                                                                                                                                                    |
| --- | --------------------------------------------------------------------------------- | ------- | ---------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| P00 | [Foundation](docs/plans/P00-foundation.md)                                        | done    | —                            | Governance, toolchain, config, CLI, native pgx/typed SQL and neutral contract boundaries, CI, and embedded SPA skeleton                                                    |
| P10 | [Indexing](docs/plans/P10-indexing.md)                                            | done    | P00                          | Bounded full-history indexing/backfill, immutable bundles, batched persistence, canonicality, finality, reorgs, and repair                                                 |
| P20 | [Enrichment & Durable Archives](docs/plans/P20-enrichment.md)                     | done    | P10                          | Tokens/NFT metadata/holders, snapshot-stable ENS, UserOperations, ABI/proxy decoding, traces, balances, statistics, optional consensus/requests, and durable blob archives |
| P30 | [Contract Platform & Runtime Operations](docs/plans/P30-contract-verification.md) | done    | P00, P10, P20                | Consolidated verification, contract intelligence, runtime, deployment, bounded telemetry, optional accelerators, Kubo/HTTPS and Lighthouse/archive Preview                 |
| P40 | [API & Data Exports](docs/plans/P40-api.md)                                       | done    | P10; incremental P20/P30     | Native REST/search/SSE, canonical NFT metadata/ENS snapshots, Etherscan V2, UserOperation/holder queries, consensus/requests/blob downloads, CSV, and bounded quotas       |
| P50 | [Web](docs/plans/P50-web.md)                                                      | done    | P40; incremental P20/P30     | Bilingual route-lazy embedded SPA, precompressed assets, event-driven explorer/personal tools, guarded NFT links, ENS display, and injected-wallet interaction             |
| P60 | [User Accounts, Billing & Personal Tools](docs/plans/P60-user-auth-billing.md)    | blocked | P10, P20, P30, P40, P50      | SIWE sessions, profiles, administration, scoped user API keys, prepaid API credit, x402 top-ups, private watches, and one-shot browser push                                |
| P70 | [Release](docs/plans/P70-release.md)                                              | blocked | P10, P20, P30, P40, P50, P60 | Security, conformance, performance, E2E, documentation, and v1 release                                                                                                     |

Allowed plan states are `planned`, `in_progress`, `blocked`, `done`, and
`superseded`.

## Current Work

P60-T17 remains blocked on live EIP-3009/Permit2 payment and writer/chain
reconciliation. P60-T20 is done with operator-confirmed browser-provider
delivery after all site tabs close; P60 retains the evidence and its limits.
P70-T04 remains blocked on the operator-provisioned reference
capacity environment. P70-T01, P70-T02, P70-T03 and P70-T05 await P60 closure;
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
