# ADR-0001: Modular Roles with PostgreSQL as Correctness Truth

Status: accepted

## Context

Etherview must run as a simple monolith while also allowing expensive and
independently scalable work to run as separate services. Maintaining separate
implementations or correctness protocols for those modes would create drift.

## Decision

Implement one component graph selected by runtime roles. `roles=all` constructs
every component in one process; split deployments construct subsets from the
same packages.

Keep a feature-aware manifest of production component keys next to runtime
assembly. Startup compares that manifest with the exact keys registered by the
component registry, and tests compare the monolith key set with the union of
all split-role key sets. A role component therefore cannot silently bypass the
parity contract.

Run the selected services under one shared supervisor in both deployment
shapes. The supervisor publishes process readiness only after every selected
service has entered its production `Run` path. It withdraws readiness before
canceling service contexts on SIGTERM, another component's failure, or an
unexpected clean exit. It waits for all peers within the single configured
`server.shutdown_timeout`; a component that exceeds the budget is named in a
typed shutdown-timeout error. Operational readiness additionally requires a
live PostgreSQL connection, while API readiness additionally requires the
durable core-index readiness fact.

PostgreSQL is authoritative for chain facts, canonicality, jobs, leases, stage
versions, and transactional outbox state. In-process notifications and optional
NATS wake workers, Redis accelerates cache/rate limiting, and S3 offloads large
objects, but consumers always confirm durable PostgreSQL state and operations
remain idempotent.

Multi-statement canonical and coverage writes take the chain-scoped
transaction advisory lock and use READ COMMITTED. A process that waited for
another role therefore receives a fresh statement snapshot after the preceding
commit; the advisory lock, targeted row locks, and atomic transaction are the
serialization protocol. Snapshot-wide isolation must not turn expected
multi-role contention into serialization failures.

## Consequences

- Monolith and split-role behavior can share the same test fixtures.
- Runtime assembly fails closed when its registered component graph drifts from
  the manifest covered by the parity suite.
- A process cannot continue silently after one of its long-lived components
  exits, and terminating instances leave readiness before draining work.
- A stuck component cannot extend process shutdown indefinitely.
- PostgreSQL is required for every production mode.
- Optional infrastructure outages may reduce throughput but cannot lose work.
- v1 services share a database and schema rather than claiming independent
  database ownership; stronger service isolation would require a future ADR and
  migration plan.
