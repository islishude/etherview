# Etherview Repository Instructions

## Before editing

Read the [development guide](docs/development.md), relevant
[architecture](docs/architecture/overview.md) sections and
[accepted ADRs](docs/decisions/index.md), and applicable
[testing rules](docs/testing.md). Read [PLAN.md](PLAN.md) and the owning
[child plan](docs/plans/index.md) when the change affects planned scope,
dependencies, acceptance or blockers. Accepted ADRs remain mandatory; update
or add one when its decision changes.

The development guide owns detailed workflow and boundary routing. The current
[Makefile](Makefile) and testing guide own commands; historical plan evidence
must not revive removed targets. Operator procedures belong in the
[runbook](docs/operations.md). Plans retain milestone status and acceptance;
routine change evidence belongs in the PR or completion report.

## Workflow

- Review and preserve staged, unstaged, and untracked work.
- Plan substantial capabilities, architecture migrations and multi-stage
  delivery using the [planning threshold](docs/development.md#planning-threshold).
  Small fixes, refactors, dependency bumps, CI repairs and documentation edits
  normally need no plan item. Do not add one merely to satisfy workflow.
- For planned work, update the existing milestone in place and claim a
  dependency-ready item before implementation. Synchronize the root only when
  scope, dependencies or overall status changes.
- Deliver regressions and applicable checks with the change. Keep milestone
  evidence concise; remove obsolete/routine records once references are resolved.
  Never reuse or renumber IDs; follow the catalog's allocation watermarks.
  Preserve unresolved blockers with explicit clearing conditions.

## Required boundaries

- Target the current fresh-database schema. No compatibility adapters, startup
  backfills, or legacy readiness states unless explicitly requested.
- PostgreSQL is authoritative for identities and publication; Redis, NATS and
  trace object caches are disposable accelerators. Optional blob bodies use
  durable object archives under ADR-0055, with PostgreSQL-owned manifests.
- Preserve chain/block identity, canonical and orphan history, endpoint pinning,
  and lease-fenced atomic publication. Never fall back from block hash to height
  or `latest`.
- Keep `serve --roles=all` and split-role components and persistence identical;
  align builders, production manifest, readiness, shutdown, and parity tests.
- Close database snapshots before external calls. Bound hostile input and work;
  return stable typed errors without nested errors, URLs, or credentials.
- Public HTTP contracts start in `api/openapi.yaml`; production SQL starts in
  `internal/db/queries/`. Regenerate outputs, never hand-edit them. Public
  integers beyond JavaScript's safe range are strings.
- Keep secrets server-side and role-scoped, explorer traffic on the generated
  same-origin client, and wallet RPC in the injected-provider allowlist.
- Consult the relevant ADR before changing APIs, persistent contracts, security,
  external services, verifier/proxy provenance, runtime topology, or
  authentication/billing identities; preserve its boundaries.

## Verification

Run targeted regressions, then all applicable gates in [docs/testing.md](docs/testing.md):

- `make generate-check`: OpenAPI, SQL, generated-client, or embedded SPA changes.
- `make source-check`: database execution-boundary changes.
- `make docs-check`: maintained docs or executable deployment/runtime surfaces.
- `make plan-check`: plan, ADR-link, or governance changes.

Follow the testing guide's restricted-host matrix without weakening targets or
acceptance criteria. Report completion only after applicable checks pass;
for planned work, record concise acceptance evidence before marking it `done`.

Keep this file limited to repository-wide rules and document routing. Add nested
`AGENTS.md` files only for genuinely different subtree rules.
