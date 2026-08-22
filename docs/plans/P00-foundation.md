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
| P00-T02 | done | P00-T01 | Go module, Apache-2.0, supported frontend toolchain defaults, Makefile, CI | `make check` |
| P00-T03 | done | P00-T01 | Single CLI, role model, YAML/env config, validation, doctor command | Go unit tests and CLI smoke tests |
| P00-T04 | done | P00-T01 | PostgreSQL migrations, pgx persistence boundary, OpenAPI contract/code generation | migration and API contract tests |
| P00-T05 | done | P00-T01 | React/Vite shell and deterministic `go:embed` asset serving | frontend build and deep-link test |
| P00-T06 | done | P00-T01 | Plan validator with CI enforcement | positive and negative plan-check fixtures |
| P00-T07 | done | P00-T02 | Enforce minimum Go, Node, and npm versions while accepting compatible newer stable releases | minimum/newer/older/malformed shell regressions |
| P00-T08 | done | P00-T02 | Repository-owned golangci-lint v2 policy for Go source and tests | configuration validation and `make lint-go` |
| P00-T09 | done | P00-T08 | Tagged Go lint coverage and unparam cleanup across production, integration, and E2E code | `make lint-go` and tagged compile/test regressions |
| P00-T10 | done | P00-T02, P00-T05 | Codex sandbox execution guidance for writable caches, browser gates, and Docker-backed checks | documentation review and `make plan-check` |
| P00-T11 | done | P00-T07, P00-T08 | Raise the repository Go baseline to 1.27.0 and pin golangci-lint 2.13.1 across development and production build inputs | toolchain regressions, lint configuration, common gates, and production image validation |

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

- P00-T11 raises the module, minimum-version checker, maintained documentation,
  and production builder to Go 1.27.0, pins golangci-lint v2.13.1, and adopts
  Go 1.27's promoted embedded-field literal syntax required by `modernize`.
  The checker regression suite, `make toolchain-check`, golangci-lint
  configuration verification, `make plan-check generate-check lint test
  test-race security-check license-check`, and `make docker-check` pass; the
  Docker check resolves `golang:1.27.0` and reports no warnings. Go source
  analysis tools used for the local evidence were rebuilt with Go 1.27.0 so
  they could parse the upgraded module and standard library.
- P00-T10 adds one maintained restricted-host workflow to `AGENTS.md` and
  `docs/testing.md`: npm, Go build, and golangci-lint caches move to scoped
  writable `/tmp` paths without discarding the warmed module cache; known
  macOS Chromium and Docker/Buildx boundaries request sandbox-external
  execution up front while preserving the exact Makefile target; blank browser
  pages remain product/runtime failures until the first console error is
  diagnosed; and temporary diagnostic servers cannot retain repository-owned
  E2E ports. `make plan-check` passes with 11 plans, 132 work items, and 86
  checked local links, and the changed documentation passes `git diff --check`.
- P00-T01: the required governance, architecture, ADR, testing, and stable child
  plan hierarchy is present; `make plan-check` passes with 8 plans, 47 work
  items, and 31 checked local links.
- P00-T02: the Go 1.26.5, Node 24.18.0, and npm 11.16.0 baseline passes
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
- P00-T07: `make toolchain-check` passes with Go 1.26.5, Node 26.4.0, and npm
  11.17.0. Its Bash regression suite passes the declared minimums and newer
  patch/minor/major versions, and rejects older, prerelease, malformed,
  leading-zero, missing, and command-failure cases. Bash 3.2 syntax and an
  independent read-only review found no remaining issue; `make plan-check`
  passes with 8 plans, 53 work items, and 51 checked local links.
- P00-T08: `.golangci.yml` validates as a v2 configuration and makes the
  repository policy explicit: standard linters plus `modernize`, Go test
  analysis, read-only module resolution, strict generated-file exclusions,
  `gofmt`, and uncapped issue reporting. `make lint-go` reports zero issues,
  `go test ./... -count=1` passes, and `git diff --check` is clean.
- P00-T09: tagged integration, Hardhat, Foundry, and runtime E2E source now
  participates in the same `standard`/`modernize`/`unparam` policy. All 42
  initial `unparam` findings were removed by narrowing production and test
  helper interfaces without suppressions. `make lint-go` reports zero issues,
  the combined tagged no-run compile and `go test ./... -count=1` pass.
- P00-T01/P00-T02/P00-T03/P00-T04/P00-T05/P00-T06/P00-T07/P00-T08/P00-T09/P00-T10 commit/PR: none
  created because this task did not authorize a commit or pull request;
  evidence is bound to the current working tree.
- Container evidence: `make docker-check`, `make compose-check`, and
  `make helm-check` pass; BuildKit resolves the versioned Go/Node/distroless bases
  and reports no Dockerfile warnings. A preceding transient Docker Hub TLS
  timeout cleared on the recorded retry.
