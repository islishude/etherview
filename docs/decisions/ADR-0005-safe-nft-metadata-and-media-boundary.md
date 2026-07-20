# ADR-0005: Safe NFT metadata and media boundary

Status: accepted

## Context

NFT metadata and image locations are controlled by untrusted contracts and can
target internal networks, redirect after validation, change DNS answers, serve
active content under an image name, or return unbounded bodies. Accepting a URL
from a public media endpoint would also turn Etherview into a general-purpose
request proxy. Cached or mirrored media would create a second persistent truth
with separate invalidation and content-moderation obligations.

## Decision

- External NFT metadata remains optional enrichment. Its document and stable
  outcome are stored in PostgreSQL and bound to the exact chain, token address,
  token ID, observed block number, and observed block hash.
- The media endpoint selects `image` only from an `available` metadata document
  whose observed block hash is still the current canonical mapping at that
  height. It never accepts an upstream URL from the HTTP request. Orphaned,
  pending, unavailable, unsafe, errored, and missing-image states remain
  explicit and distinct.
- Each media request fetches the selected URI through the same SSRF-resistant
  HTTPS/IPFS policy. Redirects and every DNS result are checked, environment
  proxies are disabled, private and special-purpose addresses are rejected,
  response size and time are bounded, and only PNG, JPEG, GIF, WebP, and AVIF
  bytes with matching signatures are returned.
- Media bytes are not persisted. Success and error responses are `no-store`,
  use `nosniff`, a restrictive CSP and same-origin resource policy, and expose
  only fixed filenames and typed media state. Source and resolved URIs are not
  returned or logged at the public boundary.
- The media endpoint always requires an authenticated API-key identity before
  database selection or network access. A deployment without configured key
  authentication returns a typed authorization failure and cannot expose this
  expensive capability anonymously.

## Consequences

Media availability depends on the current upstream on every request and the
endpoint cannot provide a durable CDN cache. An upstream outage or newly unsafe
DNS answer is surfaced as a typed failure instead of stale content. Supporting
another scheme, media format, cache, mirror, or client-supplied source requires
revisiting this security boundary and adding parser, SSRF, size, and active
content regressions first.
