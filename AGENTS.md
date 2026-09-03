# Etherview Repository Instructions

This file is the compact entry point for repository work. Keep current task
status and evidence in [the plan](PLAN.md) and [child plans](docs/plans/),
engineering workflow in [the development guide](docs/development.md),
architecture detail in [the overview](docs/architecture/overview.md) and
[accepted ADRs](docs/decisions/), runnable verification guidance in
[the testing guide](docs/testing.md), and operator procedures in
[the runbook](docs/operations.md). Accepted ADRs remain mandatory even when a
rule is not repeated here.

## Required reading

Before editing, read:

1. `PLAN.md`, the owning child plan, and its linked ADRs.
2. `docs/development.md` for the change workflow and source-of-truth rules.
3. The relevant sections of `docs/architecture/overview.md`.
4. `docs/testing.md` for applicable targets, restricted-host handling, and
   evidence requirements.

Use the current Makefile and maintained documentation as command truth. Old
plan evidence is historical and must not resurrect removed commands.

Update this file only when repository-wide entry rules or document routing
change. Add a nested `AGENTS.md` only for genuinely different subtree rules.

## Workflow

- Claim one dependency-ready `todo` item as `in_progress` before changing the
  implementation. Never share, delete, or reuse an item ID.
- Preserve and review existing staged, unstaged, and untracked work. Do not
  overwrite unrelated or parallel changes.
- Complete the implementation, regressions, plan state, acceptance checks, and
  concise evidence as one change. Keep the root plan synchronized with child
  status changes.
- Mark abandoned work `dropped` with a reason or `superseded` with its
  replacement. A `blocked` item names both the blocker and clearing condition.
- Long-lived code TODOs reference a plan item. Run `make plan-check` after
  governance changes.

## Non-negotiable boundaries

- Active development targets the current fresh-database schema; do not add
  backward-compatibility adapters, startup backfills, or legacy readiness
  states unless explicitly requested.
- PostgreSQL is authoritative. Redis, NATS, and object storage are optional,
  disposable accelerators and never the sole copy of correctness data.
- Preserve exact chain and block identity, canonical/orphan history, endpoint
  pinning, and lease-fenced atomic publication. Never fall back from a
  block-hash-scoped read to height or `latest`.
- `serve --roles=all` and split roles must use the same components and
  persistence semantics. Keep runtime builders, the production manifest,
  readiness, shutdown, and parity tests aligned.
- Do not hold database snapshots across RPC or other external calls. Treat all
  external input as hostile; bound work and output stable typed errors without
  leaking nested errors, URLs, or credentials.
- `api/openapi.yaml` owns public HTTP contracts and
  `internal/db/queries/` owns production SQL. Regenerate outputs; never
  hand-edit generated files. Public integers beyond JavaScript's safe range
  are strings.
- Keep secrets server-side and role-scoped. Browser explorer traffic uses the
  generated same-origin client; wallet RPC stays in the injected-provider
  allowlist. Preserve the authentication and billing identity boundaries in
  their accepted ADRs.
- Consult and preserve the relevant ADR before changing any public API,
  persistent contract, security or external-service boundary, verifier/proxy
  provenance, or monolith/split runtime decision. Update or add an ADR when the
  decision itself changes.

## Verification

- Run the smallest targeted regressions first, then every applicable common or
  explicit boundary gate from `docs/testing.md`.
- Run `make generate-check` after OpenAPI, SQL, generated-client, or embedded
  SPA changes; `make source-check` after database execution-boundary changes;
  and `make plan-check` after governance changes.
- Run `make docs-check` after maintained documentation or executable
  deployment/runtime-surface changes.
- Follow the restricted-host matrix in `docs/testing.md`. Permission plumbing
  may change, but the repository-owned target and its acceptance criteria may
  not be weakened.
- A work item is complete only after its targeted tests and applicable gates
  pass and its child plan records concise, current evidence.
