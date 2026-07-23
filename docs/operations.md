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

Logs are JSON and include service, version, roles, chain, environment, and,
inside traced requests, `trace_id` and `span_id`. Boundary failures use stable
`error_code` and `error_type` fields. Raw RPC, PostgreSQL, compiler, metadata,
panic, URL credential, authorization-header, and exporter errors are not log
attributes.

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

Every split role and replica exposes the same chain-scoped PostgreSQL snapshot.
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

Budget PostgreSQL connections before raising replicas. `database.max_connections`
is a per-process pool cap, so the worst-case application budget is:

```text
sum(maximum replicas for every enabled role) * database.max_connections
```

The reference Helm profile is therefore `18 * 12 = 216` steady-state
application connections. Kubernetes may start a replacement while a deleted
Pod is still terminating, and terminating replicas are not included in
`maxSurge`. A fully concurrent rollout can therefore overlap all 18 old pools
with 18 replacements: reserve up to `36 * 12 = 432` application connections,
or serialize role rollouts and measure their actual termination overlap. Also
reserve server slots for the migration Job and operator access, or use a
separately operated transaction-pooling proxy.
PostgreSQL remains mandatory: point `database.url` at the deployment's HA
writer endpoint and test its documented RTO. A database outage withdraws
readiness; it is never converted into cached success. Migration execution stays
advisory-locked across failover and must be rerun or checked after the writer
endpoint is stable.

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

`make test-soak` selects the 500 RPS/30-minute P70 defaults. Both commands fail
when their p95, error-rate, achieved-throughput, or final core-lag threshold is
missed and emit a bounded JSON report. The driver uses a bounded admission
queue and overall deadline; saturation drops count as failures instead of
extending the run indefinitely. Its final status probe requires canonical
string lag plus `core_ready=true` and `backfill_complete=true`. For
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

etherview admin repair list --config /etc/etherview/config.yaml --limit 100 --format table
```

`repair --stage core` re-fetches through the history-purpose RPC path. The
database rechecks chain, height, canonical hash, parent, and finality while
holding the chain lock. It cannot move canonicality or checkpoints. A range at
or below finalized height requires `--allow-finalized` plus the recorded
reason; this permits only a same-identity refresh.

`reindex --stage token|stats|trace` queues work for the currently canonical
block hash. It does not steal queued work or an active lease. Repair deliberately
does not infer a downstream rebuild range; schedule each required derived
stage explicitly and wait for its durable publication result.

The list command is newest-first and bounded to 1–1000 rows. Its default JSON
output and optional `--format table` both report `failure_present` without
returning stored nested error text. Use the stable
maintenance log code and metrics to choose the next investigation, then inspect
PostgreSQL under the deployment's controlled operator-access policy if deeper
forensics are required.

## API-key and label administration

API-key create and rotate output plaintext exactly once. Capture it directly
into the intended secret manager and do not copy it into tickets or logs.

```sh
etherview admin api-key create --config /etc/etherview/config.yaml \
  --name incident-reader --rate 20 --burst 40
etherview admin api-key rotate OLD_PREFIX --config /etc/etherview/config.yaml
etherview admin api-key revoke PREFIX --config /etc/etherview/config.yaml
etherview admin api-key list --config /etc/etherview/config.yaml
```

Label administration is chain-scoped and accepts only canonical address,
transaction hash, block hash/height, token, or contract identities. Use
`admin label list` to verify a change after set/delete.
