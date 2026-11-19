# Implementation boundaries

[Development index](../development.md) · Commands run from the repository root.

- [Implementation policy](#implementation-policy)
- [Boundary checklist](#boundary-checklist)
- [Database access](#database-access)
- [V2 query pricing changes](#v2-query-pricing-changes)

## Implementation policy

- Do not preserve backward compatibility during active development. The current
  fresh-database baseline is organized by domain in the numbered SQL files under
  `internal/store/migrations/`, from `0001_core.sql` through
  `0019_defi.sql`. Each file has its own version and checksum; the
  runner applies all pending files in one transaction under an advisory lock.
  Databases created with the earlier development chain or single-file baseline
  must be recreated as described in the
  [runbook](../operations/deployment.md#fresh-database-initialization). Do not add startup
  data backfills, legacy-schema adapters, or compatibility readiness states
  unless explicitly requested.
  Keep this baseline declarative: put columns, defaults, identity and constraints
  in `CREATE TABLE`, use `PARTITION OF` for initial partitions, and declare named
  child indexes before recursive parent indexes. Retain only documented ALTER
  exceptions required for dependency completion or exact catalog semantics;
  `TestBaselineDeclaresFinalObjects` checks their complete statements across
  every file. Keep domain-owned sequences, tables, initial partitions, indexes,
  functions and triggers together; cross-domain views and function schema
  bindings come last. Preserve prerequisite unique indexes before referencing
  foreign keys and define CHECK functions before their tables. Deferred PL/pgSQL
  body resolution remains transaction-local to the complete baseline. When
  reorganizing the baseline, compare before/after PostgreSQL 18 schema dumps
  (excluding dump restriction tokens) and retain object names and behavior.
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

| Change touches                                                 | Required action                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| -------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Public API or public numeric fields                            | Start in `api/openapi.yaml`, preserve string encoding beyond JavaScript's safe integer range, regenerate Go and TypeScript contracts, and run `make generate-check`. Consult [ADR-0003](../decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md).                                                                                                                                                                                                                                                                                       |
| Database queries, execution boundaries, or package direction   | Start executable SQL in `internal/db/queries/`; only the migration runner and validated partition-DDL module may own raw production SQL in Go. Public readers use the neutral public-query, ABI, proxy, and stage contract packages and never import HTTP transport or enrichment worker hubs. Run `make lint-go` (including SQL ownership checks), plus generation checks when sqlc sources change.                                                                                                                                                     |
| Runtime components or roles                                    | Update the owning typed builder and the independent production component manifest. Verify `roles=all`, split-role registration, readiness, shutdown, and parity. Consult [ADR-0001](../decisions/ADR-0001-modular-roles-and-postgresql-truth.md).                                                                                                                                                                                                                                                                                                        |
| Chain facts, RPC reads, or enrichment                          | Preserve chain plus block-hash identity, orphan facts, one-endpoint exact-state reads, canonical rechecks, and generation/lease-fenced publication. Never infer coverage from a disconnected height or fall back to `latest`. Use go-ethereum exported protocol types as specified by [ADR-0022](../decisions/ADR-0022-go-ethereum-type-and-raw-rpc-ownership.md).                                                                                                                                                                                       |
| RPC, metadata, compiler, facilitator, or other external calls  | Copy bounded inputs and close database snapshots before the call, then recheck canonicality or ownership before commit. Enforce size, time, work, redirect, and network limits; return stable typed errors and redact hostile nested details.                                                                                                                                                                                                                                                                                                            |
| Optional infrastructure                                        | Keep PostgreSQL authoritative and readiness-correct. Redis, NATS, and trace object caches remain disposable accelerators with bounded fallback. Optional blob bodies use a separate durable archive with PostgreSQL publication authority. Consult [ADR-0015](../decisions/ADR-0015-disposable-runtime-accelerators.md) and [ADR-0055](../decisions/ADR-0055-consensus-and-blob-archives.md).                                                                                                                                                            |
| Browser, authentication, or billing                            | Keep secrets out of logs, URLs, ConfigMaps, the SPA, and image layers. Use the generated same-origin API client, keep wallet RPC inside the injected-provider allowlist, and preserve the separate API-key, SIWE-user, and x402-top-up payer identities. Consult [ADR-0013](../decisions/ADR-0013-embedded-spa-serving-and-browser-security.md), [ADR-0020](../decisions/ADR-0020-siwe-user-sessions.md), [ADR-0035](../decisions/ADR-0035-user-owned-scoped-api-keys.md), and [ADR-0044](../decisions/ADR-0044-prepaid-api-billing-and-x402-topups.md). |
| Verification, proxy, Diamond, EIP-7702, CWIA, or Geas behavior | Read the mechanism's accepted ADR and the relevant architecture section before editing. Preserve exact code/block provenance, current binding fences, compiler/helper identity, and fail-closed publication semantics; do not collapse distinct mechanisms into aliases.                                                                                                                                                                                                                                                                                 |
| SPA structure                                                  | Keep core pages and both locale trees split by domain, use only the generated explorer client outside the injected wallet module, and retain the explicit Oxlint limits and Oxfmt gate in `web-lint` rather than adding blanket suppressions.                                                                                                                                                                                                                                                                                                            |
| Deployment or operator behavior                                | Update the relevant runbook under `docs/operations/` (or `preview/docs/` for Preview) and the Compose/Helm contracts. Keep secrets role-scoped and use repository Compose/Buildx wrappers and supported overrides.                                                                                                                                                                                                                                                                                                                                       |

## Database access

Follow [ADR-0050](../decisions/ADR-0050-native-pgx-and-typed-queries.md): native
pgxpool and pgx transactions, generated sqlc pgx/v5 methods, and exact numeric
values. Preserve reader routing and snapshot/lease boundaries. Transaction
cancellation requires explicit bounded rollback; uncertain session-lock
ownership requires physical connection disposal. Do not introduce stdlib
bridges, exported-query scans, or compatibility adapters. Name query arguments
and computed result columns in SQL. Use `:execrows` when callers enforce an
affected-row condition and `:exec` when only execution errors matter.

## V2 query pricing changes

Every new supported Etherscan V2 action must declare its billing classification in
`internal/etherscanops`. Queries require an explicit positive complexity weight and
cost rationale, plus catalog/handler coverage tests. Reassess bounded query work when
changing existing query paths or limits. Do not add zero weights, missing-price free
fallbacks, or deployment-specific per-action prices. Public prices and reservation
amounts derive from the same catalog and `billing.base_price_atomic`; rate-limit
buckets still spend one token per request. See ADR-0044.
