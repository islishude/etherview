# Etherview Helm chart

> Verifier migrations `0027` and `0031` are destructive verification-data
> upgrades. Migration `0031` deletes every Vyper record and dependent proxy
> publication. Stop old verifier workers and back up PostgreSQL before the
> single-version cutover; the chart does not provide dual reads or rollback
> data conversion.

The chart runs the production image either as one `all` Deployment or as the
same component graph split into `api`, `sync`, `enrich`, `trace`, `metadata`,
and `maintenance` Deployments. `values-distributed.yaml` selects the split
layout. Each autoscaled role has its own `autoscaling/v2` HPA; roles that own
singleton work are one replica by default. When public verification is enabled,
the `all` or `api` Deployment owns the durable verification worker pool,
checksum cache, and restricted Node/solc-js subprocess executor.

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
| `databaseURLKey` | `database-url` | PostgreSQL writer URL; required for every role and migration |
| `databaseReadURLKey` | `database-read-url` | optional PostgreSQL reader URL; injected only into `all` and `api` application containers |
| `rpcURLsKey` | `rpc-urls` | comma-separated all-purpose URLs or a structured JSON endpoint array |
| `apiKeyPepperKey` | `api-key-pepper` | API-key digest pepper |
| `sessionPepperKey` | `session-pepper` | user-session HMAC pepper; auth-enabled `all`/`api` application containers only |
| `x402FingerprintPepperKey` | `x402-fingerprint-pepper` | payment-authorization fingerprint pepper; billing-enabled `all`/`api` containers only |
| `x402FacilitatorHeadersKey` | `x402-facilitator-headers` | optional bounded facilitator-header JSON; billing-enabled `all`/`api` containers only |
| `natsURLKey` | `nats-url` | optional NATS URL |
| `redisURLKey` | `redis-url` | optional Redis URL |
| `s3AccessKeyKey` | `s3-access-key` | optional S3 access key |
| `s3SecretKeyKey` | `s3-secret-key` | optional S3 secret key |
| `s3SessionTokenKey` | `s3-session-token` | optional S3 session token |
| `otlpTraceEndpointKey` | `otlp-trace-endpoint` | optional OTLP/HTTP trace collector origin |
| `otlpTraceHeadersKey` | `otlp-trace-headers` | optional OTLP collector authorization headers |

`externalSecret.enabled` can materialize the same target from a SecretStore or
ClusterSecretStore. Writer database, RPC, and API-key-pepper remote keys are
always included. When `config.features.user_auth=true`,
`externalSecret.sessionPepperRemoteKey` is required and materializes the
session pepper; feature-off releases neither fetch nor inject it. The reader
entry is emitted only when
`externalSecret.databaseReadURLRemoteKey` is non-empty; NATS, Redis, S3, and
OTLP entries follow the same optional remote-key rule. Static S3 access and
secret keys must be configured together. Inline `config.database.url`,
`config.database.read_url`, and `config.security.api_key_pepper` values are
rejected by the chart schema. Both database URLs must stay empty in the
ConfigMap. RPC endpoints and NATS/Redis URLs are likewise kept empty there. S3
access keys, secret keys, and session tokens are also schema-locked to empty
values; all are supplied through Secret-backed environment variables. The OTLP
endpoint is locked to empty for the same reason; trace headers are injected
only from the optional Secret key and must never be written to chart values or
logs. `config.user_auth` accepts only the public lifetimes and size bounds; it
has no pepper field, and schema validation rejects attempts to add one.

When `config.features.x402_billing=true`,
`externalSecret.x402FingerprintPepperRemoteKey` is required. The optional
facilitator-header entry is emitted only when
`externalSecret.x402FacilitatorHeadersRemoteKey` is non-empty; neither entry is
fetched or injected when billing is disabled. `config.billing` contains only
public policy and limits. Inline fingerprint peppers or facilitator headers
are rejected by the chart schema.

User authentication is disabled by default. Before enabling it, set
`config.server.public_url` to the root public HTTPS origin (plain HTTP is
permitted only for loopback development) and place at least 32 random pepper
bytes in the configured Secret key. The chart injects
`ETHERVIEW_SESSION_PEPPER` only into an enabled `all` or `api` main container.
Migration Jobs, schema-compatibility init containers, and non-API roles never
receive it.

x402 billing is disabled by default. Enabling it also requires a root public
origin, fixed HTTPS facilitator origin on port 443, canonical non-empty facilitator CIDRs,
network/asset/EIP-712/recipient fields, and an independent 32-byte-or-longer
fingerprint pepper. The chart injects billing Secrets only into the selected
`all`/`api` main container. Run
`etherview doctor --config /etc/etherview/config.yaml` before exposing a
configured paid operation.

`config.database.read_max_connections` and
`config.database.read_min_connections` size the optional reader pool. A zero
value inherits the corresponding writer bound. A reader URL or either non-zero
reader size enables the pool only in `all` or `api` application containers; an
empty reader URL uses the writer endpoint. Rendering rejects any writer or
reader value outside the signed 32-bit range and rejects effective reader
minimums greater than effective maximums. Account for the writer and reader
pools separately in the PostgreSQL connection budget.

Secret values injected as environment variables are read only at Pod startup.
After rotating either database URL in the existing Secret, including a target
managed by External Secrets, restart the release Deployments:

```sh
kubectl rollout restart deployment \
  --namespace etherview \
  --selector app.kubernetes.io/instance=etherview
```

The chart intentionally does not compute a Secret checksum because it neither
renders nor reads Secret contents. Its `checksum/config` annotation continues
to roll Pods for ConfigMap changes.

## Process-native API TLS

Ingress TLS and process-native TLS are separate hops. `ingress.tls` terminates
the public client connection at the Ingress. To encrypt the API listener
itself, first provision a separate Secret containing a certificate and private
key, then configure:

```yaml
apiTLS:
  enabled: true
  existingSecret: etherview-api-tls
  certificateKey: tls.crt
  privateKeyKey: tls.key
```

The chart mounts that Secret read-only only into the `all` or `api` main
container and injects fixed absolute file paths through
`ETHERVIEW_SERVER_TLS_CERT_FILE` and
`ETHERVIEW_SERVER_TLS_KEY_FILE`. Migration Jobs, schema init containers, and
worker roles receive neither file. The application Service advertises
`appProtocol: https`, and API startup, liveness, and readiness probes use
HTTPS; the operations listener and metrics probes remain HTTP.

When Ingress and `apiTLS` are both enabled, the backend hop must use HTTPS.
Controllers that do not honor `appProtocol` require their controller-specific
backend-protocol annotation under `ingress.annotations`. The Helm test skips
hostname verification only for its cluster-local Service request because the
serving certificate normally names the public host.

The server loads one certificate/key pair before binding and does not
hot-reload it. After Secret rotation, restart the selected `all`/`api`
Deployments and verify the public certificate chain, hostname, and expiry.
Setting `config.server.public_url` to HTTPS alone continues to mean external
termination and does not enable process-native TLS.

Changing the session pepper is a global session revocation. Restart every
selected `all`/`api` Deployment promptly and wait for that rollout before
asking users to sign in again; replicas running different pepper versions can
temporarily disagree about otherwise valid Cookies. Keep the session and
API-key peppers independent.

Fingerprint-pepper rotation requires draining paid routes first: clear the
route map, let reservations expire, reconcile all `settling` rows, rotate all
`all`/`api` replicas together, run `doctor`, and re-enable routes gradually.
Old handler responses are never recovered by reconciliation. See the
operations runbook for the unknown-settlement procedure.

## Genesis account state

Choose exactly one of the following authoritative Genesis JSON sources:

- Set `genesisState.existingClaim` to a read-only PVC, with
  `genesisState.key` naming the file inside the claim. The chart mounts that
  one file at the absolute `genesisState.mountPath` and supplies the path
  through `ETHERVIEW_CHAIN_GENESIS_FILE`.
- Set `genesisState.url` to a public HTTPS URL. The chart supplies it through
  `ETHERVIEW_CHAIN_GENESIS_URL` without creating a PVC or file mount. An
  optional `genesisState.sha256` pins the lowercase, non-zero, 64-character
  SHA-256 digest of the exact response bytes. `genesisState.fetchTimeout`
  controls the request deadline, defaults to `60s`, and must resolve to a
  duration from `1s` through `5m`.

Both modes require `config.chain.start_block: 0` and are available only to
`all` or `sync` application Pods. Migration, API, and worker Pods receive
neither the source nor its checksum; they consume authenticated imported facts
from PostgreSQL. The URL must use HTTPS without credentials, query parameters,
fragments, redirects, or a non-default port. Runtime validation additionally
rejects private and special-purpose destinations. The default NetworkPolicy
already permits HTTPS egress on TCP port 443; no HTTP egress is added.

Direct `config.chain.genesis_file`, `config.chain.genesis_url`, and
`config.chain.genesis_sha256` values are rejected so a rendered ConfigMap
cannot bypass this role-scoped source configuration.

The structured `rpc-urls` JSON form retains each endpoint's `name`, `url`,
`purposes`, and `max_requests_per_second` fields while keeping the complete
document in the Secret. Use it when head, history, state, trace, or mempool
traffic needs a distinct upstream or per-process rate policy.

## Reference HA and capacity profile

`values-reference-capacity.yaml` is a P60 starting profile for the core/API
route mix. It enables redundant API, sync, enrichment, and maintenance roles,
two HPAs, one `PodDisruptionBudget` per selected role, and a component-scoped
hard hostname-spread constraint with at least two eligible domains. Optional
trace and metadata roles plus public verification remain
disabled so their external capability budgets can be measured separately. The
profile's maximum 18 Pods
and 12-connection writer pool cap require up to 216 application PostgreSQL
connections at steady state. Its maximum 8 API Pods add 96 connections when a
same-sized reader pool is enabled, for 312 total. `maxSurge: 0` prevents a configured rollout
surge, but terminating Pods can overlap replacements outside that count. A
fully concurrent rollout can therefore require the old and new pools together,
up to 432 connections without a reader or 624 with the same-sized reader;
otherwise roll roles serially and measure the overlap. Reserve
migration/operator capacity in addition.

The default chart leaves disruption budgets disabled because blocking a
single-replica development deployment would be surprising. Enable them only
after every selected role has enough replicas for the chosen `minAvailable`.
Role topology spread is also opt-in; the reference profile intentionally
leaves excess replicas Pending instead of presenting a one-node placement as
HA. The generated role constraint and the legacy free-form
`topologySpreadConstraints` input are mutually exclusive.
The reference profile is not the P70 500 RPS result; see the operations
runbook for the evidence boundary and tuning formula.

## Compiler executor

Public source verification requires NetworkPolicy. The selected `all` or `api`
Pods own official
`emscripten-wasm32` catalog discovery, checksum validation, and execution in a
fresh permission-restricted Node subprocess. The exact Node executable,
solc-js wrapper/dependency tree, and canonical read-only runtime manifest are
part of the production image for its native architecture. There is no runner
Deployment, Service, image value, runtime class, native compiler fallback, or
CPU-platform setting.

By default `compilerCache.existingClaim` is empty and each compiler-owning Pod
receives its existing memory-backed `emptyDir`. Set it to one operator-created
PVC to preserve the rebuildable artifact cache across Pod replacement. The
same claim is mounted by every selected `all` or `api` replica; multi-node
replicas therefore require storage supporting their concurrent read/write
mounts (normally RWX), same-directory atomic rename, coherent reads, and Unix
ownership/modes for UID/GID 65532. The chart never creates or deletes the
claim. The `sizeLimit` value applies only to the default `emptyDir`.
The default `fsGroupChangePolicy: OnRootMismatch` prevents later Pod mounts
from recursively broadening the mode-0400 artifact files after the volume root
has been initialized; an operator override must preserve that property.

Cached files are still validated for ordinary-file type, read-only mode, byte
limit, and catalog SHA-256 before every use. Persistence does not bypass
catalog freshness. Do not back up this cache or delete entries while any
compiler owner mounts it; stop those Pods, clear the rebuildable claim, then
restart them to download required versions again. Monitor and size the PVC at
the storage layer because automatic eviction could remove a compiler pinned by
an older durable job generation.

`config.verification.node_path`, `wrapper_path`, and `manifest_path` default to
that bundled runtime and may select alternate absolute paths supplied by a
trusted custom image. The chart deliberately provides no external compiler
runtime volume. All three paths must describe one read-only manifest-covered
runtime with the fixed Node/wrapper identity, every `all` or `api` replica must
use the same manifest digest, and executor-bound jobs must be drained before a
path change and replica restart.

A dedicated policy selects only `all` or `api` and permits DNS plus TCP/443 for
approved compiler catalogs and artifacts. Other worker roles receive neither
the cache nor this egress. Catalog outages do not withdraw API readiness:
version discovery reports unavailable and compiler jobs remain retryable. The
`etherview_verification_compiler_available` metric and bundled alert expose the
condition. Application Pods never require a Docker daemon, Docker socket, or
Kubernetes API access to execute compilers.

## Network policy

The default application NetworkPolicy admits the HTTP and metrics ports and
permits DNS, PostgreSQL, and optionally shared HTTPS egress. Public verification
adds a dedicated policy selecting only `all`/`api` and allowing DNS plus
TCP/443 for approved compiler catalogs and artifacts. Add endpoint-specific application rules under
`networkPolicy.additionalEgress` for NATS, Redis, plaintext/private RPC, or an
S3-compatible service or PostgreSQL endpoint on another port. Setting
`networkPolicy.enabled=false` is explicit and is rejected while public
verification is enabled.

Billing cannot use shared broad HTTPS. Set
`networkPolicy.allowExternalHTTPS=false`; put reviewed HTTPS RPC and adapter
ranges in `networkPolicy.runtimeHTTPSCIDRs`. Compiler downloads remain scoped
to the selected `all`/`api` Pods and do not broaden other roles. Billing
renders a separate NetworkPolicy selecting only `all`/`api` and allowing the
configured facilitator CIDRs on TCP/443.
The chart rejects billing when NetworkPolicy is disabled, facilitator CIDRs
are empty, broad HTTPS remains enabled, the runtime list contains an
internet-wide CIDR, or it repeats a facilitator CIDR. Operators must also
review both lists for broader overlapping ranges. Additive
`networkPolicy.additionalEgress` entries must explicitly list non-443 ports
while billing is enabled; an omitted port set, a named/default TCP port, or a
numeric range containing TCP/443 is rejected even when `to` is restricted.
The application still pins HTTPS origins and validates DNS/dial destinations
because NetworkPolicy has no hostname semantics.

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
feature-off and API-only Secret references, x402 CIDR NetworkPolicy rendering,
release-scoped x402 alerts, and invalid-value rejection.
