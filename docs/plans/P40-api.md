# P40 — API

Status: `done`

## Outcome

The explorer exposes a versioned spec-first native REST API, cursor-stable
queries, explicit completeness, API-key quotas, real-time head/reorg events, and
the agreed Etherscan V2 subset.

## References

- [Architecture](../architecture/overview.md)
- [Etherscan V2 compatibility matrix](../architecture/etherscan-v2-compatibility.md)
- [ADR-0003: Spec-first API and canonical public identifiers](../decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P40-T01 | done | P00, P10 | OpenAPI 3.0.3 envelopes, scalars, errors, cursors, generated types | contract tests |
| P40-T02 | done | P40-T01 | Status/config, block, transaction, address, and search endpoints | handler/repository tests |
| P40-T03 | done | P20, P30-T01, P30-T04, P40-T02 | Token/NFT, contract/source/ABI, stats, pending, verification endpoints | capability matrix tests |
| P40-T04 | done | P40-T01 | API-key lifecycle, anonymous/keyed quotas, CORS, health, metrics | auth/rate tests |
| P40-T05 | done | P40-T02 | Head/reorg SSE and cache invalidation | reconnect/reorg tests |
| P40-T06 | done | P40-T02, P40-T03, P40-T04 | Agreed `/v2/api` Etherscan module/action compatibility | golden compatibility tests |

## Acceptance

- [x] uint256/wei and other unsafe numbers are strings; addresses are checksummed.
- [x] Responses expose canonicality, finality, coverage, and enrichment status.
- [x] Unsupported optional capability returns a machine-readable unavailable
      state rather than a misleading empty success.
- [x] Cursor order remains stable across pages and reorg boundaries.
- [x] API keys are one-time revealed and only keyed hashes are stored.

## Current Blockers

None.

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
- P40-T03: generated native contracts and handlers cover token discovery and
  canonical transfers, exact-block NFT owner/balance/media state, trace and
  `stats@2` publication, snapshot-stable pending transactions, durable
  verification jobs, and exact code-hash-bound source/ABI artifacts. Responses
  carry finality, coverage, and stage completeness; missing, failed, disabled,
  and expired optional capabilities return typed states instead of empty data.
- P40-T03: native verification submission accepts only an address, canonical
  compiler input, and optional exact constructor suffix. The API resolves the
  newest canonical code/block/runtime/creation facts from PostgreSQL and does
  not accept caller-selected target identities. Authenticated durable reads
  remain available when public submission is disabled, and runnable API roles
  with verification reads now require API-key authentication material.
- P40-T03: the optional Sourcify v2 adapter is assembled only by API roles,
  validates the current lookup/submit/status wire contracts through the
  restricted bounded outbound client, and exposes authenticated lookup,
  local-import, explicit double-consent upload, and external-status envelopes.
  Import binds the external chain/address/runtime to the local target while
  retaining only the server-derived creation program; upload returns a
  validated external UUID ticket and never becomes local publication evidence.
- P40-T03 verification: `go test -race ./internal/api ./internal/config
  ./internal/app ./internal/etherscan ./internal/httpapi ./internal/verify
  ./internal/mempool -count=1` passes the OpenAPI/auth, capability, target
  binding, redaction, feature-off, and current Sourcify v2 matrix. A PostgreSQL
  18 integration run of
  `TestMempoolSnapshotsRemainCursorStableAndExposeFailures` proves stable
  cursors, failure states, expired-latest unavailability, expired-cursor
  rejection, and authoritative empty snapshots.
- P40-T03: with the Go 1.26.5, Node 24.18.0, and npm 11.16.0 baseline toolchain,
  `make toolchain-check generate-check lint test test-race security-check
  helm-check plan-check` passes. Govulncheck reports no called vulnerability,
  gitleaks reports no finding, and npm audit reports zero vulnerabilities.
- P40-T03 commit/PR: none created; the user requested implementation and
  verification but did not request a commit or pull request.
- P40-T06: the maintained compatibility matrix inventories all 28 registered
  module/actions with their exact methods, parameters, API-key policy,
  authoritative capability prerequisites, permanent negative capabilities,
  envelopes, and intentional wire differences. Handler goldens independently
  enforce the same action inventory, production-backend dispatch parity,
  GET/POST policy, keyed rejection, typed capability errors, and bounded form
  behavior.
- P40-T06: Core-backed ranges now prove one inclusive, tip-clamped durable
  coverage interval inside the same repeatable-read snapshot as their result
  query. Missing or gapped coverage reports `core coverage unavailable`, while
  an entirely future range remains a true no-records result. Trace and Token
  completeness runs only after that Core proof; transaction-hash absence also
  requires global Core coverage. Countdown cadence is confined to the single
  coverage interval containing the tip and rejects cross-island or
  non-continuous samples.
- P40-T06: compatibility output keeps decimal account/token/block/statistics
  quantities, emits canonical lowercase hexadecimal log quantities, adds the
  current `CompilerType` and `ContractFileName` source fields, documents the
  `MatchKind` extension, and omits unknown `blockReward` instead of fabricating
  zero.
- P40-T06 verification: `go test -race ./internal/etherscan ./internal/auth
  ./internal/httpapi -count=1`, matching `go vet`, `make plan-check`, and
  `git diff --check` pass. A PostgreSQL 18 race integration run of
  `TestEtherscanCoreCoverageDistinguishesGapsFromAuthoritativeResults` proves
  coverage-island rejection, future-only classification, gap repair, exact
  transaction-status absence, range-local countdown, hexadecimal log output,
  and the mined-reward omission against real migrations and queries.
- P40-T06: with the Go 1.26.5, Node 24.18.0, and npm 11.16.0 baseline toolchain,
  `make toolchain-check generate-check lint test test-race security-check
  helm-check plan-check` passes. Govulncheck reports no called vulnerability,
  gitleaks reports no finding, and npm audit reports zero vulnerabilities.
- P40-T06 commit/PR: none created; the user requested implementation and
  verification but did not request a commit or pull request.
