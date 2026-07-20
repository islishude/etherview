# ADR-0015: Disposable Runtime Accelerators

Status: accepted

## Context

PostgreSQL already owns canonical chain state, runtime events, durable jobs,
leases, exact stage generations, and transactional outbox delivery. Distributed
deployments benefit from lower wake latency, a shared rate bucket, bounded query
caching, and cheaper delivery of large derived responses, but making NATS,
Redis, or object storage part of any commit protocol would create a second
correctness implementation and break the PostgreSQL-only deployment.

## Decision

- NATS carries only the versioned `runtime`, `outbox`, and `jobs` wake subjects
  scoped by deployment namespace and chain ID. Messages contain no identity or
  result that a consumer trusts. Every consumer performs the same PostgreSQL
  poll/claim after a wake, retains its periodic poll, coalesces duplicates, and
  tolerates missed or malformed notifications. Broker connection and publish
  failures are redacted, self-healing latency loss and do not withdraw
  readiness.
- Redis runs one atomic, identity-hashed token bucket shared by API replicas.
  A bounded Redis failure uses the existing process-local token bucket, so the
  degraded quota is per replica but the native and compatibility error
  contracts do not change.
- The only Redis response cache is the bounded runtime-status model. A process
  fences a new cache generation before enabling reads. Each durable runtime
  event idempotently advances that generation before relay cursor advancement
  or SSE fanout. Cache writes use the generation observed before reading
  PostgreSQL, making a write that races invalidation unreachable. Redis loss
  disables cache reads and writes before invalidation reports success; a later
  durable event may safely re-enable them.
- S3-compatible storage caches normalized transaction-trace JSON only. An
  object key binds chain ID, exact block hash, durable job ID, exact completed
  generation, and transaction hash. Reads are length- and SHA-256-bounded,
  decoded through the same trace shape limits, and followed by a PostgreSQL
  canonical/publication identity check. Misses, corruption, timeouts, reorgs,
  and replay generations fall back to normalized PostgreSQL rows. Cache writes
  occur only after the read transaction commits and never affect job or stage
  completion.
- Accelerator clients are not constructed when their URL/endpoint is empty.
  Static configuration errors fail validation, while endpoint reachability is
  not a startup or `doctor` requirement. Credential-bearing URLs and S3 keys
  use environment/secret inputs and raw backend errors are never logged.
- Object storage does not hold raw RPC traces, durable job/outbox payloads,
  verification inputs or results, metadata documents, or NFT media. In
  particular it does not alter ADR-0005's no-permanent-media-mirror boundary.

## Consequences

- A PostgreSQL-only monolith retains every enabled semantic and performs no
  accelerator network calls.
- Monolith and split roles execute the same durable store, queue, publication,
  and API paths. Healthy accelerators only reduce latency, coordinate rate
  budgets, or avoid repeated large trace-row assembly.
- Optional-service outages can increase PostgreSQL load and make rate limits
  replica-local, but cannot lose work, publish stale stage generations, or
  make an otherwise healthy process unready.
- Adding another cached API model requires proof that every mutation source is
  covered by an idempotent durable invalidation generation. Adding an
  accelerator as the only copy of data or as a lease/completion witness
  requires a replacement ADR and compatible persistence protocol.
