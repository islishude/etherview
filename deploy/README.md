# Etherview deployment assets

All deployment shapes run the same `etherview` image and component packages.
PostgreSQL is always the durable correctness source. Redis, NATS, and object
storage are optional acceleration services and are never application startup
dependencies.

## Docker Compose

Copy `compose.env.example` to the repository-root `.env`, replace its example
credentials and RPC endpoint, then select exactly one application profile:

```sh
docker compose --profile monolith up --build
docker compose --profile distributed up --build
```

The `monolith` profile starts PostgreSQL, a migration run, and one
`serve --roles=all` process. The `distributed` profile starts the same migration
and one process per role. Add `--profile accelerators` only when developing an
optional adapter; those services are intentionally absent from `depends_on`.
Set the commented `ETHERVIEW_NATS_URL`, `ETHERVIEW_REDIS_URL`, and S3 variables
only when using them. The application remains ready when any accelerator is
unreachable; create the configured S3 bucket before expecting trace-cache hits.
The maintenance component runs one search-catalog and adapter-retention sweep
at startup and then at `maintenance.interval`. Its generation window and
expired-observation delete batch are configured under `maintenance`; the sweep
uses PostgreSQL only and a retryable failure does not withdraw readiness.

## Helm

The chart expects an existing Kubernetes Secret (default name `etherview`) with
`database-url` and optional `rpc-urls`, `api-key-pepper`, `nats-url`,
`redis-url`, `s3-access-key`, `s3-secret-key`, `s3-session-token`, and
`otlp-trace-endpoint` and `otlp-trace-headers` keys. With
`externalSecret.enabled=true`, the included ExternalSecret materializes the
database, RPC, and API-key-pepper entries; deployments that also source optional
adapter credentials externally must add those keys to the target Secret. Secret
values are never rendered into a ConfigMap or chart defaults.

```sh
helm lint deploy/helm/etherview
helm template etherview deploy/helm/etherview
helm template etherview deploy/helm/etherview \
  -f deploy/helm/etherview/values-distributed.yaml
```

The migration is a release-revision Job. Every application Deployment has a
`migrate status` init container, so the main process cannot start against an
incompatible schema while that Job is still running. The application migration
layer uses a PostgreSQL advisory lock, so duplicate migration execution remains
serialized. The default chart runs the monolith; `values-distributed.yaml`
selects role Deployments and enables HPA, ServiceMonitor, and PrometheusRule
resources.

Every role exposes liveness, readiness, and Prometheus metrics on its dedicated
9090 operations listener; only the API role also exposes the public 8080
listener. The default NetworkPolicy permits DNS, PostgreSQL on TCP 5432, and HTTPS RPC or
metadata egress. Set `networkPolicy.additionalEgress` for private RPC endpoints,
nonstandard PostgreSQL ports, or an in-cluster OpenTelemetry collector.
NATS, Redis, and S3-compatible endpoints on non-HTTPS ports likewise require
explicit `networkPolicy.additionalEgress` entries; the chart never broadens
egress merely because an optional adapter URL is configured.

OTLP tracing remains disabled when the optional Secret key is absent. Set
`config.observability.otlp_trace_insecure=true` only for an explicitly trusted
plain-HTTP collector, and add its port/CIDR as an explicit NetworkPolicy rule.
Collector headers use the optional `otlp-trace-headers` Secret key and never a
ConfigMap value.
The [operations runbook](../docs/operations.md) documents trace sampling,
metric staleness, alerts, and repair/reindex response.

## Image properties

The Dockerfile builds the SPA with Node, compiles one static Go binary, and then
copies only that binary into a distroless non-root image. The production stage
contains no Node runtime, package manager, Solidity/Vyper compiler, source tree,
or shell. Public compiler execution therefore requires a separately approved
sandbox runtime and must not be added to this image.
