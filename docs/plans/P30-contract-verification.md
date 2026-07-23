# P30 — Contract Verification

Status: `done`

## Outcome

Users can submit asynchronous Solidity and Vyper verification jobs whose
compiler inputs are reproducible, sandboxed, code-hash-versioned, and optionally
interoperable with Sourcify v2. External metadata access is SSRF-safe.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0011](../decisions/ADR-0011-snapshot-search-stats-and-bounded-adapters.md)
- [ADR-0014](../decisions/ADR-0014-durable-verification-identity-and-publication.md)
- [ADR-0016](../decisions/ADR-0016-compiler-supply-chain-and-sandbox.md)
- [ADR-0017](../decisions/ADR-0017-sourcify-interoperability-boundary.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P30-T01 | done | P10, P20 | Verification job/source/compiler/result schema and service boundary | repository tests |
| P30-T02 | done | P30-T01 | Allowlisted compiler manifests, checksum cache, resource-limited sandbox | tamper/limit tests |
| P30-T03 | done | P30-T02 | Solidity/Vyper Standard JSON and multi-file exact/metadata-only matching | compiler fixture tests |
| P30-T04 | done | P30-T01 | Sourcify v2 lookup/import and consent-gated submission | mocked API tests |
| P30-T05 | done | P20 | Safe HTTPS/IPFS NFT metadata and media proxy | SSRF/content tests |
| P30-T06 | done | P20 | Configurable name-service resolver and operator labels | resolver/CLI tests |
| P30-T07 | done | P30-T01, P30-T02 | Fail-closed compiler cleanup and immutable publication constraints | cleanup and PostgreSQL regressions |

## Acceptance

- [x] Verification binds to chain, address, code hash, and applicable block range.
- [x] Public compilation refuses to start when a production sandbox is not
      enforceable.
- [x] Compiler downloads are allowlisted and checksum verified; executions have
      no network and bounded resources.
- [x] Solidity and versioned Vyper Standard JSON inputs are canonical,
      inline-only, exact-target, and matched only across authenticated compiler
      metadata and declared deployment-time values.
- [x] Sourcify submission is never implicit.
- [x] Redirects, DNS rebinding, private networks, oversized content, SVG/HTML,
      and unsafe MIME types are handled without SSRF or XSS.
- [x] P30-T06: name observations are provider-isolated, exact-block-bound, and
      exposed through only stable capability state/code; dotted search freezes
      and filters to the resolver-accepted address.
- [x] P30-T06: operator labels are canonical and chain-scoped across every
      supported kind, with temporal exact block/transaction search overlays and
      JSON `[]` for an empty CLI list.
- [x] A failed, cancelled, or timed-out compiler container cannot be treated as
      safely terminal when force-removal fails or hangs.
- [x] Every newly successful verification job and verified-contract projection
      is backed by its exact immutable result; migration-only legacy rows cannot
      be used to create new unsourced publication.

## Current Blockers

None. P30-T07 passed independent re-review and the applicable common gates.

## Evidence

- P30-T01: migration `0016_durable_verification_boundary` stores the exact
  request payload and complete-input digest, server-derived hard-isolation
  policy, bounded attempt budget, and first-bound compiler kind/digest. It
  replaces target-only uniqueness with active/successful digest idempotency,
  while failed and cancelled submissions can create a fresh job.
- P30-T01: completion locks the exact live lease, revalidates the exact
  canonical code observation, and atomically records either
  `target_not_canonical` or an immutable `verification_results` row, the
  deterministic exact-first verified-contract projection, and the successful
  terminal job. Database constraints and guarded triggers reject incoherent
  job/result/projection mutation.
- P30-T01 verification: `make toolchain-check`, `make generate-check`,
  `make lint`, `make test`, and `make test-race` pass with the baseline Go
  1.26.5, Node 24.18.0, and npm 11.16.0 toolchain. PostgreSQL 18 passes
  `go test -count=1 -tags=integration ./internal/integration`, covering
  migration idempotency, full-input deduplication and failed resubmission,
  per-job isolation, compiler-provenance conflicts, attempt exhaustion,
  stale-canonical terminalization, immutable results, and deterministic
  projection. `govulncheck` and both gitleaks scans report no findings.
- P30-T02: private process manifests now validate canonical Solidity/Vyper
  versions, absolute HTTPS URLs, non-zero lowercase SHA-256 digests, and bounded
  artifact sizes. The proxy-free downloader rejects redirects and every
  private or special-purpose DNS answer, streams through its size and checksum
  limits, and atomically installs only regular `0500` files under an absolute,
  non-symlink, non-writable cache root.
- P30-T02: container startup resolves only Docker or Podman, checks daemon
  reachability, and locally inspects every digest-pinned image. Compilation
  uses `--pull=never`, no network, a read-only non-root filesystem, dropped
  capabilities, no-new-privileges, bounded CPU/memory/swap/PIDs/file
  descriptors/output/tmpfs, and a random name that is force-removed before any
  outcome is accepted. Worker cancellation waits for that bounded cleanup, and
  runtime/compiler diagnostics do not cross the stable error boundary.
- P30-T02 verification: `go test -race ./internal/verify ./internal/config
  ./internal/app ./internal/metadata ./internal/netpolicy -count=1` and matching
  `go vet` pass, including redirect/private-DNS, declared and streaming size,
  symlink/permission tamper, concurrent install, missing image, resource flag,
  diagnostic redaction, cancellation, timeout, and cleanup regressions. With
  the Go 1.26.5, Node 24.18.0, and npm 11.16.0 baseline toolchain,
  `make generate-check`, `make lint`, `make test`, `make test-race`,
  `make security-check`, and `make license-check` pass. The security gate's
  transitive `js-yaml` finding is closed by the scoped Redocly override to
  4.3.0; `npm audit --audit-level=high` reports zero vulnerabilities.
- P30-T03: submission and repository boundaries reject duplicate-key or
  indirect Standard JSON and replace caller output selections with bounded
  exact-target fields before persistence and digesting. The Vyper matrix
  preserves only version-supported inline interfaces, search paths, storage
  overrides, metadata, and layout outputs; legacy per-source selections remain
  minimal. Etherscan conversion uses the same canonicalizer without numeric
  precision loss or wildcard output amplification.
- P30-T03: compiler output selection is exact and fully shape-checked before
  any mismatch result. Solidity matching accepts only linked code, bounded
  compiler-declared immutable ranges, and complete strict CBOR metadata when
  enabled. Vyper matching enforces the fixed/exclusive/inclusive footer formats,
  compiler version, runtime/data/immutable sizes, and the 0.4.1 layout/auxdata
  agreement. Malformed output receives only the stable compiler-output code.
- P30-T03 verification: checksum-pinned official solc 0.8.30 and Vyper 0.3.4,
  0.3.9, 0.3.10, 0.4.0, 0.4.1, and 0.4.3 fixtures cover multi-file linking,
  immutables, exact/metadata-only matches, disabled metadata, malformed nested
  CBOR, and every Vyper layout/auxdata threshold. `make toolchain-check`,
  `make generate-check`, `make lint`, `make test`, `make test-race`,
  `make security-check`, and `make license-check` pass; govulncheck reports no
  called vulnerability, gitleaks reports no finding, and npm audit reports zero
  vulnerabilities.
- P30-T04: the Sourcify v2 adapter uses only bounded contract lookup, Standard
  JSON submit, and job-status calls through the shared proxy-free,
  redirect-free, public-network-only HTTPS transport. Current v2 response
  shapes, chain/address identities, match states, compiler metadata,
  bytecodes, UUIDs, content types, and response sizes are validated before use;
  remote messages and transport details are reduced to stable local errors.
- P30-T04: import accepts an exact locally authoritative chain, address, code
  hash, block hash, creation program, and runtime bytecode target, requires the
  external chain/address/runtime to match it, then emits a normal local
  verification request with external submission disabled. The external
  creation input cannot replace the local program. Upload accepts only a
  durable local job ID, re-reads and digest-checks its coherent persisted
  request, requires both its stored opt-in and a distinct consent argument, and
  exposes only the three Sourcify v2 compiler-input fields.
- P30-T04 verification: `go test -race ./internal/verify -run
  'TestSourcify' -count=1` and `go vet ./internal/verify` pass, including
  redirect/private-DNS, status and diagnostic redaction, malformed/oversized
  response, required-versus-null v2 fields, legal creation-only lookup/status,
  exact-target mismatch, forged/missing durable requests, request bounds, and
  double-consent regressions. With the pinned toolchain, `make toolchain-check
  generate-check plan-check lint test` and `make security-check` pass; npm
  audit, govulncheck, and both gitleaks scans report no actionable finding.
- P30-T05: migration `0017_exact_nft_metadata` makes logical NFT metadata
  resources multi-versioned by exact block hash, adds immutable source
  observations and terminal outcomes, enforces uint256 and outcome consistency,
  and retains orphan facts for canonical fallback.
- P30-T05: the metadata role now discovers ERC-721/1155 source URIs with one
  state endpoint and an EIP-1898 block-hash selector, expands ERC-1155 `{id}`
  templates, rechecks canonicality, and durably queues successful sources.
- P30-T05: media selection releases PostgreSQL before the hostile fetch and
  rechecks the selected newest canonical observation afterward; auth/rate-limit
  failures receive the same no-store, CSP, nosniff and same-origin headers.
- P30-T05 verification: `go test ./internal/metadata ./internal/httpapi
  ./internal/app ./internal/store -count=1`; the same metadata/httpapi/app suite
  with `-race`; and PostgreSQL 18 integration plus race runs for
  `TestPostgresMetadataPipeline*`, `TestPostgresNFTMediaSource*`, and
  `TestPostgresNFTMetadataSourceDiscovery*` all passed on 2026-07-20.
- P30-T06: migration `0018_name_adapter_provider_identity` adds the bounded
  non-secret provider namespace to adapter observation identity. The configured
  name URL is represented only by a SHA-256 namespace, so provider changes do
  not reuse success or failure caches. Label CRUD now runs through generated
  sqlc queries and returns the persisted canonical kind/key.
- P30-T06: dotted search filters only name-source candidates to the accepted
  resolver address while preserving other source matches. Exact height, block
  hash, and transaction hash lookups overlay labels from the cursor's retained
  catalog generation. Native unavailable responses carry controlled
  `capability`, `state`, and `code` details without nested resolver text.
- P30-T06 verification: `go test -race ./internal/adapters
  ./internal/adminstore ./internal/query ./internal/httpapi ./internal/app
  -count=1` and `go vet` over the same packages pass. PostgreSQL 18 runs of
  `go test [-race] -tags=integration ./internal/integration -run
  'Test(CLIOperatorLabels|Search|DottedSearch|Name|CanonicalDetachWaitsForName|SparseCanonicalReplacementWaitsForName|ConcurrentName|PostgresAdapters|ExactCore)'
  -count=1` pass. `make toolchain-check` with the baseline Node
  24.18.0/npm 11.16.0 and `make generate-check` pass.
- P30-T07: container compilation no longer relies on runtime auto-removal. The
  exact random name is always passed through a bounded `rm -f` before success,
  ordinary failure, cancellation, timeout, oversized output, or runtime panic
  is accepted. Failed or hung cleanup takes precedence and returns a stable
  fatal error; a sanitized runtime-panic sentinel is likewise fatal. The worker
  preserves either condition during cancellation, stops its supervisor
  component, and does not terminalize the leased job.
- P30-T07: append-only migration `0019_verification_publication_integrity`
  requires every newly succeeded job, whether inserted directly or transitioned
  by update, to have its exact immutable `verification_results` row at commit,
  and refuses to record the migration while any post-0016 non-legacy success is
  already incoherent. Before replacing guards or validating, it takes
  transaction-scoped write-conflicting locks on results, projections, and jobs
  in production completion order. It preserves pre-0016 unsourced projections
  in place but rejects every new unsourced insert or update. Reader integration
  fixtures now publish through a coherent job/result/projection triple instead
  of bypassing that boundary.
- P30-T07 verification: `go test -race ./internal/verify -count=1`,
  `go vet ./internal/verify`, `make lint-go`, and `make test-race` pass. A
  disposable PostgreSQL 18 run of `make test-integration` passes migration
  up/status and the full integration suite; targeted race coverage proves fresh
  install, pre-0016 upgrade through the complete pending migration tail,
  atomic rejection of a corrupt non-legacy success with an unchanged ledger,
  repair followed by ordered `0019`/`0020` application, resultless succeeded
  INSERT/UPDATE rejection, and grandfathered-only unsourced projections.
  Deterministically coordinated two-connection PostgreSQL tests inspect
  `pg_locks` to prove migration locks are granted/queued in order, concurrent
  unsourced INSERT/UPDATE wait until commit, and then fail under the new guard;
  a rejected upgrade restores the prior functions/triggers and releases its
  locks without changing historical checksum or `applied_at` values.
  Container regressions execute the fake runtime before panicking and assert
  exactly one ordered force-removal, cleanup-error precedence, sanitized fatal
  propagation, and no lease terminalization. With baseline Go 1.26.5, Node
  24.18.0, and npm 11.16.0, `make toolchain-check`, `make generate-check`, and
  `make security-check` pass; govulncheck, both gitleaks scans, and npm audit
  report no actionable finding.
- P30-T07 independent re-review found no remaining issue in the migration lock
  order, deterministic `pg_locks` queue coverage, failed-DDL/ledger rollback,
  or direct runtime-sentinel worker behavior. On the reviewed tree,
  `make lint`, `make test`, `make test-race`, `make plan-check`, and
  `git diff --check` pass in addition to the targeted PostgreSQL evidence.
