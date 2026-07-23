# P60 — Runtime & Operations

Status: `done`

## Outcome

One image runs the same components as a PostgreSQL-only monolith or split roles,
with optional NATS/Redis/S3 acceleration, reproducible Compose and Helm
deployments, observable health, safe migrations, and operator repair tooling.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0001](../decisions/ADR-0001-modular-roles-and-postgresql-truth.md)
- [ADR-0002](../decisions/ADR-0002-identity-bound-repair-and-explicit-reindex.md)
- [ADR-0004](../decisions/ADR-0004-durable-runtime-status-and-events.md)
- [ADR-0010](../decisions/ADR-0010-block-pinned-proxy-stage-and-abi-dependency.md)
- [ADR-0015](../decisions/ADR-0015-disposable-runtime-accelerators.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P60-T01 | done | P00 | Shared component lifecycle, role graph, readiness, graceful shutdown | lifecycle/parity tests |
| P60-T02 | done | P20 | PostgreSQL job/outbox plus optional NATS, Redis, and S3 adapters | outage/fallback tests |
| P60-T03 | done | P00, P10, P40, P50 | Multi-stage non-root image and monolith/distributed Compose profiles | Compose smoke tests |
| P60-T04 | done | P60-T01, P60-T02 | Helm role deployments, HPA, migration job, secrets, network policy | Helm lint/render tests |
| P60-T05 | done | P10, P20, P30-T01, P30-T02, P30-T05, P40 | Structured logs, OpenTelemetry, Prometheus metrics, alerts, admin/repair | observability tests |
| P60-T06 | done | P10–P50 | Backfill tuning, HA/failover, cache/rate policy, reference capacity profile | soak/load tests |

## Acceptance

- [x] P60-T01: production registrations fail closed against an exact,
      feature-aware role manifest, and the monolith graph equals the union of
      the split-role graphs.
- [x] P60-T01: process readiness is published only after all selected services
      start, combines with PostgreSQL/core readiness at the correct boundary,
      and is withdrawn before shutdown cancellation.
- [x] P60-T01: component failure or an unexpected clean exit cancels peers;
      graceful draining has a shared timeout and reports stuck component names.
- [x] P60-T03: the PostgreSQL-only monolith exercises the deterministic
      fixture's enabled trace, mempool, historical-state, and NFT-metadata
      semantics through the production binary. Public verification, Sourcify,
      pricing, and optional accelerators are explicitly disabled in this
      profile and are not claimed by its evidence.
- [x] P60-T03: monolith and seven-role deployments produce the same normalized
      correctness-bearing PostgreSQL projection, seven selected public API
      responses, and embedded SPA shell on the deterministic fixture. The
      split deployment remains ready and advances after one of two sync and
      one of two enrichment replicas are stopped.
- [x] NATS, Redis, or S3 loss degrades acceleration but cannot lose correctness.
- [x] Schema compatibility is checked before serving and migrations are locked.
- [x] P60-T05: OTLP/HTTP tracing is disabled without an explicit server-only
      Secret endpoint, propagates W3C trace context when enabled, and flushes
      within the shared supervisor budget without affecting readiness or
      request results on exporter failure. A remote sampled parent retains its
      identity but receives a fresh server-random sampling decision independent
      of caller-selected or replayed trace IDs.
- [x] P60-T05: current durable-job, verification, and repair backlogs come from
      chain-scoped, partial-indexed PostgreSQL active rows with closed labels;
      refresh failure retains the prior snapshot and exposes staleness.
- [x] P60-T05: split-role replicas deduplicate current PostgreSQL gauges with
      `max`, worker result counters aggregate with `sum`/`rate`, and the Helm
      rules preserve that distinction while isolating scrape targets and alert
      identity by exact release, namespace, and configured chain.
- [x] P60-T05: structured logs and repair status output expose stable bounded
      fields without nested upstream errors, and the runbook covers telemetry,
      alert response, identity-bound repair, reindex, and admin inspection.
      HTTP panic recovery preserves native, compatibility, and operational
      envelopes; committed streams abort without a second body while retaining
      wire-status metrics plus a dedicated bounded panic counter.
- [x] P60-T03: the production image runs as numeric user `65532:65532` with a
      read-only-compatible, capability-free deployment contract and contains
      no Node, package manager, shell, Go toolchain, or compiler payload.
- [x] P60-T06: worker concurrency, historical-backfill concurrency and batch
      size, and per-endpoint RPC throughput are explicitly bounded and reject
      invalid configuration before runtime.
- [x] P60-T06: a chain-scoped writer lease makes sync status monotonic across
      replicas, supports healthy expiry takeover, and keeps an active safety
      halt process-sticky instead of allowing a peer to clear it.
- [x] P60-T06: trusted-proxy parsing, the bounded process-local rate limiter,
      and the Redis fallback circuit preserve client identity and bounded
      memory/work during proxy input, high-cardinality traffic, and backend
      loss.
- [x] P60-T06: the Helm reference profile has per-role disruption budgets,
      hard topology spread, explicit rollout strategy, and documented
      steady-state and fully overlapping rollout Pod/connection bounds.
- [x] P60-T06: the bounded open-loop load driver enforces readiness, lag,
      throughput, latency, error, response-size, origin, and wall-clock
      boundaries. The short 40 RPS Compose profiles pass; the 500 RPS,
      30-minute release report remains P70-T04 work.

## Current Blockers

None. The longer capacity, security, conformance, and release evidence remains
owned by P70 and is not implied by P60 completion.

## Evidence

- P60-T01: the production `Backend.Serve` assembly builds the actual registry,
  compares its exact keys with the feature-aware manifest, and runs those same
  services through the shared lifecycle supervisor in monolith and split-role
  modes. Exact per-role feature matrices and manifest-drift rejection pass.
- P60-T01: `go test ./internal/components ./internal/app ./internal/httpapi
  -count=1`, `go test -race ./internal/components ./internal/app
  ./internal/httpapi -count=1`, and `go vet ./internal/components
  ./internal/app ./internal/httpapi` pass. Regressions cover readiness
  publication/removal order, durable core plus process readiness, PostgreSQL
  probe failure, unexpected clean exit, peer cancellation, and a named bounded
  shutdown timeout.
- P60-T01: `go test ./... -count=1` passes the full Go unit suite after the
  supervisor return-semantics change.
- P60-T01: `make plan-check` passes with 8 plans, 47 work items, and 35 checked
  local links after the in-place status and evidence update.
- P60-T01 commit/PR: none created because the repository has no `HEAD` and this
  task did not authorize a commit or pull request; evidence is bound to the
  current working tree.
- P60-T02: NATS carries only coalesced, content-free runtime/outbox/job wake
  hints while every consumer retains its PostgreSQL poll or claim. Redis uses
  an atomic shared token bucket with a process-local fallback and a fenced
  status-cache generation whose hits are postchecked; invalidation failure
  disables the cache before the durable event relay advances. S3 stores only
  bounded, checksummed normalized trace JSON under exact block/job/generation
  keys, and every hit is PostgreSQL-postchecked before use.
- P60-T02: optional adapter configuration is absent by default, secrets remain
  in environment or Secret references, and Compose plus Helm expose the same
  disposable adapters without adding them to a correctness dependency graph.
  Outage, corruption, replay-generation, late-cache-write, redaction, local
  polling, and exact component-manifest regressions pass under the race
  detector.
- P60-T02 verification: `go test -race ./internal/accelerator
  ./internal/events ./internal/enrich ./internal/catalog ./internal/config
  ./internal/app -count=1`, matching `go vet`, `make generate-check`, Compose
  profile rendering, `helm lint`, and default Helm rendering pass. A fresh
  PostgreSQL 18 database passes `make test-integration`, including all
  migrations and the complete integration package. The integration target now
  fails fast on migration errors and keeps its production database override
  out of isolated test processes.
- P60-T04: the chart renders one `all` Deployment in monolith mode or the seven
  production role Deployments in distributed mode. Per-role `autoscaling/v2`
  HPAs target only enabled roles, and schema/template validation rejects a
  missing service role, invalid replica bounds, or an HPA whose maximum is
  below its minimum.
- P60-T04: each release revision renders a bounded-TTL migration Job that calls
  the advisory-locked `migrate up` path. Every role Deployment calls exact
  checksum-aware `migrate status` in an init container before the serving
  container can start, so pending, changed, or unknown migrations fail closed.
- P60-T04: database, RPC, authentication, and accelerator credentials are
  injected only from the named existing Secret. The optional ExternalSecret
  maps the same keys, enforces paired static S3 credentials, and omits unused
  accelerator entries. Inline database credentials and API-key pepper values
  are rejected. The default NetworkPolicy admits application/metrics ingress
  and only DNS, PostgreSQL, and optional HTTPS egress, with explicit extension
  rules for private or non-standard endpoints.
- P60-T04 verification: supported toolchains pass `make toolchain-check`;
  `make helm-check` passes Helm lint and rendering for monolith and distributed
  values plus the role/HPA/migration/Secret/NetworkPolicy negative regression
  suite; `go test -race ./internal/store ./internal/app -count=1` and matching
  `go vet` pass the migration/schema and runtime wiring boundary. The negative
  suite also rejects inline database, RPC, API-pepper, NATS, Redis, and S3
  credential values, incomplete S3 ExternalSecret pairs, and invalid HPA or
  required-role configurations. `make plan-check`, `jq empty`, shell syntax,
  and `git diff --check` pass.
- P60-T04 commit/PR: none created; the user requested implementation and
  verification but did not request a commit or pull request.
- P60-T05: optional official OpenTelemetry OTLP/HTTP protobuf tracing uses W3C
  propagation, correlated structured logs, a redacted SDK error boundary, and
  an idempotent shutdown handoff inside the shared supervisor budget. Local
  roots use ratio sampling; remote sampled parents use a fresh cryptographic
  Bernoulli decision that preserves trace state without trusting or
  deterministically hashing the caller's trace ID. Disabled configuration
  constructs no exporter or trace background component. Helm injects both
  collector origin and standard OTLP headers only from Secret/ExternalSecret
  keys; Compose uses runtime environment inputs.
- P60-T05: every production role registers the same durable metric collector,
  and the production manifest parity contract includes it plus conditional
  OTLP tracing. One sqlc statement reads a consistent chain-scoped snapshot of
  only durable `queued`/`leased`, verification `queued`/`running`, and repair
  `queued`/`running` rows. Existing active claim indexes and append-only
  `0020_observability_active_repair_index` bound polling independently of
  terminal history. Unknown stage versions collapse to `other`; failed refresh
  retains the prior snapshot and increments a stable failure/staleness signal.
- P60-T05: worker/RPC observers emit only closed purpose, stage, operation, and
  result labels after their controlled boundaries. The bounded
  `admin repair list` JSON/table views report `failure_present` without reading
  stored `last_error` text. Helm alerts use `max` for replica-duplicated current
  PostgreSQL gauges and `sum`/`increase` for per-worker counters. Exact
  ServiceMonitor selectors prevent cross-release scraping; relabeled selectors
  and static alert labels retain release/namespace/chain identity. Mux-derived
  HTTP route patterns keep IDs out of telemetry, and recovered panics expose
  exact boundary envelopes, stable logs, preserved committed wire status, and
  `etherview_http_panics_total`. The operations runbook documents these rules.
- P60-T05 targeted verification: `go mod tidy`; `go test -race
  ./internal/adminstore ./internal/db ./internal/observability ./internal/enrich
  ./internal/config ./internal/ethrpc ./internal/metadata
  ./internal/maintenance ./internal/cli ./internal/app ./internal/store
  -count=1`; `make helm-check`; and `make compose-check` pass. A disposable
  PostgreSQL 18 database passes race-enabled fresh-migration/idempotency,
  simulated 0019-to-0020 upgrade with row preservation and partial-index
  predicate checks, active-gauge terminal exclusion, chain isolation, admin
  redaction, and repeated refresh-failure snapshot-retention integration tests.
  The upgraded OpenTelemetry dependency passes `govulncheck`, and the focused
  secret scan finds no leak. Final repository-wide generation, security, unit,
  race, vet, and plan gates remain pending the independent P30 repair/review.
- P60-T05 review-hardening verification: `go test -race
  ./internal/observability ./internal/httpapi ./internal/app ./web
  -count=1`, the same-package `go vet`, and a 20-run race repetition of the
  real net/http panic-log boundary pass. Helm lint/template passes for both
  layouts and the render suite passes exact multi-release ServiceMonitor,
  every-expression scope, static alert identity, and unsafe monitor/rule
  combination checks. Independent review found no remaining actionable issue.
  On the reviewed tree, `make toolchain-check`, `make generate-check`,
  `make lint`, `make test`, `make test-race`, `make security-check`,
  `make plan-check`, and `git diff --check` pass.
- P60-T03: the production Dockerfile builds the SPA and Go binary in separate
  stages, then copies only the runtime binary, embedded migrations, CA
  certificates, and timezone data into a distroless final image. The image
  contract is enforced by `deploy/runtime-smoke/check-image.sh`, and
  `make docker-image-check` passes with user `65532:65532` and no forbidden
  runtime, build, shell, package-manager, or compiler payload.
- P60-T03: `make compose-runtime-smoke` rebuilds the current working tree and
  passes against independently migrated fresh PostgreSQL 18 databases. The
  monolith and seven-role runs each publish exact `proxy@1`, `abi@1`,
  `token@1`, `stats@2`, and `trace@1` results for the final canonical block,
  reach zero core lag with a drained outbox, and match the normalized
  correctness projection, seven selected API responses, and embedded SPA
  bytes. The distributed run first binds chain identity from a config-only
  role, starts two sync and two enrichment replicas, stops one of each, and
  proves the survivors process the next block.
- P60-T03: both runtime shapes pass the in-network 40 RPS short profile with
  120/120 successful requests, zero errors, zero core lag, ready core, and
  complete backfill. The final distributed report records 40.31 successful
  requests/second and p95 2.95 ms on the deterministic local fixture.
- P60-T06: configuration and runtime tests cover explicit worker,
  historical-backfill, batch, endpoint-name, and per-endpoint RPC-rate bounds.
  Migration `0021_sync_status_writer_lease` plus PostgreSQL integration tests
  cover one active writer, monotonic status, healthy lease-expiry takeover,
  config-only genesis binding, and process-sticky safety-halt ownership.
- P60-T06: API regressions cover canonical trusted-proxy chains and hostile
  forwarding headers, bounded LRU eviction, and Redis failure/recovery with an
  exponential circuit. Helm render tests cover disruption budgets, hard spread
  constraints, strategy validation, and reject both an impossible all-zero
  rollout and simultaneous legacy/new topology-spread inputs. The reference
  profile documents 18 steady-state application Pods and 216 steady-state
  PostgreSQL connections; a fully overlapping rollout is bounded at 36 Pods
  and 432 connections.
- P60-T06: the load driver uses bounded admission, concurrency, response size,
  path/origin, deadline, and result accounting. Focused tests pass 100 repeated
  no-drop runs and prove saturation or a hung server exits within the bounded
  wall-clock budget. `make test-load` and `make test-soak` remain harness-only;
  the long 500 RPS/30-minute execution is deliberately retained in P70-T04.
- P60-T03/T06 final verification: a fresh PostgreSQL 18 database passes
  `make test-integration`; focused Go unit, race, and vet suites pass for load,
  config, RPC, sync status, rate limiting, Redis fallback, proxy/ABI replay,
  and runtime assembly; `make generate-check`, `golangci-lint run ./...`,
  `go vet ./...`, `make deployment-check`, `make license-check`, shell syntax
  checks, and `git diff --check` pass. A repository-wide `make check` also passed
  toolchain, plan, generation, lint, frontend, Go unit, race, vulnerability,
  and secret-scan stages before both npm audit requests were interrupted by
  the registry's repeatable `ECONNRESET`; that external audit interruption is
  not counted as passing evidence. Final independent read-only review found no
  remaining actionable P60 blocker, and the settled governance tree passes
  `make plan-check` with 8 plans, 53 work items, and 53 local links.
