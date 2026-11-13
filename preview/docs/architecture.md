# Preview Architecture

[Documentation index](README.md). Run shell commands from the repository root.

## Preview genesis application contracts

Preview authenticates genesis allocations for EntryPoint v0.9, SenderCreator,
SimpleAccount and its factory, and a local `.test` ENS registration system, plus the Local USD x402 fixture and funded Facilitator.
The deterministic generator executes constructors and
initializers in an isolated pinned EVM at chain ID 48815, exports complete
state, and checks canonical EntryPoint deployment identity. Build inputs and
allocation/ABI manifests participate in immutable network reuse validation.
Ordinary startup never deploys these contracts. After chain readiness, an
explicit Compose helper submits the fixed source inventory to the normal API
verification queue and requires exact-address runtime publications for every
genesis contract. The verification role performs normal pinned local compilation under
ADR-0024 and ADR-0039; no ABI or source is published directly by the helper.

The independent Preview Rundler process simulates and bundles signed operations
against internal execution node. Its public HTTPS RPC exposes only standard ERC-4337
methods. Compose runs the pinned official Rundler image as a non-root user,
after Reth becomes healthy, without a startup wrapper or Bundler healthcheck.
The Go genesis-verification tool has its own minimal image; the compiler-cache
helper uses an official Node image.
It is not an Etherview runtime component or an indexing data source;
PostgreSQL-stored canonical calldata and receipts remain the sole userop stage
inputs. Failure of the Bundler does not gate core/API readiness.

`ens.manifest` binds the Preview chain/genesis and its local deployments.
ENS runs within enrich and reads core logs; exact resolution shares the
current-chain RPC pool. Source IDs and manifest digests retain provenance;
there is no official/custom classification or fallback policy.

Preview Bundler release identity and safe-validation boundaries are owned by
[ADR-0057](../../docs/decisions/ADR-0057-preview-rundler.md).

The primary Reth transaction pool uses `--txpool.minimal-protocol-fee 1` to
match the reviewed genesis's 1 wei base fee. Its default 7 wei pool floor would
reject otherwise valid locally estimated settlement transactions. Rundler accepts
the private chain's zero priority-fee recommendation while retaining safe
validation and the execution base fee.

## DeFi demonstrations

The deterministic genesis inventory also contains six families of local DeFi
adapter demonstration contracts. `preview/cmd/previewdefimanifest` validates
all configured root, receipt and Curve pool code identities against the final
execution genesis and produces `metadata/defi.json`. Network fingerprints and
file integrity checks include it. API/enrich and all-role acceptance use that
same manifest; ordinary Preview startup submits no position transactions.
The explicit `preview-tool run defi` scenario creates Transfer evidence and
uses the unchanged enrich/publication/read path. Local fixture accounting does
not close ADR-0060's independent real-protocol acceptance in P30-T158.

## Second payment network

The Preview Compose stack also runs Reth with chain ID 31337. It is a payment
fixture only: the explorer and SIWE identity continue using Reth chain 48815.
`preview/reth/generate.mjs` derives a deterministic payment genesis from the
reviewed token allocations, including both six- and eighteen-decimal tokens and
public development accounts. No contracts are deployed during startup.

Reth v2.11.0 runs in dev mode with a three-second block interval and Amsterdam
active from genesis, including the reviewed system allocations shared with Geth.
The Reth volume records its genesis digest and full database. Restarts reuse the
database without funding accounts again. Changed genesis, missing database or
missing identity fails closed until an explicit reset. Native snapshots are not
used. Health checks require the expected chain, recent Amsterdam headers and a
non-genesis head; halted mining becomes unhealthy when the head grows stale.
One internal production facilitator serves both Reth chains, with independent
chain queues and a shared PostgreSQL journal. Each chain enforces token and
recipient whitelists; standard Permit2 and x402 proxy contracts are predeployed.

The existing Caddy gateway exposes HTTP JSON-RPC and WebSocket RPC on
`reth.etherview.localhost`. Reth has no host port. Gateway policy matches the
local Reth wallet endpoint, including wildcard local-development CORS, bounded
requests, scrubbed errors and no retries. Neither payment network is a core
readiness dependency. Multi-option account funding is tracked by P60-T25.

## Gateway and local service boundaries

Preview uses pinned Kubo, Reth and Lighthouse nodes behind the official Caddy
`preview-gateway`. Explicit `*.etherview.localhost` hosts route app/API,
read-only IPFS, execution JSON-RPC/WSS, Beacon REST/SSE and Bundler JSON-RPC. TLS terminates at
Caddy; API traffic inside the container network uses HTTP. The gateway is not
an application role and its health does not depend on optional upstreams.
Only Caddy holds its wildcard private key. Metadata trusts the public CA and
explicitly permits its private IPFS host; API media policy remains strict.
Preview separately allowlists the exact IPFS HTTPS origin for confirmed image
navigation; production defaults to no navigation exceptions.
The local Reth host accepts arbitrary Origins for JSON-RPC/WSS and returns
wildcard CORS without credentials, allowing browser-extension wallets. Beacon
and Bundler retain the exact configurable Origin allowlist. The SPA continues
using the generated same-origin client and injected wallet boundary.
Daily Preview retrieves IPFS content; acceptance seeds the same CID offline.
The independent `preview/cmd/ipfs` operator tool uses Kubo's loopback management API,
which is never forwarded through Caddy. See ADR-0005 and the Preview runbook.

Preview also runs the standalone x402 Facilitator and Local USD token on its
Reth/Lighthouse chain. Token code, constructor storage, initial owner balance
and Facilitator funding are predeployed in genesis. Startup validates the
network bundle without deploying or funding contracts; recreation preserves
chain and account state. The Facilitator is not a core role dependency;
existing credit remains spendable during its outage. The API/all role owns the fingerprint pepper and the normal production
billing components. Standard Permit2/proxy contracts are predeployed; the private-network HTTP
exception remains a development-only boundary under ADR-0044 and ADR-0059.

Old Anvil state is not migrated; the [Preview runbook](operations.md#reth-payment-chain-fixture)
requires reconciliation and a fresh billing database at the switch.
