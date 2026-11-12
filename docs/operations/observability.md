# Health and observability

[Operations index](../operations.md) · Commands run from the repository root.

- [Health and telemetry](#health-and-telemetry)
- [Multi-instance polling ownership](#multi-instance-polling-ownership)

## Health and telemetry

Every role exposes `GET /health/live`, `GET /health/ready`, and `GET /metrics`
on `server.metrics_address`. Readiness requires the shared component lifecycle
and PostgreSQL. The API role additionally requires durable core readiness on
its public listener.

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

`server.request_timeout` (default 20 seconds) bounds ordinary API execution from
authentication through pool waits, SQL and external calls. Expiry returns
`504 request_timeout` before response commitment; it never appends JSON to an
already-started response. The two GET SSE routes are exempt. The top-up payment
POST uses the separate finite `server.payment_request_timeout` before authentication
(see [payment deadlines](billing.md#payment-request-deadlines)). HTTP logging and
metrics enclose budget enforcement and therefore record its final wire status.
`server.write_timeout` is the response-write budget for ordinary handlers and
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

## Multi-instance polling ownership

Live sync, mempool, NFT source discovery and compiler catalog refresh use
PostgreSQL session advisory locks. A replica that loses a lock-owning connection
cannot publish the in-flight result through another connection. Other replicas
can resume on their next poll; slow upstream requests retain ownership until
the bounded request ends or the database session closes. Backfill ranges and
ordinary durable workers retain their existing independent claims. Increasing
replicas therefore does not increase concurrent singleton polls.

Each active singleton cycle borrows one writer-pool connection without holding
a transaction during external requests. Keep writer capacity for other workers
and readiness probes. Maintenance also uses its claim connection for actual
repair/reindex mutations. A connection failure may restart that component's
process through the existing supervisor; recovery retries the original durable
request. Do not remove a live owner's advisory lock administratively: stop the
owner or terminate its physical PostgreSQL session so late writes also fail.
