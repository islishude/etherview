# Preview Architecture

[Documentation index](README.md). Run shell commands from the repository root.

### Preview genesis application contracts

Preview authenticates genesis allocations for EntryPoint v0.9, SenderCreator,
SimpleAccount and its factory, and a local `.test` ENS registration system, plus the Local USD x402 fixture and funded Facilitator.
The deterministic generator executes constructors and
initializers in an isolated pinned EVM at chain ID 48815, exports complete
state, and checks canonical EntryPoint deployment identity. Build inputs and
allocation/ABI manifests participate in immutable network reuse validation.
Ordinary startup never deploys these contracts. After chain readiness, an
explicit Compose helper submits the fixed source inventory to the normal API
verification queue and requires exact-address runtime publications for every
genesis contract. The API role performs normal pinned local compilation under
ADR-0024 and ADR-0039; no ABI or source is published directly by the helper.

The independent Preview Rundler process simulates and bundles signed operations
against internal Geth. Its public HTTPS RPC exposes only standard ERC-4337
methods. It is not an Etherview runtime component or an indexing data source;
PostgreSQL-stored canonical calldata and receipts remain the sole userop stage
inputs. Failure of the Bundler does not gate core/API readiness.

`ens.manifest` binds the Preview chain/genesis and its local deployments.
ENS runs within enrich and reads core logs; exact resolution shares the
current-chain RPC pool. Source IDs and manifest digests retain provenance;
there is no official/custom classification or fallback policy.

Preview Bundler release identity and safe-validation boundaries are owned by
[ADR-0057](../../docs/decisions/ADR-0057-preview-rundler.md).

### Second payment network

The Preview Compose stack also runs Anvil with chain ID 31337. It is a payment
fixture only: the explorer and SIWE identity continue using Geth chain 48815.
`preview/anvil/generate.mjs` derives a deterministic payment genesis from the
reviewed token allocations, including both six- and eighteen-decimal tokens and
public development accounts. No contracts are deployed during startup.

The Anvil volume records its seed digest and state. Restarts load saved state
without funding accounts again, and changed seeds fail closed until an explicit
volume reset. A one-second checkpoint interval limits but does not eliminate
state loss after an abrupt host failure; normal shutdown dumps state before exit.
One internal production facilitator serves both Geth and Anvil, with independent
chain queues and a shared PostgreSQL journal. Each chain enforces token and
recipient whitelists; standard Permit2 and x402 proxy contracts are predeployed.

The existing Caddy gateway exposes HTTP JSON-RPC and WebSocket RPC on
`anvil.etherview.localhost`. Anvil has no host port. Gateway policy matches the
local Geth wallet endpoint, including wildcard local-development CORS, bounded
requests, scrubbed errors and no retries. Neither payment network is a core
readiness dependency. Multi-option account funding is tracked by P60-T25.
