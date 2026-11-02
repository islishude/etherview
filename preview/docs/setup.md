# Preview Setup

[Documentation index](README.md). Run shell commands from the repository root.

## Full-stack Preview

`compose.yaml` runs a local Geth/Lighthouse PoS chain and all six
application roles. It enables consensus, blob archives, public
verification, Sourcify, NFT metadata, prepaid billing and local x402 top-ups.
Core NATS is enabled by default for runtime wake notifications. Redis and the
optional trace object cache are not part of this deployment. NATS has no
persistent volume or published host port; applications connect internally at
`nats://nats:4222`. See the [NATS runbook](operations.md#nats-wake-notifications)
for overrides and outage behavior.

UserOperations are enabled by default with the canonical v0.9 EntryPoint at
block zero, and Rundler is available at `https://bundler.etherview.localhost`.
All Preview roles receive the same EntryPoint registry. Existing networks from
before these genesis allocations require an explicit fresh-network reset;
`make recreate-preview` cannot add genesis contracts to an existing chain.
See [Preview contracts](contracts.md#preview-erc-4337-and-local-ens) for the
reset procedure and signed UserOperation CLI.

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
explicit name resolution, add the hosts entries from the
[Caddy runbook](operations.md#preview-caddy-gateway). It also
copies mkcert's public `rootCA.pem` there for deterministic command-line
verification; it never copies `rootCA-key.pem`.
`make start-preview` builds images, then prepares or verifies an ignored
`.local/preview-network/` bundle. It starts Geth v1.17.10, Lighthouse v8.3.2
Beacon/validator clients and a scoped RustFS blob archive. Chain ID remains
48815 with Amsterdam/Gloas at genesis, 64 validators and three-second slots.
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
[Preview runbook](operations.md#setup-and-lifecycle).
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

## Images, readiness and persistence

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
Both lifecycle targets then run `archive-init` and `preview/scripts/ready.mjs`, which
checks successive real execution/Beacon blocks. Container readiness alone does
not establish chain progress. Preview E2E gates exercise startup and role
topology, while production image checks retain the non-root, native-architecture, and compiler-runtime
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

`preview/cmd/ipfs` uploads and pins one file, or downloads a CID/path atomically without
overwriting an existing file, through the loopback Kubo RPC API. See
[IPFS file tool](../../docs/operations.md#ipfs-file-tool) for commands and defaults.
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
storage, Kubo and its gateway, Rundler and the x402 Facilitator, and reruns
migration while preserving
PostgreSQL, chain state, validator slashing protection, blob/IPFS data, and the
host directory `.local/preview-compilers/cache`, bind-mounted at
`/var/lib/etherview/compilers/cache`. `make start-preview` and `make recreate-preview`
prepare the cache with a one-shot, network-disabled management container using
the existing Preview Node tooling image, invoked directly with `docker run --rm`.
It creates the parent and `cache`
directory as `65532:65532`, mode `0750`; the application remains non-root.
The helper has a read-only root filesystem, only the dedicated cache and script
mounts, and CHOWN/FOWNER/DAC_OVERRIDE capabilities. It receives no application
secrets or Docker socket. No host user account or host `sudo` is needed.
`PREVIEW_COMPILER_CACHE_DIR` selects a dedicated alternative host directory;
symlink paths and directories containing unrelated entries are rejected.
Existing nonempty caches must already have the expected directory ownership
and modes. Startup reports a mismatch without recursively changing existing
data; stop all consumers and explicitly reclaim the rebuildable cache before
retrying. Direct Compose startup requires an already prepared cache.
Cache entries remain ordinary
read-only files named by SHA-256 and are fully revalidated before reuse. They
do not make an expired compiler catalog usable and do not belong in backups.
The API downloads a cold miss before coordination, then uses a digest-scoped
writer PostgreSQL session advisory lock only for destination recheck, atomic
installation, and final validation. Every process sharing this directory must use
the same writer database lock domain.
`make stop-preview` removes the deployment and all project volumes, but retains
this host directory, so Solidity and Vyper artifacts survive even `down --volumes`.
For manual cache reclamation, stop every `all`/`api` process mounting the directory
before running `make clean-preview-compiler-cache`; the next compiler job
downloads the authenticated artifact again. Do not share this directory between
concurrent Preview projects using different writer databases. Override
the application tag with `ETHERVIEW_IMAGE`.
The clean command checks running containers across projects and refuses to run
while any mounts that directory. It uses the scoped helper to remove private
and read-only cache trees, then removes the empty host directory. It never
changes the ownership of unrelated files or follows cache symlinks.

After chain readiness, startup verifies sources for every genesis contract.
A failed verification step fails startup's Make target while retaining the
running chain and completed publications. Resolve the reported failure and run
`make verify-preview-genesis`; see the [operator procedure](operations.md#genesis-contract-source-verification).

Preview uses one production `x402-facilitator` for both local payment chains.
Its checked-in encrypted keystore and password are public development fixtures.
For a custom PostgreSQL password, set `X402_FACILITATOR_DATABASE_URL_FILE` to a
file containing the matching internal PostgreSQL URL. Updated standard
Permit2/proxy genesis allocations require a fresh disposable network; existing
volumes deliberately fail the fingerprint check rather than accepting changed
chain state.
