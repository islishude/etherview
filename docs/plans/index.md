# Plan Catalog

The root [implementation plan](../../PLAN.md) owns current status and release
dependencies. This catalog groups the child plans by responsibility.

## Core chain and data

- [P00 — Foundation](P00-foundation.md)
- [P10 — Indexing](P10-indexing.md)
- [P20 — Enrichment](P20-enrichment.md)

## Contract platform and runtime

- [P30 — Contract Platform & Runtime Operations](P30-contract-verification.md)
  is the canonical plan for contract verification, contract intelligence, and
  shared runtime/operations responsibilities.

## Public API and Web capabilities

- [P40 — API](P40-api.md)
- [P50 — Embedded Web](P50-web.md)
- [P64 — NFT Metadata Web](P64-nft-metadata-web.md)
- [P67 — ENS Primary Names](P67-ens-primary-names.md)
- [P74 — Etherscan V2 Read Expansion](P74-etherscan-v2-read-expansion.md)
- [P76 — ERC-4337 UserOperation Browsing](P76-erc4337-user-operations.md)
- [P77 — Authoritative ERC-20 Token Holders](P77-authoritative-erc20-token-holders.md)

- [P78 — Watchlist, Notifications and CSV](P78-watchlist-notifications-csv.md)

## Identity and billing

- [P65 — User Authentication](P65-user-auth.md)
- [P73 — Prepaid API Billing and x402 Top-ups](P73-prepaid-api-billing.md)

## Hardening and release

- [P68 — Runtime and Architecture Hardening](P68-runtime-architecture-hardening.md)
- [P75 — Runtime and Performance Hardening](P75-runtime-performance-hardening.md)
- [P70 — Release](P70-release.md)

## Planning and retention

Follow the [planning threshold](../development.md#planning-threshold). Plans
track substantial capabilities, architecture migrations and unresolved release
gates. Completed plans are compact milestone summaries, not maintenance logs.
Remove obsolete or routine items after resolving incoming dependencies, code
TODOs and documentation links. Git retains detailed history; do not create an
archive copy of the deleted ledger. Preserve unresolved blockers and acceptance
limits. Historical review/ADR identifiers are provenance, not active work.

P55 (proxy interaction) was absorbed by P20/P30/P40/P50. P66 (accountless
request billing) was replaced by P73/ADR-0044. Neither has active work.

## Identifier allocation

Never renumber or reuse an allocated ID, including removed items. Allocate a
new item only when the planning threshold is met, above both the watermark
below and every item currently in its plan. Advance the watermark in the same
change. New plan IDs must exceed P78; gaps and retired prefixes stay reserved.

| Plan | Highest allocated task number |
|---|---|
| P00 | 14 |
| P10 | 7 |
| P20 | 16 |
| P30 | 136 |
| P40 | 19 |
| P50 | 71 |
| P64 | 7 |
| P65 | 9 |
| P67 | 6 |
| P68 | 31 |
| P70 | 52 |
| P73 | 10 |
| P74 | 5 |
| P75 | 12 |
| P76 | 2 |
| P77 | 3 |
| P78 | 6 |
| P55 (retired) | 4 |
| P66 (retired) | 8 |
