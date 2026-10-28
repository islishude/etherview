# Preview Architecture

[Documentation index](README.md). Run shell commands from the repository root.

### Preview genesis application contracts

Preview authenticates genesis allocations for EntryPoint v0.9, SenderCreator,
SimpleAccount and its factory, and a local `.test` ENS registration system, plus the Local USD x402 fixture and funded Facilitator.
The deterministic generator executes constructors and
initializers in an isolated pinned EVM at chain ID 48815, exports complete
state, and checks canonical EntryPoint deployment identity. Build inputs and
allocation/ABI manifests participate in immutable network reuse validation.
Ordinary startup neither compiles nor deploys these contracts.

The independent Preview Rundler process simulates and bundles signed operations
against internal Geth. Its public HTTPS RPC exposes only standard ERC-4337
methods. It is not an Etherview runtime component or an indexing data source;
PostgreSQL-stored canonical calldata and receipts remain the sole userop stage
inputs. Failure of the Bundler does not gate core/API readiness.

`ens.mode=custom_only` omits the official RPC pool and official generation
provenance. Local generations retain endpoint and canonical block-hash fences,
reverse/forward matching and reorg invalidation. Results retain `custom_ens`
provenance. Default `official_first` retains its existing absence-only fallback.
Registration, renewal and record changes are CLI-signed transactions; the
explorer keeps its existing read/search presentation surfaces.

Preview Bundler release identity and safe-validation boundaries are owned by
[ADR-0057](../../docs/decisions/ADR-0057-preview-rundler.md).
