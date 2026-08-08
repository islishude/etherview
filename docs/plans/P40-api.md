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
- [ADR-0023: Exact transaction state differences](../decisions/ADR-0023-exact-transaction-state-differences.md)
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
| P40-T07 | done | P20-T11, P40-T02, P40-T03 | Transaction-scoped token transfer, log, trace identity, and state-change resources | contract, cursor, reorg, and capability tests |
| P40-T08 | done | P40-T02, P40-T07 | Snapshot-stable address transaction, internal-call, ERC-20, and NFT activity resources | contract, cursor, reorg, and capability tests |
| P40-T09 | done | P40-T02, P40-T05 | Writer-authoritative home snapshot and centralized SSE fanout | contract, replay, concurrency, and integration tests |
| P40-T10 | done | P20, P40-T08 | Snapshot-bound address origins, exact ERC-20 balances, and public wallet-chain configuration | contract, state, cursor, reorg, and configuration tests |
| P40-T11 | done | P30-T15, P40-T10 | Public proxy detail, canonical upgrade and initialization histories, and anonymous free verified-artifact reads | OpenAPI, generated contract, writer-query, cursor, auth, x402, and integration tests |

## Acceptance

- [x] uint256/wei and other unsafe numbers are strings; addresses are checksummed.
- [x] Responses expose canonicality, finality, coverage, and enrichment status.
- [x] Unsupported optional capability returns a machine-readable unavailable
      state rather than a misleading empty success.
- [x] Cursor order remains stable across pages and reorg boundaries.
- [x] API keys are one-time revealed and only keyed hashes are stored.
- [x] Transaction subresources expose one inclusion identity, stable pagination,
      bounded hostile data, and explicit optional-capability state.
- [x] Proxy detail and histories expose only the current canonical published
      generation, bind pagination to an immutable chain and publication
      snapshot, and return a stable stale-cursor error after reorganization or
      same-block publication replay.

## Current Blockers

None.

## Evidence

- P40-T11: the native API exposes current proxy identity plus separately
  paginated upgrade and initialization histories from writer-authoritative,
  canonical published generations. Opaque cursors bind the chain snapshot and
  publication identity; exact OpenZeppelin 5.6.1 verification bindings fence
  implementation and management targets, while verified-artifact GET is
  anonymous and absent from API-key and x402 pricing surfaces.
- P40-T11 verification: OpenAPI and generated Go/TypeScript contracts,
  operation inventory, handlers, PostgreSQL queries, migrations, and runtime
  wiring pass focused ordinary and race tests, the complete ordinary Go suite,
  owned PostgreSQL integration and integration-race gates, and
  `make generate-check`. Regressions cover stale cursors, same-block replay,
  reorgs, Beacon fanout, initialization versions, binding invalidation,
  immutable runtime authority, unverified management, and late exact Clone
  evidence in split-stage publication.
- P40-T11 production E2E: `make test-hardhat3-e2e` passes the real
  `@openzeppelin/contracts@5.6.1` fixture in both production topologies
  (`monolith` 225.84s and `distributed` 185.98s). It verifies Transparent,
  UUPS, Beacon, standard and immutable-args Clones, initializer histories,
  anonymous artifacts, native proxy APIs, and upgrade-driven binding
  invalidation and rebinding.

- P40-T09: `/api/v1/home/stream` publishes a bounded complete home snapshot
  with the durable runtime-event tail id. A single API-replica feed reads
  status, coverage/finality, six canonical blocks, and six canonical
  transactions from one writer-only repeatable-read transaction, subscribes
  from that exact event id, coalesces queued events, retries failed refreshes
  without relabeling stale data, and disconnects slow consumers.
- P40-T09 verification: `go test ./internal/httpapi ./internal/query
  ./internal/app ./internal/apiops` passes the public route inventory, SSE
  envelope and headers, stable initial 503, startup/retry/current-snapshot
  behavior, slow-subscriber isolation, bounded activity, empty-chain query,
  writer component parity, and existing durable-event regressions.
- P40-T09: the same four-package race run passes. The PostgreSQL integration
  regression compiles and covers the event tail, six-item bounds, status and
  coverage, canonical activity, and a two-block reorg in one snapshot; the
  local integration gate reported its documented skip because
  `INTEGRATION_DATABASE_URL` is unset.
- P40-T08: OpenAPI, generated Go/TypeScript contracts, HTTP routing, API
  operation/x402 normalization, and migration-owned indexes now cover
  paginated address transactions, internal calls, ERC-20 transfers, and merged
  ERC-721/ERC-1155 transfers. Queries use repeatable-read canonical snapshots,
  indexed `UNION` branches that deduplicate self activity, and cursors bound to
  chain, normalized address, activity kind, snapshot hash, and row boundary.
  Trace/Token incomplete or failed stages remain typed unavailable responses
  rather than authoritative empty pages.
- P40-T08: targeted Go tests and the same six-package race run passed for
  query, catalog, HTTP, operation inventory, migration, and application
  wiring. Regressions cover checksum input, sender/receiver/self/created
  roles, internal creation, ERC-721/1155 ordering, maximum uint256 strings,
  pagination, indexed SQL branches, stage failure, and reorg-invalidated
  cursors. `make generate-check` passed with a writable Go cache; the
  PostgreSQL integration target reported its documented skip because no
  disposable `INTEGRATION_DATABASE_URL` was available. The complete ordinary
  and race Go suites, vet, golangci-lint, vulnerability, secret, dependency,
  and license checks also passed as part of the common gate.
- P40-T07: OpenAPI, generated Go/TypeScript types, catalog readers, HTTP
  handlers, and the explicit x402 operation inventory now cover paginated
  transaction token transfers, receipt-backed logs, and persisted state
  changes. Every response carries the selected chain/block/transaction
  inclusion and stage state; cursors bind resource kind, inclusion block hash,
  publication generation, and offset. Orphan derived resources report
  `missing`, unavailable/failed generations remain distinct from authoritative
  empty results, and no handler performs live RPC.
- P40-T07: `go test ./internal/httpapi ./internal/apiops ./internal/catalog
  ./internal/query ./internal/store ./internal/app ./internal/enrich`,
  `go test ./...`, and the related six-package race command passed. Handler
  regressions cover all three resources, string quantities, inclusion
  identity, pagination input/output, log topics/data, storage before/after,
  and the new `gas_used` transaction field. `make generate-check` passed with
  no OpenAPI, sqlc, generated-client, or embedded-SPA drift.

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
- P40-T10 implementation and focused verification are complete: generated
  OpenAPI/Go/TypeScript contracts, address-origin coverage and reorg unit
  regressions, exact ERC-20 fixed-block RPC and catalog regressions, public
  wallet-config isolation, configuration boundaries, and HTTP operation
  inventory all pass. `go test ./internal/config ./internal/query
  ./internal/state ./internal/catalog ./internal/httpapi ./internal/apiops`
  and `make generate-check` pass. The aggregate `make check` also passes its
  plan, generation, lint, ordinary/race, security, license, Dockerfile,
  Compose, and Helm gates.
- P40-T10 release validation is tracked by P70-T18. The local
  `make test-integration` invocation reported its documented `SKIP` because
  `INTEGRATION_DATABASE_URL` is not configured; no production or developer
  database was guessed or reused.
