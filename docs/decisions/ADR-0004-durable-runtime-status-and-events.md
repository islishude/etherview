# ADR-0004: Durable Runtime Status and Event Replay

Status: accepted

## Context

The sync and API roles may run in different processes and may each have more
than one replica. An in-process latest-head tracker or SSE replay buffer would
therefore make API status depend on process placement, lose reconnect history
on restart, and allow an API-only replica to mistake the indexed tip for the
upstream execution head.

Head and reorganization notifications also describe canonical transitions. An
event that can be observed without its corresponding canonical transaction, or
a canonical transition without a reconnectable event, creates an inconsistent
public view.

## Decision

- PostgreSQL stores the latest authoritative sync observation and a bounded,
  monotonically identified runtime event ledger per chain.
- A canonical block commit appends its `head` event in the same transaction.
  A reorganization appends one compact `reorg` event in the transaction that
  changes canonical mappings and journals.
- Each polling cycle atomically replaces the sync status snapshot and appends a
  `status` event. Only stable, bounded error codes are persisted; RPC and
  database error details are not event payloads.
- Every API replica independently tails the ledger into an in-process fanout.
  The fanout is a latency mechanism only: it does not claim or delete rows and
  is not a correctness source.
- A configured query-cache invalidator runs idempotently for each durable event
  before the replica advances its private cursor or publishes that event. An
  invalidation failure leaves the cursor unchanged and is retried; clients are
  never told to refresh against a cache that still predates the event. An
  optional Redis implementation must disable or bypass its cache on backend
  loss before reporting successful invalidation, so Redis remains an
  acceleration rather than an availability dependency.
- `Last-Event-ID` is a decimal durable event ID. Replays use one repeatable-read
  snapshot and reject a cursor older than the retained window or ahead of the
  stream. New subscribers receive the most recent bounded window.
- The default retained/replayed window is 256 events and the implementation
  rejects configurations above 4096. Status writes prune older rows.
- WebSocket new-head subscriptions only wake the authoritative polling path;
  they never write runtime status or public events directly.
- Native and compatibility API responses use `Cache-Control: no-store` for
  browsers and unmanaged intermediaries; an explicitly configured server-side
  cache remains behind the event invalidator. The SSE stream itself uses
  `no-cache, no-transform` and reconnects by durable ID.

## Consequences

- Monolith and split API/sync roles expose the same latest/indexed/readiness
  state and reconnect semantics after process restart.
- Event delivery remains at-least-observable under duplicate wakes and relay
  polling; subscriber cursors suppress duplicate delivery.
- A client that was disconnected beyond retention must refresh REST state and
  reconnect without its expired cursor.
- PostgreSQL load includes a small status/event write per sync cycle. Optional
  NATS may later reduce wake latency, but cannot replace the ledger.
- Changing payload identity, cursor interpretation, or retention ownership
  requires an ADR and compatible API/migration plan.
