# PostgreSQL and object-store integration

[Testing index](../testing.md) · Commands run from the repository root.

- [Commands](#commands)
- [Runner timeouts](#runner-timeouts)

## Commands

- `make test-integration`: build the embedded SPA, then run real migrations and
  every integration-tagged Go test. When `INTEGRATION_DATABASE_URL` is empty,
  the Go runner owns a fresh PostgreSQL 18 Compose project and removes its
  volume afterward. Supplying the variable uses that explicitly disposable
  external database instead. The owned database sets
  `max_locks_per_transaction=1024` for concurrent full-schema creation and
  teardown; caller-supplied databases need equivalent lock-table capacity.
  Package discovery excludes `.local` scratch copies alongside dependency and
  build directories. Local runs default to the complete suite. CI partitions
  the discovered top-level tests, examples and fuzz seeds by a stable name hash
  into four independent jobs, each with its own owned PostgreSQL project.
  Subtests stay with their parent; every test name belongs to exactly one shard,
  including names shared by multiple packages. This avoids unsafe in-process
  parallelism in fixtures with process-global state. Reproduce a shard with
  `make test-integration INTEGRATION_SHARD=1 INTEGRATION_SHARDS=4`; race mode
  accepts the same settings. Sharding rejects focused-run/benchmark combinations
  and empty selections. Verbose shard logs retain individual test durations.
  Each package keeps its 15-minute Go timeout and each CI shard its independent
  20-minute bound. The stable `Checks / PostgreSQL integration` check requires
  every shard to succeed; RustFS runs once in shard 1. Failed shards do not
  cancel the remaining shards.
  The ERC-4337 regression commits exact stored EntryPoint calldata and logs,
  drives the durable `userop@1` outbox/worker path, verifies all native reads,
  search and completeness, then replaces the block and requires canonical
  withdrawal with retained orphan evidence.
  The authoritative ERC-20 Holder regression publishes `proxy@2`, `token@2`,
  and `holder@1` through the durable worker path, performs exact EIP-1898
  supply/balance calls, verifies native and Etherscan list/count parity, rejects
  direct balance mutation, and requires a reorged snapshot to become
  unavailable while retaining orphan evidence.
- `make test-integration-race`: run the same owned database lifecycle with the
  Go race detector. This expensive variant is explicit and is not part of
  default CI.
- `make test-rustfs`: start the production RustFS service in an isolated Compose
  project using fresh credentials, a fresh volume, and a random loopback S3
  port. Verify bucket creation, nonempty/empty object reads and writes, SHA-256
  metadata and corruption rejection, missing objects, persistence after forced
  recreation, and bounded failures after stopping the store. Only this test's
  resources are removed. It requires Docker and runs in the CI integration job;
  it is explicit rather than part of ordinary unit/race tests. Local Docker
  results do not establish native cross-architecture or remote-CI acceptance.

## Runner timeouts

`e2e/cmd/testintegration` owns the PostgreSQL integration invocation. Its
15-minute timeout applies independently to each Go package and remains below
the CI job's 20-minute outer bound, leaving time for dependency setup,
migrations, and teardown. If the explicit 15-minute limit is reached, inspect
the active test and goroutine dump for a hang or an actual duration regression;
do not automatically raise either timeout. Focused and race modes must continue
to use the same package bound.

## Database-wide deadlock gate

The managed runner checks the entire disposable database, across all test
schemas and packages. Ordinary, focused, sharded, race and benchmark runs all
require zero new PostgreSQL-detected deadlocks, even if application retries
succeed. Synthetic SQLSTATE injection in retry tests is not a detected lock
cycle. Detector self-tests own a separate Compose database; ordinary tests have
no deadlock allowance.

The observer records database identity, server start, statistics reset epoch
and deadlock count before migrations. After the test process exits it waits up
to 15 seconds for business sessions to disappear, then observes a five-second
statistics settling window without cached snapshots. A changed epoch, missing
statistics, invisible sessions or incomplete shutdown fails the run. External
`INTEGRATION_DATABASE_URL` databases must be exclusive, disposable and permit
full session observation; the runner never kills their sessions or resets stats.

A bounded 250 ms sampler retains the latest 128 lock-wait snapshots, including
backend identities and blocking edges, without query text or parameters.
`internal/testdbguard` also provides a concurrent start barrier and exact
backend blocking assertions. Business tests cover publication/replay/renewal,
same/reverse/overlapping replay streams, lease takeover, ENS invalidation,
token observation conflicts, verification
admission and prepaid credit/debit contention. Tests assert domain invariants
as well as completion; normal lock waits are allowed.

Failures retain `etherview-integration-evidence-*` under the host temporary
directory: `tests.log`, `deadlocks.json` and, for owned Compose databases,
`postgres.log`. CI uploads each failed shard's evidence. External database
server logs remain operator-owned and are not collected. Query sampling gaps
are counted; final statistics and quiescence are mandatory. Diagnostics are
collected before Compose teardown. These checks cover executed interleavings,
not a proof that all possible database schedules are deadlock-free.

Run the detector, runner failure-propagation and cross-domain regressions with:

```sh
go run ./e2e/cmd/testintegration -race \
  -run 'Test(DatabaseDeadlockGuardPostgres|ManagedRunner|PublicationReplayAndRenewConcurrent|ConcurrentReplayStreamsPreserveGenerations|PrepaidConcurrentCreditAndUsageCommit|VerificationConcurrentIdenticalAdmission)'
```
