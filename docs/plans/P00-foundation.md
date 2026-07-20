# P00 — Foundation

Status: `done`

## Outcome

The repository has enforceable plan governance, a reproducible Go/React
toolchain, a single role-aware CLI, validated configuration, initial database
and API contracts, and a deterministic embedded-SPA build.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0001: Modular roles and PostgreSQL truth](../decisions/ADR-0001-modular-roles-and-postgresql-truth.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P00-T01 | done | — | `AGENTS.md`, root plan, child plans, architecture/ADR/testing truth sources | `make plan-check` |
| P00-T02 | done | P00-T01 | Go module, Apache-2.0, pinned frontend toolchain, Makefile, CI | `make check` |
| P00-T03 | done | P00-T01 | Single CLI, role model, YAML/env config, validation, doctor command | Go unit tests and CLI smoke tests |
| P00-T04 | done | P00-T01 | PostgreSQL migrations, pgx persistence boundary, OpenAPI contract/code generation | migration and API contract tests |
| P00-T05 | done | P00-T01 | React/Vite shell and deterministic `go:embed` asset serving | frontend build and deep-link test |
| P00-T06 | done | P00-T01 | Plan validator with CI enforcement | positive and negative plan-check fixtures |

## Acceptance

- [x] A clean checkout can run the documented common checks.
- [x] `etherview serve --roles=all` and split role selections resolve the same
      component graph.
- [x] Invalid chain/RPC/database/security configuration fails before serving.
- [x] The Go binary serves embedded hashed assets and SPA deep links without a
      Node process.
- [x] Plan/document drift fails CI.

## Current Blockers

None.

## Evidence

- P00-T01: the required governance, architecture, ADR, testing, and stable child
  plan hierarchy is present; `make plan-check` passes with 8 plans, 47 work
  items, and 31 checked local links.
- P00-T02: exact Go 1.26.5, Node 24.18.0, and npm 11.16.0 pins pass
  `make toolchain-check`; `make check` passes every source, generation, lint,
  unit/race, security, and license stage through `license-check`, with the
  container-only result recorded below. The frontend license checker is an
  exact lockfile dependency rather than a runtime `npx` download.
- P00-T03: `go test -race ./internal/config ./internal/components
  ./internal/cli ./internal/app` passes. Doctor rejects a parseable but
  non-runnable role configuration, and the production component manifest is
  checked against actual registrations plus the monolith/split-role union.
- P00-T04: `make generate-check` and `go test -race ./internal/db
  ./internal/store ./internal/httpapi` pass. PostgreSQL 18 integration passes
  embedded migration idempotency and the production sqlc/pgx bridge query.
- P00-T05: `npm --prefix web run lint`, `npm --prefix web test`, and
  `npm --prefix web run build` pass (4 files, 24 unit tests); `make test-e2e`
  passes 2 Playwright flows against the Go embedded-asset server, including a
  deep link and injected-wallet chain mismatch.
- P00-T06: positive and negative plan fixtures pass under
  `go test -race ./internal/plancheck`; CI runs `make plan-check` and rejects
  generated or plan drift.
- P00-T01/P00-T02/P00-T03/P00-T04/P00-T05/P00-T06 commit/PR: none created
  because the repository has no `HEAD` and this task did not authorize a commit
  or pull request; evidence is bound to the current working tree.
- Container evidence: `make docker-check`, `make compose-check`, and
  `make helm-check` pass; BuildKit resolves the exact Go/Node/distroless bases
  and reports no Dockerfile warnings. A preceding transient Docker Hub TLS
  timeout cleared on the recorded retry.
