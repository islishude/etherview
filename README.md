# Etherview

Etherview is a pre-release Ethereum execution-layer explorer with a Go backend
and a bilingual React interface.

## Capabilities

- **Explore chain activity** — Browse blocks, transactions, receipts, event logs,
  withdrawals, and pending transactions, with reorg-safe indexing.
- **Understand contract execution** — Inspect execution traces, decoded calls
  and events, and proxy implementations.
- **Verify and interact with contracts** — Verify Solidity/Yul, Vyper, and Geas
  sources, with optional Sourcify interoperability; read and write contracts
  through an injected wallet.
- **Track tokens and NFTs** — Explore ERC-20 balances and holders, ERC-721 and
  ERC-1155 ownership, and NFT metadata and traits.
- **Discover ENS names** — Search names and browse registration profiles,
  resolution records, and on-chain history.
- **Follow what matters** — Sign in with Ethereum to manage private watchlists,
  receive optional browser notifications, and export data as CSV.
- **Explore protocol extensions** — Optional ERC-4337 UserOperation browsing,
  Beacon consensus observations, execution requests, and blob archives.
- **Build integrations** — Use native REST/SSE APIs, scoped API keys, an
  Etherscan V2 compatibility subset, and optional x402 billing.
- **Browse in English or Chinese** — A bilingual interface for everyday
  exploration and contract analysis.

See the [architecture overview](docs/architecture/overview.md) for details and
the [release plan](PLAN.md) for scope and status.

## Quick start

### Full-stack Preview

Requires Docker with Docker Compose. Start Docker, then run the full application
with a local Ethereum chain:

```sh
make start-preview
```

Open <https://app.etherview.localhost>.

See [Preview setup and lifecycle](preview/docs/README.md) for prerequisites,
rebuilding and cleanup. **`make stop-preview` deletes Preview data.**

To populate sample data for all Preview scenarios:

```sh
make preview-tool ARGS='run all'
```

Open the printed explorer links. See [Preview tools](preview/docs/tools.md)
for wallet setup and individual scenarios.

### Deploy against an external chain

Configure an execution RPC endpoint and chain identity using the
[deployment guide](deploy/README.md#docker-compose), then start with Docker Compose:

```sh
cp deploy/compose.env.example .env.deploy
# Edit .env.deploy with your database password, RPC endpoint, and chain identity.
docker compose --project-directory . -f deploy/compose.yaml --env-file .env.deploy --profile monolith up --build
```

Open <http://localhost:8080>. For Kubernetes, see the
[Helm chart](deploy/helm/etherview/README.md).

## Development

Requires Go >=1.27.3, Node 26.13.0, and pnpm 12.18.0.

```sh
make toolchain-check
make go-build
make test
```

For frontend hot reload, follow [Web development](docs/development/web.md)
to start a backend, then run:

```sh
pnpm install --frozen-lockfile
pnpm --dir web run dev
```

Read [AGENTS.md](AGENTS.md) and the [development guide](docs/development.md)
before contributing. Run `make check` for common checks; see the
[testing guide](docs/testing.md) for additional suites.

## Documentation

- [Design style guide](docs/design-style-guide.md)
- [Architecture](docs/architecture/overview.md) and [accepted decisions](docs/decisions/index.md)
- [OpenAPI specification](api/openapi.yaml) and [Etherscan compatibility](docs/architecture/etherscan-v2-compatibility.md)
- [Operations runbook](docs/operations.md) and [Helm chart](deploy/helm/etherview/README.md)
- [Plan catalog](docs/plans/index.md)

Licensed under [Apache-2.0](licenses/LICENSE).
