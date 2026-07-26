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
To route API reads to a replica, set `ETHERVIEW_DATABASE_READ_URL` in `.env`.
Reader pool sizes default to the mounted YAML file: either edit
`database.read_max_connections` and `database.read_min_connections` there or
export the corresponding `ETHERVIEW_DATABASE_READ_*` variables in the shell
that invokes Compose. Compose deliberately does not supply numeric defaults,
so an omitted environment override cannot replace YAML sizing with zero.
The maintenance component runs one search-catalog and adapter-retention sweep
at startup and then at `maintenance.interval`. Its generation window and
expired-observation delete batch are configured under `maintenance`; the sweep
uses PostgreSQL only and a retryable failure does not withdraw readiness.

Wallet authentication is disabled by default. To enable it, set
`features.user_auth: true` (or `ETHERVIEW_FEATURE_USER_AUTH=true`), configure
`server.public_url` as the root public HTTPS origin, and supply an independent
`ETHERVIEW_SESSION_PEPPER` containing at least 32 random bytes. Plain HTTP is
accepted only for a loopback development origin. Compose passes the pepper only
to the monolith `all` process or the split `api` process; migration and worker
services never receive it. The split services also set `ETHERVIEW_ROLES`
explicitly so role-scoped Secret loading does not depend on the mounted YAML.
Do not put the pepper in YAML, browser assets, or an image layer.

x402 billing is also disabled by default. Configure its non-secret facilitator
origin/CIDRs, network, asset, recipient, and per-operation prices under
`billing`, then set `features.x402_billing: true` and provide
`ETHERVIEW_X402_FINGERPRINT_PEPPER` with at least 32 independent random bytes.
Optional facilitator credentials are a bounded JSON object in
`ETHERVIEW_X402_FACILITATOR_HEADERS`. Compose passes both values only to the
monolith `all` or split `api` service; feature-off deployments with the
variables unset do not pass either Secret. Run `etherview doctor` against the
final API-role configuration before adding a paid route.

The reproducible deployment smoke uses a deterministic, test-only execution
RPC image and two independent PostgreSQL volumes:

```sh
make docker-image-check
make compose-runtime-smoke
```

The runtime target rebuilds the current working tree and runs that same
production image in monolith and seven-role distributed layouts with isolated
configuration and PostgreSQL volumes. The distributed
layout starts two sync and two enrichment replicas, stops one of each, advances
the fixture to a new block, probes the surviving role-local readiness
endpoints, and requires the core checkpoint, zero lag, drained outbox, and all
five exact stage publications to advance before capture. Before the RPC roles
start, the config-only verification role must bind the fresh database identity;
after failover, a test-only non-root image runs a bounded public-API load phase
inside each Compose network. The smoke then compares normalized PostgreSQL
state (including search generations), API responses, and the embedded SPA.
Trace, mempool, historical state, and NFT metadata are enabled. Verification,
Sourcify, and pricing are explicitly disabled: public verification requires an
approved external compiler sandbox/cache, while Sourcify and pricing require
separate external-service fixtures.

## Helm

The chart expects an existing Kubernetes Secret (default name `etherview`) with
`database-url` and optional `database-read-url`, `rpc-urls`, `api-key-pepper`,
`session-pepper`, `x402-fingerprint-pepper`, `x402-facilitator-headers`,
`nats-url`, `redis-url`, `s3-access-key`, `s3-secret-key`, `s3-session-token`,
`otlp-trace-endpoint`, and `otlp-trace-headers` keys. The
reader key is injected only into the monolith or distributed API process. The
session key is required and injected only when `config.features.user_auth` is
enabled, and only into that same `all` or `api` application container. It is
absent from migration and schema-init containers. With
`externalSecret.enabled=true`, the included ExternalSecret materializes the
writer database, RPC, and API-key-pepper entries. An auth-enabled release must
also set `externalSecret.sessionPepperRemoteKey`; feature-off releases do not
fetch it. Set
`externalSecret.databaseReadURLRemoteKey` to materialize the optional reader
entry; optional adapter entries follow the same non-empty remote-key rule.
Secret values are never rendered into a ConfigMap or chart defaults, and
`config.database.read_url` must remain empty. `config.user_auth` contains only
non-secret lifetimes and size limits; an inline session pepper is rejected.
Auth-enabled Helm releases must set `config.server.public_url` to a root HTTPS
origin (loopback development is the only HTTP exception).

Billing-enabled releases likewise require the public origin, fingerprint key,
fixed HTTPS facilitator origin on port 443, and explicit facilitator CIDRs. The fingerprint
and optional header keys are injected only into the `all`/`api` main container
and are neither fetched nor injected while billing is off. With External
Secrets, set `externalSecret.x402FingerprintPepperRemoteKey`; optional headers
are materialized only when
`externalSecret.x402FacilitatorHeadersRemoteKey` is non-empty.

```sh
helm lint deploy/helm/etherview
helm template etherview deploy/helm/etherview
helm template etherview deploy/helm/etherview \
  -f deploy/helm/etherview/values-distributed.yaml
helm template etherview deploy/helm/etherview \
  -f deploy/helm/etherview/values-reference-capacity.yaml
```

The migration is a release-revision Job. Every application Deployment has a
`migrate status` init container, so the main process cannot start against an
incompatible schema while that Job is still running. The application migration
layer uses a PostgreSQL advisory lock, so duplicate migration execution remains
serialized. The default chart runs the monolith; `values-distributed.yaml`
selects role Deployments and enables HPA, ServiceMonitor, and PrometheusRule
resources.

Secret-backed environment variables are read only when a Pod starts. After
rotating the writer or reader URL in an existing Secret (including through an
ExternalSecret), restart the selected Deployments, for example:

```sh
kubectl rollout restart deployment \
  --namespace etherview \
  --selector app.kubernetes.io/instance=etherview
```

The chart intentionally does not checksum Secret contents: it does not render
or read those contents. ConfigMap changes still trigger the existing
`checksum/config` rollout.

Rotating `session-pepper` deliberately invalidates every existing browser
session. Restart all selected `all`/`api` Deployments promptly after rotation;
a rolling interval with old and new peppers can reject sessions
inconsistently, so users should sign in again only after that rollout
completes. The session pepper is independent of the API-key pepper and must
never reuse it.

Every role exposes liveness, readiness, and Prometheus metrics on its dedicated
9090 operations listener; only the API role also exposes the public 8080
listener. The default NetworkPolicy permits DNS, PostgreSQL on TCP 5432, and HTTPS RPC or
metadata egress. Set `networkPolicy.additionalEgress` for private RPC endpoints,
nonstandard PostgreSQL ports, or an in-cluster OpenTelemetry collector.
NATS, Redis, and S3-compatible endpoints on non-HTTPS ports likewise require
explicit `networkPolicy.additionalEgress` entries; the chart never broadens
egress merely because an optional adapter URL is configured.

x402 billing deliberately conflicts with that default broad HTTPS rule. Set
`networkPolicy.allowExternalHTTPS=false`; list the explicit, non-facilitator
RPC and adapter ranges under `networkPolicy.runtimeHTTPSCIDRs`, and the chart
adds a separate `all`/`api`-only policy permitting the configured facilitator
CIDRs on TCP/443. The shared runtime list keeps split sync/worker roles able to
reach their reviewed HTTPS dependencies without granting them facilitator
access. Rendering fails if NetworkPolicy is disabled, the facilitator CIDR
list is empty, broad HTTPS remains enabled, the runtime list is internet-wide,
or a facilitator CIDR is repeated in it. Review the two CIDR sets for any
broader overlap as part of deployment approval. Because NetworkPolicies are
additive, billing also rejects `networkPolicy.additionalEgress` rules that omit
ports or include TCP/443, even when they carry a destination selector;
explicit non-443 rules remain available for private dependencies. Hostname and
certificate enforcement remains in the application because Kubernetes
NetworkPolicy operates only at IP/port scope.

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

`make docker-image-check` enforces that boundary by inspecting the configured
user, executing the binary as UID/GID 65532 with a read-only filesystem,
dropped capabilities, and no-new-privileges, and scanning the exported root
filesystem for build, package-manager, shell, and compiler payloads.
