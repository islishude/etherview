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

## ENS synchronization delivery

The current-chain rewrite replaces the retired ENS milestones. P20-T27 owns
core fact references and recovery; P20-T28 owns protocol discovery; P20-T29
owns independent resolution validity and publication. P30-T149 moves ENS into
enrich; P40-T27 and P50-T81 adapt database reads and display. P30-T150 owns local
production acceptance and P30-T151 independently verifies representative Mainnet
publications. P20-T27/P20-T28/P20-T29, P30-T149/P30-T150/P30-T151, P40-T27 and P50-T81 are done
with [new acceptance evidence](../reviews/ens-current-chain-acceptance.md).
Historical evidence does not close these new gates. Full directory coverage is
a deployment progress fact, not a prerequisite for exact-result publication.

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
| P30           | 152                           |
| P40           | 32                            |
| P50           | 85                            |
| P60           | 30                            |
| P70           | 52                            |
| P55 (retired) | 4                             |
| P66 (retired) | 8                             |

Name registration browsing extends ADR-0058 through P20-T30 → P40-T28 → P50-T82.
These milestones retain independent profile/resolution validity, canonical known-name
search and database-only reads. Existing runtime scenarios are extended; earlier
completed IDs and Mainnet evidence are not reopened or reused as new acceptance.

ENS operation history and the compact name page are complete under P20-T31 → P40-T29 → P50-T83.
