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
| P64-T03 | done | P64-T02, P60 | Real IPFS Preview assertion plus production-image, race, security, deployment, and common-gate closure | Preview metadata, runtime, integration, E2E, and common repository gates |

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
