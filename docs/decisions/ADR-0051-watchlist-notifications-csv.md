# ADR-0051: Private Watchlists, Durable Notifications and Bounded CSV

Status: accepted

## Decision

SIWE sessions on the PostgreSQL writer own all watch, notification and export
operations. API keys do not authorize these operations; writes require the
existing exact Origin and CSRF checks. No additional external service is needed.

Each user can watch 100 distinct addresses with a private label of up to 64
characters, transaction/ERC-20/ERC-721/ERC-1155 selectors and in/out/both direction.
Enrollment and notification re-enablement record the exact current canonical
tip under the chain lock; only later block numbers qualify. Deleting a watch
retains its notifications. Failed top-level transactions and mint/burn transfers
are included; self-transfers occur once. Internal calls and UserOperations are
outside this feature.

Core canonical/outbox changes and Token publication/replay changes atomically
invalidate block-scoped notification work in PostgreSQL. Independent durable
work generations, leases and keyset progress protect the maintenance consumer;
the bounded public runtime-event ledger is never its correctness source.
Delivery scans at most 100 source events per page, probes bounded indexed
address/watch-ID ranges, and commits at most 200 progress rows. Ineligible
followers still advance the cursor; a source-end marker prevents skipping
followers of popular addresses. Existing notification generations are repaired
even while their owner is disabled; only new delivery requires an active owner.
Publication requires a current canonical block and the exact published Token
generation. Reads recheck these witnesses so reorgs and invalidation are visible
before background reconciliation. Reattachment updates existing notifications
without resetting read state. Notifications expire after 90 days; work and
cleanup use bounded batches. Long-delayed activity older than that retention
window is not newly delivered. Public events contain no private user data.

The SPA retains one public EventSource. Authenticated visible clients refresh
private notification state every 15 seconds and on focus. Identity changes
cancel requests and discard private state. Read-all is bounded by the response's
notification ID watermark, preserving concurrent arrivals.

CSV is a documented exception to ADR-0003's successful JSON envelope: the
spec-first authenticated POST returns text/csv on success and the ordinary JSON
error envelope on failure. It accepts one address, one activity kind, UTC
[from,to) and direction. A writer repeatable-read snapshot pins the exact
canonical tip, checks continuous Core coverage and applicable Token publication,
and reads at most 10,001 records. The exported view is explicitly as of that tip.
No RPC or HTTP writes occur while the snapshot is open. Files use UTF-8, fixed
English headers, exact decimal integers, standard quoting and formula protection.

Limits are 31 days, 10,000 data rows, 16 MiB and 15 seconds of generation.
PostgreSQL admission allows five starts per rolling minute per account, one
active export per account and four per deployment. Expiring reservations recover
crashes; completion releases only its own token. Every error is returned before
CSV headers or bytes; partial files are never reported as successful. The SPA
uses its generated same-origin client and releases download object URLs.

## Consequences

New tables target the current fresh schema without historical backfills.
Maintenance and API components use the same builders in all/split deployments.
Watches, notifications and admission state remain writer-owned; optional Redis,
NATS and object storage do not participate. Limits do not charge prepaid API
balances. P78 owns implementation evidence and P70 owns release acceptance.
