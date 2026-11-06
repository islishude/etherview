# Etherview Implementation Plan

Status: `blocked`

## Goal

Build a production-oriented, single-chain-configurable Ethereum execution-layer
explorer in Go. The React SPA is embedded in the Go binary. The same components
run as a monolith or as independently scalable roles, with PostgreSQL as the
only mandatory external service.

Consensus enrichment, execution requests and blob archives are optional extensions owned by P20,
with APIs in P40, browsing in P50, and deployment/Preview in P30. Preview writer
IAM enforcement remains deferred until RustFS 1.0.5 is evaluated; P30 owns the
clearing condition.
Validator browsing, MEV accounting and L2-specific batch semantics are not core v1 scope.

## Plan Index

| ID  | Plan                                                                              | Status  | Depends on                   | Outcome                                                                                                                                                                                                                                                                                                                                      |
| --- | --------------------------------------------------------------------------------- | ------- | ---------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| P00 | [Foundation](docs/plans/P00-foundation.md)                                        | done    | —                            | Governance, toolchain, config, CLI, native pgx/typed SQL and neutral contract boundaries, CI, and embedded SPA skeleton                                                                                                                                                                                                                      |
| P10 | [Indexing](docs/plans/P10-indexing.md)                                            | done    | P00                          | Bounded full-history indexing/backfill, immutable bundles, batched persistence, canonicality, finality, reorgs, and repair                                                                                                                                                                                                                   |
| P20 | [Enrichment & Durable Archives](docs/plans/P20-enrichment.md)                     | done    | P10                          | Tokens/NFT metadata/holders, current-chain ENS, UserOperations, ABI/proxy decoding, traces, balances, statistics, optional consensus/requests, and durable blob archives; ENS v1/v2 discovery, registration profiles and publication                                                                                                         |
| P30 | [Contract Platform & Runtime Operations](docs/plans/P30-contract-verification.md) | done    | P00, P10, P20                | Local verification, Sourcify source import/publication, automatic source discovery, contract intelligence, runtime/deployment, telemetry, optional accelerators, Caddy HTTPS, Kubo and Lighthouse/archive Preview with genesis ERC-4337, local ENS and Rundler; ENS under enrich, local production parity and independent Mainnet acceptance |
| P40 | [API & Data Exports](docs/plans/P40-api.md)                                       | done    | P10; incremental P20/P30     | Native REST/search/SSE, canonical NFT metadata/ENS snapshots, Etherscan V2, UserOperation/holder queries, consensus/requests/blob downloads, CSV, and bounded quotas; database-only ENS detail/search/history, publication snapshots and status/events                                                                                       |
| P50 | [Web](docs/plans/P50-web.md)                                                      | done    | P40; incremental P20/P30     | Bilingual route-lazy embedded SPA, precompressed assets, manual explorer browsing with live home/status and task progress, guarded NFT links, ENS display, injected-wallet interaction, session-authenticated verification, and optional browser Turnstile protection; manual name/detail/history reads and expired-label hiding             |
| P60 | [User Accounts, Billing & Personal Tools](docs/plans/P60-user-auth-billing.md)    | done    | P10, P20, P30, P40, P50      | SIWE sessions, profiles, administration, scoped user API keys, prepaid API credit, x402 top-ups and standalone multi-chain facilitator (Rustack KMS and dual-Anvil local acceptance complete), private watches, and one-shot browser push                                                                                                    |
| P70 | [Release](docs/plans/P70-release.md)                                              | blocked | P10, P20, P30, P40, P50, P60 | Security, conformance, performance, E2E, documentation, and v1 release                                                                                                                                                                                                                                                                       |

Allowed plan states are `planned`, `in_progress`, `blocked`, `done`, and
`superseded`.

## Current Work

P50-T89 delivers repository-owned shadcn/ui primitives across the shared UI,
retaining the explorer brand, native interaction boundaries and strict CSP.
Its child plan records completed local frontend and embedded-browser acceptance.

P30-T155 delivers database-wide deadlock acceptance for managed integration and
both production runtime layouts, with bounded diagnostics and cross-domain
concurrency. Its child plan records completed local acceptance.

P40-T35 and P50-T88 deliver multi-topic AND queries, historical indexed event
definitions, and dynamic typed filters with browser-side topic encoding. Their
child plans record completed local database, browser and production topology acceptance.

P40-T34 and P50-T87 deliver range-scoped event queries and compact event-log browsing; their child plans record local database, browser and Preview acceptance.

P40-T33 and P50-T86 deliver indexed contract event logs, the publication-owned
historical event directory and bilingual address-page signature/topic filters.
Their child plans record local database, browser and production topology acceptance.

Delivered P00–P60 capabilities include P30-T153 independent verification
execution, PostgreSQL task claims with optional NATS wake and asynchronous
Sourcify import, plus P30-T154 session-fenced multi-instance polling, maintenance
and publication with safe shared compiler-cache startup. Their child plans summarize delivered capabilities and
historical acceptance, with independent evidence limits for Mainnet ENS, live
payments, browser-provider delivery and local compiler/runtime fixtures.
Routine maintenance does not reopen these milestones.

P70-T01, P70-T02, P70-T03 and P70-T05 are dependency-ready and await final
release conformance, security, recovery/soak and documentation acceptance.
P70-T04 remains blocked on an operator-provisioned reference capacity environment;
P70-T06 depends on all five gates. [P70](docs/plans/P70-release.md#current-blockers)
owns the clearing conditions. Feature acceptance does not close these gates.

Preview storage-enforced writer IAM remains deferred under
[P30](docs/plans/P30-contract-verification.md#current-blockers).
ENS full-directory coverage remains measured deployment progress; representative
Mainnet publication acceptance does not prove complete historical coverage.

## Global Release Gates

- [ ] Every plan required by P70 is `done` or explicitly `superseded` with reviewable evidence.
- [ ] Genesis-to-head ingestion is gap-free, restart-safe, and reorg-safe.
- [ ] Monolith and split-role modes pass the same behavioral acceptance suite.
- [x] Background current-chain ENS v1/v2 discovery, database-only reads, and label
      invalidation pass P30-T150; P30-T151 independently verifies Mainnet
      deployment, activation and representative publication reconciliation. Local fixtures do
      not close the live-source gate.
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
The [catalog](docs/plans/index.md) reserves removed IDs and identifies merged
milestones; never renumber or reuse them.
