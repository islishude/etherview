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
| P78-T06 | done | P78-T03, P78-T05 | Fix bounded indexed notification matching and disabled-account replay repair | PostgreSQL scale, pagination and disabled-account replay regressions; applicable gates |

## Acceptance

- [x] Watches are private, limited to 100 per user and start after enrollment.
- [x] Transactions and published ERC-20/721/1155 transfers produce durable,
      deduplicated notifications; replay and reorg state remains accurate.
- [x] CSV exports require SIWE and CSRF, preserve exact values and never return
      a successful truncated or incomplete file.
- [x] Account switching clears private browser data; one public EventSource remains.
- [x] Monolith and split maintenance/API roles have identical persistence and behavior.

## Current Blockers

None identified for implementation. P70/P73 external release gates remain separate.

## Evidence

Local validation completed on 2026-09-26. Implementation started from a clean
working tree. Remote CI and production acceptance are not claimed.

- P78-T01: accepted ADR-0051, OpenAPI/Go/TypeScript and sqlc generation,
  fresh migration 0068, `make generate-check`, `make source-check`,
  `make docs-check` and `make plan-check` pass.
- P78-T02: focused HTTP Cookie/Origin/CSRF and account-isolation regressions
  pass; PostgreSQL tests cover concurrent enrollment at the 100-watch boundary,
  duplicate creation, cross-owner mutations and pause/re-enable tip capture.
- P78-T03: PostgreSQL core/reorg and exact-generation Token tests cover failed
  transactions, ERC-20 mint, ERC-721 self-transfer, ERC-1155 burn/batch, delayed
  publication and replay, retained orphan history and reattachment, concurrent
  consumers, expired leases, new service instances after public-event deletion,
  and read-all watermarks preserving later arrivals. Cleanup uses bounded SQL
  batches and reads exclude records beyond 90 days.
- P78-T04: exports preserve uint256 values, UTC/fractional boundaries and empty
  results. Regressions cover 10,000/10,001 rows, the independent 16 MiB encoder
  cap, expired contexts and the 15-second database deadline, CSV quoting/formula
  protection, Core coverage gaps and pending Token publication. A real reorg
  committed after repeatable-read snapshot establishment cannot mix the file's
  chain identities; the next export observes the replacement. Concurrent
  PostgreSQL admissions enforce deployment slots, expiring reservations and
  rolling account limits. All failure paths return no successful partial file.
- P78-T05: `make check`, `make test-integration`, `make test-integration-race`,
  `make test-e2e`, `make test-schema-e2e` and `make test-runtime-e2e` pass locally.
  The aggregate gate includes all 41 Vitest files / 376 tests; the browser gate
  passes all 30 tests, including bilingual 390px accessibility and Blob download.
  Production-image tests exercise genuine SIWE enrollment, durable delivery,
  reorg history, API restart and CSV download in both monolith and split layouts.
  Identity-switch tests prove cancellation and private-cache removal; the public
  EventSource remains singular.
- Final CSV resource/snapshot additions were rechecked with `make check`, focused
  PostgreSQL Watchlist tests both normally and under race, and the production
  runtime gate. Final type-label localization was rechecked with generation,
  Web lint and the complete browser gate. The SQL row-count fixture is synthetic
  normalized data for export bounds, not independent ingestion proof.

P78 is a P70 release dependency. P70-T04 reference-capacity evidence and P73-T08
live payment reconciliation remain independent external blockers; local fixture
and production-image tests do not close either gate.

- P78-T06 review repair: notification progress is now ordered by source and watch,
  with a 100-source window, indexed 201-candidate probes and a 200-row commit
  bound. Ineligible candidates and explicit source-end markers preserve forward
  progress. A block/source/watch index supports bounded historical repair, which
  no longer depends on an active owner. New delivery still requires one.
- P78-T06 regressions: 40,000 unrelated watches and 2,000 transactions finish all
  21 pages in approximately 0.57 seconds locally, replacing the reproduced
  ten-second first-page timeout. A 700-follower fixture crosses 450 ineligible
  followers, resumes a fresh consumer and delivers exactly 250 notifications;
  replay preserves their identity/read state. Token replay while an account is
  disabled, task cleanup and account reactivation restore valid old notifications.
  This synthetic local timing is a regression result, not P70 capacity evidence.
- P78-T06 validation: focused PostgreSQL regressions pass normally and under
  race; `make check`, `make test-schema-e2e`, `make test-runtime-e2e` and the full
  `go run ./cmd/testintegration -root .` suite pass. The corresponding full
  `-race` suite and final documentation/plan checks also pass locally.
