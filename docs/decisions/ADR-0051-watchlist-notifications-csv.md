# ADR-0051: Private Watchlists, One-shot Browser Push and Bounded CSV

Status: accepted

## Decision

SIWE sessions on the PostgreSQL writer own all watch, notification and export
operations. API keys do not authorize these operations; writes require the
existing exact Origin and CSRF checks. Delivery uses the browser vendor's Web
Push service; no application-owned notification broker is needed.

Each user can watch 5 distinct addresses with a private label of up to 64
characters, transaction/ERC-20/ERC-721/ERC-1155 selectors and in/out/both direction.
Enrollment and notification re-enablement record the exact current canonical
tip under the chain lock; only later block numbers qualify. Deleting a watch stops future matching. Failed top-level transactions and mint/burn transfers
are included; self-transfers occur once. Internal calls and UserOperations are
outside this feature.

Browser notifications use Web Push with VAPID and an embedded same-origin
Service Worker. Only watches and session-bound browser subscriptions persist;
notification history, read state, delivery queues and delivery receipts do not.
Subscription endpoints are unique, limited to ten active subscriptions per user,
and stored with browser encryption keys, locale and the exact enrollment tip.
They cannot authorize API operations. Logout, expiry, revocation and disabled
users stop subsequent sends; in-flight external deliveries cannot be recalled.

The maintenance role elects one sender per chain with a PostgreSQL session lock.
It tails existing first-canonical outbox records and first successful Token
publications using bounded queries and volatile cursors. Startup, takeover and
database recovery establish a new boundary and discard backlog. Token replay
and block reattachment never repair or resend history. Delayed first Token
publication can notify only for blocks after both watch and subscription tips.
Each delivery rechecks current session, watch, canonicality and exact Token
publication before releasing database state and performing HTTP. The sender
uses eight slots, bounded source/follower pages and a ten-second block budget.
Overload, uncertainty and errors lose notifications rather than replaying them.
This is best-effort one-shot delivery, not an exactly-once guarantee.

Push requests use TTL 0 and are never retried. A push provider discards messages
when the device is unreachable. HTTP 404/410 removes the matching subscription;
other failures are redacted and counted. Endpoint URLs are HTTPS-only on port
443, have no credentials or fragments, reject redirects, and resolve exclusively
to public addresses which are pinned when dialing. No private data enters SSE.
Local development may explicitly set `push.unsafe_allow_private_networks`.
Like the metadata exception, this default-off boolean bypasses the public-IP
restriction, including Fake-IP, ordinary private and loopback addresses. It
requires no host or CIDR allowlist and affects only browser-push sends. HTTPS
port 443, normal TLS verification, no redirects, DNS answer bounds, IP pinning,
timeouts and response limits remain unchanged. Production must leave it disabled.
VAPID private keys belong only to maintenance/all; API receives the public key.
Provider failure does not withdraw core readiness or block indexing.

The browser asks permission only after a user gesture. One origin-scoped
subscription is shared across tabs and explicitly rebound after a new login.
The Service Worker handles push and same-origin transaction navigation only;
it has no fetch handler, offline cache or notification history. Visible alerts
contain a label or abbreviated address, activity kind and direction, never an
amount. Logout/account changes unsubscribe and close displayed notifications.
Browser support and OS delivery remain external capabilities. CSV retains the export contract below.

CSV is a documented exception to ADR-0003's successful JSON envelope: the
spec-first authenticated POST returns text/csv on success and the ordinary JSON
error envelope on failure. It accepts one address, one activity kind, UTC
[from,to) and direction. A writer repeatable-read snapshot pins the exact
canonical tip, checks continuous Core coverage and applicable Token publication,
and reads at most 10,001 records. The exported view is explicitly as of that tip.
No RPC or HTTP writes occur while the snapshot is open. Files use UTF-8, fixed
English headers, exact decimal integers, standard quoting and formula protection.

Limits are 31 days, 10,000 data rows, 16 MiB and 15 seconds of generation.
PostgreSQL admission allows five starts per rolling minute per account, one
active export per account and four per deployment. Expiring reservations recover
crashes; completion releases only its own token. Every error is returned before
CSV headers or bytes; partial files are never reported as successful. The SPA
uses its generated same-origin client and releases download object URLs.
Download filenames use `<address>-<start>-<end>-<type>.csv`, with the lowercase
address, UTC `YYYY-MM-DDTHH-mm-ssZ` timestamps and the requested activity kind.

## Consequences

New tables target the current fresh schema without historical backfills.
Maintenance and API components use the same builders in all/split deployments.
Watches, browser subscriptions and admission state remain writer-owned; optional Redis,
NATS and object storage do not participate. Limits do not charge prepaid API
balances. P60 owns watch/notification implementation evidence, P40 owns CSV
exports, P50 owns the private Web workspace, and P70 owns release acceptance.

## Optional CSV challenge

`turnstile.enabled` defaults off and is shared with browser verification and
future protected operations; there are no per-operation switches.
Enabled exports require `X-Turnstile-Token` with Siteverify action `csv_export`
and the configured public hostname. Validation follows SIWE/Origin/CSRF checks
and precedes export admission and the writer snapshot. Invalid challenges
return JSON 403 `turnstile_invalid`; upstream failures return JSON 503
`turnstile_unavailable`, before any CSV headers or bytes. The bounded transport,
secret isolation and non-persistence rules are shared with ADR-0024. Watch
operations are unaffected. The address toolbar opens the existing export form;
a challenge is mounted only while that authenticated form is open.
Submitting consumes the token immediately; the widget refreshes after the CSV
request finishes, on either success or failure, before another download.
