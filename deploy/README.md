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
`config.features.user_operations=true` only together with a reviewed,
chain-specific `config.erc4337.entry_points` list. Each entry binds one address
to version `0.6`, `0.7`, `0.8`, or `0.9` and an inclusive
`from_block`/optional `to_block` range. The chart schema rejects an enabled
feature without entries; application startup additionally rejects zero
addresses, overlapping ranges for one address, unknown versions, and more than
16 entries. Follow the bounded enablement and reindex procedure in the
[operations runbook](../docs/operations.md#erc-4337-useroperation-indexing).

## Full-stack Preview

`compose.preview.yaml` runs the local Geth development chain and all six
application roles. It enables public
verification and NFT metadata while leaving Sourcify, pricing, prepaid billing,
and x402 top-ups disabled. Optional NATS, Redis, and object storage accelerators are not part of
this deployment.

The Preview Geth container mounts `deploy/geth-entrypoint.sh` and initializes
the read-only Genesis file into the persistent `geth-data` volume before every
start, then `exec`s Geth in five-second developer mode. Override the execution
image, Genesis source, or published ports with `GETH_IMAGE`,
`GETH_GENESIS_FILE`, `GETH_HTTP_PORT`, and `GETH_WS_PORT`.

```sh
make preview-cert
make start-preview
curl --cacert .local/preview-tls/rootCA.pem \
  -fsS https://etherview.localhost:8080/api/v1/config
```

`make preview-cert` is an explicit, one-time local trust operation: it requires
mkcert, installs mkcert's local CA, and generates an ignored certificate pair
under `.local/preview-tls/` for `etherview.localhost`, `localhost`,
`127.0.0.1`, and `::1`. It also
copies mkcert's public `rootCA.pem` there for deterministic command-line
verification; it never copies `rootCA-key.pem`.
`make start-preview` begins by rendering an ignored
`.local/preview-genesis.json` runtime copy from the checked-in
`deploy/preview.genesis.json` template before the Docker build and Compose
startup; the value is the current Unix time in seconds encoded as a lowercase
hex quantity. The checked-in template is never modified by Preview startup.
Since that changes the Genesis block hash, run `make stop-preview` before
starting a previously created Preview again so its persistent Geth and
PostgreSQL volumes are removed explicitly. The target does not remove those
volumes for you, and `make recreate-preview` reuses the existing runtime copy
without refreshing its timestamp. If that default runtime copy is missing,
`make recreate-preview` fails with an instruction to run `make start-preview`;
it never creates or modifies a Genesis file. Complete custom Genesis-file
overrides are left unchanged and do not require the default runtime copy. Both
targets only check that the three public/certificate assets exist and never
modify the host trust store.
The template funds both the browser fixture account and Geth's upstream
development address `0x71562b71999873db5b286df957af199ec94617f7`.
Preview does not override `--miner.pending.feeRecipient`, so Geth imports and
unlocks its built-in ephemeral development account and `eth_sendTransaction`
works without a repository-owned private key. This account is development-only
and is not a production signing facility.
Preview mounts only the certificate pair read-only into the API role. Rotate
it by rerunning `make preview-cert` and then `make recreate-preview`. The public
listener is
`https://etherview.localhost:8080`; health and metrics on the operations listener remain
plain HTTP at `http://localhost:9090`. Browsers use the installed system trust;
the checked-in command path also works for curl builds that do not consult it.
The add-network control advertises the local Geth endpoint as
`http://localhost:8545`. Wallet metadata validation permits that exact
Preview-local HTTP RPC exception only; production RPC URLs and every block
explorer or icon URL remain HTTPS-only.

Verification v2 treats user-supplied Solidity/Yul and Geas input as hostile.
The `api` process
owns bounded official `emscripten-wasm32` catalog discovery, approved-origin
and redirect checks, checksum-pinned download, a rebuildable persistent cache,
and execution. Each compile starts a fresh Node 26.8.1 SEA subprocess with a minimal
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
Compose's existing `--wait --wait-timeout 180` invocation is the sole health
wait for `make start-preview` and `make recreate-preview`.
There is no second Preview polling or checker command. Compose render
regressions fix the healthcheck definition and role topology, while production
image checks retain the non-root, native-architecture, and compiler-runtime
boundaries.

NFT metadata defaults to the best-effort public `https://ipfs.io` gateway.
The checked-in Preview allows a bounded 30-second cold fetch so its strict live
gate can complete without a durable retry; the ordinary configuration default
and example remain 10 seconds.
Override it without editing the checked-in configuration:

```sh
ETHERVIEW_METADATA_IPFS_GATEWAY=https://gateway.example.com make start-preview
```

The gateway must remain an absolute public HTTPS URL accepted by the metadata
SSRF policy. Public gateways have no production availability commitment.
Compiler catalogs, compiler artifacts, and IPFS gateways are all subject to
public-IP validation. Transparent or fake-IP DNS that maps public hosts into
the RFC 2544 benchmarking range `198.18.0.0/15` is rejected by default. The
checked-in Preview passes
`ETHERVIEW_VERIFICATION_UNSAFE_ALLOW_PRIVATE_DOWNLOAD_NETWORKS=true` only to
`api` so Docker Desktop's fake-IP proxy can be used for compiler downloads. It
also passes `ETHERVIEW_METADATA_UNSAFE_ALLOW_PRIVATE_NETWORKS=true` only to the
split `metadata` worker. The mounted YAML, API NFT media path, other Preview
roles, base Compose, and Helm keep
`metadata.unsafe_allow_private_networks=false`. Role validation rejects this
metadata exception for `all`, `api`, or mixed-role processes. The exceptions
never broaden the HTTPS origin allowlist, disable TLS, permit unchecked
redirects, or skip artifact/document size and integrity checks. Do not enable
either exception in production or use it to target private services or admit
an unreviewed compiler origin.

Run `make test-preview-metadata` after `make preview-cert` for the fixed public
CID/SHA-256 acceptance gate. The Go-owned target uses a unique Preview Compose
project, a reviewed precompiled ERC-721 fixture, fresh volumes, and no internal
metadata server. It requires an initial exact durable fetch followed by one
no-Transfer ERC-4906-triggered version at a changed URI, accepts only public IPs
or Docker's `198.18.0.0/15` fake-IP with `network.policy_bypassed=true`,
recreates the metadata role, and proves neither attempt is repeated. It is
intentionally not part of `make check`.

`make recreate-preview` rebuilds the host-native production image and replaces
the six application containers while preserving PostgreSQL, Geth, and the
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
must publish all seven enabled stages, retain the orphan branch, update hourly
analytics, recover after RPC and PostgreSQL pauses plus an API restart, expose
the same API/SSE/embedded-SPA behavior, pass bounded load, and finish with
equivalent normalized durable and public state. Trace, mempool, historical
state, NFT metadata, ENS, and a deterministic transaction-deployed ERC-4337
v0.9 EntryPoint wire fixture are enabled. Verification, Sourcify, and pricing are explicitly
disabled in this ordinary runtime suite because they require the official
external compiler catalog or other external-service fixtures.

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

Migration `0031_solcjs_executor.sql` is a second irreversible verification-data
cutover. Stop every old verifier worker, back up PostgreSQL, and deploy the
migration with the new application as one change; old and new versions cannot
run together. The migration deletes all Vyper catalogs, jobs, results,
verified contracts, and dependent proxy publications, and terminates active
legacy-runner compiler jobs with `executor_migrated`. Vyper data can be
recovered only by restoring the pre-upgrade backup.

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
stage contains the application binary plus one read-only Node 26.8.1 SEA, its
canonical runtime manifest and automatically discovered private ELF libraries,
and the read-only Geas v0.3.3 helper. It contains no general Node executable,
npm, wrapper source, package metadata, `node_modules`, npx, corepack, shell,
native solc, Vyper, Go toolchain, or source tree.

`make docker-image-check` enforces that boundary by inspecting the configured
user, executing the binary as UID/GID 65532 with a read-only filesystem,
dropped capabilities, and no-new-privileges, and scanning the exported root
filesystem for forbidden build, package-manager, shell, and native compiler
payloads while validating the bundled executor manifest and self-test.
