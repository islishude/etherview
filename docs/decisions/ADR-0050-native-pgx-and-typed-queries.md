# ADR-0050: Native pgx and Typed Queries

Status: accepted

## Context

Mixing database/sql pools with native pgx generated queries requires connection
bridges, duplicate transaction paths, and manual scans outside the generated
contract. Generating database/sql instead retains those conversion costs and
adds separate array and nullable-value conventions.

## Decision

Use pgx/v5 end to end: pgxpool writer/reader pools, pgx transactions, and sqlc
pgx/v5 output. Do not expose generated SQL constants or execute them manually.
Production SQL originates in internal/db/queries; only the migration executor
and validated partition DDL boundary may execute handwritten SQL. Generated
parameters and rows are the persistence contract. Consumer interfaces may
expose native query/transaction capabilities for tests, without a second driver
or compatibility implementation.

Preserve ADR-0018 writer authority and reader routing, session parameters,
startup identity/schema checks, isolation levels, lock order, lease fences,
and ambiguous-commit confirmation. Cancellation never implies that a native
transaction was automatically rolled back: bounded detached cleanup releases
it explicitly. Never retry a commit whose outcome is unknown.

Session advisory locks hold the exact acquired pgxpool connection. An uncertain
lock acquisition or failed unlock removes the physical connection from the
pool and closes it with a bounded cleanup context. Pool release alone is not
lock cleanup. Pool Close waits for borrowed connections, so runtime teardown
starts all pool closures together and uses the supervisor's remaining shutdown
deadline. A stuck borrower must not extend process exit indefinitely. External
calls do not hold database snapshots.

Use native pgx null, UUID, JSONB and array encodings. NUMERIC and amounts retain
exact decimal representations and domain range validation, never float
conversion. Specify query argument names, casts, result aliases and cardinality
in SQL rather than adding untyped scan or conversion adapters. Nullable numeric
projections retain the native NUMERIC codec; nullable expressions whose sqlc
inference loses presence return an explicit value/presence pair. Real text
columns used through lateral joins retain pgtype.Text via column overrides.
Large scans use bounded keyset pages under the same snapshot or existing chain lock.

The existing min_connections setting now means pgxpool.MinConns, rather than
the old maximum-idle setting. Read bounds retain their documented inheritance.
Pool metrics report native acquisition and lifecycle statistics with truthful
names; unsupported database/sql statistics are not synthesized.

## Consequences

All repositories, tools and tests share one native database stack. Unit tests
exercise native consumer contracts; PostgreSQL integration verifies codecs,
SQL semantics, locks, rollback and commit behavior. This changes no database
schema, and requires no compatibility layer, backfill or startup data rewrite.
