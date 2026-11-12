# PostgreSQL and storage boundaries

[Architecture overview](overview.md). Accepted [ADRs](../decisions/index.md) own the decisions behind these contracts.

This topic owns persistence authority, database access and object-store boundaries.
See [operator procedures](../operations/trace-cache.md) and
[acceptance requirements](../testing/integration.md) for execution and validation.

- [Persistence authority and reader routing](#persistence-authority-and-reader-routing)
- [Disposable accelerators](#disposable-accelerators)
- [Partition Lifecycle and Identity Boundary](#partition-lifecycle-and-identity-boundary)

## Persistence authority and reader routing

PostgreSQL stores chain identities, canonical mappings, stage state, jobs,
leases, and outbox records. Disposable accelerators never become the only copy
of required state. Optional raw blob bodies are durably owned by the separate
archive, with PostgreSQL-owned manifests and publication fences under ADR-0055.

Every process uses native pgxpool for its mandatory writer pool. An `api` or
`all` process may additionally open a read-only pool for latency-tolerant
projections. Reader startup verifies schema and chain identity; readiness fails
closed if either configured pool is unavailable. Correctness fences remain
writer-authoritative as specified by
[ADR-0018](../decisions/ADR-0018-api-read-replica-routing.md).

[ADR-0050](../decisions/ADR-0050-native-pgx-and-typed-queries.md) owns the native
pgx/sqlc boundary. Repositories use generated parameters and rows, native pgx
transactions, bounded cancellation-independent rollback, and exact decimal
values. Dedicated session locks retain their physical connection until unlock;
uncertain ownership discards that connection. SQL originates in
`internal/db/queries`; only migrations and validated partition DDL execute
handwritten SQL. Large scans use bounded keyset pages within their original
snapshot or chain-lock boundary. P00 owns database acceptance; P30 tracks production-image acceptance.

## Disposable accelerators

Optional accelerator behavior is intentionally asymmetric: NATS carries only
coalesced poll hints, Redis shares rate buckets and caches only the durable
runtime-status model behind an event generation, and S3-compatible storage
caches only exact-generation normalized transaction traces. Every adapter has
a bounded PostgreSQL fallback and is detailed in
[ADR-0015](../decisions/ADR-0015-disposable-runtime-accelerators.md).
S3 uses AWS SDK for Go v2 for object transport, with pinned RustFS in the
optional Compose profile. Its independent data volume contains disposable
cache objects only. S3 uses an explicit static override or the refreshable AWS default credential
chain only inside `all`/`api`; absence of usable credentials cannot withdraw
readiness or turn object storage into a correctness dependency.

## Partition Lifecycle and Identity Boundary

Block-scoped fact tables use fixed half-open ranges of 1,000,000 block
numbers. The partition manager covers `transaction_inclusions`, `receipts`,
`logs`, `withdrawals`, `token_events`, `token_balance_deltas`,
`normalized_traces`, `trace_log_attributions`, `abi_decodings`,
`transaction_effective_execution_identities`, `transaction_state_changes`,
`eip7702_authorizations`, `transaction_execution_code_resolutions`,
`erc4337_user_operations`, `erc4337_user_operation_events`,
`erc4337_user_operation_participants`, `erc20_holder_snapshots`,
`erc20_holder_balances`, `native_transfers`, `native_balance_blocks`,
`native_balance_changes`, `native_balance_observations`, and `address_activities`. Before a
core bundle can write facts in a new range, its chain-locked database transaction takes the global
partition lifecycle lock, rechecks the PostgreSQL catalog, creates every table
in a fixed dependency order, evacuates any matching DEFAULT rows child-first,
and attaches the new partitions parent-first. That catalog recheck uses READ
COMMITTED statement snapshots so a process that waited for another process's
DDL sees the committed relations. The DEFAULT partitions are a recoverable
upgrade buffer, not steady-state storage.

A fully attached range is checked through the catalog without taking fact-table
locks. When DDL is needed, the manager acquires the full table-lock set with
`NOWAIT` inside a savepoint. Contention rolls back that attempt's partial locks
before retrying for at most ten seconds (or the caller's shorter deadline), so
ordinary writers with a different table order can finish. The catalog is checked
again under the acquired locks before any data movement or DDL.

Automatic recovery is atomic: a failed copy, delete, or attach rolls back both
DDL and data movement. A partially hand-managed range whose existing child
partitions make automatic foreign-key movement ambiguous returns a typed
partition-recovery error naming the table and range. Operators then stop
writers, preserve and repair rows in dependency order, and retry the same
idempotent operation.

`blocks` deliberately remains unpartitioned by `number`. Its durable identity
requires both `(chain_id, number, hash)` for block-scoped foreign keys and a
global unique `(chain_id, hash)` lookup. PostgreSQL unique and primary-key
constraints on a range-partitioned table must include the partition key, so a
`number` partition cannot enforce the hash-only identity required by canonical
and orphan lookups. `transactions` is also unpartitioned: `(chain_id, hash)` is
the transaction identity, while block number belongs to a potentially
non-canonical `transaction_inclusions` row and one transaction hash may be
retained across inclusions.

A future split may keep small, globally unique block/transaction identity
directories and partition separate block-scoped payload tables. Moving the
identity tables themselves requires a PostgreSQL/global-index design that can
still enforce the current hash uniqueness and foreign-key contracts; storage
size alone is not sufficient justification to weaken those invariants.
