# P60 — Runtime & Operations

Status: `in_progress`

## Outcome

One image runs the same components as a PostgreSQL-only monolith or split roles,
with optional NATS/Redis/S3 acceleration, reproducible Compose and Helm
deployments, observable health, safe migrations, and operator repair tooling.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0001](../decisions/ADR-0001-modular-roles-and-postgresql-truth.md)
- [ADR-0002](../decisions/ADR-0002-identity-bound-repair-and-explicit-reindex.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P60-T01 | done | P00 | Shared component lifecycle, role graph, readiness, graceful shutdown | lifecycle/parity tests |
| P60-T02 | todo | P20 | PostgreSQL job/outbox plus optional NATS, Redis, and S3 adapters | outage/fallback tests |
| P60-T03 | todo | P00, P10, P40, P50 | Multi-stage non-root image and monolith/distributed Compose profiles | Compose smoke tests |
| P60-T04 | todo | P60-T01, P60-T02 | Helm role deployments, HPA, migration job, secrets, network policy | Helm lint/render tests |
| P60-T05 | todo | P10–P40 | Structured logs, OpenTelemetry, Prometheus metrics, alerts, admin/repair | observability tests |
| P60-T06 | todo | P10–P50 | Backfill tuning, HA/failover, cache/rate policy, reference capacity profile | soak/load tests |

## Acceptance

- [x] P60-T01: production registrations fail closed against an exact,
      feature-aware role manifest, and the monolith graph equals the union of
      the split-role graphs.
- [x] P60-T01: process readiness is published only after all selected services
      start, combines with PostgreSQL/core readiness at the correct boundary,
      and is withdrawn before shutdown cancellation.
- [x] P60-T01: component failure or an unexpected clean exit cancels peers;
      graceful draining has a shared timeout and reports stuck component names.
- [ ] PostgreSQL-only monolith provides full enabled feature semantics.
- [ ] Split roles and monolith produce identical persisted state and API output.
- [ ] NATS, Redis, or S3 loss degrades acceleration but cannot lose correctness.
- [ ] Schema compatibility is checked before serving and migrations are locked.
- [ ] Production images run non-root and contain no Node/package manager/compiler
      payload unless that role explicitly mounts an approved compiler cache.

## Current Blockers

None for P60-T01. Remaining dependency-bound work stays unclaimed and is
tracked by P60-T02 through P60-T06.

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
