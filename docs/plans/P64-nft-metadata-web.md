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
| P64-T01 | done | P20, P40-T03 | Public exact-canonical NFT metadata contract, bounded display projection, image-link policy, and revised ADR | OpenAPI generation, metadata/catalog/HTTP unit and PostgreSQL integration tests |
| P64-T02 | done | P64-T01, P50-T03 | Bilingual ERC-721/ERC-1155 detail UI, ordered traits, NFT deep links, and per-open external-link confirmation | focused Vitest, embedded Playwright, responsive and accessibility checks |
| P64-T04 | done | P64-T02, P30 | Exact immutable ERC-4906/ERC-1155 metadata-update observations, parser, schema, and accepted ADR revisions | parser, migration, sqlc, source, concurrency, and reorg tests |
| P64-T05 | done | P64-T04 | Metadata-role update discoverer plus exact event-driven source refresh for direct and bounded known-ID batch signals | component parity, exact RPC, deduplication, batch, and PostgreSQL tests |
| P64-T06 | done | P64-T05 | Canonical stale-while-refresh API projection and bilingual prior-content warning without media prefetch | OpenAPI generation, HTTP/Web, accessibility, and embedded browser tests |

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

None. P70/P73 retain independent release blockers.

## Evidence

Retained completed milestones: P64-T01, P64-T02, P64-T04, P64-T05, P64-T06.

Generated contracts, metadata/parser/API tests, PostgreSQL concurrency and
reorg cases, bilingual frontend and embedded browser checks passed. Production
runtime and Preview acceptance covered exact NFT observations, event-driven
ERC-4906/ERC-1155 refresh, known-ID batch bounds and stale-content projection.
The current reproducible Preview gate uses offline local Kubo; it is not proof
of public-IPFS availability.

[Detailed completion record](https://github.com/islishude/etherview/blob/e665a0b9b5dcee02e6d90d3a2224a76031cbda3d/docs/plans/P64-nft-metadata-web.md#evidence).
These are historical results, not tests rerun by this plan cleanup.
