# Operations Runbook

Etherview keeps correctness state in PostgreSQL. Logs, Prometheus, OTLP,
NATS, Redis, and S3 are diagnostic or acceleration surfaces; losing any of
them must not authorize a state transition or make a healthy database-backed
request fail.

## Web branding at build time

Set `ETHERVIEW_WEB_BRANDING_DIR` to a directory containing `brand.json` and
the selected images. All local relative directory paths resolve from the
repository root, including when npm runs from `web/`; local absolute paths
are also supported. An unset or empty value uses `web/branding/default` and
preserves the Etherview name and icon.

For example, place these public build inputs in `branding/acme/brand.json`,
with `mark.svg` and `tab.png` beside it:

```json
{
  "name": "Acme Explorer",
  "title": "Acme Block Explorer",
  "description": "Explore the Acme network",
  "icon": "mark.svg",
  "favicon": "tab.png"
}
```

Every field is optional. `name` defaults to `Etherview`; `title` defaults to
the selected name; `description` defaults to the name followed by
` execution-layer block explorer`. `icon` supplies both the navigation Logo
and favicon, while `logo` and `favicon` independently override it. Without
an applicable image override, that location uses the bundled Etherview SVG.
Text values are shared across languages; the surrounding interface remains
Chinese/English. Chain names still come from the existing chain configuration.

Image paths must be relative to the branding directory and stay within it,
including after resolving symbolic links. Only SVG and PNG are supported;
remote URLs, absolute image paths, missing files, unknown fields, invalid
JSON and invalid field types fail the build. Use self-contained images: the
existing same-origin CSP remains in force. The build checks the image signature,
but does not sanitize, resize or convert artwork. Each image is limited to
1 MiB and `brand.json` to 16 KiB. Text must be nonempty without control
characters; name/title/description are limited to 80/160/512 Unicode code points.

```sh
ETHERVIEW_WEB_BRANDING_DIR=branding/acme npm --prefix web run dev
ETHERVIEW_WEB_BRANDING_DIR=branding/acme npm --prefix web run build
make go-build ETHERVIEW_WEB_BRANDING_DIR=branding/acme
make docker-build IMAGE=etherview:acme ETHERVIEW_WEB_BRANDING_DIR=branding/acme
```

Docker accepts the same variable as a build argument. Its directory must be
inside the repository build context and not excluded by `.dockerignore`;
in particular, `.local/` is excluded. The Web builder copies only the selected
branding directory in addition to the normal Web sources. The final image
receives the compiled Go binary with hashed embedded assets, not the branding
configuration or source directory. Do not put secrets in these public inputs.

```sh
.github/scripts/buildx.sh build --load --target production --tag etherview:acme \
  --build-arg ETHERVIEW_WEB_BRANDING_DIR=branding/acme .
ETHERVIEW_WEB_BRANDING_DIR=branding/acme .github/scripts/compose.sh build api
ETHERVIEW_WEB_BRANDING_DIR=branding/acme .github/scripts/compose.sh \
  --project-directory . -f deploy/compose.yaml --profile monolith build etherview
```

Both Compose entry points pass this value only to the image build. Normal
Preview initialization and deployment lifecycle commands still apply.
Changing a running container's environment cannot change its brand: rebuild
the binary/image and roll out that artifact. Restart the development server
after changing branding inputs. A new image produces a new content-hashed
URL; the existing embedded asset manifest, compression and immutable cache
policy apply. Theme colors and automatic multi-size icon generation are not
part of this configuration.
Both selected images count toward the existing initial asset budgets; the
per-file input limit does not increase the allowed initial page weight.

## Fresh database initialization

The fresh-database baseline consists of numbered domain migrations from
`0001_core.sql` through `0017_schema_bindings.sql` in
`internal/store/migrations/`. Initialize an empty PostgreSQL database with
`etherview migrate up`, then verify it with `etherview migrate status`. The
runner applies all pending files in one transaction under an advisory lock;
status reports the complete version set and verifies each file's checksum.
Every role uses this same baseline; serving never applies migrations implicitly.

Databases initialized with the earlier development migration chain or the
previous single-file `0001_core.sql` baseline must be recreated. The retained
`0001_core` version has a different checksum, so the old baseline is rejected
before new DDL is applied. Stop all processes using that database, retain a
backup if its data is needed, and provision a new empty database before running
the commands above. Update the writer and reader configuration together before
restarting roles. Reindex or resubmit required application data explicitly. Do not edit
migration checksums or ledger rows to make an old database appear compatible.
The migration command does not delete existing databases or Docker volumes,
and no in-place upgrade, data conversion, or startup backfill is provided.

## Health and telemetry

Every role exposes `GET /health/live`, `GET /health/ready`, and `GET /metrics`
on `server.metrics_address`. Readiness requires the shared component lifecycle
and PostgreSQL. The API role additionally requires durable core readiness on
its public listener.

## API listener TLS

The public `api`/`all` listener uses HTTP by default. To make the Go server
serve HTTPS directly, configure both absolute paths:

```yaml
server:
  public_url: https://explorer.example.com
  tls_cert_file: /run/etherview-tls/tls.crt
  tls_key_file: /run/etherview-tls/tls.key
```

`ETHERVIEW_SERVER_TLS_CERT_FILE` and
`ETHERVIEW_SERVER_TLS_KEY_FILE` override the YAML paths. The process loads the
PEM pair before opening `server.address`; an unreadable file, malformed
certificate, or mismatched private key fails startup without an HTTP fallback.
TLS 1.2 and 1.3 are supported. `server.metrics_address` remains HTTP.

Certificate files are read once. After replacing them, restart the API process
or roll the selected `all`/`api` Deployments. Monitor certificate expiry and
perform client trust-chain and hostname checks at the public origin; the
application does not issue, renew, or hot-reload certificates.

The `deploy/compose.yaml` deployment remains HTTP and expects production TLS
to terminate externally. For a locally trusted process-native HTTPS example,
initialize and start the full-stack Preview:

```sh
make preview-cert
make start-preview
```

Preview now uses the repository-root `compose.yaml`; deployment commands use
`--project-directory . -f deploy/compose.yaml --env-file .env.deploy` from the
repository root. Move existing deployment settings out of `.env` manually as
described in the [deployment guide](../deploy/README.md#docker-compose).
The project names and volume identities are unchanged; this file relocation
does not require stopping, deleting or migrating either environment's data.

The explicit certificate target runs `mkcert -install` and generates an
ignored pair for `etherview.localhost`, `localhost`, `127.0.0.1`, and `::1`.
Preview mounts that API pair read-only only into the API service. Its public
listener is `https://etherview.localhost:8080`, while
`http://localhost:9090` remains the plain HTTP operations listener. The start
target prints the browser URL after successful startup, using `ETHERVIEW_PORT`
when set (default `8080`). After building the application and gateway images,
it prepares `.local/preview-network/` using the pinned
`ethpandaops/ethereum-genesis-generator:6.2.7` image. The bundle contains the
execution genesis, Beacon configuration/state, Engine JWT and 64 development
validator keys. The checked-in `deploy/preview.genesis.json` template remains
unchanged. Chain ID is 48815, slots take six seconds, and Amsterdam/Gloas are
active at genesis. Geth v1.17.8 uses full sync and archive state; Lighthouse
v8.3.0 runs both the Beacon node and validator client. These clients form an
isolated local PoS network with no public bootnodes or external builders.

`make start-preview` reuses an intact bundle, validating every file against its
manifest and the current configuration fingerprint. Partial or changed material
fails closed. Existing volumes without matching network material require an
explicit `make stop-preview`; startup never deletes them automatically.
`make recreate-preview` reuses the existing network bundle without refreshing its timestamp;
it never creates or modifies a Genesis file. It recreates clients and workers
while retaining chain state, PostgreSQL, blob objects and validator slashing
protection. `make stop-preview` destroys all Preview volumes and the default
generated network directory, but retains TLS certificates. Moving from the old
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

RustFS 1.0.1 stores blobs in the persistent `blob-data` volume. The one-shot
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
`docker compose -f compose.yaml run --rm archive-init`; this preserves
existing objects. To recreate application roles during an archive outage, use
`docker compose -f compose.yaml up -d --no-build --force-recreate --wait api enrich`.
The full start/recreate targets still report optional service startup failures.

The archive client sends `If-None-Match: *` on writes, but RustFS 1.0.1 cannot
require that header in IAM policies. Writer credentials can still perform
unconditional writes. Storage-enforced conditional-write permissions are
deferred until RustFS 1.0.4 can be evaluated with the upstream policy fix;
upgrading to 1.0.3 alone does not provide it.

If blocks stop advancing, inspect Geth/Beacon/validator logs and Engine JWT
identity before changing data. An archive outage affects collection/downloads;
existing consensus metadata remains queryable. Restart the failed service and
use the existing `reindex --stage blob` procedure if a job reached an unavailable
terminal state. The start/recreate targets do not modify the host trust store.

Preview includes Kubo `v0.43.1` and an independent Go HTTPS gateway at
`https://ipfs.preview.test:8443`. Add `127.0.0.1 ipfs.preview.test` to the local
hosts file to open gateway links in a browser; containers resolve the same name
through a Compose network alias. `make preview-cert` also creates the separate
`ipfs.crt`/`ipfs.key` pair. Only the gateway proxy mounts this private key; only
metadata mounts the public root CA. Existing installations must rerun this
explicit certificate target before starting the updated Preview.

Daily Preview retrieves uncached content over IPFS and retains it in the
project-scoped `ipfs-data` volume. The reviewed metadata fixture is imported and
pinned idempotently at every Kubo start. `make recreate-preview` replaces Kubo
and its proxy along with the application containers while preserving their
volumes; `make stop-preview` deletes all Preview volumes, including IPFS data.
`IPFS_GATEWAY_PORT` and `IPFS_API_PORT` override the loopback-only host ports
(defaults 8443 and 5001). The gateway's container address stays at port 8443;
when overriding its host port, open it using that port in the browser. Generated
image links continue using the configured gateway URL.

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

### IPFS file tool

The independent `cmd/ipfs` command uses the local Kubo management API. This
API is published only on `127.0.0.1:5001`; the HTTPS proxy serves `/ipfs/` reads
and does not expose management operations. No host IPFS CLI is needed.

```sh
go run ./cmd/ipfs upload --api http://127.0.0.1:5001 ./file.json
go run ./cmd/ipfs upload --wrap --api http://127.0.0.1:5001 ./metadata.json
go run ./cmd/ipfs download --api http://127.0.0.1:5001 --output ./download.json CID/metadata.json
```

Upload accepts one regular file and pins its CIDv1 SHA-256/raw-leaf content.
Without `--wrap`, stdout contains the file CID; with `--wrap`, it contains the
directory CID, allowing `CID/filename` downloads. Download accepts a CID,
CID/path, or `ipfs://CID/path` and requires a new output filename. Bare CID/path
arguments preserve literal filename characters, including `+`, `%`, `?`, and
`#`. URI paths use percent encoding (for example, `my%20file.json` or
`token%231.json`), decoded once per segment; query strings, fragments and
decoded traversal/separator characters are rejected. It streams to
a same-directory temporary file, then publishes atomically without overwriting
an existing file. Failures and cancellation remove temporary output. Directory
recursion and IPNS are unsupported. Uploaded data remains pinned locally until
explicitly removed; other nodes' retention is not guaranteed.

Both commands default to `--api http://127.0.0.1:5001` and `--timeout 5m`.
HTTPS endpoints use system certificate trust. RPC redirects and environment
proxies are disabled. Diagnostics go to stderr; exit codes are 0 for success,
2 for argument errors, and 1 for operation failures. Build an independent
binary with `go build -o /tmp/etherview-ipfs ./cmd/ipfs`.

For Helm, create or provision a TLS Secret independently, then enable
`apiTLS.enabled` and set `apiTLS.existingSecret`. `ingress.tls` controls the
client-to-Ingress certificate; `apiTLS` controls the Service-to-Pod hop.
Controllers that do not honor `appProtocol: https` require their
HTTPS-backend annotation. The Helm test uses an insecure cluster-local curl
only because the public certificate normally does not name the Service DNS;
it is not a substitute for external certificate verification.

Logs default to JSON at the `info` level. Set `observability.log_level` to
`debug`, `info`, `warn`, or `error`, and set `observability.log_format` to
`json` or `text`. `ETHERVIEW_LOG_LEVEL` and `ETHERVIEW_LOG_FORMAT` override
the file; `--log-level` and `--log-format` override both for the current
command. Values are exact lowercase tokens. Both formats include service,
version, roles, chain, environment, and, inside traced requests, `trace_id`
and `span_id`. Boundary failures use stable `error_code` and `error_type`
fields. Raw RPC, PostgreSQL, compiler, metadata, panic, URL credential,
authorization-header, and exporter errors are not log attributes.

Every production record has a stable `event` and, when emitted by a long-lived
service, `component`. Runtime supervision emits `runtime_starting`,
`runtime_ready`, `runtime_stopping`, and `runtime_stopped`; individual component
starts are debug-only, while an unexpected exit names the failed component and
exposes only a stable code and error type. Repeated healthy polls are not info
logs.

Durable enrichment results use `job`, `stage`, `block`, `transition`, and
`summary` groups. `job` contains the durable ID, worker, attempt budget, and
generation; `block` contains the exact decimal number and full lowercase hash;
`transition` distinguishes the committed job status from the published stage
state and reports retry delay when applicable. `summary` contains only the
stage's closed, typed counters and outcome fields. Trace and state-difference
failures include a `transaction` hash/index only when the failing item is known;
successful block jobs never list every transaction. Claimed-job starts and
individual RPC calls are debug-only. RPC diagnostics contain only endpoint
name, purpose, method, batch counts, and duration, never parameters or results.

HTTP completion records include `request_id`, static native operation when
known, bounded mux route, result class, status, duration, and trace correlation.
They deliberately exclude path/query identities, request bodies, API-key
prefixes, users, payers, payments, and facilitator data. Adapter degradation
uses stable adapter/operation/fallback codes. Do not add `error_msg`, raw
provider text, or nested errors when extending these records.

The active PostgreSQL sync-status reporter emits an `info` progress record only
when its latest, indexed, highest-covered, lag, backfill-complete, or ready
state changes. `observability.sync_progress_log_interval` limits those records
to one per interval (default `30s`, configurable from `1s` through `1h`);
`ETHERVIEW_SYNC_PROGRESS_LOG_INTERVAL` overrides the file. Intermediate changes
are coalesced into the next record, idle heads do not produce heartbeats, and
non-reporter sync replicas stay silent. Durable worker outcomes remain
event-driven and are not delayed by this interval.

`metadata update observation transitioned` records one exact ERC-4906 or
ERC-1155 URI log after its immutable observation commits. Its bounded
`nft`, `block`, `kind`, and `transition` fields contain contract, standard,
token range, block/log identity, result, and stable malformed/stale code. It
never logs the event URI, raw log, metadata source, or nested parser text.

`metadata fetch transitioned` is emitted only after the corresponding retry or
terminal outcome commits. Its `job`, `nft`, `block`, and `transition` groups
identify the exact attempt and canonical observation. `source.scheme` and the
`request`/`network`/`failure` groups diagnose the bounded hostile-network
boundary without exposing the raw source URI or nested error. For an IPFS
source, `request.path` may contain only the public `/ipfs/<CID>/<path>` content
path and is omitted above 1024 bytes. Arbitrary HTTPS and redirect paths are
represented only by `path_length` and `path_sha256`; query values, userinfo,
fragments, and configured gateway path prefixes are never logged. DNS arrays
retain at most eight unique addresses and report their total and truncation. A
request that reuses an already policy-checked keep-alive connection may omit a
new DNS array while retaining the mandatory connected IP and policy-bypass
decision. For example, Docker fake-IP rejection includes
`nft.contract=0x... nft.id=0 transition.code=unsafe_url
request.host=ipfs.io network.resolved_ips=[198.18.17.210]
network.rejected_reasons=[special_use]
network.rejected_prefixes=[198.18.0.0/15]
failure.phase=network_policy` while the Prometheus result remains
`ssrf_rejected`. When the explicit Preview metadata exception accepts that
fake-IP route, the successful durable record instead includes
`network.policy_bypassed=true`; ordinary public DNS leaves it `false`.
Transport failures additionally expose one closed `failure.reason` such as
`dns_timeout`, `connection_refused`, `network_unreachable`,
`tls_handshake_timeout`, `tls_certificate_invalid`, or `request_timeout`.
Unknown transport failures use `transport_error`; arbitrary nested error text
never becomes a reason. A terminal retry exhaustion retains
`transition.code=attempts_exhausted` and adds the preceding stable retry code as
`transition.last_code`, so the retry class is not lost.

OTLP/HTTP tracing is off when `observability.otlp_trace_endpoint` is empty. To
enable it, supply an origin such as `https://collector.example:4318` through
`ETHERVIEW_OTLP_TRACE_ENDPOINT`; Etherview sends protobuf spans to
`/v1/traces`. Plain HTTP additionally requires
`ETHERVIEW_OTLP_TRACE_INSECURE=true`. Configure `trace_sample_ratio` in `[0,1]`
and keep `trace_export_timeout` below the process shutdown budget. Collector
authorization headers use the standard `OTEL_EXPORTER_OTLP_HEADERS` variable
and must come from a server-side Secret, never checked-in YAML or a ConfigMap.
Collector loss is reported as a redacted degraded event and never changes
readiness or a request result. The exporter starts only when an endpoint is
configured and is flushed by the shared bounded supervisor shutdown.
Remote sampled trace context keeps its W3C identity, but each server span uses
fresh cryptographic randomness for the sampling decision; a caller-selected or
replayed low-tail trace ID cannot deterministically force export. The ratio is
a long-run expectation, not a per-client quota.

HTTP request telemetry uses registered mux patterns and fixed SPA/asset/miss
labels, never path identifiers or unknown method strings. If a handler panics
before committing, the server returns the exact native, Etherscan-compatible,
or operational error envelope. After a streamed response is committed, it
preserves the wire status, increments `etherview_http_panics_total`, ends the
span as an error, and aborts without appending a second body. The net/http
internal logger discards panic values and stack text and emits only a stable
error code.

`server.write_timeout` is the whole-response budget for ordinary handlers and
the per-write/flush budget for `/api/v1/events` and `/api/v1/home/stream`.
Idle SSE connections intentionally outlive that duration; a 15-second
heartbeat keeps intermediaries aware of the stream without turning the timeout
into a connection-lifetime limit. On SIGTERM, API request contexts are canceled
before the HTTP drain begins, so active streams close within
`server.shutdown_timeout`. A drain timeout force-closes remaining connections
rather than returning while handlers still own runtime resources.

The PostgreSQL metric collector refreshes only active control-plane backlog at
`observability.metrics_refresh_interval`: durable `queued`/`leased`,
verification `queued`/`running`, and repair/reindex `queued`/`running` rows.
Terminal history is intentionally excluded from these current gauges; use the
persisted-after-transition result counters and bounded admin list for trends
and forensics. A failed refresh retains the last successful snapshot. Use
`etherview_observability_last_refresh_timestamp_seconds` together with
`etherview_observability_refresh_failures_total`; do not interpret absent or
stale queue series as zero work.

Every split role and replica reads the writer-backed chain-scoped operational
snapshot.
Deduplicate current backlog gauges with `max` per deployment and chain; never
`sum` them, because that multiplies one backlog by the replica count. Worker
result counters represent work performed by individual processes and should be
combined with `sum`, `rate`, or `increase` as appropriate. The Helm alerts
already follow this distinction.

Important series include:

- `etherview_sync_lag_blocks` and `etherview_sync_halted{reason}`;
- `etherview_rpc_requests_total{purpose,result}`;
- `etherview_durable_jobs{stage,status}` and
  `etherview_jobs_pending{queue}`;
- `etherview_verification_jobs{status}` and worker result counters;
- `etherview_repair_requests{operation,status}`,
  `etherview_repair_oldest_queued_seconds`, and maintenance result counters;
- HTTP latency/count, metadata safety, and rate-limit decision counters.
- `etherview_http_panics_total{method,route}` records a recovered handler panic
  even when a streaming response already committed a successful wire status.

The Helm `PrometheusRule` covers canonical safety halts, sync lag, RPC error
rate, durable backlog, stale PostgreSQL metric snapshots, stalled or failed
repair/reindex work, trace/verification failures, metadata SSRF rejection, and
rate-limit pressure. A canonical safety halt is not self-healing: keep the
process scrapeable, diagnose the named reason, repair the source or database,
and restart only after the identity boundary is safe.

## Genesis account state

Normal RPC indexing always stores block zero, but the header does not enumerate
prefunded EOAs or predeploys. To expose those accounts while
`chain.start_block` is zero, configure exactly one server-only source:

- Set the absolute `chain.genesis_file` path (or
  `ETHERVIEW_CHAIN_GENESIS_FILE`) and mount the chain's authoritative standard
  Genesis JSON read-only into the monolith or sync process.
- Set `chain.genesis_url` (or `ETHERVIEW_CHAIN_GENESIS_URL`) to fetch it once
  from a public HTTPS URL. The URL must use port 443 and cannot contain
  credentials, a query, a fragment, or path-traversal segments. Redirects,
  environment proxies, and private or special-use destinations are rejected.

Both sources are capped at 64 MiB and 500,000 allocation entries. A remote
response must use identity encoding and contain valid JSON. Its media type must
be JSON, vendor `+json`, `application/octet-stream`, or `text/plain`.
Optionally set `chain.genesis_sha256` (or
`ETHERVIEW_CHAIN_GENESIS_SHA256`) to the non-zero lowercase SHA-256 digest of
the exact response bytes. The digest is strict; HTTP checksum headers and
sidecar files are ignored. `chain.genesis_fetch_timeout` controls only the
remote bootstrap request, defaults to 60 seconds, and accepts values from one
second through five minutes. Its environment equivalent is
`ETHERVIEW_CHAIN_GENESIS_FETCH_TIMEOUT`.

The local file is read and parsed when the importer is constructed, then waits
for canonical block zero; the remote source is fetched only after canonical
block zero is present and the per-chain lock is held. The importer computes the
Ethereum account trie, block hash, code hashes, and storage roots, then requires
the computed block hash and state root to match the configured/indexed chain.
The import is one PostgreSQL transaction; mismatch, malformed JSON, duplicate
keys, or a write failure exposes no partial account set. Raw storage slots are
used only for root calculation and are not retained. The first successful
import, including a late import, atomically requests one source-deduplicated
block-zero proxy replay; completed restarts do not request it again. Genesis
code does not classify predeploys as tokens without normal token evidence.

Multiple sync replicas serialize an initial remote fetch with a per-chain
PostgreSQL session advisory lock and recheck completion after acquiring it.
Network I/O, checksum verification, and parsing occur outside the import
transaction. Once a complete import exists, restarts read it without contacting
the remote service; an explicitly configured checksum must still match the
persisted document digest. Transport failures, timeouts, HTTP 429, and 5xx
responses expose a stable redacted unavailable state. Other non-success status,
checksum, invalid-content, and chain-identity failures expose a stable redacted
failed state. Neither response bodies, URLs, nor nested network errors are
published.

Without a configured source, `/api/v1/genesis/accounts` returns the typed
`genesis_state` unavailable capability; this must not be interpreted as an
empty allocation. A successfully authenticated Genesis JSON whose `alloc` is
actually empty returns an empty successful page.

For Compose, mount the file through an override and set
`ETHERVIEW_CHAIN_GENESIS_FILE` to the container path, or set the remote URL,
optional checksum, and timeout environment variables without mounting a file.
For Helm, either place the file in a read-only PVC and set
`genesisState.existingClaim`, `genesisState.key`, and
`genesisState.mountPath`, or configure the mutually exclusive remote source
fields `genesisState.url`, `genesisState.sha256`, and
`genesisState.fetchTimeout`. Only monolith/sync Pods receive Genesis source
configuration. The remote source adds no database migration, public API change,
or SPA protocol change.

## RustFS trace-cache deployment

The optional `accelerators` Compose profile uses `rustfs/rustfs:1.0.1` as
`object-storage`. S3 listens on container port 9000 and the console on 9001;
production Compose publishes neither port. RustFS runs as the image's default
non-root user, with a new `rustfs-data` named volume. The `/health/ready` probe
checks the store itself, never application readiness.

To enable a local trace cache:

1. Copy the deployment environment example to a private environment file and
   set `RUSTFS_ACCESS_KEY` and `RUSTFS_SECRET_KEY` to your chosen credentials.
   Keep `ETHERVIEW_S3_ENDPOINT` unset until the bucket exists.
2. Start only the object store through the repository wrapper:
   `.github/scripts/compose.sh --project-directory . -f deploy/compose.yaml --env-file /absolute/path/to/compose.env --profile accelerators up -d --wait object-storage`.
3. Provision `etherview-cache` with an S3 client on the Compose network, or
   temporarily publish the console using this local override:

   ```yaml
   services:
     object-storage:
       ports:
         - "127.0.0.1:9001:9001"
   ```

   Save the override outside tracked files and start with both
   `--project-directory . -f deploy/compose.yaml -f /absolute/path/to/rustfs-console.yaml`,
   retaining the same `--env-file` and profile arguments. Open the local
   console at `http://127.0.0.1:9001`, sign in with the RustFS credentials, and
   create the bucket. Recreate the service without the override afterward.
4. Set `ETHERVIEW_S3_ENDPOINT=http://object-storage:9000`,
   `ETHERVIEW_S3_BUCKET=etherview-cache`, `ETHERVIEW_S3_REGION=us-east-1`, and
   `ETHERVIEW_S3_PATH_STYLE=true`. Set the explicit Etherview access/secret pair
   to matching RustFS credentials. Recreate the API-capable service using the
   same environment file and the chosen `monolith` or `distributed` profile.
5. Request an indexed transaction trace and verify that an object appears in
   the bucket and subsequent reads succeed. The cache fills only for eligible
   completed trace generations; PostgreSQL remains authoritative.

When replacing MinIO, the old `object-data` Docker volume remains untouched.
Do not mount it into RustFS or run volume-pruning commands as part of the
switch. No object migration is needed: cache misses read PostgreSQL and write
fresh RustFS objects. To bypass RustFS, unset the application S3 endpoint and
bucket and recreate the API-capable service. Retain both volumes; disabling
this accelerator does not require restoring database data.

## S3 trace-cache credentials

The S3-compatible trace cache is enabled only when `adapters.s3_endpoint` and
`adapters.s3_bucket` are set. A complete
`ETHERVIEW_S3_ACCESS_KEY(_FILE)`/`ETHERVIEW_S3_SECRET_KEY(_FILE)` pair plus an
optional session token is an explicit highest-precedence override. When that
pair is absent, only `all`/`api` loads the AWS SDK default chain: AWS
environment credentials, Web Identity, shared configuration/profile, container
credentials, then EC2 instance role. Temporary credentials are cached and
refreshed by the SDK without a process restart.

With both explicit credentials and `ETHERVIEW_S3_REGION`, unrelated missing or
malformed AWS profiles are ignored. If the explicit region is omitted, AWS
configuration is still loaded for region discovery. Credential endpoint and
STS HTTP redirects are rejected; configure their final endpoints directly.

Empty explicit keys never mean anonymous access. Missing, expired, malformed,
or unreachable credentials produce a bounded redacted cache failure and the
request uses the PostgreSQL trace projection. They do not withdraw readiness.
Object transport uses AWS SDK for Go v2 with a single request attempt and no
HTTP redirects. Region precedence is `adapters.s3_region`, AWS environment/shared
configuration, then `us-east-1`. Configure the actual bucket region when using
AWS S3 outside `us-east-1`; there is no automatic bucket-region lookup.
The existing `adapters.operation_timeout` bounds both credential retrieval and
the object operation. A malformed default-chain configuration may fail API
startup as a static configuration error; ordinary absence remains lazy until
the first cache operation.

Compose passes explicit Etherview and standard AWS credential inputs only to
the `all`/`api` service. File paths named by AWS variables must be mounted into
the container with the same absolute path. For Helm, enable the dedicated
`s3ServiceAccount` instead of annotating the shared account. IRSA uses the
dedicated account's annotations. For EKS Pod Identity, create the AWS
association separately and set `eksPodIdentity=true`; the chart then permits
only the API-capable Pod to reach the fixed EKS agent addresses on TCP/80.
Other roles retain no S3 Secret or credential-agent egress. Keep EC2 IMDS
blocked for Kubernetes workloads.

If IRSA must call STS while `networkPolicy.allowExternalHTTPS=false`, place
only reviewed regional STS CIDRs in `networkPolicy.runtimeHTTPSCIDRs`. Do not
add internet-wide or link-local ranges. After changing credential source,
restart the `all`/`api` Deployment and confirm trace-cache hits while
PostgreSQL remains authoritative.

## ENS primary names

ENS is disabled by default. Enable `features.ens` only for API-capable roles.
When the explored RPC is not exact Ethereum Mainnet, supply the dedicated L1
endpoint set through `ETHERVIEW_ENS_RPC_URLS` or its `_FILE` form; Helm exposes
the optional `secrets.ensRPCURLsKey` only to `api` and `all`. RPC URLs remain
Secret-only and are never accepted from the ConfigMap-backed `ens` section.

The default official gateway is `https://ccip-v3.ens.xyz`. A custom Registry
and modern Universal Resolver may be configured under `ens.custom`; both are
on the explored chain and are used only after official no-record. Custom names
are visibly marked and never replace an exact copy, QR, or link address. An ENS
outage degrades only names; inspect `etherview_ens_resolutions_total` and the
bounded `capability_unavailable` code without retrying writes or changing core
readiness.

## Capacity, HA, and failover

`deploy/config.reference-capacity.yaml` and the Helm
`values-reference-capacity.yaml` are a reproducible starting profile for the
core explorer read mix. They are not a release result. The Helm profile runs
redundant API, sync, enrichment, and maintenance roles, retains at least one
replica during voluntary disruption, and uses a component-scoped hard hostname
spread with at least two eligible domains. A placement that cannot satisfy
that failure-domain boundary remains Pending instead of silently collapsing
onto one node. Its configured steady-state/HPA maximum is 18 non-terminating
application Pods. Trace,
verification, metadata, Sourcify, pricing, and ENS remain separate optional
capability profiles because their RPC, compiler, and external-service costs
must be measured independently. Its rolling strategy sets `maxSurge: 0` and
`maxUnavailable: 1`, preventing an intentional surge while the per-role
disruption budgets retain one available replica.

Budget PostgreSQL connections before raising replicas.
`database.max_connections` is the writer-pool cap for every application
process. A non-empty `database.read_url` or either non-zero reader bound enables
one additional pool only in an `api` or `all` process. An empty reader URL
inherits the writer endpoint, and a zero reader bound inherits the corresponding
writer bound. `min_connections` is the native pgxpool minimum connection
count, not a maximum idle count. Idle lifetime is five minutes; connection
lifetime is thirty minutes.

Every process exports its local writer pool and, when present, API reader pool
through `etherview_database_max_open_connections`,
`etherview_database_connections`, `etherview_database_empty_acquire_count_total`,
`etherview_database_empty_acquire_wait_seconds_total`, and
`etherview_database_connections_closed_total`. Empty-acquire counters describe
successful acquisitions that initially found no idle connection and their wait
time. Connection closures distinguish idle time and lifetime; the former
stdlib idle-limit counter has no native equivalent. Use the bounded `pool` label to
separate writer and reader saturation. The same scrape exposes
`etherview_go_goroutines`, `etherview_go_heap_alloc_bytes`,
`etherview_go_heap_objects`, `etherview_go_gc_cycles_total`, and
`etherview_go_gc_pause_seconds_total`; correlate these process-local signals
with Pod/container CPU and memory rather than treating either source alone as a
capacity result.

For capacity planning, define:

```text
W = database.max_connections
R = database.read_max_connections, or W when read_max_connections is zero
P = maximum concurrent application Pods across every enabled role
A = maximum concurrent Pods containing the api role

steady application connections = P * W + A * R
```

Omit `A * R` when the reader is disabled. The reference distributed Helm
profile has `P = 18`, `A = 8`, and `W = 12`: it therefore permits 216 writer
connections. Enabling a same-sized reader gives `R = 12` and adds 96 reader
connections, for 312 steady-state application connections.

Kubernetes may start a replacement while a deleted Pod is still terminating, and
terminating replicas are not included in `maxSurge`. A fully concurrent rollout
can therefore overlap old and replacement pools: reserve up to twice the
steady-state application budget—432 without the reader or 624 with the
same-sized reader in the reference profile—or serialize role rollouts and
measure their actual termination overlap. Also reserve server slots for the
migration Job and operator access, or use a separately operated
transaction-pooling proxy.

PostgreSQL remains mandatory: point `database.url` at the deployment's HA
writer endpoint and test its documented RTO. A database outage withdraws
readiness; it is never converted into cached success. Migration execution stays
advisory-locked across failover and must be rerun or checked after the writer
endpoint is stable.

Reader sessions force read-only transactions and must expose the writer's exact
migration ledger and configured chain/genesis identity at startup. Once enabled,
the reader has no automatic writer fallback: its outage withdraws API readiness
without stopping split sync, worker, verification, or maintenance roles.
Replica replay must remain bounded and monotonic enough for cursor pagination;
ordinary read-model results are not promised read-after-write consistency.

`runtime.worker_count` controls durable enrichment, trace, metadata, and
maintenance workers in each process. `verification.worker_count` independently
controls the compiler-verification workers in each API-capable process;
`verification.derived_worker_count` controls factory-derived verification work.
`runtime.backfill_workers` controls independent sync range claimers, while
`runtime.backfill_batch_blocks` bounds each durable range lease to 1–256
blocks. `runtime.backfill_batch_bytes` and `runtime.backfill_batch_rows` bound
the complete raw ownership and relational work retained by each worker while
that lease is processed. The worker reserves half of each aggregate budget for
the next fetched block, commits restart-safe canonical subsegments when the
other half fills, and rejects a single block that cannot fit rather than
exceeding the bound. Multiplying either worker value by replicas increases
PostgreSQL, RPC, and aggregate memory pressure; it does not change lease
ownership or publication fencing. Start with the reference values and use
queue age, sync lag, RPC latency, pool saturation, CPU, heap, and GC together
rather than tuning from CPU alone.

Address ERC-20 holdings retain every first-seen exact `(chain, owner, token,
block hash)` balance in `erc20_balance_reconciliations`, including zero and
orphaned observations. Repeated requests for the same snapshot do not add rows,
but a newer queried block hash does even when the numeric balance is unchanged.
There is no backfill or retention sweep. Capacity reviews must monitor the
table and owner-snapshot index with `pg_total_relation_size`, PostgreSQL write
rate, and state-RPC call reduction; do not delete rows as routine cache cleanup
because they are immutable reorg/audit facts.

The `ETHERVIEW_RPC_URLS` Secret accepts either the original comma-separated
all-purpose shorthand or a JSON endpoint array. Use the structured form for
capacity work so head latency, historical throughput, exact-state traffic, and
trace traffic can be isolated:

```json
[
  {
    "name": "live-a",
    "url": "https://rpc.example.invalid/live",
    "purposes": ["head"],
    "max_requests_per_second": 25
  },
  {
    "name": "history-a",
    "url": "https://rpc.example.invalid/history",
    "purposes": ["history", "state"],
    "max_requests_per_second": 100
  }
]
```

Endpoint limits are per process. For a shared upstream, the sum of every
replica's configured limit must remain within the provider budget. URLs stay in
the Secret even when they do not currently contain credentials.

Anonymous rate limiting uses the direct peer unless it matches one of the
canonical IPs or CIDRs in `security.trusted_proxies`. Only a trusted peer may
supply a bounded `X-Forwarded-For` chain, which is resolved from right to left
to the first untrusted hop. Never trust an internet-wide CIDR. Process-local
anonymous and authenticated bucket sets have independent cardinality limits
(16,384 and 65,536 per process respectively).
An inactive bucket expires only after a complete refill and its idle TTL; if a
set is full before then, a new identity is rate-limited rather than resetting
an existing client's state. When Redis is configured, a timeout falls back to
that bounded local limiter and opens a short circuit so a continuing Redis
outage does not spend the full adapter timeout on every request. The fallback
quota is per replica; it preserves availability, not a globally exact budget.
Runtime-status cache invalidation uses an independent circuit with the same
bounded backoff. While open, cache reads and writes remain disabled and durable
event relay continues from PostgreSQL without another Redis call per event.
After backoff, one event probes the generation fence and invalidation; only a
successful probe re-enables cache access.

For application failover, run at least two replicas for every role whose
continuity is required. Durable backfill and job leases permit another replica
to resume after graceful release or expiry without stealing an active lease.
One short-lived PostgreSQL reporter lease selects the sync replica that writes
the aggregate status/event stream; backfill workers do not each append an
event. An ordinary reporter failure publishes a conservative snapshot and
releases quickly. A canonical-safety halt remains sticky and scrapeable on the
reporting process and protects its snapshot for the active lease, but it is not
a permanent cluster latch: after lease expiry a healthy peer may take over. If
the fault is chain-wide, peers reach the same boundary independently. API
replicas independently replay PostgreSQL runtime events. NATS, Redis, and S3
may be restarted under traffic; their loss changes latency or quota scope only.
The runtime Compose smoke exercises monolith/distributed semantic parity and
worker loss on a deterministic dataset; the repository load driver records
route mix, throughput, latency, errors, and final core lag.

The short runtime load profile is a regression for the harness and failover
contract. P70-T04 remains responsible for the final revision's 500 RPS,
30-minute report with named hardware, dataset, RPC behavior, resource peaks,
common-query p95 below 500 ms, error rate below 0.1%, and lag no more than two
blocks. Do not promote that release gate from a shorter runtime run.

Run a bounded tuning pass against an already deployed instance with:

```sh
mkdir -p artifacts
ETHERVIEW_LOAD_BASE_URL=https://explorer.example.invalid \
ETHERVIEW_LOAD_REVISION=0123456789abcdef \
ETHERVIEW_LOAD_DATASET=mainnet-snapshot-2026-07-23 \
ETHERVIEW_LOAD_HARDWARE=kubernetes-reference-profile \
ETHERVIEW_LOAD_RPC_BEHAVIOR=isolated-head-history-state \
ETHERVIEW_LOAD_PATHS='["/api/v1/status","/api/v1/blocks?limit=20","/api/v1/tokens/0x1111111111111111111111111111111111111111/holders?limit=200","/api/v1/tokens/0x1111111111111111111111111111111111111111/holders/count","/v2/api?chainid=1&module=token&action=tokenholderlist&contractaddress=0x1111111111111111111111111111111111111111&page=1&offset=1000","/v2/api?chainid=1&module=token&action=tokenholdercount&contractaddress=0x1111111111111111111111111111111111111111"]' \
make test-load >artifacts/load.json
```

Replace the example token address with a named high-cardinality ERC-20 whose
Holder snapshot is complete in the reference dataset. Run this read mix while
new canonical blocks contain ERC-20 transfers so `holder@1` reconciliation and
the HTTP workload compete for the same representative PostgreSQL and RPC
resources.

`make test-soak` selects the 500 RPS/30-minute P70 defaults and fixes the
reference gates at p95 below 500 ms, error rate below 0.1%, final lag no
greater than two blocks, and at least 99% successful throughput (495 RPS).
Its fixed five-second request timeout bounds the complete final drain to 11
seconds, inside that 1% throughput allowance; this avoids making the result
depend on the last few in-flight completions without accepting the former 475
RPS floor. Both commands fail when their p95, error-rate, achieved-throughput,
or final core-lag threshold is missed and emit a bounded JSON report. The
driver uses a bounded admission queue and overall deadline; saturation drops
count as failures instead of extending the run indefinitely. Its final status
probe requires canonical string lag plus `core_ready=true` and
`backfill_complete=true`. For
authenticated routes, pass a server-readable key through `ETHERVIEW_LOAD_API_KEY_FILE` or
`ETHERVIEW_LOAD_API_KEY`; the driver rejects credentialed URLs, cross-origin
paths, redirects, and `apikey` query parameters.

## ERC-4337 UserOperation indexing

UserOperation browsing is disabled by default. Enable it only with an explicit
EntryPoint registry whose address, wire version, and inclusive active block
range have been verified for the indexed chain. The four currently supported
official singleton addresses are shown below; replace each `from_block` with
that deployment's exact block on the selected chain, and set `to_block` when a
deployment is no longer accepted there:

```yaml
features:
  user_operations: true

erc4337:
  entry_points:
    - address: "0x5FF137D4b0FDCD49DcA30c7CF57E578a026d2789"
      version: "0.6"
      from_block: 123
      to_block: 456
    - address: "0x0000000071727De22E5E9d8BAf0edAc6f37da032"
      version: "0.7"
      from_block: 457
      to_block: 789
    - address: "0x4337084d9e255ff0702461cf8895ce9e3b5ff108"
      version: "0.8"
      from_block: 790
      to_block: 999
    - address: "0x433709009B8330FDa32311DF1C2AFA402eD8D009"
      version: "0.9"
      from_block: 1000
```

The numeric ranges above are placeholders, not mainnet deployment facts.
Entries for the same address must not overlap; at most 16 entries are accepted.
An operator may instead provide the complete array through the bounded JSON
environment value `ETHERVIEW_ERC4337_ENTRY_POINTS`. Configuration is normalized
into a digest that scopes publications, coverage, readers, and cursors, so an
address/version/range change does not expose results produced under the old
registry.

New canonical heads enter `userop@1` through the ordinary enrichment outbox.
Migrations never schedule historical work. After first enablement or a registry
change, record the canonical range and run an explicit bounded reindex, then
wait for continuous UserOperation completeness in `/api/v1/status`:

```sh
etherview reindex --config /etc/etherview/config.yaml \
  --from 12000000 --to 12010000 --stage userop \
  --reason "publish userop@1 for the reviewed EntryPoint registry"
```

Do not use a successful empty list as evidence beyond the published coverage
range. The index intentionally excludes pending Bundler mempools, submission,
simulation, nested EntryPoint calls, and locally reconstructed UserOperation
hashes.

## Repair and reindex

Before scheduling work, record the exact chain, inclusive block range, current
canonical hashes, finalized height, and an operator reason. Never use repair as
fork choice.

```sh
etherview repair --config /etc/etherview/config.yaml \
  --from 12000000 --to 12000010 --stage core \
  --reason "replace incomplete receipts from validated history provider"

etherview reindex --config /etc/etherview/config.yaml \
  --from 12000000 --to 12000010 --stage token \
  --reason "rebuild token facts after core repair"

etherview reindex --config /etc/etherview/config.yaml \
  --from 0 --to 12000010 --stage proxy \
  --reason "publish proxy v2 after the OpenZeppelin schema cutover"

etherview reindex --config /etc/etherview/config.yaml \
  --from 0 --to 12000010 --stage abi \
  --reason "publish ABI v5 after trace v4 and proxy v2 are complete"

etherview reindex --config /etc/etherview/config.yaml \
  --from 0 --to 12000010 --stage trace \
  --reason "publish trace v4 execution identities and constructor decoding"

etherview reindex --config /etc/etherview/config.yaml \
  --from 0 --to 12000010 --stage state_diff \
  --reason "publish state_diff v3 complete-prestate execution identities"

etherview reindex --config /etc/etherview/config.yaml \
  --from 12000000 --to 12000010 --stage userop \
  --reason "publish userop@1 for the reviewed EntryPoint registry"

etherview admin repair list --config /etc/etherview/config.yaml --limit 100 --format table
```

`repair --stage core` re-fetches through the history-purpose RPC path. The
database rechecks chain, height, canonical hash, parent, and finality while
holding the chain lock. It cannot move canonicality or checkpoints. A range at
or below finalized height requires `--allow-finalized` plus the recorded
reason; this permits only a same-identity refresh.

`reindex --stage consensus|blob|native_transfer|proxy|abi|token|stats|trace|state_diff|userop|holder` queues work for
the currently canonical block hash. It does not steal queued work or an active
lease. Repair deliberately does not infer a downstream rebuild range; schedule
each required derived stage explicitly and wait for its durable publication
result. After the
OpenZeppelin proxy cutover, schedule `proxy` before `abi`; the ABI worker also
refuses to claim a block until the current `proxy@2` result is published.
`holder@1` also waits for the exact block's `token@2` result and terminal
`proxy@2` result. Authoritative holder reads require a bounded sequence of
Holder reindex requests starting at block zero and continuing through the
current canonical tip; neither migration nor startup schedules this history.
Each request must retain its operator reason. The enrich role uses its
state-purpose endpoint for exact EIP-1898 `balanceOf` and `totalSupply` calls;
missing historical state leaves the affected Holder block unavailable rather
than publishing event-derived balances.
The `trace@4` cutover is an explicit bounded reindex, never a migration-time
historical enqueue. Each completed Trace generation requests the existing
`proxy@2` replay and then `abi@5`; wait for those publications before treating
the range as proxy-interaction complete. Nodes that reject `withLog` with
`-32602` still publish the call tree, but their logs remain visibly on the
conservative address fallback path.

The baseline includes transaction execution-code rows and the current
`state_diff@3` witness. It creates no historical coverage or replay workload.
Run `reindex --stage state_diff` for the chosen canonical range and
wait for `state_diff@3`. Its completion requests `trace@4`; after Trace
publishes, wait for `proxy@2` and then `abi@5`, then rebuild and verify the
affected coverage range. Operators must not substitute block-end or `latest`
code for unavailable transaction prestate.

The baseline also includes partitioned ABI-owned effective execution identities
for `abi@5`; initialization never backfills or queues history.
For a bounded historical or Preview range, first ensure its canonical
`state_diff@3` and `trace@4` publications exist, replay `proxy@2`, then run
`reindex --stage abi` and wait for `abi@5`. The replay uses only stored exact
block evidence. Do not derive an identity from block-end state, `latest`, or
the last delegation observed for an address in the block.

### Proxy detection deployment acceptance

Standard and Solady legacy CWIA detection always run. Published evidence is
returned as `proxy_detection`; there is no separate collection or visibility
switch. Safe and Diamond remain independent, default-off capabilities:

```yaml
features:
  safe_proxy_detection: true
  diamond_proxy_detection: true
```

Use `ETHERVIEW_FEATURE_SAFE_PROXY_DETECTION` and
`ETHERVIEW_FEATURE_DIAMOND_PROXY_DETECTION` for equivalent environment overrides.
Apply the selected family settings consistently to all enrichment processes.

The unified schema and binary are a fresh-baseline cutover. Recreate older
development databases using [fresh database initialization](#fresh-database-initialization)
and remove retired configuration keys; strict YAML rejects unknown fields.
Update external clients to `proxy_detection`. There are no wire aliases,
startup conversions or automatic historical scans.

Before serving a new revision to users, run an isolated acceptance deployment
with the exact image, schema and intended detector configuration. Record its
chain and a finalized range of at most 10,001 blocks, including known positives
for each enabled family and 100 deterministic negatives (or the whole smaller
population). Use the
[deployment review template](reviews/proxy-detection-deployment-review-template.md).
Existing regression/CI results do not establish live-chain acceptance.

For an explicitly approved bounded historical sample, record canonical boundary
hashes and the operator reason, then replay Proxy before ABI:

```sh
etherview reindex --config /etc/etherview/config.yaml \
  --from "$FROM_BLOCK" --to "$TO_BLOCK" --stage proxy \
  --reason "proxy detection deployment acceptance" --allow-finalized
```

Reindex `abi` only after the exact Proxy generations have published. The command
resolves heights to exact hashes; generation fences make retries idempotent.
Replaying older history can establish new authoritative CWIA coverage, so the
range and that effect must be part of the operator's explicit backfill approval.
Migrations and startup never enqueue that work.

Require complete current publications and independently read back canonical
standard/CWIA relations, exact bindings, detector evidence and Diamond Loupe/Cut
maps. Review every unknown, inconsistent and conflicting outcome. Check Safe
shell/slot-0/official-singleton distinctions; use the maintained fixed-mainnet
identities for official provenance claims. Bulk acceptance does not cover
interface-only deep Safe candidates. Confirm that Safe cannot authorize ordinary
implementation writes and that Diamond calls target the Diamond with selector
and code identity checks. Public responses must contain the expected evidence
without changing existing binding and management eligibility.

Compare `etherview_proxy_detection_duration_ms`, RPC call/error counters,
result/conflict counters, Safe counters, allocation/GC and database write volume
against the prior revision for the same sample. The standard RPC path is now
included in the shared context counters; additional summary rows are expected.
No unexplained unknowns, publication gaps or authority changes qualify as a pass.

Disabling `safe_proxy_detection` stops new Safe detection. Disabling
`diamond_proxy_detection` stops new Loupe work and DiamondCut indexing. Neither
removes historical facts or hides already published evidence. There is no global
hide-results or old-detector fallback: withdraw a failing revision from service
and deploy a corrected matching binary/schema. Do not point an older binary at
the new baseline. Keep controlled exports for diagnosis and preserve orphan facts.

The list command is newest-first and bounded to 1–1000 rows. Its default JSON
output and optional `--format table` both report `failure_present` without
returning stored nested error text. Use the stable
maintenance log code and metrics to choose the next investigation, then inspect
PostgreSQL under the deployment's controlled operator-access policy if deeper
forensics are required.

The maintenance role also owns feature-gated PostgreSQL housekeeping. When
wallet authentication is enabled it deletes at most 1,000 expired challenges
and at most 1,000 expired or revoked sessions per sweep. When prepaid billing
is enabled it expires bounded legacy/top-up payment leases, releases expired
usage reservations, and expires untouched top-up intents without changing a
committed debit. All sweeps are chain-scoped, select
candidate rows with `SKIP LOCKED`, use the writer, run once at startup and then
on `maintenance.interval`, and stop with the shared supervisor. A transient
failure retains readiness, logs only
`user_auth_cleanup_failed`, `x402_billing_expiry_failed`, or
`prepaid_billing_expiry_failed`, and retries with
bounded backoff. Disabling a feature registers no corresponding housekeeper.
The split maintenance role must not receive session, fingerprint, or
facilitator-header Secrets.

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

Rotating `ETHERVIEW_SESSION_PEPPER` invalidates every session without changing
user, role, or audit rows. Roll all `all`/`api` replicas promptly so replicas do
not temporarily disagree about Cookies, then require every user to sign in
again. Never reuse the API-key or x402 fingerprint pepper.

For rollback, disable `features.user_auth` and restart the API processes; keep
the additive tables for audit and a later retry. The feature flag alone does
not mutate session rows. If a later re-enable must not accept any still-current
Cookie, rotate the session pepper as part of the rollback before restarting.

## Prepaid API billing and x402 top-ups

Initialize the current baseline with both features off. The schema retains
`legacy_request` as an audit-only payment purpose; such rows never produce
account credit. The baseline does not import historical accountless payments.
Configure `features.api_billing`, the CAIP-2 network,
asset, reservation TTLs, and the explicit
`billing.operations.<etherscan.module.action>.amount_atomic` catalog first.
Native `/api/v1` reads remain free regardless of this catalog.

Enable `features.x402_topups` only after SIWE and user-owned API keys are
enabled. Configure the public origin, top-up bounds, recipient, ordered
`[eip3009, permit2]` methods, EIP-712 asset fields, HTTPS facilitator origin,
and exact facilitator CIDRs. Inject the independent fingerprint pepper and
optional facilitator headers only into `all`/`api`. Helm requires a dedicated
API/all TCP/443 egress policy; every DNS result must remain inside the
configured CIDRs. `facilitator_unsafe_allow_http` exists only for the
repository-owned disposable local environment and Helm fixes it to false.

Validate the final API-role configuration before enabling top-ups:

```sh
etherview doctor --config /etc/etherview/config.yaml
```

Roll out with an empty operation-price map, enable top-ups, perform one
EIP-3009 and one Permit2 staging top-up, and then add priced actions in small
batches. Facilitator failure prevents only new top-ups. Existing account credit
continues to serve priced `/v2/api` requests because consumption has no
facilitator dependency.

A top-up in `settling` with `settlement_unknown` requires independent
facilitator/chain inspection. `settlement_pending` additionally carries one
immutable transaction hash; settled reconciliation must reuse it. Inspect and
reconcile with:

```sh
etherview admin billing inspect \
  --config /etc/etherview/config.yaml \
  --id 00000000-0000-0000-0000-000000000000

etherview admin billing reconcile \
  --config /etc/etherview/config.yaml \
  --id 00000000-0000-0000-0000-000000000000 \
  --outcome settled \
  --transaction-hash 0x0000000000000000000000000000000000000000000000000000000000000001
```

A settled top-up reconciliation atomically settles the payment and intent,
appends one credit entry, and advances the account. Never retry settlement or
replace a known pending hash. Usage maintenance releases only expired
reservations; it never refunds a committed debit, including when the process
crashes after commit but before response delivery. Administrator corrections
use the authenticated adjustment endpoint with an explicit reason.

For rollback, first clear `billing.operations` so no new usage reservations
start, then disable `features.x402_topups`, and finally disable
`features.api_billing` if required. Permanently retain accounts, balances,
entries, usage, intents, payments, and payment events. Never convert legacy
accountless request settlements into credit.

The disposable local environment is:

```sh
make start-x402-local
X402_LOCAL_TOPOLOGY=distributed make recreate-x402-local
make stop-x402-local
```

It uses Anvil, PostgreSQL 18, the production Etherview image, and separate
test-only contract/facilitator images. Its deterministic development keys and
fixtures must never be used outside that environment.

### Base Sepolia prepaid release gate

The release gate is a one-shot pair of real EIP-3009 and Permit2 reports. Each
must cover SIWE, top-up settlement, account credit, user-owned API-key debit,
and writer/chain reconciliation. It remains blocked without operator funds,
file-only payer credentials, a staging facilitator, writer access, an
independent RPC, and the deployed image digest. Local Anvil evidence cannot
close it, and failed or unknown real-payment attempts must never be retried
automatically.

### Factory-derived verification rollout and backfill

Factory-derived verification consumes only PostgreSQL-persisted authenticated
compilation units, canonical `trace@4` CREATE/CREATE2 frames, and canonical code
observations. It does not require archive/debug RPC and never inherits a
submitter's Sourcify consent. Roll out in this order on every `all`/`api`
replica:

1. Initialize the current baseline with all derived flags false. Successful
   verification starts retaining new authenticated compilation units.
2. Set `DERIVED_VERIFY_ENABLED=true` only. Workers record bounded dry-run
   attempts but do not publish child verification.
3. Review `etherview_derived_verification_total{kind,result}` for unexpected
   ambiguity, runtime mismatch, stale evidence, or backlog growth, then set
   `DERIVED_VERIFY_BACKFILL_ENABLED=true` to publish historical unique matches.
4. Set `DERIVED_VERIFY_FORWARD_ENABLED=true` only after backfill is enabled.
   The post-`trace@4` dispatcher then schedules future and transitive work
   without running the matcher in the trace transaction.

The baseline creates the forward queue with immutable `trace@4`/`proxy@2`
publication-generation events and durable rescan floors. When changing this
queue protocol, stop or replace every `all`/`api` replica as one rollout; do
not mix worker versions with incompatible queue semantics. Initialization and
startup do not queue failed scans or `pending_runtime` attempts and do not
create a historical workload.

Before recovering an upgraded Preview deployment, record redacted aggregate
state from an operator database session. Preserve the result with the rollout
evidence; do not include connection strings, addresses, hashes, compilation
IDs, or other row-level data:

```sql
SELECT status, count(*) FROM derived_verification_scans
GROUP BY status ORDER BY status;
SELECT count(*) AS scan_count FROM derived_verification_scans;
SELECT status, count(*) FROM derived_verification_attempts
GROUP BY status ORDER BY status;
SELECT status, count(*) FROM derived_verification_forward_blocks
GROUP BY status ORDER BY status;
```

After all new replicas are healthy, explicitly recover Preview with the
existing audited command; migration startup must not substitute for it:

```sh
etherview admin derived-verification backfill \
  --config /etc/etherview/config.yaml \
  --reason "derived verification queue hardening recovery"
```

Then observe only the closed-label
`etherview_derived_verification_total{kind,result}` series. In particular,
review `dispatch=trace_generation|proxy_generation`,
`rewind=trace|pending_runtime`, and `lease=renewed|lost` together with the
aggregate scan/attempt/forward-event counts. Never add addresses, hashes, or
compilation IDs as metric labels. Production defaults remain disabled.

Worker and scan bounds are controlled by `DERIVED_VERIFY_WORKER_COUNT` and
`DERIVED_VERIFY_MAX_TRACES_PER_SCAN`. To request an audited idempotent rescan of
all eligible compilation epochs, or one factory only, use:

```sh
etherview admin derived-verification backfill \
  --config /etc/etherview/config.yaml \
  --reason "reviewed rollout backfill"

etherview admin derived-verification backfill \
  --config /etc/etherview/config.yaml \
  --address 0x0000000000000000000000000000000000000001 \
  --reason "retry factory after runtime observations arrived"
```

The command never deletes attempts or verified artifacts. For each unleased
scan it first resolves the exact canonical creator-code epoch, queues one scan
at that epoch start, and retains an incorrect later-start duplicate as failed
with `superseded_epoch_start` so it cannot be forward-dispatched again.
Existing terminal attempts keep publication idempotent, while pending-runtime
attempts can be reconsidered. A reorg marks attempt provenance stale and
current readers stop resolving the orphaned child until the exact block hash
is reattached.

For an existing Preview deployment, rebuild and recreate only the application
services with `make recreate-preview`; do not remove the PostgreSQL or Geth
volumes. Then run the address-scoped command inside the API container. This
repairs scans created by an older binary whose cursor began at verification
publication rather than at the creator code epoch.

### Runtime smoke verification fixture (development only)

The runtime parity smoke target defaults `ETHERVIEW_RUNTIME_FIXTURE_IMAGE` to
Foundry `v1.8.5` and starts anvil on the Prague hard fork as its deterministic
chain fixture source. Its Go harness generates temporary EIP-7702 keys and raw
transactions, funds them through `anvil_setBalance`, and does not require host
`cast` or reusable private keys. Do not rely on hardcoded chain, block, or
transaction hashes in manual checks; capture identity from
`eth_getBlockByNumber('0x0')` and transaction submission results at runtime and
reuse the captured values.
Set `ANVIL_ARGS` only for local launch tuning (for example alternate anvil
defaults) and keep these test-only overrides out of production runbooks.

## Application-locked Vyper runtimes

API/all use the lock embedded in the application. No Vyper catalog URL, public
key, signing service or catalog renewal is required. The supported supported versions
and their exact dependency choices change only with an application release.
Vyper 0.2.0 is no longer supported. All locked wheels are downloaded from
`https://files.pythonhosted.org`. No PyPI index lookup, pip installation or
native Python fallback occurs during verification.

Remove the retired `verification.vyper_catalog_url` and
`verification.vyper_catalog_public_key` configuration fields, plus their
`ETHERVIEW_VERIFICATION_VYPER_CATALOG_URL` and
`ETHERVIEW_VERIFICATION_VYPER_CATALOG_PUBLIC_KEY` environment settings, before
rolling out the new application.

Keep outbound HTTPS access to that host for cold cache misses. Existing valid
raw archives and assembled packages are reused offline. The existing
`verification.cache_directory` volume is rebuildable performance data: loss can
require downloading several archives per version, and upstream removal prevents
cold reconstruction. Stop all cache owners before manual cleanup. Hash failures
must be resolved by restoring exact bytes, never editing the embedded lock.

The image contains read-only runtimes at `/opt/etherview/python-wasm/modern`
and `/opt/etherview/python-wasm/legacy`, plus the host Node SEA. Vyper
0.4.0–0.4.3 uses Pyodide 314.0.7 / Python 3.14.2; the other 21 baseline versions use
Pyodide 0.29.5 / Python 3.13.2. Both startup probes are required. Every compile
uses a fresh process with V8 old-generation capped at 128 MiB, WASM memory at
384 MiB and configured I/O/time limits. These are not total RSS bounds.

Before any executor-identity rollout, stop new Vyper submissions and drain
queued/running jobs. This is an operator prerequisite; the fresh-database
baseline does not perform an upgrade-time queue check. Terminal evidence in
an already compatible database remains unchanged. Deploy an exact image digest with native
AMD64/ARM64 matrix and monolith/split acceptance, then check readiness and both
runtime probes before resuming. Do not mix different bound executor identities
or rebind jobs. The internal PostgreSQL generation records the application
snapshot and has no remote expiry; Solidity catalog freshness is unchanged.

Release validation uses `compiler/vyper/acceptance.mjs` with both native
acceptance records. It does not sign or publish a catalog. Retain current
assembly/integrity/license evidence. Application releases own recipe changes and
version withdrawal; live upstream metadata never changes an in-flight job.

## Private watches, notifications and CSV exports

With `features.user_auth` enabled, authenticated users can manage Watchlist and
Notifications tabs in `/account`; each address page also offers watch and CSV
actions. Labels and notification records are private to the SIWE user. The
maintenance role runs `43-watchlist-notifications` with writer access and no
session pepper; only API/all receives that existing secret. No email, Webhook,
browser push, Redis, NATS or object-store setup is required.

Monitoring starts strictly after the canonical tip recorded on enrollment or
re-enablement. Each account has at most 100 active watch entries, including
entries with notifications paused. Removing a watch preserves existing
notifications. Failed transactions and token mint/burn events are included;
Token notifications await exact published enrichment. Orphaned events remain
visible as invalidated, while a token replay temporarily marks its old output
as unpublished. Notifications are retained for 90 days; activity delayed beyond
90 days is not newly delivered. Cleanup runs in bounded maintenance batches.

A stopped maintenance role delays notifications without losing its durable
work. Restore the role and PostgreSQL connectivity to resume automatically;
expired 30-second leases are reclaimable. The stable operational warning code
`watchlist_processing_unavailable` indicates a retried processing/cleanup
failure. Check maintenance logs and Token stage health when delivery is late.
The public SSE stream is unrelated to private delivery and contains no watch
addresses, labels, account IDs or notifications.

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

### Preview Go gateway

`make start-preview`, `make recreate-preview`, and `make test-preview-metadata`
build the independent gateway from current source before their `--no-build`
Compose startup. `make preview-gateway-build` builds it separately;
`IPFS_GATEWAY_IMAGE` overrides its default `etherview-ipfs-gateway:local` tag.
The image contains only the Go executable and runs as root to read the existing
0600 certificate key bind mount. Keep the key permissions unchanged.

The executable accepts `serve --listen :8443 --upstream http://ipfs:8080
--tls-cert /run/ipfs-tls/tls.crt --tls-key /run/ipfs-tls/tls.key` (these are the
defaults). Its `healthcheck` command checks only the container-local readiness
listener. TLS certificates load at startup; recreate the gateway after rotation.
Invalid certificates fail startup, and upstream failures return a redacted 502.

Preview genesis predeploys the system contracts from the pinned Geth developer
allocation, including withdrawal, consolidation and builder request queues.
Activating a fork without its contracts can prevent Geth from building any
blocks. Changes to these allocations change the genesis hash: use a fresh
Compose project for validation, and do not initialize existing chain volumes
with a different genesis. `recreate-preview` preserves the same runtime genesis
and volumes; it is not a genesis migration procedure.

The CI `Preview IPFS metadata E2E` job runs the full offline-Kubo acceptance
on pull requests, main pushes and the daily schedule. It creates a temporary
runner CA and dedicated API/gateway certificates through `make preview-cert`;
no operator certificates or secrets are required. Its seven-day artifact
contains only acceptance reports and diagnostic logs, not certificate keys.

## Amsterdam protocol data

Amsterdam block responses expose exact decimal `slot_number` and
`block_access_list_hash` when present in the authenticated execution header.
Pre-upgrade blocks omit them. BAL bodies are not fetched or indexed.
Block gas utilization uses the maximum of the execution/state dimensions;
execution fees and base-fee burn use
receipt charged gas. The active enrichment versions are stats@4, token@2,
abi@5 and trace@4.

The always-on native_transfer@1 stage indexes only EIP-7708 protocol ETH logs.
Transaction and address native-transfer tabs expose this separate history,
including exact wei amounts and indexed coverage. Fees, withdrawals and
historical trace transfers are excluded. Missing publication returns unavailable;
replay and reorg revoke coverage until the current generation is published.
An operator may explicitly reindex `--stage native_transfer` for a reviewed
canonical range. No startup reindex or legacy data adapter is installed.

New disposable Preview networks activate Amsterdam at genesis. Existing Preview
volumes retain their genesis identity; do not replace their runtime genesis or
remove their volumes as part of an application update.

## Vyper compiler update proposals

The `Vyper compiler updates` GitHub workflow checks PyPI daily at 09:37
Asia/Shanghai. See the [compiler maintenance guide](../compiler/vyper/README.md#proposed-updates-from-pypi)
for manual discovery/generation, pinned tooling and PR lifecycle rules. Enable
Actions PR creation in repository settings; approve the generated PR's CI runs.
A maintainer must review changed dependency and archive identities before merging, and native
AMD64/ARM64 plus monolith/split acceptance remains mandatory before release.

Vyper 0.2.0 is no longer listed or accepted by the new application. Stop new
Vyper submissions and drain queued/running jobs before deploying this change,
then roll all roles to the same application identity and verify both runtime
probes (legacy uses 0.2.1). Do not rebind jobs, delete historical verification
records, or clear existing caches as part of this rollout. Historical terminal
results remain readable. The scheduled workflow does not deploy the application.

## Optional consensus and blob archives

Enable `consensus.enabled` to collect Beacon observations through the enrich
role. Configure `ETHERVIEW_CONSENSUS_URL` and optionally a JSON string-map in
`ETHERVIEW_CONSENSUS_HEADERS`; their `_FILE` variants accept mounted secrets.
Only enrich/all reads these credentials. The endpoint may be a controlled
internal HTTP service; redirects are rejected. API instances read PostgreSQL.

`ETHERVIEW_CONSENSUS_ENABLED`, `ETHERVIEW_CONSENSUS_POLL_INTERVAL` (12s),
`ETHERVIEW_CONSENSUS_REQUEST_TIMEOUT` (10s), `ETHERVIEW_CONSENSUS_WORKER_COUNT` (2),
and `ETHERVIEW_CONSENSUS_MAX_RESPONSE_BYTES` (33554432) override YAML.
Network identity is fixed after matching a local execution block; a different
Beacon genesis is an error requiring operator investigation, never automatic
replacement. Header slots remain available when consensus history is missing.
The collector normalizes `SLOT_DURATION_MS` and older `SECONDS_PER_SLOT`
parameters to the same network identity; conflicting values are rejected.
Full Beacon bodies are checked against the pinned SSZ root, including Gloas
progressive containers. Numeric SSZ preset parameters come from the node spec;
absent parameters use the protocol types' mainnet defaults. Custom presets must
provide their overrides. A wrong preset or unsupported structure fails closed.

Blob archival is independently enabled by `ETHERVIEW_CONSENSUS_BLOBS_ENABLED`.
Provision a dedicated bucket without expiry or cache eviction and configure
`ETHERVIEW_CONSENSUS_BLOBS_ENDPOINT`, `ETHERVIEW_CONSENSUS_BLOBS_BUCKET`,
`ETHERVIEW_CONSENSUS_BLOBS_REGION`, and `ETHERVIEW_CONSENSUS_BLOBS_PATH_STYLE`.
Optional static secrets `ETHERVIEW_CONSENSUS_BLOBS_ACCESS_KEY`,
`ETHERVIEW_CONSENSUS_BLOBS_SECRET_KEY`, and
`ETHERVIEW_CONSENSUS_BLOBS_SESSION_TOKEN` also support `_FILE`; absent keys use
the AWS SDK credential chain. Enrich/all requires conditional PutObject and
GetObject; API requires GetObject only. Compose uses `ETHERVIEW_CONSENSUS_BLOBS_READ_ACCESS_KEY`,
`ETHERVIEW_CONSENSUS_BLOBS_READ_SECRET_KEY` and
`ETHERVIEW_CONSENSUS_BLOBS_READ_SESSION_TOKEN` for the API role; enrich/all
uses the ordinary archive credential inputs. Helm selects separate
`secrets.blobArchiveRead*Key` entries for API and `secrets.blobArchive*Key`
entries for writers. Alternatively, provision workload identities and select
existing names in `blobArchiveServiceAccounts.api`, `.enrich` and `.all`;
these override the trace-cache ServiceAccount only on archive roles. For nonstandard Beacon/S3 ports, set the Helm network policy's
explicit additional egress rules. Never grant archive credentials to sync,
trace, metadata, maintenance or migration processes.

The archive supports standard full-blob retrieval, and the older sidecar API
for Deneb/Electra. Fulu may require a supernode or provider that reconstructs
full blobs. A normal node's missing historical blobs are reported unavailable;
no client-side data-column reconstruction or fabricated empty result is used.
New blocks take priority over bounded historical discovery. Separate persistent
forward and backward cursors also fill blocks indexed while collection was
disabled, without discarding older scan progress. Reorgs also rewind discovery
while disabled; generation checks prevent an in-flight scan from losing that
rewind. Canonical-attach replay identities deduplicate discovery and live dispatch.
Retry a chosen
range using `reindex --stage consensus` or `reindex --stage blob` with the
normal reason and finalized-range safeguards. A 404 does not prove a missed slot.

Back up PostgreSQL and the archive bucket together. Restore the bucket before
serving blob downloads and validate manifests by replaying the selected range.
Retained manifest references are independently read back and checked before
republication; intact objects do not require historical Beacon blob availability.
Retained consensus observations can also be republished without fetching the
historical body, after checking the current node's network identity. Only missing
objects require upstream retrieval. Immutable keys and checksums make retry after
upload/commit failure safe.
Corrupt existing objects are never silently overwritten: restore the exact
object from a known-good backup, or remove the corrupt object under operator
control and replay while the upstream still retains its bytes. An unavailable
upstream cannot recover lost archived history. Unreferenced uploads and orphan
blob objects are retained; there is no automatic archive garbage collection.

`/status` and `/home` expose the last successful consensus observation and its
freshness. Consensus-node or archive outages affect their own collection and
read capability without changing execution finality or core readiness.
