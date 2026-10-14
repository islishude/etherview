# P78 — Watchlist, Notifications and CSV Exports

Status: `done`

## Outcome

SIWE-owned private address watches, durable in-app transaction and token-transfer
notifications, and bounded snapshot-consistent address CSV exports.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0051](../decisions/ADR-0051-watchlist-notifications-csv.md)
- [ADR-0020](../decisions/ADR-0020-siwe-user-sessions.md)
- [ADR-0012](../decisions/ADR-0012-lease-fenced-derived-publication.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P78-T01 | done | P10, P20, P40, P50, P65 | ADR, OpenAPI, fresh schema and typed queries | generation, schema, plan checks |
| P78-T02 | done | P78-T01 | Writer-authoritative watch management and isolation | handler and PostgreSQL regressions |
| P78-T03 | done | P78-T02 | Durable notification processing, reorgs and cleanup | restart, generation, lease and concurrency regressions |
| P78-T04 | done | P78-T02 | Bounded authenticated CSV snapshots and admission | export correctness and concurrency regressions |
| P78-T05 | done | P78-T03, P78-T04 | Bilingual Web, production parity, documentation and acceptance | browser, integration/race, schema/runtime and aggregate gates |

## Acceptance

- [x] Watches are private, limited to 100 per user and start after enrollment.
- [x] Transactions and published ERC-20/721/1155 transfers produce durable,
      deduplicated notifications; replay and reorg state remains accurate.
- [x] CSV exports require SIWE and CSRF, preserve exact values and never return
      a successful truncated or incomplete file.
- [x] Account switching clears private browser data; one public EventSource remains.
- [x] Monolith and split maintenance/API roles have identical persistence and behavior.

## Current Blockers

None. P70/P73 retain independent release blockers.

## Evidence

Retained completed milestones: P78-T01, P78-T02, P78-T03, P78-T04, P78-T05.

Local validation on 2026-09-26 passed `make check`, integration/race,
embedded browser, schema and production runtime gates. Tests covered private
ownership, durable/reorg-aware delivery, restart, lease fencing, session cache
isolation, exact repeatable-read CSV, concurrent admission, 10,000-row and
16 MiB limits, and JSON-only failure responses. Production-image tests covered
real SIWE, notifications and export in both layouts.

Later indexed matching and disabled-account replay regressions passed with
40,000 unrelated watches, 2,000 transactions and a paginated follower fixture.
That synthetic local timing is not reference-capacity evidence. Remote CI and
production deployment were not claimed in this feature's completion record.

[Detailed completion record](https://github.com/islishude/etherview/blob/e665a0b9b5dcee02e6d90d3a2224a76031cbda3d/docs/plans/P78-watchlist-notifications-csv.md#evidence).
These are historical results, not tests rerun by this plan cleanup.
