# Accounts and browser services

[Operations index](../operations.md) · Commands run from the repository root.

- [API-key and label administration](#api-key-and-label-administration)
- [Wallet-user authentication administration](#wallet-user-authentication-administration)
- [Private watches, notifications and CSV exports](#private-watches-notifications-and-csv-exports)
- [Optional Turnstile protection](#optional-turnstile-protection)

## API-key and label administration

API-key create and rotate output plaintext exactly once. Capture it directly
into the intended secret manager and do not copy it into tickets or logs.

```sh
etherview admin api-key create --config /etc/etherview/config.yaml \
  --name incident-reader --rate 20 --burst 40 \
  --scope api:read --scope contract:verify
etherview admin api-key rotate OLD_PREFIX --config /etc/etherview/config.yaml
etherview admin api-key revoke PREFIX --config /etc/etherview/config.yaml
etherview admin api-key list --config /etc/etherview/config.yaml
```

Omitting `--scope` grants both maintained scopes. `api:read` covers keyed read
quota, NFT media, and configured API-key billing bypass;
`contract:verify` covers the native and Etherscan verification workflow.
Self-service keys are separately controlled by `features.user_api_keys` and
the fixed policy under `user_auth`; the CLI remains able to inspect and revoke
all operator and user-owned keys.

Label administration is chain-scoped and accepts only canonical address,
transaction hash, block hash/height, token, or contract identities. Use
`admin label list` to verify a change after set/delete.

## Wallet-user authentication administration

Wallet authentication is disabled by default. Initialize the current baseline
while the feature remains off, then configure the root
public HTTPS origin, enable `features.user_auth`, and inject an independent
session pepper of at least 32 random bytes only into the `all` or `api` process.
The migration, sync, enrichment, verification, metadata, and maintenance roles
must not receive that Secret. Plain HTTP is valid only for a loopback
development origin.

The first administrator cannot be created from an API key or a connected
wallet alone. The target wallet must first complete the ordinary SIWE login so
its chain-scoped user row exists. An operator can then promote that address
through the PostgreSQL writer:

```sh
etherview admin user set-role \
  --config /etc/etherview/config.yaml \
  --address 0x0000000000000000000000000000000000000001 \
  --role admin
```

The existing session observes the new role on its next writer-backed request;
no new Cookie is required. Use the same writer-only operator path to disable or
re-enable a user and to revoke all of one user's sessions:

```sh
etherview admin user set-status \
  --config /etc/etherview/config.yaml \
  --address 0x0000000000000000000000000000000000000001 \
  --status disabled
etherview admin user set-status \
  --config /etc/etherview/config.yaml \
  --address 0x0000000000000000000000000000000000000001 \
  --status active
etherview admin user revoke-sessions \
  --config /etc/etherview/config.yaml \
  --address 0x0000000000000000000000000000000000000001
```

Disabling a user atomically revokes active sessions; re-enabling that user does
not revive them. Role changes keep sessions but take effect on their next
writer-backed authorization check across every API replica. Command output is
bounded JSON and never contains session or CSRF material.
The `admin user` commands force a non-API maintenance configuration before
environment Secret-file loading. Their runtime operation uses only the
PostgreSQL writer, and configuration loading never opens session, x402
fingerprint, or facilitator-header Secret files.

Rotating `ETHERVIEW_SESSION_PEPPER` invalidates every session Cookie without changing
user, role, or audit rows. Roll all `all`/`api` replicas promptly so replicas do
not temporarily disagree about Cookies, then require every user to sign in
again. Push senders do not hold that pepper: when revoking browser push as part
of rotation, revoke the affected users' sessions with `admin user revoke-sessions`
or disable push on maintenance/all before rotation. Never reuse the API-key or
x402 fingerprint pepper.

For rollback, disable `features.user_auth` and restart the API processes; keep
the additive tables for audit and a later retry. The feature flag alone does
not mutate session rows. If a later re-enable must not accept any still-current
Cookie, rotate the session pepper as part of the rollback before restarting.

## Private watches, notifications and CSV exports

With `features.user_auth` enabled, users manage Watchlist in `/account` and on
address pages. Watches remain private and durable, with a 5-address limit
including paused entries. Enrollment/re-enablement starts after the current
canonical tip. CSV export remains available without browser push.

Browser push is optional and disabled by default. Set `push.enabled`,
`push.public_key` and `push.contact` (mailto or HTTPS operator contact), or
`ETHERVIEW_PUSH_ENABLED`, `ETHERVIEW_PUSH_PUBLIC_KEY`, `ETHERVIEW_PUSH_CONTACT`.
Generate and retain a standard P-256 VAPID key pair; supply its unpadded base64url
private key with `ETHERVIEW_PUSH_PRIVATE_KEY` or `_FILE` only to maintenance/all.
The public key is the uncompressed P-256 point in unpadded base64url. Changing the
key pair requires browsers to disable and re-enable their subscriptions. Helm
uses `secrets.pushPrivateKeyKey`; ExternalSecret deployments additionally set
`externalSecret.pushPrivateKeyRemoteKey`. File-based secrets require an explicit
read-only mount. Local and Docker builds embed the same `/push-sw.js` artifact.
Migration and offline administration neither load the private key nor start a
sender, even when the shared configuration enables push.

Use HTTPS and the Watchlist button to grant permission and enable this browser.
There are at most ten active subscriptions per account. Subscriptions are bound
to the current login and stop on logout, session expiry/revocation or user
disablement. Closing the page does not end the login. Unsupported browsers and
permission denial are shown in the UI; this does not provide email or Webhooks.

See [Browser push](../../preview/docs/operations.md#browser-push).

When validating background click navigation, close DevTools and allow the
Service Worker to stop before sending a real transaction notification, then
allow it to stop again before clicking. Keep this check separate from browser
automation. Injected push tests verify notification content and the retained
transaction target; they do not establish native OS click delivery.
If a notification arrives but clicking it only activates Chrome or does nothing,
check the browser's native notification callback and helper-process logs before
changing application routes.

For local development with Stash/Clash Fake-IP DNS or a private push receiver,
set `push.unsafe_allow_private_networks: true`, or set the following environment
override on the sender:

```dotenv
ETHERVIEW_PUSH_UNSAFE_ALLOW_PRIVATE_NETWORKS=true
```

The default is `false`. Like `metadata.unsafe_allow_private_networks`, enabling
this option bypasses the public-IP restriction, including for Fake-IP, ordinary
private and loopback addresses. No host or CIDR lists are required. It applies
only to the browser-push sender; other outbound clients retain their own policy.
HTTPS port 443, TLS hostname/certificate validation, no redirects, DNS answer
limits, checked-IP dialing, timeouts and response size limits remain enforced.
Use this unsafe exception only in a trusted development environment. Helm uses
`config.push.unsafe_allow_private_networks`; Compose passes the environment
setting only to maintenance/all. Build the updated image and recreate maintenance
to apply it, then trigger a new matching transaction: restart discards backlog.

Maintenance runs `43-browser-push` only when enabled. One PostgreSQL session lock
per chain selects the sender. It uses first-canonical and first successful Token
publications, volatile cursors and bounded work. Restart/recovery discards backlog;
replay and reattachment do not resend. Failed top-level transactions and token
mint/burn are included, self-transfers once. Token activity waits for publication.

Every message is attempted once with TTL 0; offline devices, provider failures,
timeouts and overload lose that alert. There is no history, unread count, retry
queue or delivery guarantee. An already sent system notification cannot be
retracted after a reorg. HTTP 404/410 removes expired endpoints. Inspect
`etherview_browser_push_total{result="sent|failed|dropped|expired"}` and the stable
`browser_push_unavailable` warning; no endpoint, credentials or activity payload
is logged. Public SSE carries no private watch data. Provider failures do not
withdraw core readiness. Normal authentication housekeeping removes invalid
subscriptions and export admissions even when browser push is disabled.

CSV downloads require SIWE and CSRF and do not debit API credit. Choose one
address, transaction/ERC-20/NFT type, direction and UTC `[from,to)` range. Files
are as of the returned canonical snapshot: `X-Snapshot-Block-Number` and
`X-Snapshot-Block-Hash` are also set for an empty file. Nonempty rows repeat the
snapshot identity. Amounts remain raw exact decimal integers; import numeric
columns as text in spreadsheet programs to avoid their precision coercion.

Limits are 31 days, 10,000 rows, 16 MiB, 15 seconds of generation, five starts per
rolling minute per account, one active generation per account and four per
deployment. `export_limit` requires a smaller interval; `export_rate_limit`
returns HTTP 429 with `Retry-After: 60`. `activity_unavailable` means the required
Core coverage/Token publication or writer is unavailable; retry after recovery.
Failures return JSON before any CSV bytes. Files are generated in memory and
not retained. Crash reservations expire after 20 seconds and maintenance prunes
old admission records. Existing HTTP write limits still bound file delivery.

## Optional Turnstile protection

Create a [Cloudflare Turnstile](https://developers.cloudflare.com/turnstile/)
**Managed** widget for the hostname in
`server.public_url`. Configure `turnstile.site_key` and set `turnstile.enabled`
to true (false by default). This single switch protects browser contract
verification, CSV exports and all future Turnstile-protected operations.
Each operation retains its own action validation. Environment equivalents are
`ETHERVIEW_TURNSTILE_SITE_KEY` and `ETHERVIEW_TURNSTILE_ENABLED`.
Supply the secret only to API/all as
`ETHERVIEW_TURNSTILE_SECRET_KEY` or `ETHERVIEW_TURNSTILE_SECRET_KEY_FILE`; the
file option requires a read-only mount in those services. Never place the secret
in YAML, ConfigMaps, browser config, image build arguments or logs.

Replace the removed `turnstile.verification_enabled` and
`turnstile.csv_export_enabled` keys with `turnstile.enabled`; replace either old
`ETHERVIEW_TURNSTILE_VERIFICATION_ENABLED` /
`ETHERVIEW_TURNSTILE_CSV_EXPORT_ENABLED` environment variable with
`ETHERVIEW_TURNSTILE_ENABLED`. Enable the shared switch if either old guard was
enabled. Application startup rejects removed YAML keys and environment
variables; Helm rejects removed values and uses `config.turnstile.enabled`.
There are no compatibility aliases. Compose only forwards current variable
names, so replace old entries in your environment file before recreating
services; an old entry alone no longer enables protection.

Restart API/all after changing settings; the same image works with either
configuration. The SPA learns `enabled` and `site_key` from `/api/v1/config`
(the `turnstile` object is omitted when disabled);
reload already-open tabs after changing them. Preview and external Compose
forward public settings to services and secret inputs only to API/all. Helm
uses `config.turnstile`, `secrets.turnstileSecretKey` and, when ExternalSecret is
used, `externalSecret.turnstileSecretRemoteKey`. The chart adds API/all HTTPS
and DNS egress when protection is enabled. Outside Kubernetes, permit outbound
HTTPS to `challenges.cloudflare.com`; browsers must also reach that origin.
No Cloudflare pre-clearance cookie or CDN proxy is required.

Test local verification, all Sourcify operations (including result-page import)
and an address CSV download on the actual configured hostname. Confirm a fresh
challenge is required after consumption/expiry and that an invalid token is
rejected with 403 before work starts. Unavailable validation returns 503 and
preserves the form for retry; normal reads/indexing remain available. Native
Hardhat/Foundry/Etherscan API-key submissions do not require browser challenges.
Turning either flag off and restarting API/all rolls back that guard without
schema changes. Public site keys are not secrets, but live secret rotation must
be coordinated with Cloudflare. Automated mock transport/widget tests do not
establish live widget/domain correctness or remote provider availability.

## Administration pages

Open Account by selecting the signed-in identity in the wallet menu. The connected
wallet address still opens its explorer address page. Wallet connection and login identity remain
in that menu; Account omits those status panels. An active administrator can open Account’s
**Admin** tab for Overview, Users, Billing (when enabled), and Sync Status. These views use
`/account?tab=admin&section=overview|users|billing|status`; the former `/admin/users`,
`/admin/billing`, and `/status` URLs redirect to their corresponding account view.

Users supports submitted address substring searches across the configured
chain, with settings and session revocation in a user drawer. Billing separates
Overview from Payments; advanced filters, grouped breakdowns, and payment
details are disclosed on demand. Amounts remain exact and grouped by network
and asset rather than combined across currencies.

`/status` and `GET /api/v1/status` require an administrator Cookie session.
API keys cannot authorize this endpoint. Anonymous requests return 401,
ordinary users return 403, and disabled user authentication returns the existing
503 `user_auth_unavailable` error. Use the operational health listener for probes
and public `/api/v1/home` for anonymous load workloads. Home block progress and
public browsing event notifications remain available.

Admin opens Overview by default: the current-chain user total includes disabled
users and administrators. Billing Summary shows the last 24 hours when billing
is enabled; full filters and payment records remain in Billing. Users are
identified and searched by wallet address; custom display names are not stored
or editable. Empty lists hide pagination; an empty later page offers a separate
return-to-first-page action.
