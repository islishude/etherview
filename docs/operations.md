# Operations Runbook

Etherview keeps correctness state in PostgreSQL. Logs, Prometheus, OTLP,
NATS, Redis, and S3 are diagnostic or acceleration surfaces; losing any of
them must not authorize a state transition or make a healthy database-backed
request fail.

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

The checked-in base Compose deployment remains HTTP and expects production TLS
to terminate externally. For a locally trusted process-native HTTPS example,
initialize and start the full-stack Preview:

```sh
make preview-cert
make start-preview
```

The explicit certificate target runs `mkcert -install` and generates an
ignored pair for `etherview.localhost`, `localhost`, `127.0.0.1`, and `::1`.
Preview mounts that pair read-only only into the API service. Its public
listener is `https://etherview.localhost:8080`, while
`http://localhost:9090` remains the plain HTTP operations listener. The start
target renders an ignored `.local/preview-genesis.json` runtime copy from the
checked-in `deploy/preview.genesis.json` template at the beginning of the
target, before the Docker build and Compose startup. Only the runtime copy gets
the current Unix-seconds timestamp; the template is never modified by Preview
startup. Because this changes the Genesis block hash, run `make stop-preview`
before starting a previously created Preview again; the start target does not
automatically remove its persistent volumes. `make recreate-preview` reuses the
existing runtime copy (creating it once if missing) without refreshing its
timestamp, and custom Genesis-file overrides are left unchanged. The start and
recreate targets do not modify the host trust store.

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

The active PostgreSQL sync-status reporter emits an `info` progress record only
when its latest, indexed, highest-covered, lag, backfill-complete, or ready
state changes. `observability.sync_progress_log_interval` limits those records
to one per interval (default `30s`, configurable from `1s` through `1h`);
`ETHERVIEW_SYNC_PROGRESS_LOG_INTERVAL` overrides the file. Intermediate changes
are coalesced into the next record, idle heads do not produce heartbeats, and
non-reporter sync replicas stay silent. Durable worker outcomes remain
event-driven and are not delayed by this interval.

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
verification, metadata, Sourcify, and pricing remain separate optional
capability profiles because their RPC, compiler, and external-service costs
must be measured independently. Its rolling strategy sets `maxSurge: 0` and
`maxUnavailable: 1`, preventing an intentional surge while the per-role
disruption budgets retain one available replica.

Budget PostgreSQL connections before raising replicas.
`database.max_connections` is the writer-pool cap for every application
process. A non-empty `database.read_url` or either non-zero reader bound enables
one additional pool only in an `api` or `all` process. An empty reader URL
inherits the writer endpoint, and a zero reader bound inherits the corresponding
writer bound.

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

`runtime.worker_count` controls durable enrichment, trace, verification,
metadata, and maintenance workers in each process. `runtime.backfill_workers`
controls independent sync range claimers, while
`runtime.backfill_batch_blocks` bounds each lease and transaction to 1–256
blocks. Multiplying either worker value by replicas increases PostgreSQL and
RPC pressure; it does not change lease ownership or publication fencing.
Start with the reference values and use queue age, sync lag, RPC latency, pool
saturation, CPU, and memory together rather than tuning from CPU alone.

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
buckets expire when inactive. When Redis is configured, a timeout falls back
to that bounded local limiter and opens a short circuit so a continuing Redis
outage does not spend the full adapter timeout on every request. The fallback
quota is per replica; it preserves availability, not a globally exact budget.

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

The short P60 load profile is a regression for the harness and failover
contract. P70-T04 remains responsible for the final revision's 500 RPS,
30-minute report with named hardware, dataset, RPC behavior, resource peaks,
common-query p95 below 500 ms, error rate below 0.1%, and lag no more than two
blocks. Do not promote that release gate from a shorter P60 run.

Run a bounded tuning pass against an already deployed instance with:

```sh
mkdir -p artifacts
ETHERVIEW_LOAD_BASE_URL=https://explorer.example.invalid \
ETHERVIEW_LOAD_REVISION=0123456789abcdef \
ETHERVIEW_LOAD_DATASET=mainnet-snapshot-2026-07-23 \
ETHERVIEW_LOAD_HARDWARE=kubernetes-reference-profile \
ETHERVIEW_LOAD_RPC_BEHAVIOR=isolated-head-history-state \
ETHERVIEW_LOAD_PATHS='["/api/v1/status","/api/v1/blocks?limit=20&sort=desc"]' \
make test-load >artifacts/load.json
```

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
  --reason "publish ABI v3 after trace v3 and proxy v2 are complete"

etherview reindex --config /etc/etherview/config.yaml \
  --from 0 --to 12000010 --stage trace \
  --reason "publish trace v3 execution identities and constructor decoding"

etherview reindex --config /etc/etherview/config.yaml \
  --from 0 --to 12000010 --stage state_diff \
  --reason "publish state_diff v3 complete-prestate execution identities"

etherview admin repair list --config /etc/etherview/config.yaml --limit 100 --format table
```

`repair --stage core` re-fetches through the history-purpose RPC path. The
database rechecks chain, height, canonical hash, parent, and finality while
holding the chain lock. It cannot move canonicality or checkpoints. A range at
or below finalized height requires `--allow-finalized` plus the recorded
reason; this permits only a same-identity refresh.

`reindex --stage proxy|abi|token|stats|trace|state_diff` queues work for the currently canonical
block hash. It does not steal queued work or an active lease. Repair deliberately
does not infer a downstream rebuild range; schedule each required derived
stage explicitly and wait for its durable publication result. After the
OpenZeppelin proxy cutover, schedule `proxy` before `abi`; the ABI worker also
refuses to claim a block until the current `proxy@2` result is published.
The `trace@3` cutover is an explicit bounded reindex, never a migration-time
historical enqueue. Each completed Trace generation requests the existing
`proxy@2` replay and then `abi@4`; wait for those publications before treating
the range as proxy-interaction complete. Nodes that reject `withLog` with
`-32602` still publish the call tree, but their logs remain visibly on the
conservative address fallback path.

Migration `0040` originally introduced the transaction execution-code rows and
`state_diff@2`. Migration `0047` is the current explicit bounded cutover: it
changes the public witness to `state_diff@3` and clears proxy-interaction
coverage that depended on the superseded version, but it never enqueues
history. Run `reindex --stage state_diff` for the chosen canonical range and
wait for `state_diff@3`. Its completion requests `trace@3`; after Trace
publishes, wait for `proxy@2` and then `abi@4`, then rebuild and verify the
affected coverage range. Operators must not substitute block-end or `latest`
code for unavailable transaction prestate.

Migration `0048` adds the partitioned ABI-owned effective execution identity
and advances the ABI witness to `abi@4`; it never backfills or queues history.
For a bounded historical or Preview range, first ensure its canonical
`state_diff@3` and `trace@3` publications exist, replay `proxy@2`, then run
`reindex --stage abi` and wait for `abi@4`. The replay uses only stored exact
block evidence. Do not derive an identity from block-end state, `latest`, or
the last delegation observed for an address in the block.

### Proxy detection V2 shadow rollout

Proxy detection V2 is additive and defaults off. It never replaces the legacy
OpenZeppelin observation, verified binding, ABI dependency, or browser write
authority.

1. Apply migrations `0038_proxy_detection_v2.sql` and
   `0043_erc2535_diamonds.sql`, then enable only shadow collection on every
   `enrich`/`all` process:

   ```yaml
   features:
     proxy_detection_v2: true
     safe_proxy_detection: true
     diamond_proxy_detection: true
     proxy_detection_v2_public: false
   ```

   The equivalent environment variables are
   `ETHERVIEW_FEATURE_PROXY_DETECTION_V2=true` and
   `ETHERVIEW_FEATURE_SAFE_PROXY_DETECTION=true`,
   `ETHERVIEW_FEATURE_DIAMOND_PROXY_DETECTION=true`, and
   `ETHERVIEW_FEATURE_PROXY_DETECTION_V2_PUBLIC=false`.
2. Reindex a bounded canonical range. The maintenance command resolves each
   number to its current exact block hash, and the normal durable generation
   fence makes retries idempotent:

   ```sh
   etherview reindex --config /etc/etherview/config.yaml \
     --from 21000000 --to 21010000 --stage proxy \
     --reason "Safe and Diamond proxy detector shadow sample"
   ```

   Reindex `proxy` before `abi` for the same bounded range. Diamond selector
   history and Loupe snapshots are block-hash keyed and reorg-safe; historical
   ABI bindings become available only after the exact `proxy@2` generation is
   published. The migration never performs an unbounded historical scan.
3. Compare `proxy_detection_evidence` rows whose `candidate_kind` is
   `proxy_v2` with the published legacy proxy observation from the same durable
   job generation. Review all `inconsistent` and `unknown` results, all V2/OZ
   disagreements, confirmed and compatible Safe samples, confirmed Diamond
   snapshots, partial/truncated Diamonds, and a random negative sample. For
   Diamonds also compare `diamond_cut_events` replay with the latest published
   `diamond_loupe_snapshots` map. `unknown` may be reindexed after the
   historical RPC is healthy; `not-detected` is terminal evidence and is not
   automatically retried.
4. Monitor `etherview_proxy_detection_duration_ms`,
   `etherview_proxy_detection_rpc_calls_total`,
   `etherview_proxy_detection_rpc_errors_total`,
   `etherview_proxy_detection_results_total`, the ambiguous/inconsistent
   counters, and both `etherview_safe_proxy_*` counters. A non-Safe fingerprint
   miss must add no Safe-specific storage or call request. Alert on sustained
   Diamond `inconsistent`, `unknown`, or truncated results and on exact-state
   RPC errors; do not reinterpret them as negative detection.
5. After sample approval, enable `proxy_detection_v2_public` on API processes.
   The contract page then shows the V2 family, evidence status, Safe singleton
   role, and selector-filtered Diamond facet/current-cut surfaces. This still
   does not enable implementation-as-proxy writes for Safe, and Diamond calls
   remain addressed to the Diamond rather than to a facet.

Rollback is a configuration restart: disable `proxy_detection_v2_public`
first. Disable only `safe_proxy_detection` to stop the Safe detector while
retaining framework/OZ and Diamond shadow comparisons. Disable only
`diamond_proxy_detection` to stop new Loupe detection and DiamondCut indexing;
existing exact-block snapshots and raw canonical/orphan Cut facts remain for
audit and old public generations remain readable until public V2 is disabled.
Disable `proxy_detection_v2` to stop the entire V2 suite. Retain the additive
evidence rows for audit.
No database rollback or legacy reindex is required because old readers exclude
the `proxy_v2` candidate kind. Re-enable shadow collection before public
exposure; validation rejects the inverse configuration.

The list command is newest-first and bounded to 1–1000 rows. Its default JSON
output and optional `--format table` both report `failure_present` without
returning stored nested error text. Use the stable
maintenance log code and metrics to choose the next investigation, then inspect
PostgreSQL under the deployment's controlled operator-access policy if deeper
forensics are required.

The maintenance role also owns feature-gated PostgreSQL housekeeping. When
wallet authentication is enabled it deletes at most 1,000 expired challenges
and at most 1,000 expired or revoked sessions per sweep. When x402 billing is
enabled it expires at most 1,000 timed-out `reserved` or `verified` payments
and appends the corresponding ledger events. Both are chain-scoped, select
candidate rows with `SKIP LOCKED`, use the writer, run once at startup and then
on `maintenance.interval`, and stop with the shared supervisor. A transient
failure retains readiness, logs only
`user_auth_cleanup_failed` or `x402_billing_expiry_failed`, and retries with
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

Wallet authentication is disabled by default. Apply migration
`0023_user_auth.sql` while the feature remains off, then configure the root
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

## x402 billing rollout and reconciliation

x402 v2 exact-EVM billing is disabled by default. Apply the additive
`0024_x402_billing.sql` migration with `features.x402_billing: false`, then
configure the root `server.public_url`, fixed HTTPS `billing.facilitator_url`,
on its default or explicit port 443, canonical facilitator CIDRs, CAIP-2
network, asset/EIP-712 fields, recipient,
and an explicit price for each eligible operation. Keep `billing.routes`
empty until the deployment and facilitator checks pass. Inject an independent
32-byte-or-longer `ETHERVIEW_X402_FINGERPRINT_PEPPER` only into `all`/`api`;
optional facilitator credentials use
`ETHERVIEW_X402_FACILITATOR_HEADERS(_FILE)` and must never enter YAML, logs,
browser assets, migration Jobs, init containers, or worker roles.

For Helm, billing requires `networkPolicy.enabled=true`,
`networkPolicy.allowExternalHTTPS=false`, and at least one
`billing.facilitator_allowed_cidrs` entry. The chart creates a second egress
policy selecting only `all` or `api` and allowing only those CIDRs on TCP/443.
List reviewed non-facilitator HTTPS RPC and adapter destinations under
`networkPolicy.runtimeHTTPSCIDRs`; that shared policy path keeps split sync and
worker roles functional without granting them facilitator access. Internet-wide
runtime CIDRs and exact reuse of a facilitator CIDR fail rendering, and the
deployment review must also reject broader overlapping ranges. NetworkPolicies
are additive, so every `networkPolicy.additionalEgress` entry must explicitly
exclude TCP/443 while billing is enabled; missing port lists, named/default TCP
ports, and ranges containing 443 fail chart rendering. Explicit non-443 rules
remain available for private dependencies. The application still pins each
configured HTTPS origin and validates every DNS answer and dial; NetworkPolicy
alone does not authenticate a hostname.

Validate the runnable API-role configuration and facilitator `/supported`
contract before exposing any paid operation:

```sh
etherview doctor --config /etc/etherview/config.yaml
```

Roll out with an empty route map first, then enable one staging operation on
Base Sepolia and exercise the complete `402 -> signed authorization -> settle
-> PostgreSQL ledger -> transaction` path. Confirm the operation, network,
asset, amount, recipient, payer, and transaction hash independently before
expanding the allowlist one operation at a time. A rollback clears
`billing.routes` first and then disables `features.x402_billing`; retain the
ledger and event tables as audit history.

A facilitator outage affects paid routes only: they return a stable 503 and
must not fall through to the handler, while free routes and API-key bypass
routes retain their ordinary behavior. Investigate
`EtherviewX402FacilitatorUnavailable` and rerun `doctor`; never broaden egress
or disable certificate/origin checks as an outage workaround.
`EtherviewX402LedgerUnavailable` means the writer-backed payment transition
could not be completed and is critical even if the facilitator is healthy.

`EtherviewX402SettlementReconciliationRequired` is a writer-backed current
gauge deduplicated across replicas with `max`. It covers an explicit
`settlement_unknown` immediately and a `settling` row with no failure marker
once it has remained there longer than the fixed two-minute
crash-reconciliation delay. Inspect the bounded, non-secret payment and event
projection:

```sh
etherview admin billing inspect \
  --config /etc/etherview/config.yaml \
  --id 00000000-0000-0000-0000-000000000000
```

Before reconciling, independently determine whether settlement reached the
facilitator and chain. Record a confirmed settlement only with its exact
non-zero transaction hash, or explicitly mark it failed:

```sh
etherview admin billing reconcile \
  --config /etc/etherview/config.yaml \
  --id 00000000-0000-0000-0000-000000000000 \
  --outcome settled \
  --transaction-hash 0x0000000000000000000000000000000000000000000000000000000000000001

etherview admin billing reconcile \
  --config /etc/etherview/config.yaml \
  --id 00000000-0000-0000-0000-000000000000 \
  --outcome failed
```

Do not rerun the handler or automatically retry settlement. Reconciliation
atomically appends an operator event but never restores or releases the
discarded old HTTP response; the caller must issue a new request and payment
authorization if it still needs the resource.

Rotate the fingerprint pepper only through a drain: clear all paid routes,
wait for `reserved`/`verified` rows to expire, reconcile every `settling` row,
rotate every `all`/`api` replica together, run `doctor`, and then re-enable one
route. Old fingerprints cannot be reproduced after rotation. Rotate
facilitator headers with the same API-replica rollout discipline, although it
does not invalidate fingerprints. Never reuse the session or API-key pepper.

### Base Sepolia real-payment release gate

Run `make test-x402-testnet` only after the empty-route rollout, `doctor`, and a
single staging operation have passed. This command is an explicit real charge,
not a health probe: verify the funded payer, configured recipient, exact atomic
price, asset domain, target/resource pair, writer database, and Base Sepolia
RPC before setting `ETHERVIEW_X402_TESTNET_CONFIRM` to
`BASE_SEPOLIA_REAL_PAYMENT`. The complete required input contract and `0600`
Secret-file format are documented in [Testing](testing.md#real-x402-base-sepolia-gate).

One invocation creates at most one authorization and never supplies an API key
or Cookie. Preserve its successful JSON report with the release evidence,
record the independently verified target deployment image/build digest, and
compare the transaction hash with the writer ledger and chain receipt. The
report's `harness_revision` attests only the clean local Go harness build, not
the remote deployment. The report deliberately excludes all URLs, credentials,
authorizations, and protected response bodies.

Never automatically rerun a failed invocation.
`x402_testnet_paid_outcome_unknown` means the authorization may already have
reached the server; `x402_testnet_paid_reconciliation_incomplete` means the
facilitator confirmed settlement but ledger, chain, or report evidence did not
finish. Follow the existing `admin billing inspect` and `reconcile` procedure,
and accept that the old protected response cannot be recovered. Start a new
invocation only after the first outcome is conclusively reconciled and an
operator intentionally approves another real payment.

### Runtime smoke verification fixture (development only)

The runtime parity smoke target defaults `ETHERVIEW_RUNTIME_FIXTURE_IMAGE` to
Foundry `v1.7.1` and starts anvil on the Prague hard fork as its deterministic
chain fixture source. Its Go harness generates temporary EIP-7702 keys and raw
transactions, funds them through `anvil_setBalance`, and does not require host
`cast` or reusable private keys. Do not rely on hardcoded chain, block, or
transaction hashes in manual checks; capture identity from
`eth_getBlockByNumber('0x0')` and transaction submission results at runtime and
reuse the captured values.
Set `ANVIL_ARGS` only for local launch tuning (for example alternate anvil
defaults) and keep these test-only overrides out of production runbooks.
