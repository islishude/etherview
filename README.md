# Etherview

Etherview is a pre-release Ethereum execution-layer explorer. It combines a Go
backend with an embedded React application and runs either as one process or as
independently scalable roles.

PostgreSQL is the only required external service and remains the source of
truth. Redis, NATS, and S3-compatible storage are optional accelerators.

## Capabilities

- Reorg-safe block, transaction, receipt, log, withdrawal, and mempool indexing
- Block-scoped traces, ABI/proxy decoding, tokens, NFTs, balances, and statistics
- Solidity/Yul contract verification and optional Sourcify interoperability
- Native REST/SSE APIs, an Etherscan V2 compatibility subset, and an embedded
  bilingual explorer
- API keys, SIWE wallet sessions, optional x402 billing, Prometheus metrics,
  optional process-native API TLS, Compose, and Helm deployments

Consensus-layer browsing, archived blob bodies, MEV accounting, and
L2-specific batch semantics are outside the v1 core scope.

## Project status

The implementation plans through P65 are complete. P66 billing conformance and
P70 release evidence are still open, so the repository does not yet claim a
v1.0.0 production release. [PLAN.md](PLAN.md) is the authoritative status and
evidence index.

## Quick start

Docker Compose starts PostgreSQL, applies migrations, builds the current tree,
and serves the monolith on <http://localhost:8080>.

```sh
cp deploy/compose.env.example .env
# Set a local password and a compatible execution RPC endpoint in .env.
docker compose --profile monolith up --build
```

Use `--profile distributed` for one process per runtime role. Optional
accelerators are enabled separately with `--profile accelerators`; application
correctness and readiness do not depend on them. See
[deploy/README.md](deploy/README.md) for configuration, Secret, read-replica,
authentication, billing, and deployment details.

For the repository's full-stack Preview, including the local Geth development
chain, all six application roles, host-native public verification, and NFT
metadata:

```sh
make preview-cert
make start-preview
```

`preview-cert` explicitly installs mkcert's local CA and writes the ignored
localhost certificate pair plus a public CA copy used by Preview readiness
checks. Open
<https://etherview.localhost:8080> after startup; the operations listener remains
available over HTTP on <http://localhost:9090>.
Preview builds the production image for the current Docker host architecture.
The `api` process downloads checksum-pinned `emscripten-wasm32` solc-js
artifacts and executes each bounded Standard JSON compilation in a fresh,
permission-restricted Node 26.7.0 SEA subprocess. It also supports address verification
for multi-file Geas v0.3.3 sources (including ethereum/sys-asm relative
`#include` and `assemble()` entrypoints) through the bundled read-only helper.
There is no standalone runner, Docker socket, nested runtime, or caller-chosen
compiler executable or CPU platform. Use
`make recreate-preview` to rebuild the application roles while preserving
PostgreSQL, Geth, and the checksum-addressed compiler cache. Use
`make stop-preview` to remove the complete Preview and all its volumes. See the
[deployment guide](deploy/README.md#full-stack-preview) for prerequisites,
enabled features, and endpoint overrides.

## Frontend local workflow

`web/` uses Vite for live development and local preview:

```sh
npm --prefix web install
npm --prefix web run dev
```

Then open `http://127.0.0.1:5173`.

```sh
npm --prefix web run build
```

Builds the production frontend bundle used by the embedded server.

```sh
npm --prefix web run preview
```

Starts a local preview server on `http://127.0.0.1:4173` for route and asset
verification.

The frontend expects a local backend at `http://localhost:8080`:
`/api/v1/...` requests are proxied to `http://localhost:8080/api/v1/...` in
Vite dev/preview mode.

## Build and verify

The minimum supported toolchains are Go 1.26.5, Node.js 24.18.0, and npm
11.16.0; compatible newer stable releases are supported.

```sh
make toolchain-check
make go-build
make test
```

`make check` runs the common source, race, security, license, generation, and
deployment gates. Service-backed and long-running suites are explicit targets;
see [docs/testing.md](docs/testing.md).

The service-backed release tests provision their own disposable dependencies:

```sh
make test-integration
make test-schema-e2e
make test-runtime-e2e
```

`make test-integration` starts and removes a fresh PostgreSQL 18 instance when
`INTEGRATION_DATABASE_URL` is unset. The schema and runtime targets use the
production image; the runtime E2E exercises both monolith and complete
six-application-role deployments. No manual PostgreSQL startup is required.

## Documentation

- [Architecture](docs/architecture/overview.md)
- [OpenAPI specification](api/openapi.yaml)
- [Operations runbook](docs/operations.md)
- [Helm chart](deploy/helm/etherview/README.md)
- [Contribution rules](AGENTS.md)

Licensed under [Apache-2.0](LICENSE).
