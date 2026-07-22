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
| `rpcURLsKey` | `rpc-urls` | comma-separated RPC URLs |
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

`make helm-check` lints both layouts and runs the render regression suite. The
suite checks role and HPA topology, migration/schema gates, Secret references,
NetworkPolicy rendering, and invalid-value rejection.
