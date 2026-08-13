# ADR-0018: API Read Replica Routing with Writer Authority

Status: accepted

## Context

Every Etherview role requires PostgreSQL, but public explorer queries can place
substantial read load on the same endpoint that serializes canonical chain,
job, authentication, verification, and observation writes. Operators need an
optional PostgreSQL reader endpoint without making asynchronous replica state
an authority for reorg decisions or write-after-read workflows.

A mechanical split based only on whether a method executes `SELECT` is unsafe.
Some reads fence an external RPC call, validate canonicality after untrusted
network work, consume a row just written in the same request, or select the
exact target of a durable write. Replica lag at those boundaries can accept an
orphan target, reject a successful write as absent, or serve a stale external
result.

## Decision

Every process keeps the mandatory writer pool. Only a process containing the
`api` role may create the optional reader pool. `roles=all` therefore creates
both pools, while split sync, enrichment, trace, verification, metadata, and
maintenance processes never depend on the reader endpoint.

The reader is enabled when `database.read_url`,
`database.read_max_connections`, or `database.read_min_connections` is set. An
empty reader URL inherits the writer URL, permitting a separately bounded
read-only pool without a distinct server. Zero reader connection bounds inherit
their writer counterparts; negative values and an effective minimum greater
than the effective maximum are invalid.

Reader sessions force `default_transaction_read_only=on` and use a distinct
application name. Startup pings the endpoint, verifies the exact migration
ledger, and requires the configured chain/genesis identity to match the writer.
API operational readiness checks both pools, and public readiness bypasses the
optional Redis status cache so a cached success cannot hide pool loss. Once a
reader is configured, reader failure is fail-closed: Etherview does not
silently shift its configured read load to the writer.

The reader serves latency-tolerant PostgreSQL projections:

- ordinary native explorer queries;
- catalog/token/NFT/trace projection reads;
- ordinary Etherscan compatibility queries.

The writer remains authoritative for:

- schema migration, chain binding, canonical state tips, and post-RPC
  canonicality checks;
- sync status/events, operational metric snapshots, cache invalidation
  ordering, and API readiness comparison;
- API-key authentication and all verification job/artifact reads and writes;
- verification target resolution and Etherscan verification submission;
- external name/price observation persistence; when name resolution is
  configured, search is writer-routed so a request can consume its own
  observation;
- mempool observation reads/writes, the unified transaction detail lookup while
  mempool collection is enabled, and NFT media selection/revalidation; and
- every worker, maintenance, admin, repair, and migration operation.

The public readiness query compares the reader's durable core coverage with the
writer-backed runtime status, so ordinary replay lag withdraws API readiness.
This is not a general PostgreSQL replication-lag protocol: operators must expose
a reader service with bounded, monotonic replay suitable for cursor pagination.
Same-height replay lag can still make an ordinary historical projection briefly
stale, while the writer-routed canonical, verification, authentication, and
external-call fences preserve correctness-sensitive decisions.

## Consequences

- Read traffic can scale independently without changing any write transaction
  or durable ownership protocol.
- A read-replica outage affects API readiness but cannot stop split writer and
  worker roles.
- Enabling a reader adds one pool only for each `api` or `all` Pod. Capacity
  planning counts writer pools for every Pod and reader pools only for those
  API-bearing Pods.
- Async replication lag is observable as unready or stale read-model behavior;
  there is no automatic primary fallback or claim of read-after-write
  consistency for ordinary projections.
- Secret rotation still requires a Pod rollout because database URLs are read
  from environment variables at process start.
