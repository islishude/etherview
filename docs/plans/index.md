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

[ADR-0058](../decisions/ADR-0058-background-ens-synchronization-and-database-only-reads.md)
spans existing owners: P20-T23 through P20-T26 provide persistence, v1/custom
and v2 discovery, and background publication; P30-T146 provides the role;
P40-T26 and P50-T80 provide database reads and display; P30-T147 and P30-T148
separate local production acceptance from live Mainnet evidence. P70 consumes
those outcomes through its existing release gates. P20-T23 through P20-T26,
P30-T146/P30-T147, P40-T26 and P50-T80 are `done`.
P30-T148 remains `in_progress`: real Mainnet activation, bounded collection and
independent samples passed; complete history and database publication
reconciliation remain open in the [live report](../reviews/P30-ens-mainnet-acceptance.md).

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
| P20           | 26                            |
| P30           | 148                           |
| P40           | 26                            |
| P50           | 80                            |
| P60           | 21                            |
| P70           | 52                            |
| P55 (retired) | 4                             |
| P66 (retired) | 8                             |
