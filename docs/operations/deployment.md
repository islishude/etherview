# Deployment and builds

[Operations index](../operations.md) · Commands run from the repository root.

- [Repository build dependencies](#repository-build-dependencies)
- [Web branding at build time](#web-branding-at-build-time)
- [Fresh database initialization](#fresh-database-initialization)
- [API listener TLS](#api-listener-tls)
- [Image license inventory](#image-license-inventory)
- [Independent verification deployment](#independent-verification-deployment)

## Repository build dependencies

Run `pnpm install --frozen-lockfile` at the repository root to install all four pnpm workspaces from
one lockfile. The compiler workspace requires Node 26.13.0; root pnpm policy
enforces engines and disables dependency lifecycle scripts. Production and
Hardhat Docker builds use the same lock and install only their selected
workspaces. Build the Hardhat client with `make hardhat3-client-image-build`;
its Docker context is the repository root and its project directory is
`/repo/e2e/hardhat3`, including the shared build-state volume. Production still
contains the packaged SEA and authenticated WASM runtimes, without npm or a
Node dependency tree.

## Web branding at build time

Set `ETHERVIEW_WEB_BRANDING_DIR` to a directory containing `brand.json` and
the selected images. All local relative directory paths resolve from the
repository root, including when pnpm runs from `web/`; local absolute paths
are also supported. An unset or empty value uses `web/branding/default` and
preserves the Etherview name and icon.

For example, place these public build inputs in `branding/acme/brand.json`,
with `mark.svg` and `tab.png` beside it:

```json
{
  "name": "Acme Explorer",
  "title": "Acme Block Explorer",
  "description": "Explore the Acme network",
  "icon": "mark.svg",
  "favicon": "tab.png"
}
```

Every field is optional. `name` defaults to `Etherview`; `title` defaults to
the selected name; `description` defaults to the name followed by
` execution-layer block explorer`. `icon` supplies both the navigation Logo
and favicon, while `logo` and `favicon` independently override it. Without
an applicable image override, that location uses the bundled Etherview SVG.
Text values are shared across languages; the surrounding interface remains
Chinese/English. Chain names still come from the existing chain configuration.

Image paths must be relative to the branding directory and stay within it,
including after resolving symbolic links. Only SVG and PNG are supported;
remote URLs, absolute image paths, missing files, unknown fields, invalid
JSON and invalid field types fail the build. Use self-contained images: the
existing same-origin CSP remains in force. The build checks the image signature,
but does not sanitize, resize or convert artwork. Each image is limited to
1 MiB and `brand.json` to 16 KiB. Text must be nonempty without control
characters; name/title/description are limited to 80/160/512 Unicode code points.

```sh
ETHERVIEW_WEB_BRANDING_DIR=branding/acme pnpm --dir web run dev
ETHERVIEW_WEB_BRANDING_DIR=branding/acme pnpm --dir web run build
make go-build ETHERVIEW_WEB_BRANDING_DIR=branding/acme
make docker-build IMAGE=etherview:acme ETHERVIEW_WEB_BRANDING_DIR=branding/acme
```

Docker accepts the same variable as a build argument. Its directory must be
inside the repository build context and not excluded by `.dockerignore`;
in particular, `.local/` is excluded. The Web builder copies only the selected
branding directory in addition to the normal Web sources. The final image
receives the compiled Go binary with hashed embedded assets, not the branding
configuration or source directory. Do not put secrets in these public inputs.

```sh
.github/scripts/buildx.sh build --load --target production --tag etherview:acme \
  --build-arg ETHERVIEW_WEB_BRANDING_DIR=branding/acme .
ETHERVIEW_WEB_BRANDING_DIR=branding/acme .github/scripts/compose.sh \
  --project-directory . -f deploy/compose.yaml --env-file .env.deploy --profile monolith build etherview
```

The deployment Compose entry point passes this value only to the image build.
Normal deployment lifecycle commands still apply.
Changing a running container's environment cannot change its brand: rebuild
the binary/image and roll out that artifact. Restart the development server
after changing branding inputs. A new image produces a new content-hashed
URL; the existing embedded asset manifest, compression and immutable cache
policy apply. Theme colors and automatic multi-size icon generation are not
part of this configuration.
Both selected images count toward the existing initial asset budgets; the
per-file input limit does not increase the allowed initial page weight.

## Fresh database initialization

The fresh-database baseline consists of numbered domain migrations from
`0001_core.sql` through `0018_browsing.sql` in
`internal/store/migrations/`. Initialize an empty PostgreSQL database with
`etherview migrate up`, then verify it with `etherview migrate status`. The
runner applies all pending files in one transaction under an advisory lock;
status reports the complete version set and verifies each file's checksum.
Every role uses this same baseline; serving never applies migrations implicitly.

Databases initialized with the earlier development migration chain or the
previous single-file `0001_core.sql` baseline must be recreated. The retained
`0001_core` version has a different checksum, so the old baseline is rejected
before new DDL is applied. Stop all processes using that database, retain a
backup if its data is needed, and provision a new empty database before running
the commands above. Update the writer and reader configuration together before
restarting roles. Reindex or resubmit required application data explicitly. Do not edit
migration checksums or ledger rows to make an old database appear compatible.
The migration command does not delete existing databases or Docker volumes,
and no in-place upgrade, data conversion, or startup backfill is provided.

## API listener TLS

The public `api`/`all` listener uses HTTP by default. To make the Go server
serve HTTPS directly, configure both absolute paths:

```yaml
server:
  public_url: https://explorer.example.com
  tls_cert_file: /run/etherview-tls/tls.crt
  tls_key_file: /run/etherview-tls/tls.key
```

`ETHERVIEW_SERVER_TLS_CERT_FILE` and
`ETHERVIEW_SERVER_TLS_KEY_FILE` override the YAML paths. The process loads the
PEM pair before opening `server.address`; an unreadable file, malformed
certificate, or mismatched private key fails startup without an HTTP fallback.
TLS 1.2 and 1.3 are supported. `server.metrics_address` remains HTTP.

Certificate files are read once. After replacing them, restart the API process
or roll the selected `all`/`api` Deployments. Monitor certificate expiry and
perform client trust-chain and hostname checks at the public origin; the
application does not issue, renew, or hot-reload certificates.

The `deploy/compose.yaml` deployment remains HTTP and expects production TLS
to terminate externally. Run deployment commands with
`--project-directory . -f deploy/compose.yaml --env-file .env.deploy`.
For a locally trusted Caddy HTTPS example, see [Setup and lifecycle](../../preview/docs/operations.md#setup-and-lifecycle).

For Helm, create or provision a TLS Secret independently, then enable
`apiTLS.enabled` and set `apiTLS.existingSecret`. `ingress.tls` controls the
client-to-Ingress certificate; `apiTLS` controls the Service-to-Pod hop.
Controllers that do not honor `appProtocol: https` require their
HTTPS-backend annotation. The Helm test uses an insecure cluster-local curl
only because the public certificate normally does not name the Service DNS;
it is not a substitute for external certificate verification.

## Image license inventory

Production application images carry reviewed project and third-party notices
under `/licenses`, including `/licenses/LICENSE` and
`/licenses/THIRD_PARTY_NOTICES.md`. This directory is copied from the repository
without build-time notice downloads or collection. Dependency upgrades that
change a notice require updating the repository inventory before rebuilding.

## Independent verification deployment

Run `serve --roles=api` for admission and reads and `serve --roles=verification`
for compiler catalogs, discovery, Sourcify and local/derived execution. Both
use the same writer PostgreSQL database; the API needs no compiler cache or
runtime self-test. `serve --roles=all` includes the identical components.
Scale verification independently, starting with one replica; total local
compilation concurrency is replicas times `verification.worker_count`.

With `adapters.nats_url` configured, committed tasks send best-effort `jobs`
wake hints and workers also retain `runtime.poll_interval` polling. Without
NATS, PostgreSQL polling and leases perform all scheduling. A NATS outage
increases pickup latency without losing work or withdrawing readiness. API
remains available when workers stop; monitor queued age, failed tasks and
compiler availability on verification instances. Expired leases are reclaimed
within the attempt budget; stale workers cannot publish.

Give compiler caches and verification HTTPS egress only to verification/all.
Use the same `security.api_key_pepper` on API and verification for encrypted
Sourcify credentials. Keep all execution replicas on the same runtime identity.
This fresh-baseline change requires database recreation under the initialization
procedure and coordinated API/verification rollout, not mixed-version operation.
Stop every cache-mounting worker before explicit cache cleanup.
