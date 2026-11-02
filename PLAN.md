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
| P40 | [API & Data Exports](docs/plans/P40-api.md)                                       | done    | P10; incremental P20/P30     | Native REST/search/SSE, canonical NFT metadata/ENS snapshots, Etherscan V2, UserOperation/holder queries, consensus/requests/blob downloads, CSV, and bounded quotas; database-only ENS reads, publication snapshots and source status/events                                                                                                |
| P50 | [Web](docs/plans/P50-web.md)                                                      | done    | P40; incremental P20/P30     | Bilingual route-lazy embedded SPA, precompressed assets, manual explorer browsing with live home/status and task progress, guarded NFT links, ENS display, injected-wallet interaction, session-authenticated verification, and optional browser Turnstile protection; manual ENS reads and expired-label hiding                             |
| P60 | [User Accounts, Billing & Personal Tools](docs/plans/P60-user-auth-billing.md)    | done    | P10, P20, P30, P40, P50      | SIWE sessions, profiles, administration, scoped user API keys, prepaid API credit, x402 top-ups and standalone multi-chain facilitator (Rustack KMS and dual-Anvil local acceptance complete), private watches, and one-shot browser push                                                                                                    |
| P70 | [Release](docs/plans/P70-release.md)                                              | blocked | P10, P20, P30, P40, P50, P60 | Security, conformance, performance, E2E, documentation, and v1 release                                                                                                                                                                                                                                                                       |

Allowed plan states are `planned`, `in_progress`, `blocked`, `done`, and
`superseded`.

## Current Work

P20-T32, P40-T32, P50-T85 and P60-T23 are done: S2 blob archives, paid V2
content, session/CSRF-protected browser GET with cached preview/download, and
the `etherview.*` billing operation namespace passed full local acceptance.
Aggregate checks, integration race, 225 browser tests, RustFS, production schema,
all/split runtime with local payments, and full Lighthouse recovery/finality
passed on 2026-11-01. [P20-T32](docs/plans/P20-enrichment.md#blob-access-and-archive-update)
owns shared evidence and its limits. Independent release blockers remain open.

P30-T152 completed the [Preview manual scenario CLI](preview/docs/tools.md) with
proxy and EIP-7702 lifecycle controls, durable local run journals and inspection
links. Isolated all/split CLI and bilingual desktop/mobile browser acceptance passed;
wallet-provider actions and live payments remain separate manual gates.

P60-T22 completed whole-token top-up input and mandatory complexity-weighted V2 query pricing with local browser, writer and all/split payment acceptance. P60-T17 has now passed its independent live-payment gate.

ENS now indexes current-chain core block/log facts under enrich and serves
published names through database-only reads (ADR-0058). P20-T27/T28/T29,
P30-T149/T150/T151, P40-T27 and P50-T81 are done with
[new local and representative Mainnet evidence](docs/reviews/ens-current-chain-acceptance.md).
Exact verified results can publish while directory history is incomplete.
Full historical coverage remains deployment progress, not a completed claim.

P60-T17 is done: live Ethereum Sepolia EIP-3009/Permit2 payments have finalized
independent chain/writer reconciliation and unique successful Anvil API debits. Sepolia
remains payment-only. P60-T24 is done: payment-view wallet scope, immediate network
selection, retained failed selections and departure restoration passed local
frontend/browser acceptance; prior live cross-chain acceptance remains separate. P60-T25 is also done: multi-chain, multi-token unified credit and the second Preview payment chain passed local all/split, browser, writer, gateway and contract acceptance. T25 requires local acceptance only; the local Permit2 fixture makes no production-contract compatibility claim.
P60-T20 is done with operator-confirmed browser-provider delivery after all site
tabs close; P60 retains the evidence and its limits.
P70-T04 remains blocked on the operator-provisioned reference
capacity environment. P70-T01, P70-T02, P70-T03 and P70-T05 are now dependency-ready
and await execution of their final release gates; P70-T04 must also cover the
final ENS runtime and workload. P70-T06 is dependency-gated. Their child plans
own clearing conditions.

Completed plans retain feature milestones and concise historical acceptance.
Routine fixes, dependency bumps, CI repairs and documentation edits do not
reopen them or require new plan items.

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

ENS registration browsing and name-identity search are complete under P20-T30,
P40-T28 and P50-T82. This extends the completed current-chain rewrite; existing
P60/P70 safety, capacity, recovery and live acceptance boundaries remain in force.

ENS operation history and name-page redesign are complete under P20-T31 → P40-T29 → P50-T83.

P40-T30 completed unified proxy browsing and interaction reads on the latest completed snapshot.

P40-T31 completed latest-processed holder, NFT owner and statistics reads, including retained hourly rollups; wallet actions no longer inherit unrelated browsing invalidation.

P60-T26 completed the standalone multi-chain facilitator, durable broadcast progress
and recoverable top-up steps with local acceptance. P60-T27 completed Rustack KMS
and dual-Anvil end-to-end acceptance in both all/split layouts, including fault
recovery and independent receipt/journal/credit/debit reconciliation. P60 is done
within its accepted scope; P70 release gates remain separate.
