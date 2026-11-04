# Embedded browser acceptance

[Testing index](../testing.md) · Commands run from the repository root.

- [Commands](#commands)
- [Watchlist, notification and CSV acceptance](#watchlist-notification-and-csv-acceptance)
- [Suspended route imports](#suspended-route-imports)
- [Browser verification acceptance](#browser-verification-acceptance)
- [Turnstile and address toolbar acceptance](#turnstile-and-address-toolbar-acceptance)
- [Manual explorer refresh acceptance](#manual-explorer-refresh-acceptance)

## Commands

- `make test-e2e`: build the embedded SPA and a temporary Go E2E binary, then
  run Playwright against that embedded distribution. Local runs use installed
  Chrome; CI sets `PLAYWRIGHT_USE_BUNDLED=1` after installing Playwright
  Chromium. The embedded Go API fixture includes independent EIP-7702
  delegation and clearing transactions so the suite proves lazy Authorization
  loading, deep links and cursor pages, applied/skipped tuples with raw
  signatures, transaction-time delegate calldata identity, clearing without
  stale fallback, bilingual keyboard accessibility, and 390px overflow safety.
  On a restricted macOS automation host that denies Chromium's Mach
  bootstrap rendezvous, first rerun the unchanged target outside the process
  sandbox with approval. If that is unavailable, use
  `PLAYWRIGHT_USE_BUNDLED=1 PLAYWRIGHT_SINGLE_PROCESS=1 make test-e2e` only as
  the documented diagnostic fallback; it gives every test an isolated worker
  because a single-process browser cannot safely reuse test contexts. A
  launched browser with blank pages still requires console/runtime diagnosis.
  CI remains on the ordinary multi-process browser.
  The same gate verifies route-lazy delivery, one durable browser event stream,
  event-driven home/status and manual list refresh, on-demand CodeMirror, and embedded
  precompressed asset behavior without console or CSP failures.
  It also browses the ERC-4337 global list, detail, transaction activity, and
  address activity against the generated client, including a decoded failure
  reason and feature-gated navigation.
  The suite also enables a deterministic generated-client ENS fixture to prove
  Viem normalization, name-detail navigation without source classifications, exact address
  disclosure, snapshot reuse, bilingual accessibility, and 390px overflow.

## Watchlist, notification and CSV acceptance

Private tools in P60, CSV exports in P40, and their Web surfaces in P50 extend
the existing gates. HTTP cases prove SIWE ownership, Origin/CSRF, bounded
subscription inputs and JSON-before-CSV failures. PostgreSQL cases use fresh
migrations and real Core/Token publication to check first-activity delivery,
replay suppression, account/session lifecycle, watch limits and CSV snapshots.
The fresh schema contains no notification history or durable notification queue.

Web Push transport tests decrypt encrypted payloads and verify VAPID, TTL 0,
single-attempt failures, redirect rejection and hostile-network bounds.
Private-network exception regressions cover default-off behavior, strict boolean
configuration, explicit Fake-IP/private/loopback opt-in, mixed DNS answers,
default rebinding rejection, checked-IP dialing and unchanged redirect/TLS boundaries.
Run `go test -race ./internal/netpolicy ./internal/config ./internal/watchlist
./internal/app` and `make deployment-check` for changes to this exception.
Browser tests cover explicit permission, unsupported/denied states, session isolation,
worker payload/click validation, embedded CSP/cache policy, bilingual layouts
and CSV downloads. Production all/split topology tests retain real SIWE, chain
activity and CSV acceptance. Controlled receivers are protocol evidence only;
real browser-provider delivery with the page closed requires separate HTTPS
browser evidence and must never be inferred from mocks or local receivers.

CSV bounds remain 31 days, 10,000 combined rows, 16 MiB and 15 seconds, including
exact uint256 values and continuous Core/Token coverage. All-type exports cover
transactions, ERC-20, ERC-721 and ERC-1155 in one snapshot, with per-row types,
self-transfer deduplication, direction/time boundaries and combined limits.
Reorg snapshot tests prove
that one exported file stays on its original canonical view.

## Suspended route imports

`web/src/router-lazy.test.tsx` exercises delayed imports both within `act` and
with the browser scheduler, where React 19.3 diagnoses a library that calls
`use(promise)` only before the import settles. The route adapter delegates
suspension to React.lazy while retaining TanStack preload/retry/error behavior.
The regression observes console errors without suppressing them; full browser
acceptance continues through `make test-e2e` against the embedded Go server.

The native EventSource recovery regression holds the lazy blocks-page chunk
until the app stream opens, then waits for populated route data before sending
a head event. It removes interception before observing native reconnect headers
and checks fresh requests separately after the head and cursor recovery. SSE
readiness alone does not establish that a lazy page has query observers.

## Browser verification acceptance

P50-T78 covers the two-step Verify page, session-only `/verification-jobs`
operations, and preserved API-key-only native/Etherscan protocols. HTTP tests
exercise Origin/CSRF, revocation, capability switches, request admission and
redacted status projection before expensive work. Frontend and embedded
browser tests cover drafts through login/return, result links and refresh,
account-change cancellation, terminal polling, bilingual mobile accessibility,
and separate local/Sourcify results. Production `make test-runtime-e2e` includes
real SIWE login, Geas address verification with and without a creation
entrypoint, and native-key submission in both all and split roles. PostgreSQL
regressions accept omitted/null creation matches for runtime-only Geas, reject
invented or missing required match evidence, and check published artifact reads.
These local checks do not establish remote CI acceptance.

## Turnstile and address toolbar acceptance

Run focused `internal/turnstile`, config, HTTP, app and embedded Web tests,
frontend tests, and the embedded browser suite. Cover the shared default-off
switch enabling/disabling every protected operation, removed configuration
rejection, public config omission while disabled,
secret role isolation, authentication before validation, token bounds/expiry and
replay rejection, action/hostname mismatch, redirects, timeout/cancellation,
malformed/oversized responses, JSON-before-CSV failures and unchanged native
API-key routes. Widget tests cover lazy loading, failure/retry, single-use tokens,
account changes and cleanup across all browser submission variants and CSV.
For CSV, hold the response pending to verify that the widget refreshes only
after success or failure; assert the downloaded filename includes the selected
address, UTC range and activity kind.
Check the address watch icon immediately after the QR control and the CSV link
at the bottom right of ordinary transaction history, in both languages/themes
at desktop and 390px. Cover empty history, keyboard navigation, accessibility,
and direct loading of the export page. Watch clicks stay on the address page;
anonymous clicks open a half-shaded modal with Escape/backdrop dismissal, focus
containment and restoration. Login closes the dialog without watching; a second
click is required. Watch operations show a loading icon and block repeat clicks
until the mutation and list refresh finish, then show the confirmed state. Both
success and failure use the shared top-center Message. Cover add/remove, slow
responses, failure, account changes and automatic three-second dismissal. The
export login card uses a short, content-width button aligned with
its message; login never starts a download. Cover All as the default type and
the `-all.csv` filename.

Shared operation feedback uses Message across account, wallet, verification,
contract interaction, billing, copying and download controls. Check stacking,
deduplication, manual dismissal, hover/focus timer suspension, translated close
controls, modal top-layer accessibility, teardown and reduced motion. Include
390px and desktop in both languages/themes. Loading/read failures, field
validation and durable transaction/payment outcomes remain in their owning
views; they must not disappear with a transient message.
Watch reads must fail closed with retry; account changes and logout must cancel
private operations and clear results. CSV challenges mount only on the signed-in
export page and are removed on navigation.

Acceptance requires `make check`, `make test-e2e`,
`make test-integration-race` and `make test-runtime-e2e`. Controlled transport and
browser fixtures establish local behavior only; production-domain Cloudflare
widget validation is a separate operator smoke test.

## Manual explorer refresh acceptance

P50-T11 restricts passive requests to home/status and verification/payment task
progress. Test heads, publication bursts, reorgs, initial SSE opening, native
reconnect and online recovery: ordinary views must retain request counts and
pagination. In-range publications mark snapshots; out-of-range events do not.
Manual refresh preserves route, tab, filters and drafts, resets visible pagers
only, and rejects late cancelled results. Verify failed refresh retry, invalid
cursor blocking, binding guards, identity-mismatch hiding and ENS expiry without
requests. New addresses must not renew existing names.

Run focused frontend/browser regressions followed by `make web-test`,
`make web-lint`, `make test-e2e`, `make generate-check`, `make docs-check` and
`make plan-check`. Browser acceptance includes both languages and mobile layout.
Check title alignment, mobile overflow, status indicators, keyboard activation
and focus/Escape tooltip behavior in `manual-refresh.spec.ts`. These checks
establish local behavior, not remote CI or live chain acceptance.
