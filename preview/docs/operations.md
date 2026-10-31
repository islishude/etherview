# Preview Operations

[Documentation index](README.md). Run shell commands from the repository root.

## Setup and lifecycle

Initialize and start the full-stack Preview:

```sh
make preview-cert
make start-preview
```

Preview settings live in `.local/preview.env`. Start/recreate automatically
initialize missing settings and reuse existing VAPID keys; `make preview-env`
prepares the file separately. See [browser push configuration](#browser-push).

Preview now uses the repository-root `compose.yaml`; deployment commands use
`--project-directory . -f deploy/compose.yaml --env-file .env.deploy` from the
repository root. Move existing deployment settings out of `.env` manually as
described in the [deployment guide](../../deploy/README.md#docker-compose).
The project names and volume identities are unchanged; this file relocation
does not require stopping, deleting or migrating either environment's data.

The explicit certificate target installs mkcert trust and generates an ignored
`*.etherview.localhost` pair mounted read-only only into Caddy. The public app is
`https://app.etherview.localhost`; `http://localhost:9090` remains the operations
listener. See name resolution in [Preview Caddy gateway](#preview-caddy-gateway).
The gateway uses loopback port 443 (`PREVIEW_HTTPS_PORT` overrides it). After
building the application and pulling the official Caddy image,
it prepares `.local/preview-network/` using the pinned
`ethpandaops/ethereum-genesis-generator:6.2.7` image. The bundle contains the
execution genesis, Beacon configuration/state, Engine JWT and 64 development
validator keys. The checked-in `preview/genesis.json` template remains
unchanged. Chain ID is 48815, slots take three seconds, and Amsterdam/Gloas are
active at genesis. Geth v1.17.10 uses full sync and archive state; Lighthouse
v8.3.2 runs both the Beacon node and validator client. These clients form an
isolated local PoS network with no public bootnodes or external builders.

`make start-preview` reuses an intact bundle, validating every file against its
manifest and the current configuration fingerprint. Partial or changed material
fails closed. Existing volumes without matching network material require an
explicit `make stop-preview`; startup never deletes them automatically.
`make recreate-preview` reuses the existing network bundle without refreshing its timestamp;
it never creates or modifies a Genesis file. It recreates clients and workers
while retaining chain state, PostgreSQL, blob objects and validator slashing
protection. `make stop-preview` destroys all Preview volumes and the default
generated network directory, but retains TLS certificates and the Solidity/Vyper
cache in `.local/preview-compilers/cache`. See [cache lifecycle](setup.md) for
explicit reclamation with `make clean-preview-compiler-cache`; deleting Docker
containers or volumes does not clear it. Moving from the old
Geth dev Preview requires this explicit reset. A complete custom bundle can be
selected with `PREVIEW_NETWORK_DIR`; individual `ETHERVIEW_GENESIS_FILE` and
`GETH_GENESIS_FILE` overrides are rejected. Custom bundles are never deleted by
the stop target and must be reset by their owner.

The template retains its pre-funded development addresses and system contracts.
Transactions are signed locally with the standard Hardhat development account
`0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266` (the first account from
`test test test test test test test test test test test junk`) and submitted
through `eth_sendRawTransaction`. Geth no longer unlocks accounts or provides
the `dev` RPC namespace. Never fund these public development keys on external
networks. The browser wallet flow is unchanged.

Preview enables consensus enrichment and blob archival. Lighthouse runs as a
supernode with blob pruning disabled. Its Beacon API is published only on
`127.0.0.1:${LIGHTHOUSE_HTTP_PORT:-5052}`; Engine RPC is internal, with its JWT
mounted only into Geth and Lighthouse. Validator keys are isolated from
application roles. Startup waits for successive real execution/Beacon blocks.

RustFS 1.0.4 stores blobs in the persistent `blob-data` volume. The one-shot
`archive-init` service uses RustFS CLI v0.1.36 to provision the private
`etherview-preview-blobs` bucket and scoped users idempotently. There is no
expiry policy or trace-cache bucket reuse. API has GetObject permission;
enrich has GetObject/PutObject, without deletion rights. Override development
passwords through `PREVIEW_BLOB_READ_SECRET`, `PREVIEW_BLOB_WRITE_SECRET`, and
RustFS administrator variables before initialization. Administrator credentials
are never passed to application roles. RustFS ports are not published by the
daily stack. The application core healthcheck remains independent of both
optional services, including during container recreation. `archive-init` is an
explicit one-shot profile, outside the core dependency graph. Start/recreate
first bring up the runtime, then run `archive-init` separately and propagate its
exit status before checking real block production. Initialization failure does
not prevent already started API/enrich containers from becoming ready.
After correcting credentials or restoring storage, repeat
`docker compose --env-file .local/preview.env -f compose.yaml run --rm archive-init`; this preserves
existing objects. To recreate application roles during an archive outage, use
`docker compose --env-file .local/preview.env -f compose.yaml up -d --no-build --force-recreate --wait api enrich`.
The full start/recreate targets still report optional service startup failures.

### Preview x402 billing

`make start-preview` and `make recreate-preview` build the test-only
Facilitator image and enable Account billing on the Geth/Lighthouse chain ID
**48815**; no public testnet or external payment Facilitator is involved.
Local USD code, constructor storage, the owner's initial token balance and
10 local ETH for the settlement account are already included in genesis.
Startup sends no token deployment or funding transactions. Network manifest
and fingerprint checks reject stale or changed bundles; recreation preserves
token balances, spent authorizations and PostgreSQL credit. Genesis changes
require an explicit fresh-network reset, as described in the
[contract lifecycle](contracts.md#preview-erc-4337-and-local-ens).

Open `https://app.etherview.localhost/account`, add the configured Preview
network (`https://geth.etherview.localhost`, chain ID 48815), and import the
public development wallet below. Never use this key for real assets:

```text
Address: 0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266
Private key: ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80
Local USD: 0x8464135c8F25Da09e49BC8782676a84730C318bC (6 decimals)
```

Sign in, open Billing, and enter `0.0001` whole tokens (100 atomic units) to top up with each of `eip3009` and
`permit2`. The latter asks for an exact-amount approval followed by a signature.
The initial token allocation is 10^18 atomic units. Create a user API key with
`api:read`, then call `/v2/api?chainid=48815&module=account&action=balance&address=0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266&apikey=YOUR_KEY`.
Each successful `account.balance` call costs 2 atomic units; `contract.getabi`
costs 1. All supported query prices derive from their complexity weights and
`billing.base_price_atomic: "1"`; failed calls release their reservation.
Native explorer reads stay free. After two top-ups and one successful balance
read the available credit is `0.000198` whole tokens (198 atomic units).

The Facilitator has no host port and cannot block API/core readiness. Stopping
it prevents new settlement but existing credit remains spendable. The local
HTTP exception accepts only the configured service host within private Docker
IPv4 ranges, rejects redirects and retains response/time bounds. For custom
address pools, set `ETHERVIEW_X402_FACILITATOR_ALLOWED_CIDRS` to the exact bridge
CIDRs. `make recreate-preview` preserves network and database volumes;
`make stop-preview` deletes them. Insufficient settlement gas requires an
explicit local transfer to `0x70997970C51812dc3A010C7d01b50e0d17dc79C8`.
If broadcasting or receipt lookup becomes uncertain, the local Facilitator
returns `settlement_pending` with the signed transaction hash. Resume the chain,
inspect that exact receipt, then use the [billing reconciliation procedure](../../docs/operations.md#prepaid-api-billing-and-x402-top-ups);
never create a replacement payment to recover an uncertain settlement.

Permit2 here is the test token's local witness settlement implementation. This
is product/accounting acceptance only; P60-T17 still requires both live-method
reports and independent writer/chain reconciliation.

### Preview archive IAM limitation

The archive client sends `If-None-Match: *` on writes, but upgrading to RustFS
1.0.4 does not resolve storage-enforced conditional-write IAM permissions.
The current writer policy grants GetObject/PutObject without requiring that
header, so writer credentials can still issue unconditional writes and overwrite
an existing key. API read-only permissions, application conditional creation,
and independent archive integrity checks remain active; they do not establish
storage-side protection against direct use of writer credentials.

Upstream [PR #8615](https://github.com/rustfs/rustfs/pull/8615) addresses policies
rejecting `s3:if-none-match` and `s3:if-match` with `invalid key name`. Its scope
covers bucket, IAM and STS policies: values come from nonblank request headers,
not identity claims that could fabricate a missing precondition. For writes,
`If-None-Match` must be `*`; unsupported ETag values return `501 NotImplemented`,
while an existing destination with `*` returns `412 PreconditionFailed`.
The PR also checks raw headers and parsed inputs separately to prevent form
uploads from bypassing validation. It does not itself provide
`s3:ObjectCreationOperation` or full AWS conditional-policy parity.

Storage-enforced permissions remain deferred until RustFS 1.0.5 is evaluated.
This is the next evaluation target, not a promise that the release fixes IAM;
a merged PR or release milestone is insufficient. Before clearing the limitation,
verify the deployed image contains the fix and all nodes support the policy keys.
Using the scoped writer credentials and the proposed policy, test successful
conditional creation, denial of missing/blank conditions and unconditional
writes, rejection of overwrites and unsupported ETag conditions, and unchanged
stored bytes after every rejected operation. Retain the API read-only denial
check separately. Do not claim IAM enforcement from the application archive
regression alone.

### Preview recovery

If blocks stop advancing, inspect Geth/Beacon/validator logs and Engine JWT
identity before changing data. An archive outage affects collection/downloads;
existing consensus metadata remains queryable. Restart the failed service and
use the existing `reindex --stage blob` procedure if a job reached an unavailable
terminal state. The start/recreate targets do not modify the host trust store.

## IPFS gateway

Preview includes Kubo `v0.43.1` behind Caddy at `https://ipfs.etherview.localhost`.
The shared wildcard pair is mounted only into Caddy; metadata receives only the
public CA. Existing installations rerun `make preview-cert` before recreation.
Metadata text remains available, and images resolved through this `.localhost`
gateway are unverified external links requiring confirmation before each opening.
Preview explicitly sets `metadata.image_navigation_allowed_origins` to
`["https://ipfs.etherview.localhost"]`; production examples and Helm default to `[]`.
The list permits navigation exceptions for exact HTTPS host/port pairs only.
Host matching ignores case and a trailing dot; omitted ports equal 443. Wildcards,
credentials, non-root paths, queries and fragments are invalid. Use ASCII hostnames
(or their punycode form). Other local destinations remain `unsafe` without a URL.
The list does not grant server-side fetch authority or validate destination content,
DNS answers, or subsequent browser redirects.

`ETHERVIEW_METADATA_IMAGE_NAVIGATION_ALLOWED_ORIGINS` overrides the list with
comma-separated origins; an explicitly empty value clears it. Apply the same
configuration to the API process in split mode or the `all` process in monolith
mode. Changing the browser-facing gateway port requires updating both the gateway
URL and its allowlist entry. Recreate the application processes after configuration
changes; no database migration or metadata refetch is required.

Daily Preview retrieves uncached content over IPFS and retains it in the
project-scoped `ipfs-data` volume. The reviewed metadata fixture is imported and
pinned idempotently at every Kubo start. `make recreate-preview` replaces Kubo
and its proxy along with the application containers while preserving their
volumes; `make stop-preview` deletes all Preview volumes, including IPFS data.
`PREVIEW_HTTPS_PORT` and `IPFS_API_PORT` override the loopback-only host ports
(defaults 443 and 5001). The gateway's container address stays at port 443;
when overriding its host port, open it using that port in the browser. Configured
public URLs must also be updated when changing the browser-facing port.

Preview keeps `metadata.unsafe_allow_private_networks=false` in its mounted
YAML and overrides it only in the split metadata worker for this controlled
local HTTPS gateway. TLS validation, fetch bounds, and document validation
remain enabled. API NFT media and every other role remain strict, so the API
media proxy does not fetch private gateway content. Gateway image links remain
user-confirmed navigation only. An explicit `ETHERVIEW_METADATA_IPFS_GATEWAY`
can select another HTTPS gateway; no automatic public fallback exists.

Run `make test-preview-metadata` for the isolated offline acceptance gate. It
uses a unique project, fresh volumes, and random loopback ports. The Kubo daemon is offline with `Gateway.NoFetch=true`; fixed-content
and CLI roundtrips need no external retrieval. The test preserves both exact
metadata versions and their single attempts, checks owned gateway addresses,
and proves a metadata-worker restart does not add an attempt. Successful and
failed runs retain diagnostics and a success report when available.

## Browser push

For Preview at `https://app.etherview.localhost`, the root Compose entry point
uses ignored `.local/preview.env` for general local settings, including the
VAPID key pair.
`make start-preview` and `make recreate-preview` run `make preview-env` first:
it creates the file when missing, generates a P-256 key pair, fills missing push
settings and enforces mode 0600. Generated private scalars retain leading zero
bytes to encode exactly 32 bytes in unpadded base64url. Existing values, comments
and key pairs are preserved; generated defaults enable Preview push and leave private-network
access disabled. Only maintenance receives the private key; Compose does not
inject the whole file into containers. `.local/` is excluded from Git and Docker
build contexts. Production push remains disabled by default.

Preview lifecycle commands explicitly load that file, without loading root `.env`.
`PREVIEW_ENV_FILE` overrides the path for Make commands. To migrate existing
settings, move the root `.env` contents into `.local/preview.env`, keeping
its existing private key and matching public key, then remove root `.env`.
`ETHERVIEW_PREVIEW_PUSH_ENV_FILE` is no longer used.

Turnstile settings share `.local/preview.env` with Push and other Preview
settings. Compose explicitly passes Turnstile settings only to API and the
VAPID private key only to maintenance; it never injects the entire file into a
container. Turnstile stays disabled when its settings are absent. Initialization
preserves existing settings and comments and does not generate Turnstile keys.
For example, add your existing keys:

```dotenv
ETHERVIEW_TURNSTILE_ENABLED=true
ETHERVIEW_TURNSTILE_SITE_KEY=your-site-key
ETHERVIEW_TURNSTILE_SECRET_KEY=your-secret-key
```

When migrating from `.local/preview-push.env` and `.local/turnstile.env`, merge
their contents into `.local/preview.env` before starting Preview. Preserve the
existing VAPID pair and Turnstile keys, resolve any conflicting duplicate settings
explicitly, and remove the old files only after verifying the merged values.
Startup does not read or migrate the old paths. Run `make preview-env` to validate
the VAPID pair, fill missing Push defaults and enforce mode 0600. `.local/` is
excluded from Git and Docker build contexts.

Both Make and raw Compose use the same settings: Make selects `PREVIEW_ENV_FILE`,
while raw Compose requires `--env-file .local/preview.env` (or your custom path).
Exported host variables override matching file values under Compose precedence,
including Turnstile; unset them to use the file's values.
After changing the file, recreate the API to apply it:

```sh
docker compose --env-file .local/preview.env up -d --no-deps --force-recreate api
```

`make recreate-preview` also applies the configuration when recreating Preview.
This local loading mechanism does not change production `deploy/compose.yaml`.

Run `make preview-env` separately before using raw Compose commands:

```sh
make preview-env
docker compose --env-file .local/preview.env config --quiet
```

Compose itself does not generate the file. An existing public key without its
private key, or a mismatched pair, fails initialization instead of silently
rotating browser subscriptions.
File-based keys may retain `ETHERVIEW_PUSH_PRIVATE_KEY_FILE` with the matching
public key and an explicit read-only Compose mount.

Use a trusted Preview TLS certificate. Existing databases from before this schema change must be
recreated under the [fresh-database policy](../../docs/operations.md#fresh-database-initialization);
`make recreate-preview` replaces runtime containers but does not reset an old
database. After fresh initialization, sign in, add a watch, enable this browser,
then close all site tabs while leaving the browser/system able to receive push.
Create a new matching transaction only after enabling. Verify the summary has
no amount and opens the correct transaction, then verify logout stops subsequent
alerts. Record browser/OS versions, notification permission, deployed build and
the observed result. Offline delivery is deliberately not an acceptance goal:
TTL 0 drops messages instead of storing them for later delivery.

### Preview Caddy gateway

Preview uses `caddy:2.11.7-alpine` directly, configurable with
`PREVIEW_GATEWAY_IMAGE`. `make preview-images` pulls it; lifecycle commands and
CI no longer build a gateway binary. `make test-preview-gateway` exercises the
shipped Caddyfile in real containers before full Preview acceptance.

Run `make preview-cert` to install mkcert trust and create
`.local/preview-tls/gateway.crt`, `gateway.key` and the public `rootCA.pem`.
Only Caddy mounts the wildcard private key, which remains 0600. If a host client
does not resolve these names to loopback, configure these explicit mappings
(`hosts` does not implement wildcards):

```text
127.0.0.1 app.etherview.localhost
127.0.0.1 ipfs.etherview.localhost
127.0.0.1 geth.etherview.localhost
127.0.0.1 beacon.etherview.localhost
127.0.0.1 bundler.etherview.localhost
```

Docker uses explicit network aliases so container clients reach Caddy rather
than their own loopback interface. Tests use isolated address mapping and never
edit system hosts.

| URL                                           | Upstream                            |
| --------------------------------------------- | ----------------------------------- |
| `https://app.etherview.localhost`             | API and embedded SPA, internal HTTP |
| `https://ipfs.etherview.localhost/ipfs/<cid>` | Kubo read-only content              |
| `https://geth.etherview.localhost`            | Geth JSON-RPC POST                  |
| `wss://geth.etherview.localhost`              | Geth subscriptions                  |
| `https://beacon.etherview.localhost/eth/…`    | Beacon REST and SSE                 |
| `https://bundler.etherview.localhost`         | Rundler ERC-4337 JSON-RPC POST      |

`PREVIEW_HTTPS_PORT` defaults to 443 on 127.0.0.1; ephemeral ports are used by
tests. Nondefault browser ports require matching public URL and wallet URLs,
plus allowed Origins for Beacon/Bundler in an operator config. The gateway does not open port 80,
automatically issue certificates, or expose an administration API. Its internal
loopback health check reports serving readiness independently of upstreams.

`geth.etherview.localhost` accepts any Origin for JSON-RPC and WSS, including
MetaMask/browser-extension origins, `null` and requests without Origin. HTTP
responses use `Access-Control-Allow-Origin: *`; OPTIONS preflight returns 204.
Cross-origin credentials are not enabled.

`PREVIEW_NODE_ALLOWED_ORIGINS` applies only to Beacon and Bundler and replaces
their complete space-separated list; defaults are
`https://app.etherview.localhost http://localhost:5173 http://127.0.0.1:5173`.
Those hosts reject unlisted/null Origins and accept CLI clients without Origin.
Changing this variable does not restrict Geth. Browsers may separately prompt
for local-network access; grant it for the trusted local development page. Node
CORS does not grant application sessions or relax the app's CSP. Node request
bodies are limited to 32 MiB; subscriptions end after 24 hours and should
reconnect. No writes are retried.
Engine, validator and Kubo administration routes are not exposed.

After changing only the Caddy configuration, recreate just the gateway to apply
it without restarting the chain or replacing data volumes:

```sh
.github/scripts/compose.sh --env-file .local/preview.env -f compose.yaml up -d --no-deps --force-recreate preview-gateway
```

Use the configured `PREVIEW_ENV_FILE` instead if overridden. Recreate Preview
after other certificate or configuration changes. Caddy is the
only TLS terminator in this topology; process-native TLS remains available for
other deployments. Migration removes the old gateway container through
Compose's orphan cleanup and preserves chain/database/IPFS/archive volumes.
Old local certificate files can be removed manually after migration; they are
no longer mounted. Browser users must log in again on the new app domain.
Do not use `stop-preview` for migration because that command deletes volumes.

Preview genesis predeploys the system contracts from the pinned Geth developer
allocation, including withdrawal, consolidation and builder request queues.
Activating a fork without its contracts can prevent Geth from building any
blocks. Changes to these allocations change the genesis hash: use a fresh
Compose project for validation, and do not initialize existing chain volumes
with a different genesis. `recreate-preview` preserves the same runtime genesis
and volumes; it is not a genesis migration procedure.

The CI `Preview metadata, consensus, and blobs E2E` job runs the full offline-Kubo acceptance
on pull requests, main pushes and the daily schedule. It creates a temporary
runner CA and wildcard gateway certificate through `make preview-cert`;
no operator certificates or secrets are required. Its seven-day artifact
contains only acceptance reports and diagnostic logs, not certificate keys.

### Preview configuration layout

The repository-root `compose.yaml` is the single Preview entrypoint. It uses
Compose `extends` to load service settings from `preview/compose/`:
`app.yaml`, `chain.yaml`, `storage.yaml`, `gateway.yaml` and `x402.yaml`.
Host bind mounts and build contexts remain in the root entrypoint so relative
environment overrides retain repository-root semantics. Named volumes and the
project identity also remain there. Do not start the service fragments directly.

Preview configuration, genesis, contract artifacts and helper scripts live in
`preview/`; `deploy/` retains the external-chain production template.
Local USD code and initial state are part of genesis; no x402 initializer runs
at startup. Existing network bundles require explicit recreation after genesis
changes, which discards their local chain and billing data.

## External source configuration

Preview enables Sourcify at `https://sourcify.dev/server` and enables
`features.verification` and `bytecode_db.enabled`. Sourcify and the bytecode
search client enable their independent `unsafe_allow_private_networks` settings
for local Fake-IP development. TLS verification, checked-IP dialing, redirect
rejection and bounded requests remain enforced; these settings do not change
metadata or compiler-download policies.

Set `ETHERVIEW_BYTECODE_DB_ENABLED=false` to disable bytecode discovery.
Set `ETHERVIEW_SOURCIFY_UNSAFE_ALLOW_PRIVATE_NETWORKS=false` and
`ETHERVIEW_BYTECODE_DB_UNSAFE_ALLOW_PRIVATE_NETWORKS=false` to restore strict
public-network checks. Put overrides in `.local/preview.env` (or the file
selected by `PREVIEW_ENV_FILE`) and run `make recreate-preview` to apply them.
See the [Sourcify workflow](../../docs/operations.md#sourcify-operations) and
[bytecode discovery](../../docs/operations.md#automatic-bytecode-database-source-discovery)
for publication, credential and verification boundaries.

## Verification recovery

For an existing Preview deployment, rebuild and recreate only the application
services with `make recreate-preview`; do not remove the PostgreSQL or Geth
volumes. Then run the [address-scoped recovery command](../../docs/operations.md#factory-derived-verification-rollout-and-backfill) inside the API container. This
repairs scans created by an older binary whose cursor began at verification
publication rather than at the creator code epoch.

## Genesis updates

New disposable Preview networks activate Amsterdam at genesis. Existing Preview
volumes retain their genesis identity; do not replace their runtime genesis or
remove their volumes as part of an application update.

## Genesis contract source verification

`make start-preview` runs source verification after real PoS readiness.
`make recreate-preview` preserves existing publications and does not run this step. The `genesis-verify` Compose profile uses the existing
Preview Node/Rundler image, a read-only source bundle, and the internal API and
Geth endpoints. It does not deploy contracts or change chain state.

Run `make verify-preview-genesis` to retry this step on a running Preview.
The host wrapper creates an API key scoped to `api:read` and `contract:verify`,
passes it only over stdin, and revokes it in cleanup. Source verification uses
the ordinary API queue and API-role compiler workers. It requires the normal
compiler catalog and checksum-verified compiler downloads; first startup can
therefore take longer or fail if compiler hosts are unavailable.

The helper checks the complete non-empty-code allocation inventory against the
runtime genesis and canonical authenticated import before submission. Each
contract must have an exact-address published artifact for its expected chain
and code hash, with a successful runtime match. Genesis verification supplies
no creation evidence. Existing matching artifacts are reused when verification is rerun;
terminal failures, missing sources, identity mismatches and bounded timeouts
fail the Make target. A retry retains completed publications. Inspect helper
output for the contract name and, on a polling timeout, the durable job ID.

The fixed source inventory and checksums live in `preview/verification`.
Sources are not discovered from public services during startup. Refreshing
sources is an explicit reviewed development operation; see
[verification source maintenance](testing.md#genesis-verification-inputs).
