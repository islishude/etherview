# P64 — NFT Metadata Web

Status: `done`

## Outcome

ERC-721 and ERC-1155 instance pages expose the newest exact canonical metadata
observation as bounded inert text and ordered traits. Image bytes are never
embedded or prefetched: users see only a reviewed HTTPS destination and must
accept a bilingual warning before opening it in a separate, opener-free,
no-referrer tab.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0003: Spec-first API and canonical public identifiers](../decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md)
- [ADR-0005: Safe NFT metadata and media boundary](../decisions/ADR-0005-safe-nft-metadata-and-media-boundary.md)
- [ADR-0008: Versioned token observations and exact state reconciliation](../decisions/ADR-0008-versioned-token-observations-and-exact-state-reconciliation.md)
- [ADR-0013: Embedded SPA serving and browser security](../decisions/ADR-0013-embedded-spa-serving-and-browser-security.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P64-T01 | done | P30-T05, P40-T03 | Public exact-canonical NFT metadata contract, bounded display projection, image-link policy, and revised ADR | OpenAPI generation, metadata/catalog/HTTP unit and PostgreSQL integration tests |
| P64-T02 | done | P64-T01, P50-T03 | Bilingual ERC-721/ERC-1155 detail UI, ordered traits, NFT deep links, and per-open external-link confirmation | focused Vitest, embedded Playwright, responsive and accessibility checks |
| P64-T03 | done | P64-T02, P30 | Real IPFS Preview assertion plus production-image, race, security, deployment, and common-gate closure | Preview metadata, runtime, integration, E2E, and common repository gates |
| P64-T04 | done | P64-T03 | Exact immutable ERC-4906/ERC-1155 metadata-update observations, parser, schema, and accepted ADR revisions | parser, migration, sqlc, source, concurrency, and reorg tests |
| P64-T05 | done | P64-T04 | Metadata-role update discoverer plus exact event-driven source refresh for direct and bounded known-ID batch signals | component parity, exact RPC, deduplication, batch, and PostgreSQL tests |
| P64-T06 | done | P64-T05 | Canonical stale-while-refresh API projection and bilingual prior-content warning without media prefetch | OpenAPI generation, HTTP/Web, accessibility, and embedded browser tests |
| P64-T07 | done | P64-T06 | Live Preview event update plus production runtime, integration/race, security, deployment, and common-gate closure | Preview metadata, runtime, integration, E2E, and common repository gates |

Allowed item states are `todo`, `in_progress`, `blocked`, `done`, and `dropped`.

## Acceptance

- [x] The API returns only the newest retained NFT metadata observation whose
      exact height and hash remain canonical; orphan-only history is distinct
      from a missing observation.
- [x] Available documents expose bounded name, description, and ordered scalar
      traits without JavaScript number coercion, raw JSON, metadata source URIs,
      animation URLs, or external URLs.
- [x] HTTPS image destinations are syntactically safe and IPFS destinations use
      the configured HTTPS gateway; unsupported or unsafe destinations never
      become links.
- [x] ERC-721 retains exact owner state while ERC-1155 never invents a unique
      owner, and both standards share the same metadata detail route.
- [x] The SPA renders no NFT image and performs no media or external request
      before the user confirms each navigation.
- [x] Every external navigation is bilingual, keyboard accessible, opener-free,
      and no-referrer, with the target host visible before confirmation.
- [x] The authenticated `/media` proxy, its API-key requirement, and its
      SSRF/content validation remain unchanged.
- [x] Generated contracts, PostgreSQL, browser, Preview metadata, production
      runtime, race, security, deployment, and plan gates pass.
- [x] Canonical ERC-4906 single/batch and ERC-1155 URI events create immutable,
      orphan-retained update observations without trusting their URI payloads.
- [x] Direct update signals refresh their exact Token ID, while a batch signal
      visits only already discovered IDs in its closed range with bounded work.
- [x] Every event-driven source call stays on one state endpoint and uses the
      exact event block hash; stale blocks never enqueue or publish a refresh.
- [x] A same-URI standard update still refetches the document, while contracts
      that emit no standard update event receive no periodic or request-driven
      refresh.
- [x] Pending or failed refreshes retain the prior canonical available content
      with explicit latest/content observation identities and a bilingual stale
      warning; reorg removes the warning and restores the prior version.
- [x] Source, generation, PostgreSQL, browser, Preview metadata, production
      runtime, race, security, deployment, and plan gates pass for P64-T04-T07.

## Current Blockers

None.

## Evidence

- P64-T01 adds the generated `getNFTMetadata` contract, a repeatable-read
  canonical PostgreSQL projection with orphan-only detection, exact scalar
  trait formatting, bounded text, and HTTPS/IPFS external-link classification.
  ADR-0005 now distinguishes user-directed navigation from the unchanged
  authenticated media proxy. Focused metadata, HTTP, API-operation, and app
  tests plus Go vet and the OpenAPI contract check pass; the real PostgreSQL
  scenario is compiled and is scheduled through the owned integration gate in
  P64-T03.
- P64-T02 renders plain metadata text, ordered traits, exact truncation notices,
  and a non-prefetching external-link review control on the shared ERC-721 and
  ERC-1155 route. ERC-721 retains exact owner reads; ERC-1155 performs none.
  The full Web suite passes 31 files/344 tests, TypeScript lint and production
  build pass, and the host-authorized embedded Chromium gate passes all 22
  flows, including English/Chinese warning dialogs, an exact opener-free
  no-referrer popup, zero pre-confirmation external requests, responsive layout,
  and WCAG 2.1 A/AA scans.
- P64-T03 passes owned PostgreSQL 18 integration and integration-race; the
  integration packages complete in 144.80s and 162.06s respectively with
  newest-canonical selection, terminal states, fallback after reorg, and
  orphan-only rejection. `make test-preview-metadata` passes against the fixed
  public IPFS document with one 205-byte fetch, SHA-256
  `a87d3d327d1a2c7f839000c080e07cd152b49ddf653f1a5afa5144eeec103d8d`,
  the expected name/description and HTTPS gateway image link, a 401 anonymous
  media response, and restart-stable one-attempt persistence.
- P64-T03 production parity and closure pass: `make test-runtime-e2e` completes
  monolith in 31.23s and all-six-role distributed topology in 42.43s;
  host-authorized `make test-e2e` passes 22/22; `make generate-check`,
  `make plan-check`, and the aggregate `make check` pass. The aggregate includes
  Go vet, zero golangci-lint findings, ordinary/race and 31-file/344-test Web
  suites, govulncheck with no called vulnerability, gitleaks, high-severity npm
  audits, license checks, Buildx validation, Compose checks, and Helm rendering.
- P64-T04 adds migration `0058_nft_metadata_update_events`, generated SQL for
  exact update-log discovery and immutable canonical insert, strict ERC-4906
  single/batch and ERC-1155 URI parsing, stable malformed codes, and accepted
  ADR updates. `make source-check`, focused metadata/store/db tests, and the
  owned PostgreSQL 18 `make test-integration` pass; the integration package
  completes in 179.86s with concurrent idempotency, conflict, direct-mutation,
  stale-before-write, and retained-orphan assertions.
- P64-T05 registers `41-nft-metadata-updates` in the metadata role and
  production manifest, merges transfer/direct/bounded-known-ID batch candidates,
  and retains the exact EIP-1898 source authority. Focused metadata/app and
  source checks pass. The final owned PostgreSQL 18 `make test-integration`
  passes with the integration package at 183.84s, proving no-Transfer direct
  refresh, same-URI refetch, exact canonical selector, same-block deduplication,
  bounded batch IDs, ERC-1155 URI template expansion, and reorg-before-commit
  rejection. One prior full run reached the same passing metadata integration
  result before an unrelated one-second solc-js self-test timed out; its isolated
  host-external rerun passed in 0.20s before the clean full rerun.
- P64-T06 adds generated `content_observation`/`content_stale` fields and one
  repeatable-read selection over canonical update, source, fetch, and prior
  available content. Pending/error refreshes retain the old document and reorg
  restores the prior available identity. OpenAPI/SQL generation, source/plan
  checks, HTTP tests, 35-file/361-test Web suite, and owned PostgreSQL 18
  integration pass; the integration package completes in 174.80s. The
  host-authorized embedded Chromium gate passes all 24 flows, including
  English/Chinese stale warnings, both block identities, responsive WCAG scans,
  and zero image/media prefetch.
- P64-T07 extends the fixed Preview ERC-721 fixture with a no-Transfer
  `MetadataUpdate` transaction. `make test-preview-metadata` passes with the
  initial observation at block 4, the update at block 6, two retained source
  versions, exactly two 205-byte fetch attempts with SHA-256
  `a87d3d327d1a2c7f839000c080e07cd152b49ddf653f1a5afa5144eeec103d8d`,
  updated API content, and no third attempt after worker restart.
  `make test-schema-e2e` applies and reports migration 0058 compatible;
  `make test-runtime-e2e` passes monolith and distributed topologies in 84.72s;
  the final `make test-integration` and `make test-integration-race` pass with
  the integration package at 179.85s and 194.29s respectively; and the embedded
  Chromium gate remains 24/24. The final PostgreSQL regressions additionally
  prove that a batch signal does not create an API observation for an
  undiscovered in-range ID, the latest exact token classification wins over an
  older compatible classification, and an identical retained orphan update is
  still reported noncanonical. The final Preview rerun completes in 41.53s.
- P64-T07 closure passes `make source-check`, `make generate-check`,
  `make plan-check`, `git diff --check`, and the aggregate `make check`. The
  aggregate includes Go vet, zero golangci-lint findings, ordinary/race and
  35-file/361-test Web suites, govulncheck with no called vulnerability,
  gitleaks with no leaks, high-severity npm audits, license checks, Buildx
  validation, Compose checks, and Helm lint/rendering. The Hardhat fixture's
  transitive audit continues to report the repository-known eight low-severity
  `elliptic` findings with no available fix; the configured high-severity gate
  passes.
