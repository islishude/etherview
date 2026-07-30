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
Logs default to `info` and JSON. Configure `observability.log_level` and
`observability.log_format` in the mounted YAML, or export
`ETHERVIEW_LOG_LEVEL` and `ETHERVIEW_LOG_FORMAT` in the shell invoking Compose.
The Compose entries are value-less so an unset host variable preserves the
mounted YAML value. The sync reporter's changed-progress log interval follows
the same rule through `observability.sync_progress_log_interval` or
`ETHERVIEW_SYNC_PROGRESS_LOG_INTERVAL`.
The maintenance component runs one search-catalog and adapter-retention sweep
at startup and then at `maintenance.interval`. Its generation window and
expired-observation delete batch are configured under `maintenance`; the sweep
uses PostgreSQL only and a retryable failure does not withdraw readiness.

The checked-in base Compose deployment remains HTTP and expects production TLS
to terminate externally. The full-stack Preview below is the repository's
local process-native HTTPS workflow. Custom deployments may still configure
the application TLS file settings directly, but no separate production
Compose TLS overlay is shipped.

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

## Full-stack Preview

`compose.preview.yaml` runs the local Reth development chain and all seven
application roles. It enables public verification and NFT metadata while
leaving Sourcify, pricing, and x402 billing disabled. Optional NATS, Redis, and
object storage accelerators are not part of this deployment.

```sh
make preview-cert
make start-preview
curl --cacert .local/preview-tls/rootCA.pem \
  -fsS https://localhost:8080/api/v1/config
```

`make preview-cert` is an explicit, one-time local trust operation: it requires
mkcert, installs mkcert's local CA, and generates an ignored certificate pair
under `.local/preview-tls/` for `localhost`, `127.0.0.1`, and `::1`. It also
copies mkcert's public `rootCA.pem` there for deterministic command-line
verification; it never copies `rootCA-key.pem`.
`make start-preview` and `make recreate-preview` only check that those three
public/certificate assets exist; they never modify the host trust store.
Preview mounts only the certificate pair read-only into the API role. Rotate
it by rerunning `make preview-cert` and then `make recreate-preview`. The public
listener is
`https://localhost:8080`; health and metrics on the operations listener remain
plain HTTP at `http://localhost:9090`. Browsers use the installed system trust;
the checked-in command path also works for curl builds that do not consult it.
The add-network control advertises the local Reth endpoint as
`http://localhost:8545`. Wallet metadata validation permits that exact
Preview-local HTTP RPC exception only; production RPC URLs and every block
explorer or icon URL remain HTTPS-only.

Verification v2 downloads checksum-pinned native compiler artifacts into the
`compiler-cache` volume, then passes each compiler and Standard JSON request
through framed stdin to one pre-pulled, digest-pinned generic runner image.
Solidity catalog discovery defaults to `auto` and follows the platform
directories published by
[`argotorg/solc-bin`](https://github.com/argotorg/solc-bin):
`bin`, `emscripten-asmjs`, `emscripten-wasm32`, `linux-amd64`, `linux-arm64`,
`macosx-amd64`, `wasm`, and `windows-amd64`. Container mode reads the actual
runner image platform; private process mode uses the host platform and can use
Linux, macOS, or Windows native packages.
It does not silently substitute a different CPU architecture when a version is
absent from the matching catalog. Every invocation binds the selected catalog
platform and validates ELF, Mach-O, or PE format before execution.
The emscripten/WASM directories remain recognized catalog platforms but are
not admitted as the plan's single-artifact compiler package: current builds
require unlisted sidecar/runtime inputs and cannot satisfy the same immutable
SHA-256 provenance. `catalog_urls.solidity` is only an optional approved-mirror
override.
`make start-preview` builds the generic runner for `linux/amd64`, resolves its
exact local image content digest, and saves it under the ignored
`.local/preview-compiler/` directory. Using AMD64 aligns both Solidity and the
Blockscout Vyper catalog with one runner platform; non-AMD64 developer hosts
therefore require container emulation, and preflight fails closed when the
nested daemon cannot execute it. A digest-pinned, one-shot Docker CLI service
loads that archive into a dedicated nested daemon and copies only its static
client into a read-only verify-role volume. The API and verify roles receive
the same exact runner reference, while no other application role can reach the
compiler network. A networkless volume initializer retains only `CAP_CHOWN`;
the following preflight and verify role both run as UID/GID 65532, and
preflight proves that identity can write the compiler cache before verify
starts. It also executes the runner once under the production CPU, memory,
PID, network, capability, identity, and filesystem bounds. The compiler
container runs without network access, with a read-only root, non-root
identity, dropped capabilities, tmpfs, and bounded input, output, and cleanup
behavior.

Preview never mounts the host container socket, publishes the nested daemon,
or gives it a host bind mount. The nested daemon is nevertheless a privileged
container so it can enforce compiler cgroup limits; privilege gives that local
tooling broad authority over the host kernel and devices. Run Preview only on
a trusted development machine, and never copy this local-only boundary into
the production Compose or Helm deployment.

After Compose starts, a bounded Go-owned check probes all seven roles through
their internal `/health/ready` endpoints, proves the exact runner is loaded in
the isolated daemon, verifies the public HTTPS feature contract with the
copied CA, and requires stable container identities and restart counts. A
restarting role therefore makes `make start-preview` or
`make recreate-preview` fail even when Compose's own `--wait` returns success.

NFT metadata defaults to the best-effort public `https://ipfs.io` gateway.
Override it without editing the checked-in configuration:

```sh
ETHERVIEW_METADATA_IPFS_GATEWAY=https://gateway.example.com make start-preview
```

The gateway must remain an absolute public HTTPS URL accepted by the metadata
SSRF policy. Public gateways have no production availability commitment.
Compiler catalogs, compiler artifacts, and IPFS gateways are all subject to
the same public-IP validation. Transparent or fake-IP DNS that maps those
public hosts into the RFC 2544 benchmarking range `198.18.0.0/15` is rejected
fail closed. Use a policy-approved environment and resolver; do not weaken
`PublicIP`, hardcode resolver bypasses, or pin mutable DNS snapshots.
The checked-in Preview Compose file is the sole exception: its `verify` role
sets `ETHERVIEW_VERIFICATION_UNSAFE_ALLOW_PRIVATE_DOWNLOAD_NETWORKS=true` so a
local transparent proxy's fake IPs can reach the fixed compiler download
origins. This disables the compiler downloader's private-network rejection for
that role only. It is unsafe for production, does not apply to NFT metadata,
and must not be copied into the base Compose or Helm deployment.

`make recreate-preview` rebuilds the application and runner images, reloads the
exact runner digest, and replaces the seven application containers while
preserving PostgreSQL, Reth, the isolated compiler daemon state, and compiler
artifact cache.
`make stop-preview` removes the deployment and all persistent volumes. Override
the application tag with `ETHERVIEW_IMAGE`.

The reproducible deployment E2E uses a deterministic Anvil fixture and two
independent PostgreSQL volumes:

```sh
make docker-image-check
make test-schema-e2e
make test-runtime-e2e
```

The schema target drives the production migration image from Go. The runtime
target rebuilds the current working tree and drives the same production image
in monolith and seven-role distributed layouts from a build-tagged Go test.
The distributed layout starts two sync and enrichment replicas, stops one of
each, and proves the survivors process a competing-hash reorg. Both layouts
must publish all six deployed stages, retain the orphan branch, update hourly
analytics, recover after RPC and PostgreSQL pauses plus an API restart, expose
the same API/SSE/embedded-SPA behavior, pass bounded load, and finish with
equivalent normalized durable and public state. Trace, mempool, historical
state, and NFT metadata are enabled. Verification, Sourcify, and pricing are
explicitly disabled: public verification requires an approved external
compiler sandbox/cache, while Sourcify and pricing require separate
external-service fixtures.

The pinned Anvil fixture currently emits `blobGasPrice` without
`blobGasUsed` on ordinary receipts. A bounded test-only Go RPC adapter removes
only that orphan field and preserves complete blob-fee pairs; production
receipt validation remains unchanged.

For anvil configuration, override `ETHERVIEW_RUNTIME_FIXTURE_IMAGE` to pin or
test an alternate Foundry image tag. Set
`RUNTIME_E2E_KEEP_ARTIFACTS=true` to retain successful diagnostics too.
Successful Compose commands are captured without streaming. On failure the
terminal reports the exact mode and phase, the artifact directory, and one
bounded `compose ps` table. That directory contains `failure-summary.txt`,
`compose-ps.txt`, the complete timestamped `compose.log`, and any JSON
snapshots or load reports produced before failure. CI reuses its previously
built production image through `make test-runtime-e2e-prebuilt` and uploads
this bundle as `runtime-e2e-diagnostics-<run-id>-<attempt>` for seven days;
local development should continue to use `make test-runtime-e2e` so the image
is rebuilt from the current working tree.

## Helm

### Verifier v2 upgrade

Migration `0027_verifier_v2.sql` intentionally drops all v1 verification jobs,
results, and published contracts before creating the v2 catalog, job, result,
and publication contracts. Back up PostgreSQL before upgrading if historical
verification data may be needed. There is no dual-read period, data rollback
conversion, or compatibility route for the removed v1 REST API.

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

Process-native API TLS is independent of public Ingress termination. Set
`apiTLS.enabled=true` and `apiTLS.existingSecret` to a pre-existing Secret
containing the configured `certificateKey` and `privateKeyKey` entries. The
chart mounts it only into the selected `all`/`api` main container, switches
the Service `appProtocol`, public probes, and Ingress backend port to HTTPS,
and keeps the 9090 operations listener on HTTP. `ingress.tls` still configures
the client-facing certificate. If the selected Ingress controller does not
honor `appProtocol: https`, add its HTTPS-backend annotation under
`ingress.annotations`. Rotate the Secret with a controlled `all`/`api`
rollout; the chart intentionally does not checksum Secret contents.

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
