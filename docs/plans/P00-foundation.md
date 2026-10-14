# P00 — Foundation

Status: `done`

## Outcome

The repository has enforceable plan governance, a reproducible Go/React
toolchain, a single role-aware CLI, validated configuration, initial database
and API contracts, and a deterministic embedded-SPA build.

## References

- [Architecture](../architecture/overview.md)
- [Development](../development.md)
- [ADR-0001: Modular roles and PostgreSQL truth](../decisions/ADR-0001-modular-roles-and-postgresql-truth.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P00-T03 | done | — | Single CLI, role model, YAML/env config, validation, doctor command | Go unit tests and CLI smoke tests |
| P00-T04 | done | — | PostgreSQL migrations, pgx persistence boundary, OpenAPI contract/code generation | migration and API contract tests |
| P00-T05 | done | — | React/Vite shell and deterministic `go:embed` asset serving | frontend build and deep-link test |

## Acceptance

- [x] A clean checkout can run the documented common checks.
- [x] `etherview serve --roles=all` and split role selections resolve the same
      component graph.
- [x] Invalid chain/RPC/database/security configuration fails before serving.
- [x] The Go binary serves embedded hashed assets and SPA deep links without a
      Node process.
- [x] Plan/document drift fails CI.

## Current Blockers

None. P70/P73 retain independent release blockers.

## Evidence

Retained completed milestones: P00-T03, P00-T04, P00-T05.

CLI/configuration unit and smoke tests, PostgreSQL migration and generated
API contracts, and frontend build/deep-link checks passed for the initial
foundation. Toolchain, lint and governance maintenance now belongs to the
normal development workflow, not additional foundation milestones.

[Detailed completion record](https://github.com/islishude/etherview/blob/e665a0b9b5dcee02e6d90d3a2224a76031cbda3d/docs/plans/P00-foundation.md#evidence).
These are historical results, not tests rerun by this plan cleanup.
