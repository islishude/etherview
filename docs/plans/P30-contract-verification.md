# P30 — Contract Verification

Status: `in_progress`

## Outcome

Users can submit asynchronous Solidity and Vyper verification jobs whose
compiler inputs are reproducible, sandboxed, code-hash-versioned, and optionally
interoperable with Sourcify v2. External metadata access is SSRF-safe.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0011](../decisions/ADR-0011-snapshot-search-stats-and-bounded-adapters.md)
- [ADR-0014](../decisions/ADR-0014-durable-verification-identity-and-publication.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P30-T01 | done | P10, P20 | Verification job/source/compiler/result schema and service boundary | repository tests |
| P30-T02 | todo | P30-T01 | Allowlisted compiler manifests, checksum cache, resource-limited sandbox | tamper/limit tests |
| P30-T03 | todo | P30-T02 | Solidity/Vyper Standard JSON and multi-file exact/metadata-only matching | compiler fixture tests |
| P30-T04 | todo | P30-T01 | Sourcify v2 lookup/import and consent-gated submission | mocked API tests |
| P30-T05 | done | P20 | Safe HTTPS/IPFS NFT metadata and media proxy | SSRF/content tests |
| P30-T06 | done | P20 | Configurable name-service resolver and operator labels | resolver/CLI tests |

## Acceptance

- [x] Verification binds to chain, address, code hash, and applicable block range.
- [ ] Public compilation refuses to start when a production sandbox is not
      enforceable.
- [ ] Compiler downloads are allowlisted and checksum verified; executions have
      no network and bounded resources.
- [ ] Sourcify submission is never implicit.
- [x] Redirects, DNS rebinding, private networks, oversized content, SVG/HTML,
      and unsafe MIME types are handled without SSRF or XSS.
- [x] P30-T06: name observations are provider-isolated, exact-block-bound, and
      exposed through only stable capability state/code; dotted search freezes
      and filters to the resolver-accepted address.
- [x] P30-T06: operator labels are canonical and chain-scoped across every
      supported kind, with temporal exact block/transaction search overlays and
      JSON `[]` for an empty CLI list.

## Current Blockers

P30-T02 and P30-T04 are now unblocked but remain unclaimed. P30-T03 continues
to depend on P30-T02; P30-T01, P30-T05, and P30-T06 are complete.

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
  `make lint`, `make test`, and `make test-race` pass with the pinned Go
  1.26.5, Node 24.18.0, and npm 11.16.0 toolchain. PostgreSQL 18 passes
  `go test -count=1 -tags=integration ./internal/integration`, covering
  migration idempotency, full-input deduplication and failed resubmission,
  per-job isolation, compiler-provenance conflicts, attempt exhaustion,
  stale-canonical terminalization, immutable results, and deterministic
  projection. `govulncheck` and both gitleaks scans report no findings.
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
  -count=1` pass. `make toolchain-check` with the repository-pinned Node
  24.18.0/npm 11.16.0 and `make generate-check` pass.
