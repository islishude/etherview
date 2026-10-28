# Etherview deployment assets

All deployment shapes run the same `etherview` image and component packages.
PostgreSQL owns identities and publication. Redis, NATS, and the trace object
cache are optional accelerators. Optional blob bodies use a separate durable
archive under [ADR-0055](../docs/decisions/ADR-0055-consensus-and-blob-archives.md);
archive loss affects blob retrieval, while core readiness remains independent.

## Docker Compose

The repository-root `compose.yaml` runs the local [full-stack Preview](#full-stack-preview).
Use `make preview-cert` once and `make start-preview` to prepare its certificates,
network bundle and archive; bare Compose startup does not perform that setup.

`deploy/compose.yaml` is the maintained external-chain deployment template,
also exercised by the production runtime tests. Run the following commands from
the repository root. Copy `deploy/compose.env.example` to `.env.deploy`, replace
its example credentials and RPC endpoint, then select exactly one application
profile:

```sh
cp deploy/compose.env.example .env.deploy
docker compose --project-directory . -f deploy/compose.yaml --env-file .env.deploy --profile monolith up --build
docker compose --project-directory . -f deploy/compose.yaml --env-file .env.deploy --profile distributed up --build
```

Keep `--project-directory .` on every deployment invocation, including local
overlays, so build contexts and relative bind mounts resolve from the repository
root. Use the same file, environment and project-directory arguments for `ps`,
`logs`, `down` and recreation.

If an existing deployment uses a repository-root `.env`, move its deployment
settings to `.env.deploy` manually and remove those settings from `.env` before
starting Preview. In particular, external RPC/config overrides and
`COMPOSE_PROJECT_NAME` must not redirect Preview into the deployed chain or
project. Exported shell variables still override environment-file values.
The project names remain `etherview` for deployment and `etherview-preview` for
Preview; their existing named volumes remain reusable without a reset or migration.

The `monolith` profile starts PostgreSQL, a migration run, and one
`serve --roles=all` process. The `distributed` profile starts the same migration
and one process per role. Add `--profile accelerators` only when developing an
optional adapter; those services are intentionally absent from `depends_on`.
Public verification is configured in the mounted YAML through
`features.verification` and `security.public_verification`; it is not a Compose
profile. The selected `all` or `api` process owns the checksum-verified solc-js
cache and restricted Node subprocess executor; there is no standalone compiler
service or image reference.
Set the commented `ETHERVIEW_NATS_URL`, `ETHERVIEW_REDIS_URL`, and S3 variables
only when using them. The application remains ready when any accelerator is
unreachable; create the configured S3 bucket before expecting trace-cache hits.
The explicit Etherview S3 access/secret pair is the highest-precedence local
override. If it is absent, `all`/`api` uses the AWS SDK default credential chain
and refreshes temporary environment, profile, Web Identity, container, or EC2
role credentials. Compose passes standard AWS variables only to API-capable
services when the operator sets them; file-based sources also need an explicit
read-only mount. Missing credentials do not enable anonymous writes and degrade
only the disposable trace cache.
To route API reads to a replica, set `ETHERVIEW_DATABASE_READ_URL` in `.env.deploy`.
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

The `deploy/compose.yaml` deployment remains HTTP and expects production TLS
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

User-owned API keys are independently disabled by default. Enable
`features.user_api_keys` only with user authentication enabled and an
independent `ETHERVIEW_API_KEY_PEPPER`. The deployment policy under `user_auth`
sets `api_key_rate`, `api_key_burst`, and `max_active_api_keys`; users cannot
raise those values. The safe public configuration exposes only whether the
capability is enabled, never either pepper.

x402 top-ups and prepaid API billing are disabled independently by default.
Configure the network, asset, and `etherscan.<module>.<action>` prices, then
enable `features.api_billing`. Enable `features.x402_topups` only with SIWE,
user-owned keys, top-up bounds, ordered `[eip3009, permit2]`, recipient, and
the reviewed Facilitator origin/CIDRs. Provide
`ETHERVIEW_X402_FINGERPRINT_PEPPER` with at least 32 independent random bytes.
Optional facilitator credentials are a bounded JSON object in
`ETHERVIEW_X402_FACILITATOR_HEADERS`. Compose passes both values only to the
monolith `all` or split `api` service; feature-off deployments with the
variables unset do not pass either Secret. Run `etherview doctor` against the
final API-role configuration before exposing the Account top-up endpoint.
Existing credit consumption has no Facilitator dependency.

ERC-4337 UserOperation browsing is also disabled by default. Set
`features.user_operations` to `true` in the mounted YAML only together with a
reviewed, chain-specific `erc4337.entry_points` list (under `config` in Helm
values). Each entry binds one address
to version `0.6`, `0.7`, `0.8`, or `0.9` and an inclusive
`from_block`/optional `to_block` range. The chart schema rejects an enabled
feature without entries; application startup additionally rejects zero
addresses, overlapping ranges for one address, unknown versions, and more than
16 entries. Follow the bounded enablement and reindex procedure in the
[operations runbook](../docs/operations.md#erc-4337-useroperation-indexing).

## Full-stack Preview

`compose.yaml` runs a local Geth/Lighthouse PoS chain and all six
application roles. It enables consensus, blob archives, public
verification, Sourcify and NFT metadata while leaving pricing, prepaid billing,
and x402 top-ups disabled. Optional NATS, Redis, and object storage accelerators are not part of
this deployment.

To enable UserOperations on an existing Preview, set
`ETHERVIEW_FEATURE_USER_OPERATIONS=true` and `ETHERVIEW_ERC4337_ENTRY_POINTS`
in `.local/preview-push.env`. Use the deployed EntryPoint's address, version and
deployment block, then run `make recreate-preview` and the
[bounded UserOperation reindex](../docs/operations.md#erc-4337-useroperation-indexing).
All Preview roles receive the same registry.

The Preview Geth container mounts `preview/geth-entrypoint.sh` and initializes
the read-only Genesis file into the persistent `geth-data` volume before every
start, then `exec`s Geth with Lighthouse driving block production. Override the
execution image or published ports with `GETH_IMAGE`, `GETH_HTTP_PORT`, and
`GETH_WS_PORT`; use a complete network bundle when changing genesis.

```sh
make preview-cert
make start-preview
curl --cacert .local/preview-tls/rootCA.pem \
  -fsS https://app.etherview.localhost/api/v1/config
```

`make preview-cert` is an explicit, one-time local trust operation: it requires
mkcert, installs mkcert's local CA, and generates an ignored certificate pair
under `.local/preview-tls/` for `*.etherview.localhost`. If the host client needs
explicit name resolution, add the four hosts entries from the
[Caddy runbook](../docs/operations.md#preview-caddy-gateway). It also
copies mkcert's public `rootCA.pem` there for deterministic command-line
verification; it never copies `rootCA-key.pem`.
`make start-preview` builds images, then prepares or verifies an ignored
`.local/preview-network/` bundle. It starts Geth v1.17.10, Lighthouse v8.3.2
Beacon/validator clients and a scoped RustFS blob archive. Chain ID remains
48815 with Amsterdam/Gloas at genesis, 64 validators and six-second slots.
The checked-in genesis template remains unchanged. Network files, Engine JWT
and validator keys are generated together and verified by a manifest.
`make recreate-preview` requires an intact bundle and preserves its timestamp,
chain data, blob objects and slashing protection. `make stop-preview` explicitly
destroys Preview volumes and the default network bundle while retaining TLS.
Old Geth dev Preview installations require this reset before the first PoS run.
`PREVIEW_NETWORK_DIR` selects a complete compatible bundle; individual Genesis
file overrides are rejected. Custom bundles are never deleted automatically.

Use the pre-funded Hardhat account and local signing with
`eth_sendRawTransaction`; there is no unlocked Geth account or `dev` namespace.
The public development mnemonic is documented in the
[Preview runbook](../docs/operations.md#api-listener-tls).
Never use these keys on an external network. Beacon HTTP defaults to loopback
port 5052 (`LIGHTHOUSE_HTTP_PORT` overrides it), while Engine RPC and RustFS
remain internal. Only API/enrich receive scoped archive credentials. See the
runbook for lifecycle, credential overrides and failure diagnosis.
Preview mounts the certificate pair read-only only into Caddy; the API uses
container-network HTTP. Rotate
it by rerunning `make preview-cert` and then `make recreate-preview`. The public
listener is
`https://app.etherview.localhost`; health and metrics on the operations listener remain
plain HTTP at `http://localhost:9090`. Browsers use the installed system trust;
the checked-in command path also works for curl builds that do not consult it.
The add-network control advertises the local Geth endpoint as
`https://geth.etherview.localhost`. Production RPC URLs and every block
explorer or icon URL remain HTTPS-only.

Verification v2 treats user-supplied Solidity/Yul and Geas input as hostile.
The `api` process
owns bounded official `emscripten-wasm32` catalog discovery, approved-origin
and redirect checks, checksum-pinned download, a rebuildable persistent cache,
and execution. Each compile starts a fresh Node 26.12.1 SEA subprocess with a minimal
environment, private temporary directory, read-only permissions, bounded
heap/input/output/time, and process-group cleanup. The subprocess receives no
network, child-process, worker, addon, WASI, FFI, or inspector permission.
Node's permission model is defense in depth, not a JavaScript security
boundary. Every bound job records the exact catalog generation, artifact
format, compiler SHA-256, and runtime executor digest.

Geas address verification accepts only the statically linked v0.3.3 helper
bundled in the image. It uses no catalog or download: startup verifies the
helper's Go module checksum, read-only ordinary-file identity, executable
SHA-256, and self-test. Each requested runtime/optional creation entrypoint is
assembled twice from an in-memory source filesystem before exact bytecode
matching.

The trusted runtime locations are explicit under `verification.executor_path`
and `verification.geas_path`, with matching
`ETHERVIEW_VERIFICATION_EXECUTOR_PATH` and
`ETHERVIEW_VERIFICATION_GEAS_PATH` overrides. Defaults point at the
runtime bundled in the production image. An alternate absolute clean executor
path must identify one coherent read-only SEA tree; its sibling
`runtime-manifest.json` and `lib/` directory are fixed by layout and must pass
the complete identity, file-set, checksum, and self-test validation. Standard
Compose and Helm deployments do not add a runtime volume; use a trusted host
installation or custom image when relocating these files. Keep every
API-capable replica on the same runtime manifest digest, and drain bound
verification jobs before changing paths and restarting those replicas.

`make start-preview` builds the production application image for the current
Docker host architecture and then starts Compose with `--no-build`. It removes
orphan containers from this Compose project and clears only obsolete local
runner-reference files; it does not delete unrelated images. No Docker daemon,
socket, CLI, nested container runtime, privileged service, standalone compiler
container, or CPU-platform selection participates in compiler execution.
Every long-lived application container has a Docker healthcheck that runs
`/etherview healthcheck` against its own loopback operational readiness
endpoint. The command loads no configuration or Secrets and needs no shell,
Node process, `curl`, helper image, auxiliary container, or Docker socket.
Compose's `--wait --wait-timeout 180` invocation waits for container health.
Both lifecycle targets then run `archive-init` and `preview-ready.mjs`, which
checks successive real execution/Beacon blocks. Container readiness alone does
not establish chain progress. Compose render regressions fix the healthcheck
definition and role topology, while production image checks retain the non-root, native-architecture, and compiler-runtime
boundaries.

NFT metadata defaults to the local `https://ipfs.etherview.localhost` Kubo
HTTPS gateway. Daily Preview uses IPFS network retrieval; the 30-second fetch
budget remains bounded, and the ordinary metadata default stays 10 seconds.
Kubo `v0.43.1` retains pinned content in `ipfs-data` and imports the fixed
metadata fixture idempotently. If needed, add `127.0.0.1 ipfs.etherview.localhost` to the host's
hosts file for browser navigation. Caddy uses the shared wildcard certificate;
metadata alone receives the public CA. Loopback ports 443 (gateway) and 5001 (management
API) are published; override them with `PREVIEW_HTTPS_PORT`/`IPFS_API_PORT`.

`ETHERVIEW_METADATA_IPFS_GATEWAY` still permits an explicit HTTPS override,
without automatic public-gateway fallback. The metadata-only private-network
exception permits the controlled local gateway, while API media fetches, other
roles, deployment Compose and Helm remain strict. TLS, redirect, content and resource
checks are preserved. The separate API-only compiler-download exception remains
available for Docker Desktop fake-IP routing. Neither exception is a production
SSRF policy or permission to contact unrelated private services.

`cmd/ipfs` uploads and pins one file, or downloads a CID/path atomically without
overwriting an existing file, through the loopback Kubo RPC API. See
[IPFS file tool](../docs/operations.md#ipfs-file-tool) for commands and defaults.
The gateway proxy never exposes that management API.

Run `make test-preview-metadata` after `make preview-cert` for offline real-Kubo
acceptance. It owns fresh volumes and random loopback ports, disables external
retrieval, validates CLI roundtrips and TLS, and preserves both exact metadata
versions, single attempts, canonical holdings, and restart persistence. Its
network assertions identify only the owned gateway; reports include immutable
image identities and are retained on success too. It is outside `make check`;
the CI runtime workflow runs the prebuilt target with its produced image.
It makes no public-IPFS availability claim.

`make recreate-preview` rebuilds the host-native production image and replaces
the six application containers, execution/Beacon/validator clients, object
storage, Kubo and its gateway, and reruns migration while preserving
PostgreSQL, chain state, validator slashing protection, blob/IPFS data, and the
project-scoped `compiler-cache` named volume. Cache entries remain ordinary
read-only files named by SHA-256 and are fully revalidated before reuse. They
do not make an expired compiler catalog usable and do not belong in backups.
The API downloads a cold miss before coordination, then uses a digest-scoped
writer PostgreSQL session advisory lock only for destination recheck, atomic
installation, and final validation. Every process sharing this volume must use
the same writer database lock domain.
`make stop-preview` removes the deployment and all project volumes, including
the rebuildable compiler cache. For manual cache reclamation, stop every
`all`/`api` process mounting the volume before removing it; the next compiler
job downloads the authenticated artifact again. Override
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
in monolith and six-application-role distributed layouts from a build-tagged
Go test.
The distributed layout starts two sync and enrichment replicas, stops one of
each, and proves the survivors process a competing-hash reorg. Both layouts
must publish all nine enabled execution stages, retain the orphan branch, update hourly
analytics, recover after RPC and PostgreSQL pauses plus an API restart, expose
the same API/SSE/embedded-SPA behavior, pass bounded load, and finish with
equivalent normalized durable and public state. Trace, mempool, historical
state, NFT metadata, ENS, and a deterministic transaction-deployed ERC-4337
v0.9 EntryPoint wire fixture are enabled. Verification, Sourcify, and pricing are explicitly
disabled in that core-runtime scenario. The same runtime target also runs a
separate browser-verification scenario with real SIWE and bundled Geas in both
topologies, including native-key submission and optional Turnstile rejection.
Solidity/Vyper compilation and Sourcify use their separate acceptance suites.

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

### Database baseline

The migration Job initializes the current fresh-database baseline. Databases
created with the earlier development migration chain must be recreated; the
Job rejects their old baseline checksum and does not delete or convert data.
Stop all connected roles and retain any needed backup before provisioning a
new empty database. Follow the [initialization procedure](../docs/operations.md#fresh-database-initialization)
before starting the release. Do not modify migration ledger checksums to bypass
compatibility checks.

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

S3 static Secret keys are optional explicit overrides and are injected only
into `all`/`api` when `config.adapters.s3_endpoint` is set. For AWS workload
identity, enable `s3ServiceAccount`; the chart creates or selects a dedicated
ServiceAccount for those API-capable Pods while migrations and other roles keep
the shared account. IRSA uses its annotations. EKS Pod Identity associations
are created outside the chart; setting `eksPodIdentity=true` adds only the
fixed link-local agent egress required by the SDK.

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

The Dockerfile builds the SPA and application/helper Go binaries, and assembles a
distroless non-root image for BuildKit's target architecture. The production
stage contains the application binary plus one read-only Node 26.12.1 SEA, its
canonical runtime manifest and automatically discovered private ELF libraries,
two authenticated Python WASM runtimes for Vyper, and the read-only Geas v0.3.3
helper. Vyper compiler packages are assembled from application-locked PyPI inputs
into the rebuildable cache. It contains no general Node executable,
npm, wrapper source, npm package metadata, `node_modules`, npx, corepack, shell,
native solc or native Python/Vyper executable, Go toolchain, or application source tree.

`make docker-image-check` enforces that boundary by inspecting the configured
user, executing the binary as UID/GID 65532 with a read-only filesystem,
dropped capabilities, and no-new-privileges, and scanning the exported root
filesystem for forbidden build, package-manager, shell, and native compiler
payloads while validating the bundled executor manifest and self-test.

### Preview configuration layout

The repository-root `compose.yaml` is the single Preview entrypoint. It uses
Compose `extends` to load service settings from `preview/compose/`:
`app.yaml`, `chain.yaml`, `storage.yaml`, `gateway.yaml` and `x402.yaml`.
Host bind mounts and build contexts remain in the root entrypoint so relative
environment overrides retain repository-root semantics. Named volumes and the
project identity also remain there. Do not start the service fragments directly.

Preview configuration, genesis, contract artifacts and helper scripts live in
`preview/`; `deploy/` retains the external-chain production template.
Local USD code and initial state are part of genesis; no x402 initializer runs
at startup. Existing network bundles require explicit recreation after genesis
changes, which discards their local chain and billing data.
