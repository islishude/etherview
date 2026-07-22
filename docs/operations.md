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
