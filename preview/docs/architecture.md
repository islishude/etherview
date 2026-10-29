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
