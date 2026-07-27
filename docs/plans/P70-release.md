# P70 — Release

Status: `in_progress`

## Outcome

Etherview v1.0.0 has conformance, migration, security, performance, deployment,
and user/operator evidence sufficient for a production public release.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0018](../decisions/ADR-0018-api-read-replica-routing.md)
- [ADR-0019: Authenticated genesis state import](../decisions/ADR-0019-authenticated-genesis-state-import.md)
- [ADR-0020: SIWE user sessions](../decisions/ADR-0020-siwe-user-sessions.md)
- [ADR-0021: x402 request billing](../decisions/ADR-0021-x402-request-billing.md)
- [ADR-0022: Go-ethereum type and raw RPC ownership](../decisions/ADR-0022-go-ethereum-type-and-raw-rpc-ownership.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P70-T01 | todo | P10–P66 | Execution/API/token/proxy/verification/authentication/billing conformance matrix | conformance suite |
| P70-T02 | todo | P10–P66 | Threat model, security audit, dependency, compiler, session, and payment supply-chain review | security gates |
| P70-T03 | todo | P10–P66 | Monolith/split E2E, migration/rollback, outage, reorg, payment, and soak suite | release CI |
| P70-T04 | in_progress | P60 | 500 RPS reference capacity report and tuning guide | load report |
| P70-T05 | todo | P00–P66 | User/operator/API/authentication/billing/runbook/upgrade documentation | doc review and link check |
| P70-T06 | todo | P70-T01–P70-T05, P70-T08, P70-T09 | SBOM, checksums, signed multi-arch artifacts and v1.0.0 release | release verification |
| P70-T07 | done | P60 | Database read/write pool split configuration, deployment wiring, and capacity guidance | helm config/schema tests |
| P70-T08 | blocked | P10–P60 | Authenticated local/remote genesis account state, predeploy enrichment, native API, and block-zero UI | root, persistence, API, browser, security, and split-role tests |
| P70-T09 | blocked | P10–P60 | Replace duplicative Ethereum RPC/domain types and codecs with reviewed go-ethereum equivalents while retaining explicit hostile-input, persistence, and public-contract adapters | focused compatibility, integration, generation, security, license, and common gates |

## Acceptance

- [ ] Every P00–P66 plan and root release gate is complete with evidence.
- [ ] Clean deployment, upgrade, rollback, backup/restore, and repair procedures
      are independently reproducible.
- [ ] Security findings have no unresolved critical/high issue.
- [ ] Reference capacity target passes with documented hardware and dataset.
- [ ] Published artifacts are reproducible, checksummed, signed, and accompanied
      by an SBOM.
- [x] P70-T07: only API-bearing processes open the optional read-only pool;
      startup validates its schema and chain identity, readiness covers both
      pools without automatic fallback, and every correctness-sensitive read or
      write remains writer-routed.
- [x] P70-T07: configuration, Compose, Helm Secret/ExternalSecret wiring,
      effective connection bounds, and API-only capacity accounting have
      regression coverage and pass the applicable repository gates.
- [ ] P70-T08: an optional bounded Genesis JSON source is authenticated against
      block zero and exposes exact EOA/predeploy account facts through
      PostgreSQL, proxy/ABI enrichment, native API, and the embedded block-zero
      UI; missing input remains explicitly unavailable.
- [ ] P70-T09: reviewed go-ethereum types own recognized protocol semantics,
      including transaction types 0 through 4, while unsupported future
      transaction types fail permanently and atomically before persistence or
      coverage advancement; `blocks.raw` keeps the original block top-level
      shape, versioned root metadata carries validated PoW uncle headers and is
      stripped by `DecodeStoredBlock`, legacy empty-uncle rows remain readable,
      and legacy non-empty uncle hashes without headers fail permanently
      pending exact RPC-backed repair. Receipt fields outside the trie,
      including gas-use deltas, creation address, and effective gas price, are
      authenticated against the matching transaction and block context.
      Transport limits, redaction, supported-object unknown-field retention,
      SQL persistence, and public contracts remain explicit compatible
      adapters.

## Current Blockers

P00 through P65 are complete, while P66 remains `in_progress`: P66-T08 is
blocked on operator-provided Base Sepolia funding, payer credentials, a
compatible staging facilitator and priced route, the matching writer and
independent RPC endpoint, and the deployed image/build digest needed for live
settlement and ledger reconciliation evidence.

P70-T08's local and remote Genesis implementation plus every non-browser gate
are complete, but its browser acceptance remains unavailable because the
managed macOS sandbox denies Chromium's MachPort rendezvous. That blocker
clears when the Genesis browser acceptance can run in CI or another environment
allowed to launch Chromium. P70-T01 through P70-T03 and P70-T05 remain `todo`;
P70-T04 is `in_progress` while its reference-capacity tooling and final report
are prepared.

P70-T09 implementation, focused compatibility, PostgreSQL integration,
generation, lint, security, license, Helm, Compose, ordinary Go, and race gates
pass. Its final image and common-gate evidence is blocked because the managed
sandbox denies Docker buildx access to its local activity state and the
unsandboxed approval request exhausted the current approval allowance. The
blocker clears when `make docker-check`, `make docker-build
docker-image-check`, `make compose-runtime-smoke`, and then `make check` pass in
CI or another environment with Docker buildx access. P70-T06 and the v1 release
remain blocked on P66 completion, Genesis browser evidence, P70-T09 completion,
conformance, security, release-CI, long-capacity, and documentation evidence.

## Evidence

- P70-T07 configuration: YAML, environment, and `_FILE` inputs support an
  optional reader URL plus independently bounded pool sizes. Zero reader
  values inherit writer settings; negative, overflowed, malformed, and
  effective `min > max` inputs fail before runtime.
- P70-T04 tooling: the Compose parity fixture and load driver share one
  distroless `runtime-tools` image, with all three Go binaries emitted by the
  existing `go-builder`. The fixture remains the image default while the load
  service overrides its entrypoint. The target builds successfully; both
  binary entrypoints, the numeric non-root user, Compose configuration, plan
  validation, shell syntax, and whitespace checks pass. This scoped
  maintenance is not 500 RPS or soak evidence.
- P70-T07 runtime: only `api`/`all` opens the forced-read-only reader pool.
  Startup checks its migration ledger and exact chain/genesis identity.
  Ordinary projections and the explicit Etherscan read inventory use it;
  canonical/RPC fences, runtime and metric state, authentication,
  verification, external observations, media, mempool, and all writes remain
  writer-backed. Both operational and public readiness fail closed, with the
  latter bypassing Redis status cache.
- P70-T07 deployment: Compose preserves mounted YAML sizing unless an operator
  supplies an environment override. Helm injects the optional reader Secret
  only into API-bearing containers, supports an optional ExternalSecret key,
  rejects inline credentials and invalid effective bounds, and documents
  restart-on-secret-rotation. The reference maximum is 216 writer plus 96
  reader connections (312 steady state, 624 for full old/new overlap).
- P70-T07 verification: `go test ./internal/config ./internal/app
  ./internal/httpapi -count=1`, the corresponding `go test -race`, and
  `go test ./... -count=1` pass. `make toolchain-check`, `make lint`,
  `make generate-check`, `make helm-check`, and `make compose-check` pass.
  `make plan-check` and `git diff --check` pass after the evidence update.
- P70-T07 integration boundary: `make test-integration` passes against a
  disposable PostgreSQL 18 database after applying and checking every
  migration. The integration-tag regression verifies real writer/reader
  session settings, SQLSTATE `25006`, reader schema compatibility, and
  chain/genesis matching. Both pools used the same PostgreSQL endpoint, so no
  asynchronous replica-lag or reader-outage result is claimed by this scoped
  item.
- P70-T08 implementation: bounded duplicate-key-safe Genesis JSON parsing uses
  Ethereum account/storage tries and the execution header through Amsterdam,
  and requires the configured plus canonical block-zero hash and state root.
  Migration `0022_genesis_state` persists database-guarded immutable balance,
  nonce, code, code hash, and storage-root facts without raw slots; exact
  predeploy code plus its proxy wake commit atomically. Missing input remains a
  typed unavailable capability. The source is either the existing absolute
  local file or a mutually exclusive public-HTTPS URL, and both remain limited
  to indexing from block zero.
- P70-T08 remote boundary: URL validation rejects credentials, query, fragment,
  non-443 ports, path traversal, redirects, environment proxies, and any DNS
  answer in a private or special-use network. Direct verified-IP dialing,
  identity encoding, the 64 MiB declared/streamed limit, an explicit MIME
  allowlist, and JSON validation bound the GET. An optional non-zero lowercase
  SHA-256 authenticates the exact response bytes before JSON validation.
  Network and hostile-content failures expose only stable redacted states.
- P70-T08 remote lifecycle: the importer checks immutable completion before any
  request, waits for canonical block zero, and holds a per-chain PostgreSQL
  session advisory lock across its second completion check and fetch. HTTP,
  checksum, and parsing remain outside the import transaction. Successful
  completion removes the remote dependency; a configured digest is compared
  directly with the persisted digest. Concurrent replicas fetch once, and
  checksum or canonical-identity failures commit no account, code, or proxy-job
  fact.
- P70-T08 public surfaces: generated Go/TypeScript contracts expose cursor-bound
  `/api/v1/genesis/accounts` pages with decimal-string quantities. The embedded
  bilingual SPA exposes `/genesis` and links canonical block zero to it.
  Compose supports file or remote environment inputs. Helm either mounts the
  PVC file or injects URL, digest, and timeout only into `all`/`sync` roles; it
  rejects source conflicts, unmounted inline values, non-zero starts, invalid
  digests, and out-of-range timeouts.
- P70-T08 verification: trie/hash/parser, capability API, cursor,
  proxy-candidate, split-role parity, immutable-observation, remote
  configuration, SSRF/DNS, proxy, redirect, status, MIME, encoding, size,
  timeout, checksum-order, and error-redaction tests pass, as do 68
  browser-component tests. Reference vectors cover fixed roots/hashes,
  100-account branching, and an Amsterdam genesis header. PostgreSQL 18
  `make test-integration` verifies file/URL byte-equivalent facts, one fetch for
  concurrent importers, completed offline restart, persisted-digest conflict,
  canonical completion reauthentication, and zero partial writes on checksum,
  block-hash, state-root, or temporary HTTP failure.
- P70-T08 common gates: `go test ./...`, `make test-race`,
  `make generate-check`, `make security-check`, `make lint` with a writable
  lint cache, `make toolchain-check`, `make compose-check`, `make helm-check`,
  `make license-check`, `make plan-check`, and `git diff --check` pass. The API
  generator override uses the patched `minimatch`/`brace-expansion` chain;
  both npm audits report zero vulnerabilities, `govulncheck` reports zero
  reachable vulnerabilities, and both working-tree and history secret scans
  are clean.
- P70-T08 browser boundary: both system Chrome and bundled Playwright Chromium
  reached the embedded Go server; the non-browser fallback/header case passed,
  while six cases stopped before page creation because macOS denied Chromium's
  MachPort rendezvous. No application assertion failed. An unsandboxed rerun
  was requested and rejected only because workspace approval credits were
  exhausted, so browser acceptance remains unclaimed.
- P70-T09 type ownership: `internal/ethrpc` retains only bounded transport,
  endpoint-pool, capability, scheduling, observation, and stable-error
  concepts. Protocol scalars and recognized RPC objects use go-ethereum
  `common`, `hexutil`, `core/types`, `rpc`, and `core.Genesis` types directly;
  there are no repository aliases or replacement Ethereum models in that
  package. Genesis JSON decodes directly into `core.Genesis`, and
  `core.Genesis.ToBlock()` owns the state root and block identity after a narrow
  duplicate-key, normalized-map-collision, resource, and uint256 preflight.
- P70-T09 raw and persistence boundary: raw-first block acquisition preserves
  supported-object unknown fields and validates header, transaction, receipt,
  log, withdrawal, sender, root, inclusion, uncle, gas-delta, contract-address,
  and effective-gas-price relationships before any atomic repository write.
  Geth transaction types 0 through 4 are supported; type 127 and other
  unsupported formats fail permanently before persistence or coverage
  advancement. Root-preserving versioned metadata carries validated PoW uncle
  headers, and legacy rows missing required non-empty headers return
  `ErrStoredUncleHeadersUnavailable`.
- P70-T09 compatibility: downstream indexing, enrichment, mempool, metadata,
  billing, x402, native API, Etherscan adapters, stores, and test fixtures use
  the direct geth values while SQL and OpenAPI contracts remain unchanged.
  Dynamic effective gas price is authenticated against the matching
  transaction and block base fee; fresh receipts require it, compatible stored
  rows may derive a missing value, and poisoned or context-free values are
  never exposed as verified.
- P70-T09 license closure: the reviewed go-ethereum v1.17.2 scanner baseline
  now includes the separately licensed `crypto/bn256` package. The transitive
  `github.com/holiman/bloomfilter/v2` v2.0.3 module archive omits its
  repository-root MIT license, so the narrow scanner exception fixes the
  version, checked-in text SHA-256, dependency presence, and exact scanner
  result without expanding the permissive allowlist. The production image
  copies all reviewed license texts, and its rootfs check requires each path.
- P70-T09 verification: `go test ./... -count=1`, `go test -race ./...
  -count=1`, `make test-integration`, and the integration-tag race suite pass
  against disposable PostgreSQL 18. `make toolchain-check`, `make plan-check`,
  `make generate-check`, `make lint`, `make security-check`, `make
  license-check`, `make helm-check`, `make compose-check`, and `git diff
  --check` pass. One initial ordinary test overlapped the full race run and
  timed out waiting for Docker in
  `TestContainerCompilerValidatesAndAppliesIsolation`; the isolated case and
  serialized full suite both passed immediately afterward. Docker build,
  production-image rootfs, runtime-parity smoke, and therefore the aggregate
  `make check` remain unclaimed under the blocker above.
