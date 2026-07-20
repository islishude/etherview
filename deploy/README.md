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
The maintenance component runs one search-catalog and adapter-retention sweep
at startup and then at `maintenance.interval`. Its generation window and
expired-observation delete batch are configured under `maintenance`; the sweep
uses PostgreSQL only and a retryable failure does not withdraw readiness.

## Helm

The chart expects an existing Kubernetes Secret (default name `etherview`) with
`database-url` and optional `rpc-urls` and `api-key-pepper` keys. It can instead create that Secret
through External Secrets when `externalSecret.enabled=true`; secret values are
never rendered into a ConfigMap or chart defaults.

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

## Image properties

The Dockerfile builds the SPA with Node, compiles one static Go binary, and then
copies only that binary into a distroless non-root image. The production stage
contains no Node runtime, package manager, Solidity/Vyper compiler, source tree,
or shell. Public compiler execution therefore requires a separately approved
sandbox runtime and must not be added to this image.
