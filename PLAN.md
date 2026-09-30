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
| P70 | [Release](docs/plans/P70-release.md) | blocked | P10, P20, P30, P40, P50, P64, P65, P67, P68, P73, P74, P75, P77, P78 | Security, conformance, performance, E2E, documentation, and v1 release |
| P73 | [Prepaid API Billing](docs/plans/P73-prepaid-api-billing.md) | blocked | P30, P40, P65 | x402 account top-ups and PostgreSQL prepaid credit for bounded Etherscan V2 reads |
| P74 | [Etherscan V2 Read Expansion](docs/plans/P74-etherscan-v2-read-expansion.md) | done | P20, P40, P65 | Authoritative withdrawals, holdings, funding, block counts, and advanced compatibility filters |
| P75 | [Runtime and Performance Hardening](docs/plans/P75-runtime-performance-hardening.md) | done | P10, P30, P68, P73, P74 | Bounded traversal/backfill, efficient persistence/projections, compatibility reads, and lean SPA delivery |
| P76 | [ERC-4337 UserOperation Browsing](docs/plans/P76-erc4337-user-operations.md) | done | P10, P20, P30, P40, P50, P68, P75 | Canonical EntryPoint v0.6-v0.9 UserOperation indexing, APIs, search, and bilingual Web browsing |
| P77 | [Authoritative ERC-20 Token Holders](docs/plans/P77-authoritative-erc20-token-holders.md) | done | P20, P30, P40, P50, P68, P74, P75 | Genesis-covered, exact-state-reconciled ERC-20 holder snapshots, APIs, compatibility, and Web browsing |
| P78 | [Watchlist, Notifications and CSV](docs/plans/P78-watchlist-notifications-csv.md) | done | P10, P20, P40, P50, P65 | Private watches, durable in-app notifications and bounded address exports |

Allowed plan states are `planned`, `in_progress`, `blocked`, `done`, and
`superseded`.

## Phase Results

- P00, P10, and P20 are complete; foundation, canonical chain history,
  enrichment, reorg retention, and lease-fenced publication are documented in
  their child-plan evidence.
- P30-T95–P30-T99 complete pinned Python Vyper verification. PR #56 CI passes
  all nine checks at `4b10534d01d05a9a26876673c3467b767ad885fa`, including native
  AMD64/ARM64 monolith/split production E2E. Subsequent local review fixes have
  separate validation evidence and are not covered by that CI run.
  Its current work items preserve distinct verification, proxy, ABI, Trace,
  EIP-7702, Geas, CWIA, derived-verification, deployment, and runtime evidence in
  [P30 evidence](docs/plans/P30-contract-verification.md#evidence).
- P30-T100 fixes concurrent Compose output capture; race regressions and local
  ARM64 Foundry monolith/split E2E pass. Native AMD64 CI has not been rerun
  with this fix.
- P40 and P50 are complete; native API, compatibility, embedded SPA, wallet,
  browser, and generated-contract evidence remains in their child plans.
- P64, P65, P67, P74, P75, and P76 are complete with their current
  PostgreSQL, browser, runtime, deployment, and common-gate evidence.
- P77 is complete with exact-state holder reconciliation, generated APIs,
  compatibility billing, bilingual Web browsing, PostgreSQL/race, production
  topology, security, license, and common-gate evidence. P70 remains blocked by
  P73-T08 live payment reconciliation and P70-T04 reference-capacity evidence.
  Local or synthetic evidence does not close either external gate.

P30-T107–T113 are complete. [PR #63 CI run 34694346465](https://github.com/islishude/etherview/actions/runs/34694346465)
passes all 11 checks at `dc2d689c0edda6173a77e257889b60e13fa8c9c5`, including
26-version Vyper matrices on native Linux AMD64/ARM64, both Hardhat production
topologies, Foundry, PostgreSQL, browser, security and deployment-surface gates.
Both native Vyper acceptance artifacts confirm monolith/split success with 26
descriptor digests. P30 returns to done; P70/P73 external release blockers remain.

P30-T114 is complete: Node 26.9.0 and solc 0.8.37 pass all 11 checks in
[PR #64 CI run 35206666871](https://github.com/islishude/etherview/actions/runs/35206666871)
(attempt 2) at `5315b89db49071126d26afe802bf8047cdd406fc`, including the
production image boundary and native AMD64/ARM64 verification E2E. P30 returns
to done; P70/P73 external release blockers remain.

P67-T06 is complete: ENS hashing and wire encoding use
`github.com/ensdomains/go-ens/v4 v4.0.0`, with Unicode/boundary regressions
and local Go unit/race/lint, security/license, and docs/plan gates passing.
P67 remains done; P70/P73 external release blockers remain.

P68-T10 is complete: Web linting and formatting now use pinned Oxlint/Oxfmt,
with preserved size limits, an explicit cyclomatic-complexity baseline, and
local unit, generated-contract, embedded-browser, docs, and plan gates passing.
P68 remains done; P70/P73 external release blockers remain.

P68-T11 is complete: PR #86's cold billing-page test awaits asynchronous
React rendering, and the egress rejection assertion accepts Helm 3/4 schema
path formats. Local Web unit/browser, generation, deployment, docs, and plan
gates pass; remote CI has not been rerun with this fix.

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

P68 is done: PR #92 CI at `aebb61fee2783e3f6bb8810a81296b05c142bce6`
clears the production schema/runtime and native amd64/arm64 Hardhat/Foundry
acceptance; the earlier local `make check` also passed. P68-T25 adds local Kubo
Preview and the single-file IPFS tool, with CLI unit/race, lint, security/license,
Compose/docs/plan, and daily startup/recreation/persistence checks passing.
P68-T24's final `make test-preview-metadata` passes its reviewed offline
real-Kubo replacement gate with both exact metadata versions and single attempts.
The [P68 evidence](docs/plans/P68-runtime-architecture-hardening.md#evidence) retains the historical
ipfs.io failure and new local evidence. CI does not run Preview; no new remote
CI or public-IPFS availability is claimed. P70/P73 release blockers remain.

P68-T26 fixes Kubo multipart filename escaping and IPFS URI decoding, with
filename/URI roundtrip and traversal regressions, race, lint, docs and plan
checks passing. Native pgx acceptance is consolidated into the development
guide. Docker was unavailable for this follow-up; prior container evidence
remains revision-specific.

P68-T27 rewrites the native pgx acceptance section around implementation
boundaries, validation coverage and benchmark limits, removing Git and PR
history and routing Preview evidence to P68. Documentation and plan checks
pass; this documentation-only change adds no runtime acceptance claim.


P30-T115 replaces MinIO with pinned RustFS and AWS SDK S3 transport, retaining
PostgreSQL-authoritative cache fallback and using a fresh independent volume.
Local real-RustFS, Go unit/race, lint, security/license, deployment, docs and
plan gates pass; remote CI has not run for this change. P30 remains done and
P70/P73 external release blockers remain unchanged.


P30-T116 fixes S3 credential redirects and isolates complete explicit
credentials/region from unrelated AWS profiles. Local regressions, race,
RustFS, lint, security, docs and plan gates pass; P30 remains done and the
P70/P73 external release blockers are unchanged.

P78 completes private SIWE Watchlists, durable reorg-aware in-app notifications
and bounded snapshot CSV exports under ADR-0051. Local aggregate, PostgreSQL
integration/race, browser, fresh-schema and monolith/split runtime gates pass;
[P78 evidence](docs/plans/P78-watchlist-notifications-csv.md) records the limits.
Remote CI and production acceptance remain unclaimed; P70/P73 external release
blockers are unchanged.

P78-T06 closes the notification review findings with bounded indexed matching,
persisted source/follower progress and disabled-owner historical repair. Local
scale/pagination/replay regressions, full PostgreSQL integration/race, aggregate
checks and fresh-schema/production runtime acceptance pass. P70/P73 external
release blockers remain unchanged.
