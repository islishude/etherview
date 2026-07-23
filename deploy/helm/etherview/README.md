# Etherview Helm chart

The chart runs the production image either as one `all` Deployment or as the
same component graph split into `api`, `sync`, `enrich`, `trace`, `verify`,
`metadata`, and `maintenance` Deployments. `values-distributed.yaml` selects
the split layout. Each autoscaled role has its own `autoscaling/v2` HPA; roles
that own singleton work are one replica by default.

Install or upgrade with Job waiting enabled so the release command observes
the migration result:

```sh
helm upgrade --install etherview deploy/helm/etherview \
  --namespace etherview --create-namespace --wait --wait-for-jobs
```

The revision-named migration Job runs `etherview migrate up`. That command
holds the repository's PostgreSQL transaction advisory lock and checks every
applied migration checksum. Every role Pod separately runs `migrate status` in
an init container, so an incompatible or not-yet-migrated schema cannot reach
the serving process. Finished migration Jobs have a configurable TTL.

## Secrets

The chart references `secrets.existingSecret`; it never renders credential
values into a Kubernetes `Secret`. The required database key and optional
runtime keys are:

| Value | Default Secret key | Use |
|---|---|---|
| `databaseURLKey` | `database-url` | PostgreSQL URL; required |
| `rpcURLsKey` | `rpc-urls` | comma-separated all-purpose URLs or a structured JSON endpoint array |
| `apiKeyPepperKey` | `api-key-pepper` | API-key digest pepper |
| `natsURLKey` | `nats-url` | optional NATS URL |
| `redisURLKey` | `redis-url` | optional Redis URL |
| `s3AccessKeyKey` | `s3-access-key` | optional S3 access key |
| `s3SecretKeyKey` | `s3-secret-key` | optional S3 secret key |
| `s3SessionTokenKey` | `s3-session-token` | optional S3 session token |
| `otlpTraceEndpointKey` | `otlp-trace-endpoint` | optional OTLP/HTTP trace collector origin |
| `otlpTraceHeadersKey` | `otlp-trace-headers` | optional OTLP collector authorization headers |

`externalSecret.enabled` can materialize the same target from a SecretStore or
ClusterSecretStore. Database, RPC, and pepper remote keys are always included;
the NATS, Redis, S3, and OTLP entries are emitted only when their remote-key values
are non-empty. Static S3 access and secret keys must be configured together.
Inline `config.database.url` and `config.security.api_key_pepper` values are
rejected by the chart schema. RPC endpoints and NATS/Redis URLs are likewise
kept empty in the ConfigMap. S3 access keys, secret keys, and session tokens
are also schema-locked to empty values there; all are supplied through their
Secret-backed environment variables. The OTLP endpoint is locked to empty for
the same reason; trace headers are injected only from the optional Secret key
and must never be written to chart values or logs.

The structured `rpc-urls` JSON form retains each endpoint's `name`, `url`,
`purposes`, and `max_requests_per_second` fields while keeping the complete
document in the Secret. Use it when head, history, state, trace, or mempool
traffic needs a distinct upstream or per-process rate policy.

## Reference HA and capacity profile

`values-reference-capacity.yaml` is a P60 starting profile for the core/API
route mix. It enables redundant API, sync, enrichment, and maintenance roles,
two HPAs, one `PodDisruptionBudget` per selected role, and a component-scoped
hard hostname-spread constraint with at least two eligible domains. Optional
trace, verification, and metadata roles remain disabled so their external
capability budgets can be measured separately. The profile's maximum 18 Pods
and 12-connection pool cap require up to 216 application PostgreSQL
connections at steady state. `maxSurge: 0` prevents a configured rollout
surge, but terminating Pods can overlap replacements outside that count. A
fully concurrent rollout can therefore require the old and new pools together,
up to 36 Pods and 432 application connections; otherwise roll roles serially
and measure the overlap. Reserve migration/operator capacity in addition.

The default chart leaves disruption budgets disabled because blocking a
single-replica development deployment would be surprising. Enable them only
after every selected role has enough replicas for the chosen `minAvailable`.
Role topology spread is also opt-in; the reference profile intentionally
leaves excess replicas Pending instead of presenting a one-node placement as
HA. The generated role constraint and the legacy free-form
`topologySpreadConstraints` input are mutually exclusive.
The reference profile is not the P70 500 RPS result; see the operations
runbook for the evidence boundary and tuning formula.

## Network policy

The default NetworkPolicy admits the HTTP and metrics ports and permits DNS,
PostgreSQL, and HTTPS egress. Add endpoint-specific rules under
`networkPolicy.additionalEgress` for NATS, Redis, plaintext/private RPC, or an
S3-compatible service or PostgreSQL endpoint on another port. Setting
`networkPolicy.enabled=false` is explicit and removes the policy.

When `serviceMonitor.enabled` is set, scrape relabeling adds immutable
`etherview_release` and `etherview_namespace` target labels. Every bundled
alert selects both labels, so releases sharing one Prometheus cannot mask or
amplify each other's current gauges and counters. Enabling
`prometheusRule.enabled` therefore also requires the chart ServiceMonitor.
Each alert also carries release, namespace, and configured chain ID as static
labels so Alertmanager does not deduplicate incidents from different releases.

See the [operations runbook](../../../docs/operations.md) for metric staleness,
alert response, OTLP sampling/shutdown, and identity-bound repair/reindex
procedures.

`make helm-check` lints the layouts and runs the render regression suite. The
suite checks role, HPA, and disruption-budget topology, migration/schema gates,
Secret references, NetworkPolicy rendering, and invalid-value rejection.
