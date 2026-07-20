# P40 — API

Status: `in_progress`

## Outcome

The explorer exposes a versioned spec-first native REST API, cursor-stable
queries, explicit completeness, API-key quotas, real-time head/reorg events, and
the agreed Etherscan V2 subset.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0003: Spec-first API and canonical public identifiers](../decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P40-T01 | done | P00, P10 | OpenAPI 3.0.3 envelopes, scalars, errors, cursors, generated types | contract tests |
| P40-T02 | done | P40-T01 | Status/config, block, transaction, address, and search endpoints | handler/repository tests |
| P40-T03 | todo | P20, P30, P40-T02 | Token/NFT, contract/source/ABI, stats, pending, verification endpoints | capability matrix tests |
| P40-T04 | done | P40-T01 | API-key lifecycle, anonymous/keyed quotas, CORS, health, metrics | auth/rate tests |
| P40-T05 | done | P40-T02 | Head/reorg SSE and cache invalidation | reconnect/reorg tests |
| P40-T06 | todo | P40-T02, P40-T03, P40-T04 | Agreed `/v2/api` Etherscan module/action compatibility | golden compatibility tests |

## Acceptance

- [x] uint256/wei and other unsafe numbers are strings; addresses are checksummed.
- [ ] Responses expose canonicality, finality, coverage, and enrichment status.
- [ ] Unsupported optional capability returns a machine-readable unavailable
      state rather than a misleading empty success.
- [x] Cursor order remains stable across pages and reorg boundaries.
- [x] API keys are one-time revealed and only keyed hashes are stored.

## Current Blockers

P40-T03 and P40-T06 await the remaining P20/P30 capability outputs. There is
no blocker on the already completed native core API, auth, or event stream.

## Evidence

- P40-T01: `make generate-check` passes OpenAPI-to-Go, OpenAPI-to-TypeScript,
  sqlc, and embedded-SPA regeneration with no drift. Raw OpenAPI contract tests
  reject duplicate YAML keys and enforce version 3.0.3, `/api/v1`, generated
  success/error envelopes, canonical decimal uint256 strings, documented
  checksum/lowercase public identifiers, common JSON error fallbacks, and the
  shared bounded `OpaqueCursor` input/output schema.
- P40-T01: `go test -race ./internal/api ./internal/httpapi ./internal/query
  ./internal/catalog ./internal/mempool` passes generated Go-envelope/string
  scalar checks, strict cursor decoding, checksum vectors, snapshot-bound
  ordering, and stale-after-reorg rejection.
- P40-T01: `npm --prefix web run lint` and `npm --prefix web test` pass (4 test
  files, 26 tests). The SPA uses generated error/metadata types and rejects a
  success envelope missing `request_id`/`chain_id` or an error envelope missing
  its required `request_id`.
- P40-T01 commit/PR: none created because the repository has no `HEAD` and this
  task did not authorize a commit or pull request; evidence is bound to the
  current working tree.
- P40-T02: `go test -race ./internal/query ./internal/state
  ./internal/httpapi` passes status/config string quantities and completeness,
  gap-aware readiness, stable block and transaction pagination, cursor
  invalidation after canonical changes, retained orphan lookup, EIP-55 address
  output, fixed-canonical-block address state, explicit archive/state
  unavailability, and block/hash/address/name/Token/label search.
- P40-T02: handler regressions reject invalid block, transaction, address,
  search, limit, and cursor inputs before the repository and map not-found,
  unavailable, not-ready, invalid-cursor, and internal failures to the native
  error envelope. Repository tests reject malformed persisted identities and
  trailing raw JSON rather than publishing inconsistent facts.
- P40-T02 commit/PR: none created because the repository has no `HEAD` and this
  task did not authorize a commit or pull request; evidence is bound to the
  current working tree.
- P40-T04: `go test -race ./internal/auth ./internal/httpapi
  ./internal/observability ./internal/cli` and the corresponding `go vet`
  targets pass. Tests cover one-time issuance, HMAC-only persistence,
  authentication/revocation, concurrent atomic rotation with one active
  successor, anonymous/keyed token buckets, boundary-correct 429 envelopes,
  exact CORS allowlisting, health/readiness, and rate-limit metrics.
- P40-T04: PostgreSQL 18 race integration
  `TestCLIBackendPersistsMigrationsMaintenanceAndAdminState` passes the real
  create/list/rotate/revoke CLI lifecycle. Rotation preserves the name and
  quota, atomically revokes the old prefix and persists only the replacement
  digest, and a second rotation of the revoked prefix creates no extra key.
- P40-T04 commit/PR: none created because the repository has no `HEAD` and this
  task did not authorize a commit or pull request; evidence is bound to the
  current working tree.
- P40-T05: `go test -race ./internal/events ./internal/httpapi -count=1`
  passes durable-ID replay, a live reorg followed by reconnect after broker and
  HTTP-process recreation, cursor failure classification, SSE anti-buffering
  headers, and `no-store` native API responses. Cache invalidation is proven to
  precede fanout; a failed safety check leaves the relay cursor unchanged and
  retries the same idempotent event.
- P40-T05: PostgreSQL 18 race integration
  `go test -race -tags=integration ./internal/integration -run
  'Test(CanonicalTransitionsAndRuntimeStatus|BoundedRuntimeReplay)' -count=1`
  passes atomic canonical head/reorg/status event persistence and independent
  bounded replay by API replicas.
- P40-T05 commit/PR: none created because the repository has no `HEAD` and this
  task did not authorize a commit or pull request; evidence is bound to the
  current working tree.
