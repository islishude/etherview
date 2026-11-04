# Development Guide

This guide owns the repository's engineering workflow and change routing.
Start with the workflow below, then read the topic guide for the boundary you
are changing. Accepted ADRs and applicable test gates remain mandatory.

## Topic guides

| Task                                                                   | Guide                                                             |
| ---------------------------------------------------------------------- | ----------------------------------------------------------------- |
| Change APIs, SQL, runtime roles, security boundaries or billing prices | [Implementation boundaries](development/implementation.md)        |
| Configure Go tags/lint, pnpm, generated clients, workers or styles     | [Go and frontend tooling](development/tooling.md)                 |
| Update dependencies, locks or license notices                          | [Dependencies and license inventory](development/dependencies.md) |
| Choose validation commands and interpret results                       | [Testing and verification](testing.md)                            |
| Deploy, administer or recover a running service                        | [Operations](operations.md)                                       |
| Work on the disposable local environment                               | [Preview documentation](../preview/docs/README.md)                |

## Source-of-truth map

| Concern                                                       | Authoritative source                                                                          |
| ------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| Milestone scope, dependencies, status, and acceptance         | [Root plan](../PLAN.md), [plan catalog](plans/index.md), and [child plans](plans)             |
| Routine change rationale and validation                       | PR description or completion report; Git history                                              |
| Current system behavior and component boundaries              | [Architecture overview](architecture/overview.md)                                             |
| Accepted consequential decisions and invariants               | [ADR catalog](decisions/index.md) and accepted ADR files                                      |
| Runnable commands, test scope, restricted hosts, and evidence | [Makefile](../Makefile) and [testing guide](testing.md)                                       |
| Deployment, telemetry, recovery, and administration           | [Operations runbook](operations.md)                                                           |
| UI design principles and visual review                        | [Design style guide](design-style-guide.md)                                                   |
| Public HTTP contract                                          | [OpenAPI source](../api/openapi.yaml)                                                         |
| Historical native pgx acceptance                              | [Native pgx acceptance record](reviews/native-pgx-acceptance.md#native-pgx-acceptance-record) |
| Production executable SQL                                     | [sqlc query sources](../internal/db/queries)                                                  |

Accepted ADRs are mandatory. Update or add one when a public API, persistent
contract, security boundary, external-service boundary, or monolith/split
runtime decision changes. Keep milestone status out of `AGENTS.md`, and keep
design detail out of plans once an ADR or architecture document owns it.

Preview-specific setup, operations, architecture and acceptance live in
[preview/docs](../preview/docs/README.md); shared guides link to those procedures.
ADRs and milestone status remain in their repository-wide catalogs.

## Planning threshold

Create or extend a plan only for a substantial capability, a consequential
architecture/persistent-contract migration, or multi-stage work whose
dependencies, rollout or acceptance need to survive the current change.
An explicit user request for a tracked plan also qualifies. Use one milestone
per independently verifiable outcome, not one item per file, test, review
finding, command or commit. Reuse the owning milestone for its implementation,
regressions and acceptance instead of splitting each activity into new items.

Small bug fixes, behavior-preserving refactors, styling/copy changes, routine
dependency updates, CI/test repairs and documentation maintenance normally do
not need a plan item. Record their rationale, tests and limitations in the PR
or completion report. Size alone is not the criterion: a small change that
alters a consequential boundary still follows the relevant ADR and planning
rules. Do not reopen a completed plan for ordinary maintenance.

Plans are a roadmap and acceptance summary, not a changelog. Update existing
scope, status, blockers and evidence in place. After completion, retain a short
capability summary, meaningful acceptance evidence and its limits. Remove
abandoned/superseded approaches and routine records once incoming dependencies,
code TODOs and documentation links have been resolved. Git owns detailed
history; do not move the same ledger into a new archive document.

Never remove unresolved feature work, release gates or failures merely to
reduce document size. While an abandoned item still has active consumers,
mark it `dropped` with a reason or `superseded` with its replacement. A `blocked`
item names its blocker and clearing condition. Never renumber or reuse IDs;
the [catalog watermarks](plans/index.md#identifier-allocation) reserve removed
numbers. Long-lived code TODOs should link to an actionable issue or existing
milestone; they do not automatically justify creating a plan item.

## Change workflow

1. Inspect staged, unstaged, and untracked files. Preserve all pre-existing
   work and keep changes within the requested scope.
2. Read the architecture, ADRs and testing rules relevant to the change.
   Consult the root and owning child plan when planned scope, dependencies,
   acceptance or blockers are affected.
3. Apply the planning threshold. For planned work, claim a dependency-ready
   existing `todo` item as `in_progress` before implementation; add an item only
   for a qualifying outcome not already covered. For routine work, proceed
   without editing plans.
4. Implement the behavior and focused regressions together. Keep monolith and
   split-role behavior aligned when the change crosses process boundaries.
5. Run targeted checks first, then the applicable maintained gates. Follow
   `docs/testing.md` when a browser, Docker, cache, or service boundary is
   involved.
6. Record concise commands, results and limitations in the PR or completion
   report. For planned work, update the owning milestone's acceptance once;
   synchronize the root only for scope, dependency or overall-state changes.
   Review staged, unstaged and untracked files again before reporting completion.

## Completion checklist

- Add regressions for malformed inputs and, where relevant, reorgs,
  canonicality, optional-capability loss, numeric bounds, concurrency, and
  security-sensitive parsing.
- Run the smallest focused checks before broader gates. The Makefile is command
  truth; `docs/testing.md` defines each gate's scope and evidence rules.
- Run `make openapi-check` after OpenAPI changes.
  Run `make generate-check` after OpenAPI, SQL, generated-client, or embedded
  SPA changes; `make lint-go` after database execution or package-boundary changes;
  run `make docs-check` after maintained documentation or executable
  deployment/runtime-surface changes; and run `make plan-check` after plan,
  ADR-link, or governance changes.
- Do not substitute local mocks or a weaker browser/container mode for a
  required production, integration, or operator gate. Record what actually ran
  and leave unmet external evidence explicit.
- A planned item becomes `done` only after its targeted checks and applicable
  gates pass and its child plan contains concise current evidence. Routine
  changes use the same applicable checks without manufacturing plan entries.
