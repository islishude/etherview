# P50 — Embedded Web

Status: `in_progress`

## Outcome

A responsive Chinese/English React SPA covers the core explorer workflows,
communicates only with versioned APIs, is embedded in the Go binary, and uses an
injected EIP-1193 wallet for all contract reads and writes.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0003: Spec-first API and canonical public identifiers](../decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md)
- [ADR-0013: Embedded SPA serving and browser security](../decisions/ADR-0013-embedded-spa-serving-and-browser-security.md)
- [Tailwind CSS with Vite](https://tailwindcss.com/docs/installation/using-vite)
- [Tailwind Preflight](https://tailwindcss.com/docs/preflight)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P50-T01 | done | P00 | React/Vite routing, generated API client, i18n, theme, design primitives | frontend unit/build tests |
| P50-T02 | todo | P40 | Home, blocks/orphans, transactions, addresses, and search pages | Playwright core flows |
| P50-T03 | todo | P20, P30, P40 | Token/NFT, contract, verify, charts, pending, and sync-status pages | capability UI tests |
| P50-T04 | todo | P30, P40 | EIP-6963 discovery and wallet-only contract read/write forms | provider/mismatch tests |
| P50-T05 | done | P50-T01 | Embedded assets, deep-link fallback, cache headers, CSP, accessibility | binary E2E and a11y tests |

## Acceptance

- [x] No frontend runtime service or external CDN is required.
- [ ] Chinese/English, light/dark, keyboard, responsive, and WCAG 2.1 AA flows
      cover all primary pages.
- [ ] Optional unavailable capabilities are explained or hidden consistently.
- [ ] Contract calls require a discovered wallet on the configured chain; the
      backend never receives private key material or signs transactions.
- [x] RPC credentials and server-only settings do not exist in built assets.

## Current Blockers

P40 is not complete.

## Evidence

- P50-T01: `make toolchain-check` passes the exact Go 1.26.5, Node 24.18.0,
  and npm 11.16.0 repository pins. A clean
  `npm --prefix web ci --ignore-scripts` followed by
  `npm --prefix web run check:api`, `npm --prefix web run lint`,
  `npm --prefix web run test`, and `npm --prefix web run build` passes with 8
  test files and 32 tests. Coverage includes typed deep-link/search routing,
  the sole same-origin generated OpenAPI transport, large string quantities,
  first-load and switched Chinese/English document language, persisted
  light/dark theme, and Tailwind-backed design primitives; Vite emits only
  local content-hashed assets.
- P50-T01: `go test -race ./internal/webui -count=1` passes the embedded
  distribution checks, including absence of server configuration markers and
  external entrypoints in the built assets.
- P50-T01 commit/PR: none created because the repository has no `HEAD` and this
  task did not authorize a commit or pull request; evidence is bound to the
  current working tree.
- P50-T05: `make toolchain-check`, `go test -race ./internal/webui -count=1`,
  `npm --prefix web run lint`, and `npm --prefix web run test` pass; Vitest
  reports 9 files and 33 tests. Handler regressions cover HTML media-range
  precedence, malformed quality values, reserved and asset-shaped misses,
  exact eight-character Vite base64url hashes, revalidating non-hashed files,
  real-file `HEAD`, SHA-256 ETags, conditional responses, and security headers.
- P50-T05: `make test-e2e` explicitly builds a temporary Go binary containing
  the `go:embed` distribution and passes 4 Playwright flows. The suite proves
  deep-link isolation, no-store shell/miss behavior, immutable hashed assets,
  conditional-response CSP/security headers, no external browser requests,
  keyboard skip navigation, narrow layout, reduced motion, and WCAG 2.1 A/AA
  scans in both light English and dark Chinese. The expanded scan found the
  dark-theme filled-control contrast regression and verified its fix. The
  `make generate-check` no-drift gate also passes.
- P50-T05 commit/PR: none created because this task explicitly requested no
  commit; evidence is bound to the current working tree.
