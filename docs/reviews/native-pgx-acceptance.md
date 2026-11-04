# Native pgx migration acceptance (2026-09-20)

[Development index](../development.md) · Commands run from the repository root.

- [Native pgx acceptance record](#native-pgx-acceptance-record)

## Native pgx acceptance record

This section summarizes the native pgx migration's acceptance coverage and
local benchmark observations recorded on 2026-09-20. These are historical
results, not a substitute for validating later changes. Use
[ADR-0050](../decisions/ADR-0050-native-pgx-and-typed-queries.md) for the database
contract, the [testing guide](../testing.md) for current gate requirements, and
[P00](../plans/P00-foundation.md) for database/package acceptance and
[P30](../plans/P30-contract-verification.md#production-acceptance-boundaries) for
production-image evidence.

### Migration scope

The recorded migration preserved exact native pgx values, reader routing,
snapshots, chain/session locks, bounded cleanup and lease-fenced publication.
It added no schema changes, startup backfills or compatibility adapters and
retained monolith/split parity. Current implementation requirements belong to
[ADR-0050](../decisions/ADR-0050-native-pgx-and-typed-queries.md),
[database development rules](../development/implementation.md#database-access)
and [storage architecture](../architecture/storage.md); this dated record does
not define a second current policy.

### Validation coverage

The recorded acceptance includes the following passing checks. When changing
these boundaries, rerun the applicable gates against the changed tree and
record the command, environment, result and any unmet requirement in the PR or
completion report, and in the owning milestone when this is planned work.

| Area                                     | Checks and recorded coverage                                                                                                                                                                                              |
| ---------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Generated contracts and source ownership | `make generate-check` and `make source-check`: reproducible generation and enforced SQL execution boundaries.                                                                                                             |
| Common quality gates                     | `make check`: generation, lint, unit/race, security/license and deployment validation. The completed local run passed the full aggregate gate.                                                                            |
| PostgreSQL behavior                      | `make test-integration` and `make test-integration-race`: all six PostgreSQL 18 packages passed, covering typed values, query semantics and persistence invariants.                                                       |
| Worker and home-feed concurrency         | Focused race tests covered task-local lease loss, peer liveness, fatal errors, cancellation, home event-version fencing, replica switching and concurrent waiters.                                                        |
| Embedded browser behavior                | `make test-e2e`: 29 Chrome tests passed, including expired-cursor recovery, single EventSource ownership, version-floor handling and UserOperation reorg updates.                                                         |
| Production schema and runtime            | Production-image schema lifecycle and monolith/split topology parity passed; native Linux AMD64/ARM64 Vyper, Hardhat and Foundry matrices also passed. See the testing guide for each suite's commands and prerequisites. |

Compilation alone does not establish runtime acceptance, and rendered Compose
or Helm validation does not establish production-image behavior. Earlier local
Docker prerequisite failures were followed by successful production checks;
a partially completed aggregate command must still be recorded as incomplete.

Preview metadata has its own `make test-preview-metadata` gate. Its accepted
local Kubo replacement verifies exact content, two canonical metadata versions,
one attempt per version and restart persistence. The prior public-gateway
failure and replacement evidence belong to P30-T35 in
[P30](../plans/P30-contract-verification.md#production-acceptance-boundaries).
The current CI runtime workflow runs this gate; it does not establish
public-IPFS availability. Live payment and reference-capacity acceptance remain
separate release requirements.

### Core read/write benchmark

Host: macOS 27.0, native arm64, Go 1.27.1, PostgreSQL 18. The pre-migration
baseline and native pgx measurements ran serially with the same fixture and
`-benchtime 1s`. The table shows one baseline sample and the range of two native
samples. An earlier diagnostic run overlapping an integration test is excluded.

```sh
go run ./e2e/cmd/testintegration -root . -packages ./internal/integration -benchmark '^BenchmarkCorePersistenceAndProjection$' -benchtime 1s
```

Typical fixtures contain 20 transactions and one log per transaction; large
fixtures contain 200 transactions and two logs per transaction.

| Case                           | Baseline → native ms/op |         Baseline → native bytes/op | Baseline → native allocs/op |
| ------------------------------ | ----------------------: | ---------------------------------: | --------------------------: |
| typical/idempotent_core_commit |     8.007 → 7.754–7.758 |    1,498,508 → 1,478,811–1,485,492 |      11,663 → 11,572–11,584 |
| typical/block_page             |     0.816 → 0.728–0.745 |               14,713 → 6,458–6,459 |                   181 → 123 |
| typical/transaction_page       |  18.860 → 17.914–19.015 |          837,128 → 751,537–752,908 |      13,178 → 12,706–12,718 |
| large/idempotent_core_commit   |  52.085 → 51.441–52.475 | 14,399,008 → 14,599,800–14,600,065 |   132,296 → 132,241–132,252 |
| large/block_page               |     0.877 → 0.811–0.840 |                     14,711 → 6,474 |                   181 → 123 |
| large/transaction_page         |  20.838 → 20.360–23.415 |    4,607,222 → 4,384,576–4,386,665 |      77,485 → 75,384–75,400 |

Block reads used fewer allocations; transaction reads used fewer bytes and
allocations. Large-write time stayed close to the baseline while bytes rose
about 1.4 percent. One large transaction-page sample was about 12 percent slower
than baseline and the other was slightly faster. These short samples establish
neither a latency improvement nor a regression and do not satisfy the P70
reference-capacity gate. Performance acceptance requires separate repeated
measurements with the dataset, hardware, duration and load documented.
