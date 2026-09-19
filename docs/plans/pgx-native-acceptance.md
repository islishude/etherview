# P68 Native pgx Acceptance

Updated: 2026-09-20.

These checks were collected in the isolated `codex/pgx-native` working tree,
based on `a805559556025c3f88699a2fb5bc51d137be4d0a`. The user preserved the
previous database/sql attempt in a stash and requested integration of the
native implementation into local `main`. The stash is retained separately.
No remote publication has been performed.
[P68](P68-runtime-architecture-hardening.md) owns task status.

## Implementation boundary

- Production, CLI and test database access use pgx/v5, pgxpool and pgtype.
  Generated queries use pgx/v5 with private statements and typed parameters and
  results. Anonymous generated parameters/results have been removed; 97 queries
  whose callers discard affected counts now use `:exec`, while conditional writes
  retain `:execrows`. Parameter permutations were checked against all 607 SQL
  statements and their native parameter/result types before PostgreSQL regression.
  Business SQL execution remains only inside generated code. Migrations
  and validated partition DDL retain their existing explicit raw boundaries.
- Read/write routing, chain locks, transaction isolation, lease fencing and
  ambiguous-commit confirmation remain intact. Cancellation uses bounded native
  rollback; uncertain advisory-lock sessions are removed and physically closed.
  Pool teardown shares the supervisor deadline, including stuck borrowers.
- Exact numeric/NULL projections and native arrays/UUID/JSONB are validated.
  API-key, canonical-coverage, Genesis candidate and holder-candidate scans use
  bounded keyset pages; candidate pages share a snapshot that closes before RPC.
- Worker lease loss is task-local. Home HTTP/SSE includes decimal event versions
  with a two-second fence. Browser queries carry typed event metadata and recover
  one EventSource after CLOSED, including expired cursors and UserOperation reorgs.
- No schema migration, startup backfill or compatibility adapter was added.

## Passing evidence

- `go build ./...`, `make lint-go` (zero issues), source, docs and plan checks.
- `make compose-check helm-check` passes rendered deployment and Helm contracts;
  this does not replace the blocked production-image acceptance.
- `make generate-check` through the common gate; OpenAPI and sqlc regeneration
  remains reproducible. Web lint and 373 Vitest cases plus asset-policy tests pass.
- `make test-e2e`: all 29 real Chrome tests pass, including server-generated
  expired-cursor HTTP 400, cursor-free recovery, single EventSource ownership,
  UserOperation head refresh and removal of orphaned detail.
- Focused worker/home/component race tests pass. They cover renewal/completion/
  failure lease loss, continued work and peer liveness, fatal error preservation,
  cancellation, home version 10→11, replica switching, concurrent waiters and a
  late obsolete frontend response. Accepted HTTP snapshots also raise the
  QueryClient version floor; fresh clients remain isolated.
- `make test-integration`: all six PostgreSQL 18 packages pass. The core
  integration package completes in 225.143 seconds. `make test-integration-race`
  passes the same six packages, with the core package in 229.401 seconds.
  Owned PostgreSQL projects and volumes were removed after each run.
- Production E2E test sources compile with `go test -run '^$'
  -tags=runtimee2e,hardhat3e2e,foundrye2e,previewmetadatae2e,hardhat3verify ./e2e/...`.
  This is compile-only evidence.
- `make check` passes generation, Go/Web lint, Go/Web unit tests, Go race,
  security and license checks, then stops at the external Docker syntax-image
  authentication boundary below. No vulnerabilities in called symbols or leaked
  secrets were reported by those checks. After the final query-contract and
  browser version-floor updates, `make generate-check lint test test-race` and
  `make security-check license-check` pass again. The external Docker prerequisite
  remains blocked; this is not a successful aggregate `make check` result.

## Core read/write benchmark

Host: macOS 27.0, native arm64, Go 1.27.1, owned PostgreSQL 18. Baseline and native
measurements ran serially with the same fixture and `-benchtime 1s`. The table
shows one baseline sample and the range of two final native samples:

`go run ./cmd/testintegration -root . -packages ./internal/integration -benchmark '^BenchmarkCorePersistenceAndProjection$' -benchtime 1s`

The earlier diagnostic benchmark overlapping an integration run is excluded.
These are short local samples, not statistical significance claims or
P70 reference-capacity evidence. Typical fixtures have 20 transactions/one log;
large fixtures have 200 transactions/two logs per transaction.

| Case | Baseline → native ms/op | Baseline → native bytes/op | Baseline → native allocs/op |
|---|---:|---:|---:|
| typical/idempotent_core_commit | 8.007 → 7.754–7.758 | 1,498,508 → 1,478,811–1,485,492 | 11,663 → 11,572–11,584 |
| typical/block_page | 0.816 → 0.728–0.745 | 14,713 → 6,458–6,459 | 181 → 123 |
| typical/transaction_page | 18.860 → 17.914–19.015 | 837,128 → 751,537–752,908 | 13,178 → 12,706–12,718 |
| large/idempotent_core_commit | 52.085 → 51.441–52.475 | 14,399,008 → 14,599,800–14,600,065 | 132,296 → 132,241–132,252 |
| large/block_page | 0.877 → 0.811–0.840 | 14,711 → 6,474 | 181 → 123 |
| large/transaction_page | 20.838 → 20.360–23.415 | 4,607,222 → 4,384,576–4,386,665 | 77,485 → 75,384–75,400 |

Block reads reduce allocations materially. Transaction reads reduce bytes and
allocations. Large-write time stays close to this baseline; large-write bytes
rise roughly 1.4 percent. One large transaction-page sample is approximately
12 percent slower than the baseline, while the confirmation sample is slightly
faster. These variable short samples do not establish a latency improvement or
a regression; longer repeated capacity measurements remain separate. Both
results are retained, and no acceptance threshold was relaxed.

## External blocker and clearing condition

Two exact Buildx attempts failed before the working-tree production image could
be built: Docker Hub's `docker/dockerfile:1` frontend authentication first hit a
TLS handshake timeout, then a TCP connection reset at `auth.docker.io/token`.

Commands attempted:

- `make check` stops at `docker-check` for that external authentication failure.
- `make -k test-schema-e2e test-runtime-e2e test-hardhat3-e2e test-foundry-e2e test-preview-metadata`
  stops these production acceptances at their shared `docker-build` prerequisite.
  Client fixture images building successfully do not satisfy production-image
  acceptance. The existing September 9 `etherview:local` image was not reused.

Clear by restoring Docker Hub frontend/token access, then rerun `make check` and
all five exact production acceptance targets against this working tree. Until
then monolith/split production parity, schema-image, Hardhat, Foundry and live
Preview metadata acceptance remain unverified. P70/P73 external release gates
remain separate and unchanged.
