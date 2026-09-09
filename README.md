# Etherview

Etherview is a pre-release Ethereum execution-layer explorer with a Go backend
and embedded bilingual React application. Run it as one process or independently
scalable roles. PostgreSQL is the only required external service and the source
of truth; Redis, NATS, and S3-compatible storage are optional accelerators.

## Capabilities

- Reorg-safe block, transaction, receipt, log, withdrawal, and mempool indexing
- Block-scoped traces, ABI/proxy decoding, tokens, NFTs, balances, and statistics
- Optional ERC-4337 EntryPoint v0.6-v0.9 UserOperation browsing
- Solidity/Yul and Geas contract verification, with optional Sourcify interoperability
- Native REST/SSE APIs and an Etherscan V2 compatibility subset
- API keys, SIWE sessions, optional x402 billing and API TLS, Prometheus metrics,
  Compose, and Helm deployments

Consensus-layer browsing, archived blob bodies, MEV accounting, and L2-specific
batch semantics are outside the v1 core scope. See [the release plan](PLAN.md)
for current status and remaining gates.

## Quick start

Docker Compose starts PostgreSQL, applies migrations, builds the current tree,
and serves the monolith at <http://localhost:8080>:

```sh
cp deploy/compose.env.example .env
# Set a local password and a compatible execution RPC endpoint in .env.
docker compose --profile monolith up --build
```

Use `--profile distributed` for one process per role; add
`--profile accelerators` to enable optional infrastructure. See the
[deployment guide](deploy/README.md) for prerequisites and configuration.

### Full-stack Preview

Preview includes a local Geth development chain, all six application roles,
public contract verification, and NFT metadata:

```sh
make preview-cert
make start-preview
```

`preview-cert` installs mkcert's local CA and creates ignored localhost
certificates. Open <https://etherview.localhost:8080>; the operations listener
is at <http://localhost:9090>. Solidity/Yul compilation uses a bounded,
bounded Go/wazero subprocess. Vyper 0.4.3 runs in source-built CPython WASI
with the same runtime bundle; production needs neither Node nor native Python.

Use `make recreate-preview` to rebuild application roles while preserving data
and the compiler cache. **`make stop-preview` deletes Preview and all its
volumes.** See [Preview setup and lifecycle](deploy/README.md#full-stack-preview)
for prerequisites, compiler details, and endpoint overrides.

## Development

Minimum toolchains: Go 1.27.0, Node.js 24.18.0, and npm 11.16.0. Compatible newer
stable releases are supported.

```sh
make toolchain-check
make go-build
make test
```

For frontend development, run the backend at <http://localhost:8080>, then:

```sh
npm --prefix web install
npm --prefix web run dev
```

Open <http://127.0.0.1:5173>. Vite proxies `/api` to the backend.
Use `npm --prefix web run build` for the production bundle and
`npm --prefix web run preview` to inspect it at <http://127.0.0.1:4173>.

`make check` runs the common gates. Service-backed and long-running tests are
explicit targets documented in the [testing guide](docs/testing.md).
Read [AGENTS.md](AGENTS.md) and the [development guide](docs/development.md)
before contributing.

## Documentation

- [Architecture](docs/architecture/overview.md) and [accepted decisions](docs/decisions/index.md)
- [OpenAPI specification](api/openapi.yaml) and [Etherscan compatibility](docs/architecture/etherscan-v2-compatibility.md)
- [Operations runbook](docs/operations.md) and [Helm chart](deploy/helm/etherview/README.md)
- [Plan catalog](docs/plans/index.md)

Licensed under [Apache-2.0](LICENSE).
