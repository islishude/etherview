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
  token ID, observed block number, and observed block hash. The logical
  resource key is paired with an exact block-hash identity key, so a later
  observation never overwrites an orphan that may become canonical again.
  Exact source observations and terminal fetch outcomes are write-once;
  identical concurrent writes are no-ops and disagreements are integrity
  failures.
- The metadata role discovers ERC-721 `tokenURI(uint256)` and ERC-1155
  `uri(uint256)` from canonical transfer candidates and exact standard update
  signals: ERC-4906 `MetadataUpdate`/`BatchMetadataUpdate` and ERC-1155 `URI`.
  Update logs are validated, stored by exact block hash/log identity, immutable,
  and retained after reorg. Their payload is only a trigger; one state RPC
  endpoint and one EIP-1898 block-hash selector remain the source authority for
  the whole exact observation. ERC-1155 `{id}` templates use the required
  64-character lowercase hexadecimal token ID. The canonical mapping is
  rechecked after the RPC call and before a successful URI enters the durable
  fetch queue. Reverts, malformed results, and exact-state capability gaps
  become stable source observations; transient transport failures remain
  retryable without persisting nested RPC text.
- A direct standard update refreshes its exact token ID. A batch ERC-4906 range
  walks only token IDs already discovered from canonical transfers or retained
  exact source observations and produces one bounded candidate at a time; it
  never numerically expands an untrusted uint256 range. Transfer and update
  signals for the same contract, token ID, and block hash collapse to the
  block's final post-state URI. Every accepted standard update refetches the
  document even when the URI string is unchanged. There is deliberately no
  periodic, request-triggered, or manual refresh for contracts that omit the
  standard events.
- The media endpoint selects `image` only from an `available` metadata document
  whose observed block hash is still the current canonical mapping at that
  height. It copies the bounded source identity, releases the database query,
  fetches externally, and then verifies that the same exact observation is
  still the newest canonical one before returning bytes. It never accepts an
  upstream URL from the HTTP request. Orphaned, pending, unavailable, unsafe,
  errored, and missing-image states remain explicit and distinct. A reorg may
  select an older retained canonical document; an observation that changes
  during the fetch is rejected instead of returning stale bytes.
- The native metadata display endpoint is a separate, bounded read projection.
  It selects the newest exact canonical update/source/fetch signal and the
  newest canonical available document in one repeatable-read snapshot. While a
  newer refresh is pending or failed, the prior available document remains
  visible with distinct latest and content observation identities plus an
  explicit stale marker. It returns only inert name/description text, at most
  100 ordered scalar traits, exact observation identity, and a typed image-link
  state. It never returns raw JSON, the metadata source/resolved URI,
  `animation_url`, or `external_url`. Orphan-only and missing histories stay
  distinct.
- A displayable `image` value is navigation data, not validated media. Absolute
  HTTPS values may be returned after rejecting credentials, fragments, control
  characters. Localhost and non-public IP literals are rejected unless their
  exact HTTPS origin is explicitly configured in
  `metadata.image_navigation_allowed_origins` (default empty). This navigation-only
  exception matches normalized hostname and effective port, never wildcards or
  subdomains, and applies equally to direct HTTPS and resolved IPFS images.
  It grants no metadata or media fetch authority. Valid `ipfs://` values are
  converted through the configured HTTPS gateway; every other scheme is
  non-linkable. The SPA never embeds or prefetches the target. It reveals the
  target host, warns that the NFT author controls the destination, and requires
  a fresh explicit confirmation before each opener-free, no-referrer external
  navigation. This boundary does not claim DNS or content safety and does not
  replace the authenticated media proxy.
- Each media request fetches the selected URI through the same SSRF-resistant
  HTTPS/IPFS policy. Redirects and every DNS result are checked, environment
  proxies are disabled, private and special-purpose addresses are rejected,
  response size and time are bounded, and only PNG, JPEG, GIF, WebP, and AVIF
  bytes with matching signatures are returned.
- `metadata.unsafe_allow_private_networks` is a default-off, development-only
  exception for a metadata-only worker. Role validation rejects it for `all`,
  `api`, mixed roles, or a disabled NFT-metadata feature. The checked-in
  Preview enables it only in the split `metadata` process for its controlled
  local Kubo HTTPS gateway (and Docker Desktop fake-IP routing when an operator
  explicitly selects a public gateway). API media fetches, `deploy/compose.yaml`, and
  Helm remain strict. URL, TLS, redirect, time, size, content, and
  document validation are unchanged, and a successful non-public connection
  records the bounded `network.policy_bypassed=true` diagnostic. This escape
  hatch is not a production SSRF policy or authorization to target unrelated
  private services. The Preview gateway terminates HTTPS with a dedicated local
  certificate, and only the metadata worker receives the public CA. Its TLS
  private key belongs only to the gateway proxy. The Kubo management API is a
  separate loopback-only operator endpoint, never proxied by the HTTPS gateway.
  Daily Preview may retrieve IPFS content over the network. Acceptance uses an
  offline Kubo daemon with `Gateway.NoFetch=true`, seeds
  the reviewed content locally, and checks the actual owned gateway addresses.
  This proves local IPFS integration, not public gateway availability.
- Media bytes are not persisted. Success and error responses are `no-store`,
  use `nosniff`, a restrictive CSP and same-origin resource policy, and expose
  only fixed filenames and typed media state. Metadata document source and
  resolved URIs are not returned or logged at the public boundary; the separate
  display projection may return only the reviewed external image navigation
  target described above. These headers wrap authentication and rate limiting,
  so early rejection responses retain the same boundary.
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

Opening a display link deliberately leaves Etherview. The external site sees a
direct browser request and may return non-image, malicious, misleading, or
tracking content despite the syntactic link policy. The mandatory confirmation,
target disclosure, no-referrer navigation, and lack of automatic loading make
that user-directed risk explicit without granting the server anonymous outbound
fetch authority.

### Preview gateway implementation

Preview uses the official pinned Caddy image and the shared `preview-gateway`
service. The IPFS virtual host accepts only GET/HEAD `/ipfs/` reads with no body
or upgrade; raw escaped paths reject traversal, nested encoded separators and
control characters before Caddy's normal path matching. Query encoding and
streamed bytes are preserved. Kubo's management API is never proxied.

The same listener routes only `app`, `ipfs`, `geth`, and `beacon` under
`etherview.localhost`. Caddy terminates TLS using the operator-generated mkcert
wildcard pair, mounted only into Caddy with its key at 0600. Automatic issuance,
HTTP redirects and the admin API are disabled; rotation requires recreation.
The app uses container-network HTTP while its HTTPS public origin remains the
SIWE and Secure-cookie authority (ADR-0027). Metadata alone trusts the public CA
and opts into its controlled private gateway; API media policy stays strict.
Preview explicitly allowlists only `https://ipfs.etherview.localhost` for image
navigation. Images resolved through that gateway are available as unverified,
user-confirmed external links. Other local origins remain `unsafe` without a
navigation URL; the API media proxy retains its strict network policy. The
allowlist covers only the initial destination, not subsequent browser redirects.

Node routes support Geth JSON-RPC/WSS, Beacon `/eth/` REST/SSE and Bundler
JSON-RPC. The local Geth host accepts every Origin, including browser extensions,
`null` and requests without Origin, with `Access-Control-Allow-Origin: *` and
local OPTIONS responses. This permits wallet RPC access without configuring
extension origins. Beacon and Bundler retain the explicit space-separated Origin
allowlist: requests without Origin are accepted, while null/unlisted origins
are rejected. `PREVIEW_NODE_ALLOWED_ORIGINS` does not restrict Geth. The gateway
strips upstream CORS and Origin on Geth and Beacon proxy requests and does not
enable credentialed cross-origin requests. It never proxies Engine or validator
APIs and does not expand the SPA CSP.

Only the loopback HTTPS host port is published. A container-loopback health
listener checks gateway serving, independently of upstream availability. Core
roles never depend on gateway readiness. Connections and headers have five- and
thirty-second limits; IPFS read/write inactivity is bounded to thirty seconds.
Node bodies are bounded to 32 MiB, subscriptions to 24 hours, and shutdown to ten
seconds. No node writes are retried. Proxy failures are redacted; request URLs,
bodies, credentials and nested upstream errors are not logged. Actual-container
regressions own these contracts, including hostile paths and long idle streams.
