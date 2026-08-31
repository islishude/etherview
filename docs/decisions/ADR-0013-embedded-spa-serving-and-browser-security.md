# ADR-0013: Embedded SPA serving and browser security boundary

Status: accepted

## Context

Etherview ships one Go binary and never runs a production frontend service.
That makes the embedded-file handler part of the public security boundary: an
over-broad SPA fallback can disguise an API or operational miss as a successful
HTML response, while incorrect cache policy can pin an obsolete index document
or serve mutable bytes under an immutable lifetime. The browser shell also
needs a policy that cannot silently grow external script, style, font, or
worker dependencies.

## Decision

- Vite writes the complete production distribution under
  `web/dist`, and `go:embed` packages that directory into the Go
  binary. `index.html` may reference only same-origin, content-hashed build
  assets; production has no CDN or frontend runtime service.
- Route components are dynamic imports. Feature pages, ECharts, verified-source
  rendering, and CodeMirror are absent from the initial shell graph; the source
  editor loads only after an exact verified artifact is displayed. The checked
  asset graph has explicit raw/compressed budgets and forbidden feature-chunk
  patterns.
- The build emits deterministic Gzip and Brotli representations for
  compressible hashed assets plus a bounded manifest containing every
  representation's path, size, and SHA-256 digest. The embedded handler loads
  that manifest once, negotiates `br`, `gzip`, or identity, emits
  representation-specific strong ETags and `Vary: Accept-Encoding`, and uses
  identity for range requests. Sidecars and the manifest are never directly
  addressable.
- `web/dist` is a build-only artifact and is not committed; the repository
  stores source and embed logic, while build pipelines generate distribution
  files before verification and packaging.
- Existing embedded files support `GET` and `HEAD`. A missing path receives the
  SPA index only for a safe, non-reserved `GET` request that accepts HTML.
  API, Etherscan compatibility, health, metrics, asset-shaped, malformed, HEAD,
  and mutating requests never receive the fallback.
- `index.html`, fallback documents, misses, and method errors are `no-store`.
  Exact Vite `assets/<name>-<8-character-hash>.<extension>` files receive a
  one-year immutable policy and a SHA-256 ETag. Other real files must
  revalidate and are never inferred to be immutable merely because their name
  contains a dash.
- Every handler response, including errors and conditional responses, carries
  `nosniff`, same-origin resource policy, frame denial, no-referrer, a bounded
  permissions policy, and the repository browser-security headers. The CSP
  starts with `default-src 'none'`, enumerates only required same-origin
  resource types, permits `data:` only for wallet-provided images, and contains
  neither `unsafe-inline`, `unsafe-eval`, nor an external network origin.
- Each `GET` or `HEAD` response that serves the SPA shell (the root or an HTML
  navigation fallback) generates a fresh, unpredictable
  32-byte CSPRNG nonce encoded with base64url. The handler injects that raw
  nonce into trusted shell head metadata and adds only the matching
  `'nonce-<value>'` source to `style-src`. CodeMirror reads the metadata and
  supplies the raw value through `EditorView.cspNonce` for its generated style
  element. This is the sole intended inline-style exception: application
  scripts, arbitrary inline CSS, and inline style attributes remain forbidden;
  `style-src-attr` is not relaxed, and resources/errors retain the baseline
  CSP without a nonce.
- The baseline shell keeps semantic landmarks, a keyboard skip link, visible
  language and theme controls at narrow widths, reduced-motion behavior, and
  an exact-value alternative for charts. Vitest runs deterministic semantic
  accessibility checks; Playwright runs the full WCAG 2.1 A/AA scan against
  the built distribution served by the Go harness.

## Consequences

Adding an inline bootstrap, external resource, worker, iframe, or new browser
resource type requires an explicit revision of this decision and its CSP tests.
Only the trusted, per-response CodeMirror runtime style may consume the shell
nonce; no other inline style or script may do so. Precompression never applies
to the nonce-bearing shell. New public routes under a
reserved namespace remain protected even before a route handler exists. A
developer-server rendering or source-tree scan alone cannot satisfy the browser
boundary: cache, fallback, nonce, and header regressions are tested against the
embedded Go artifact.
