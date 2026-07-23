# P50 — Embedded Web

Status: `done`

## Outcome

A responsive Chinese/English React SPA covers the core explorer workflows,
communicates only with versioned APIs, is embedded in the Go binary, and uses an
injected EIP-1193 wallet for all contract reads and writes.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0003: Spec-first API and canonical public identifiers](../decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md)
- [ADR-0013: Embedded SPA serving and browser security](../decisions/ADR-0013-embedded-spa-serving-and-browser-security.md)
- [EIP-1193: Ethereum Provider JavaScript API](https://eips.ethereum.org/EIPS/eip-1193)
- [EIP-6963: Multi Injected Provider Discovery](https://eips.ethereum.org/EIPS/eip-6963)
- [Tailwind CSS with Vite](https://tailwindcss.com/docs/installation/using-vite)
- [Tailwind Preflight](https://tailwindcss.com/docs/preflight)
- [Testing](../testing.md)

## Work Items

| ID      | Status      | Depends on    | Deliverable                                                              | Verification              |
| ------- | ----------- | ------------- | ------------------------------------------------------------------------ | ------------------------- |
| P50-T01 | done        | P00           | React/Vite routing, generated API client, i18n, theme, design primitives | frontend unit/build tests |
| P50-T02 | done        | P40           | Home, blocks/orphans, transactions, addresses, and search pages          | Playwright core flows     |
| P50-T03 | done        | P20, P30, P40 | Token/NFT, contract, verify, charts, pending, and sync-status pages      | capability UI tests       |
| P50-T04 | done        | P30, P40      | EIP-6963 discovery and wallet-only contract read/write forms             | provider/mismatch tests   |
| P50-T05 | done        | P50-T01       | Embedded assets, deep-link fallback, cache headers, CSP, accessibility   | binary E2E and a11y tests |

## Acceptance

- [x] No frontend runtime service or external CDN is required.
- [x] Chinese/English, light/dark, keyboard, responsive, and WCAG 2.1 AA flows
      cover all primary pages.
- [x] Optional unavailable capabilities are explained or hidden consistently.
- [x] Contract calls require a discovered wallet on the configured chain; the
      backend never receives private key material or signs transactions.
- [x] RPC credentials and server-only settings do not exist in built assets.
- [x] `web/dist` is treated as a build-only artifact and must be generated
      during build/test pipelines, not checked into git.

## Current Blockers

None. All P50 work items and acceptance checks are complete.

## Evidence

- P50-T01: `make toolchain-check` passes the Go 1.26.5, Node 24.18.0,
  and npm 11.16.0 supported baselines. A clean
  `npm --prefix web ci` followed by
  `npm --prefix api run check:api`, `npm --prefix web run lint`,
  `npm --prefix web run test`, and `npm --prefix web run build` passes with 8
  test files and 32 tests. Coverage includes typed deep-link/search routing,
  the sole same-origin generated OpenAPI transport, large string quantities,
  first-load and switched Chinese/English document language, persisted
  light/dark theme, and Tailwind-backed design primitives; Vite emits only
  local content-hashed assets.
- P50-T01: `go test -race ./web -count=1` passes the embedded
  distribution checks, including absence of server configuration markers and
  external entrypoints in the built assets.
- P50-T01 commit/PR: none created because the repository has no `HEAD` and this
  task did not authorize a commit or pull request; evidence is bound to the
  current working tree.
- P50-T05: `make toolchain-check`, `go test -race ./web -count=1`,
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
- P50-T02: `npm --prefix web run check:api`, `npm --prefix web run lint`,
  `npm --prefix web run test`, and `npm --prefix web run build` pass with 10
  test files and 37 tests. Regressions cover opaque server-issued cursor
  round-trips and history, cache-bypassing invalid-cursor restart with a fresh
  first-page request, contiguous coverage versus higher live islands,
  exact-hash retained-orphan navigation, canonical-only block and transaction
  lists, exact-block address state, localized stage/state/account types with a
  stable diagnostic code, bilingual labels, and string-preserved large
  amounts.
- P50-T02: `PLAYWRIGHT_USE_BUNDLED=1 make test-e2e` builds the embedded SPA into
  a temporary Go binary and passes all 5 Playwright flows. The core flow covers
  Home, paginated blocks and transactions, transaction and address detail,
  search pagination, retained orphan detail, Chinese switching, keyboard
  activation, narrow populated-table layout, and complete WCAG 2.1 A/AA Axe
  scans in light English and dark Chinese. The scan found a bundled-Chromium
  contrast-analysis deadlock on the translucent body gradient and verifies the
  production solid theme background fix without disabling any WCAG rule.
- P50-T02 review closure: targeted
  `npm --prefix web run test -- CorePages.test.tsx App.test.tsx` passes 2 files
  and 15 tests; `npm --prefix web run lint`, `npm --prefix web run build`, and
  the full 10-file/37-test Vitest suite pass. With the rebuilt embedded
  distribution, `PLAYWRIGHT_USE_BUNDLED=1 npm --prefix web run test:e2e`
  passes all 5 flows, including an actual transaction second page whose cursor
  contains reserved `?`, `+`, `&`, `/`, `#`, and `=` characters and must
  round-trip unchanged to select its fixture. `make generate-check` also
  passes after the embedded-SPA changes.
- P50-T02: `go test -race ./web -count=1`, `make toolchain-check`,
  and `make generate-check` pass. No commit or pull request was created;
  evidence is bound to the current working tree.
- P50-T03: the generated same-origin client now drives token discovery and
  exact canonical NFT balances/ownership, code-hash-bound published
  verification artifacts, durable verification-job reads and guarded new
  submissions, `stats@2` ranges and exact-value tables, immutable pending
  snapshots, and separate indexed-stage/configured-feature status. Typed
  unavailable, failed, expired, disabled, and invalid-cursor states remain
  distinct from authoritative empty results. API keys stay in request headers
  and are excluded from URLs and query-cache identities.
- P50-T03: closing public verification submission no longer hides authenticated
  reads of already-published artifacts or durable jobs. Standard JSON receives
  bounded UTF-8, duplicate-key, nesting, object-shape, and safe-number checks
  before submission. Capability labels, exact large quantities, match/status
  values, and all new controls are covered in both Chinese and English.
- P50-T03: `npm --prefix api run check:api`,
  `npm --prefix web run lint`, `npm --prefix web run test`, and
  `npm --prefix web run build` pass; Vitest reports 11 files and 50 tests.
  `make toolchain-check generate-check`, `make lint`, `make test`,
  `go test -race ./web -count=1`, and `git diff --check` also pass.
- P50-T03: `PLAYWRIGHT_USE_BUNDLED=1 make test-e2e` builds the temporary
  Go binary with its `go:embed` distribution and passes all 6 Playwright flows.
  The added capability flow traverses token-to-NFT deep links, exact owner
  balances, disabled-submission job/artifact reads, aggregate charts, pending
  snapshots, and sync/capability state. It scans every P50-T03 route under
  light English and dark Chinese at desktop and narrow widths with the complete
  WCAG 2.1 A/AA ruleset, while proving keyboard activation, zero document
  overflow, and no external browser request.
- P50-T03 security checks: `gitleaks dir --no-banner --redact .` reports no
  leaks; `go test ./internal/auth ./internal/metadata ./internal/verify ./web`
  passes; the clean frontend install reports zero vulnerabilities. A fresh
  `govulncheck ./...` database refresh was attempted twice but
  `vuln.go.dev` timed out/reset the connection, so no fresh Go vulnerability
  result is claimed.
- P50-T03 commit/PR: none created because this task did not request a commit or
  pull request; evidence is bound to the current working tree.
- P50-T04: EIP-6963 discovery validates and snapshots bounded provider metadata,
  preserves the first provider for a UUID, caps the discovery set, and keeps the
  raw EIP-1193 provider private to the wallet boundary. The public context
  exposes only bounded display metadata and fixed `readContract` /
  `sendTransaction` capabilities. The exact five-method allowlist is
  `eth_requestAccounts`, `eth_accounts`, `eth_chainId`, `eth_call`, and
  `eth_sendTransaction`; every operation rechecks the selected account and
  configured chain.
- P50-T04: provider arrays, strings, quantities, calldata, and results are
  validated within explicit count/byte bounds before use. Account parsing does
  not trust array instance methods or Proxy getters. Preflight and completion
  bind the same wallet-session object, so disconnect/reconnect and account or
  chain ABA events cannot admit an old result. A write that reached the provider
  without a trustworthy hash or completion session reports an unknown outcome
  and requires wallet inspection before retry; hostile messages and data never
  reach the DOM.
- P50-T04: the address-only contract entry reaches one accessible shared
  read/write workbench without requiring a verification code hash. Shared value
  and calldata are bound to both payable reads and writes. Inputs stay frozen
  until an in-flight wallet request settles, stale write outcomes remain
  explicit, and connection/wrong-chain state remain distinct. Connect,
  disconnect, and external provider-disconnect transitions restore focus;
  disconnects are also announced through an always-mounted live region.
- P50-T04: targeted wallet, static-boundary, and capability-page regressions
  pass 3 files and 33 tests. The full `npm --prefix web run lint` and
  `npm --prefix web run test` gates pass with 11 files and 67 tests. Static
  checks prove the production SPA contains only the fixed wallet method
  allowlist, does not expose the raw provider, and has no wallet path through
  the generated backend transport. Regressions cover discovery/account/result
  limits, malformed and Proxy-backed responses, silent drift, request error
  codes, same-provider reconnect, account/chain ABA, and uncertain writes.
- P50-T04: `PLAYWRIGHT_USE_BUNDLED=1 make test-e2e` builds the temporary Go
  binary with its `go:embed` distribution and passes all 7 Playwright flows.
  The same-chain flow proves exact payable read/write provider payloads and
  bounded results, zero backend requests after entering the wallet boundary,
  absence of private-key or wallet-signing surfaces, stable rejection and
  uncertain-write handling, operation locking across account ABA, connect and
  disconnect focus recovery, external-disconnect announcements, long metadata
  containment, and complete WCAG 2.1 A/AA scans. The mismatch flow proves no
  call or transaction request is sent. The expanded 320-pixel pass found and
  verified the header-overflow regression fix.
- P50-T04 common gates: `make toolchain-check generate-check`, `make lint`,
  `make test`, `make security-check`, `go test -race ./web -count=1`, and
  `git diff --check` pass.
  `govulncheck ./...` reports no reachable code or imported-package
  vulnerabilities (one vulnerable required module is not called);
  `gitleaks dir --no-banner --redact .` reports no leaks;
  `go test ./internal/auth ./internal/metadata ./internal/verify ./web` passes;
  and the clean web install reports zero vulnerabilities.
- P50-T04 dependency closure: `api/package.json` overrides the generator's
  transitive `js-yaml` to 4.3.0; `npm --prefix api ls js-yaml --all` confirms
  the override, while clean API install and `npm --prefix api audit
  --audit-level=high` report zero vulnerabilities. `make security-check` now
  permanently audits both the API generator and frontend; its refreshed full
  run passes both audits, Go vulnerability analysis, worktree and Git-history
  leak scans, the generated-client build, and focused security tests.
- P50-T04 commit/PR: none created because this task did not request a commit or
  pull request; evidence is bound to the current working tree.
