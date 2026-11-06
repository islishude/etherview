# Plan Catalog

The root [implementation plan](../../PLAN.md) owns current status and release
dependencies. This catalog groups the child plans by responsibility.

## Core chain and data

- [P00 — Foundation](P00-foundation.md)
- [P10 — Indexing](P10-indexing.md)
- [P20 — Enrichment & Durable Archives](P20-enrichment.md)

## Contract platform and runtime

- [P30 — Contract Platform & Runtime Operations](P30-contract-verification.md)
  is the canonical plan for contract verification, contract intelligence, and
  shared runtime/operations responsibilities.

## Public API and Web capabilities

- [P40 — API & Data Exports](P40-api.md)
- [P50 — Embedded Web](P50-web.md)

## User accounts and billing

- [P60 — User Accounts, Billing & Personal Tools](P60-user-auth-billing.md)

## Release

- [P70 — Release](P70-release.md)

## Consolidated capability ownership

ENS discovery, resolution, registration profiles and history belong to P20-T27;
P40-T27 owns database-only APIs and P50-T81 owns browsing. P30-T150 retains local
production acceptance and P30-T151 retains independent representative Mainnet
reconciliation. Those evidence boundaries remain separate.

Completed follow-up items are folded into their existing capability milestone:

| Owning milestone                                    | Folded identifiers                                                                  |
| --------------------------------------------------- | ----------------------------------------------------------------------------------- |
| P20-T01 / P20-T13 / P20-T20 / P20-T27               | T10 / T03 / T32 / T28–T31 respectively                                              |
| P30-T15 / P30-T138 / P30-T141 / P30-T144 / P30-T150 | T16 / T142 / T140 / T143 and T145 / T149 respectively                               |
| P40-T11 / P40-T21 / P40-T22 / P40-T27               | T30 / T31 (NFT/statistics scope in T03) / T32 / T28–T29 respectively                |
| P50-T01 / P50-T11 / P50-T75 / P50-T81               | T76 / T84 / T85 / T82–T83 respectively                                              |
| P60-T13 / P60-T25 / P60-T26                         | T22–T23 (whole-token input in T14) / T24 / T30 respectively                         |
| P60-T20                                             | T19's durable notification design is removed; current delivery is volatile Web Push |

These are ownership redirects, not new acceptance claims. Historical review
identifiers remain provenance. Superseded implementations and routine change
logs live in Git, not additional archive plans.

## Planning and retention

Follow the [planning threshold](../development.md#planning-threshold). Plans
track substantial capabilities, architecture migrations and unresolved release
gates. Completed plans are compact milestone summaries, not maintenance logs.
Remove obsolete or routine items after resolving incoming dependencies, code
TODOs and documentation links. Git retains detailed history; do not create an
archive copy of the deleted ledger. Preserve unresolved blockers and acceptance
limits. Historical review/ADR identifiers are provenance, not active work.

P55 (proxy interaction) was absorbed by P20/P30/P40/P50. P66 (accountless
request billing) was replaced by P60/ADR-0044. Neither has active work.

## Identifier allocation

Never renumber or reuse an allocated ID, including removed items. Allocate a
new item only when the planning threshold is met, above both the watermark
below and every item currently in its plan. Advance the watermark in the same
change. New plan IDs start at P80; all lower gaps and retired prefixes stay reserved.

| Plan          | Highest allocated task number |
| ------------- | ----------------------------- |
| P00           | 14                            |
| P10           | 7                             |
| P20           | 32                            |
| P30           | 155                           |
| P40           | 35                            |
| P50           | 89                            |
| P60           | 30                            |
| P70           | 52                            |
| P55 (retired) | 4                             |
| P66 (retired) | 8                             |
