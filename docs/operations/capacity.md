# Capacity, HA, and failover

[Operations index](../operations.md) · Commands run from the repository root.

- [Capacity, HA, and failover](#capacity-ha-and-failover)

## Capacity, HA, and failover

`deploy/config.reference-capacity.yaml` and the Helm
`values-reference-capacity.yaml` are a reproducible starting profile for the
core explorer read mix. They are not a release result. The Helm profile runs
redundant API, sync, enrichment and maintenance roles, retains at least one
replica during voluntary disruption, and uses a component-scoped hard hostname
spread with at least two eligible domains. A placement that cannot satisfy
that failure-domain boundary remains Pending instead of silently collapsing
onto one node. Its configured steady-state/HPA maximum is 18 non-terminating
application Pods. Trace,
verification, metadata, Sourcify and pricing remain separate optional
capability profiles because their RPC, compiler, and external-service costs
must be measured independently. ENS shares enrich Pods with a separate bounded worker pool; measure its current-chain
RPC and database budgets alongside the final workload.
Its rolling strategy sets `maxSurge: 0` and
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
profile has `P = 20`, `A = 8`, and `W = 12`: it therefore permits 240 writer
connections. Enabling a same-sized reader gives `R = 12` and adds 96 reader
connections, for 336 steady-state application connections.

Kubernetes may start a replacement while a deleted Pod is still terminating, and
terminating replicas are not included in `maxSurge`. A fully concurrent rollout
can therefore overlap old and replacement pools: reserve up to twice the
steady-state application budget—480 without the reader or 672 with the
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
controls the compiler-verification workers in each verification/all process;
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

When API-key authentication is configured, `security.ingress_rate` and
`security.ingress_burst` bound all native/compatibility requests per client IP
before credential parsing or writer lookups (defaults: 1000 requests/second,
2000 burst). This budget is independent of the existing anonymous/key quota;
a valid key still consumes its own quota after authentication. Size this
aggregate ingress budget for legitimate clients sharing a NAT. Environment
overrides are `ETHERVIEW_INGRESS_RATE` and `ETHERVIEW_INGRESS_BURST`.

Both SSE routes share `security.sse_max_connections` (default 1024 per API
process) and `security.sse_max_connections_per_client` (default 16 per client
IP). Excess streams return `429 stream_limit_exceeded` and `Retry-After: 1`
before replay or subscription allocation. Disconnect, cancellation, replay
failure and write failure release admission. Limits apply equally to keyed
and anonymous callers. The corresponding environment overrides are
`ETHERVIEW_SSE_MAX_CONNECTIONS` and `ETHERVIEW_SSE_MAX_CONNECTIONS_PER_CLIENT`.
These connection limits are per replica even when rate buckets use Redis.

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
