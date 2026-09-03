# Development Guide

This guide owns the repository's engineering workflow and change-routing
rules. It complements the compact `AGENTS.md` entry point without duplicating
the detailed architecture, testing, or operations manuals.

## Source-of-truth map

| Concern | Authoritative source |
| --- | --- |
| Current scope, dependencies, status, and evidence | [Root plan](../PLAN.md) and [child plans](plans/) |
| Current system behavior and component boundaries | [Architecture overview](architecture/overview.md) |
| Accepted consequential decisions and invariants | [Accepted ADRs](decisions/) |
| Runnable commands, test scope, restricted hosts, and evidence | [Makefile](../Makefile) and [testing guide](testing.md) |
| Deployment, telemetry, recovery, and administration | [Operations runbook](operations.md) |
| Public HTTP contract | [OpenAPI source](../api/openapi.yaml) |
| Production executable SQL | [sqlc query sources](../internal/db/queries/) |

Accepted ADRs are mandatory. Update or add one when a public API, persistent
contract, security boundary, external-service boundary, or monolith/split
runtime decision changes. Put task status and evidence in plans rather than in
`AGENTS.md`, and keep design detail out of plans once an ADR or architecture
document owns it.

## Change workflow

1. Read the root plan, the owning child plan, its dependencies, and every
   linked ADR or testing rule relevant to the change.
2. Select one dependency-ready `todo` work item and mark it `in_progress`
   before changing implementation. Synchronize the root plan when the child
   plan's overall status changes.
3. Inspect staged, unstaged, and untracked files. Preserve all pre-existing
   work and avoid opportunistic edits outside the claimed scope.
4. Implement the behavior and focused regressions together. Keep monolith and
   split-role behavior aligned when the change crosses process boundaries.
5. Run targeted checks first, then the applicable maintained gates. Follow
   `docs/testing.md` when a browser, Docker, cache, or service boundary is
   involved.
6. Update acceptance state and record concise commands and results in the child
   plan. Review all staged, unstaged, and untracked files again before
   reporting completion.

Never delete or reuse a work-item ID. Mark abandoned work `dropped` with a
reason or `superseded` with its replacement. A `blocked` item names the blocker
and the condition that clears it. Long-lived code TODOs must cite a plan item.

## Implementation policy

- Do not preserve backward compatibility during active development. Migrations
  target the current fresh-database schema; do not add startup data backfills,
  legacy-schema adapters, or compatibility readiness states unless explicitly
  requested.
- Choose the simplest implementation that fully meets current requirements.
  Prefer established, maintained libraries over custom replacements.
- Make architectural decisions for the long term. Do not introduce a stopgap
  that is intended to be replaced later.
- Generated artifacts are outputs, not editing surfaces. Change their
  authoritative source and regenerate them.
- Keep tracked templates immutable when runtime-specific artifacts can be
  generated in ignored workspace paths.

## Boundary checklist

Use this table to locate the complete rules before changing a boundary. The
linked documents, not this summary, define the exact contract.

| Change touches | Required action |
| --- | --- |
| Public API or public numeric fields | Start in `api/openapi.yaml`, preserve string encoding beyond JavaScript's safe integer range, regenerate Go and TypeScript contracts, and run `make generate-check`. Consult [ADR-0003](decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md). |
| Database queries, execution boundaries, or package direction | Start executable SQL in `internal/db/queries/`; only the migration runner and validated partition-DDL module may own raw production SQL in Go. Public readers use the neutral public-query, ABI, proxy, and stage contract packages and never import HTTP transport or enrichment worker hubs. Run `make source-check`, plus generation checks when sqlc sources change. |
| Runtime components or roles | Update the owning typed builder and the independent production component manifest. Verify `roles=all`, split-role registration, readiness, shutdown, and parity. Consult [ADR-0001](decisions/ADR-0001-modular-roles-and-postgresql-truth.md). |
| Chain facts, RPC reads, or enrichment | Preserve chain plus block-hash identity, orphan facts, one-endpoint exact-state reads, canonical rechecks, and generation/lease-fenced publication. Never infer coverage from a disconnected height or fall back to `latest`. Use go-ethereum exported protocol types as specified by [ADR-0022](decisions/ADR-0022-go-ethereum-type-and-raw-rpc-ownership.md). |
| RPC, metadata, compiler, facilitator, or other external calls | Copy bounded inputs and close database snapshots before the call, then recheck canonicality or ownership before commit. Enforce size, time, work, redirect, and network limits; return stable typed errors and redact hostile nested details. |
| Optional infrastructure | Keep PostgreSQL authoritative and readiness-correct. Redis, NATS, and object storage remain disposable accelerators with explicit bounded fallback behavior. Consult [ADR-0015](decisions/ADR-0015-disposable-runtime-accelerators.md). |
| Browser, authentication, or billing | Keep secrets out of logs, URLs, ConfigMaps, the SPA, and image layers. Use the generated same-origin API client, keep wallet RPC inside the injected-provider allowlist, and preserve the separate API-key, SIWE-user, and x402-top-up payer identities. Consult [ADR-0013](decisions/ADR-0013-embedded-spa-serving-and-browser-security.md), [ADR-0020](decisions/ADR-0020-siwe-user-sessions.md), [ADR-0035](decisions/ADR-0035-user-owned-scoped-api-keys.md), and [ADR-0044](decisions/ADR-0044-prepaid-api-billing-and-x402-topups.md). |
| Verification, proxy, Diamond, EIP-7702, CWIA, or Geas behavior | Read the mechanism's accepted ADR and the relevant architecture section before editing. Preserve exact code/block provenance, current binding fences, compiler/helper identity, and fail-closed publication semantics; do not collapse distinct mechanisms into aliases. |
| SPA structure | Keep core pages and both locale trees split by domain, use only the generated explorer client outside the injected wallet module, and retain the exact `web-lint` policy rather than adding blanket suppressions. |
| Deployment or operator behavior | Update `docs/operations.md` and the relevant Compose/Helm contracts. Keep secrets role-scoped and use repository Compose/Buildx wrappers and supported overrides. |

## Completion checklist

- Add regressions for malformed inputs and, where relevant, reorgs,
  canonicality, optional-capability loss, numeric bounds, concurrency, and
  security-sensitive parsing.
- Run the smallest focused checks before broader gates. The Makefile is command
  truth; `docs/testing.md` defines each gate's scope and evidence rules.
- Run `make generate-check` after OpenAPI, SQL, generated-client, or embedded
  SPA changes; `make source-check` after database execution-boundary changes;
  run `make docs-check` after maintained documentation or executable
  deployment/runtime-surface changes; and run `make plan-check` after plan,
  ADR-link, or governance changes.
- Do not substitute local mocks or a weaker browser/container mode for a
  required production, integration, or operator gate. Record what actually ran
  and leave unmet external evidence explicit.
- A work item becomes `done` only after its targeted checks and applicable
  gates pass and the child plan contains concise current evidence.
