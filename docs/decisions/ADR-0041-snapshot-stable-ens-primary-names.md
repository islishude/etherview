# ADR-0041: Snapshot-stable ENS Primary Names

Status: accepted

## Context

ADR-0011 established optional externally resolved names as exact
configured-chain facts supplied by one HTTPS adapter. That contract cannot
represent current ENS primary names for an arbitrary EVM chain: official ENS
resolution starts on Ethereum L1, multi-chain records use the explored chain's
coin type, offchain resolvers require CCIP-Read, and safe address display
requires reverse resolution followed by a matching forward resolution.

Address names are presentation identities, not historical transaction facts.
They must remain stable while a user traverses a cursor page, but they must not
delay core ingestion, turn an upstream outage into a core API failure, expose
RPC credentials, or permit a temporary official failure to switch a name into
a private namespace.

## Decision

- ENS is an explicitly enabled API capability. `api` and `all` own resolution;
  core indexing and ordinary projections never wait for it. PostgreSQL remains
  the public cache, generation, snapshot, and failure authority.
- Official resolution calls the fixed Ethereum Mainnet Universal Resolver at
  one exact finalized block. Ethereum uses coin type 60; other representable
  EVM chain IDs use the ENSIP-11 coin type. A dedicated, chain-identity-checked
  L1 RPC pool is required unless the explored chain is exact Ethereum Mainnet.
- An optional custom Registry and modern Universal Resolver may be configured
  only on the explored chain. It is called at one exact local canonical block
  and only after the official source returns a definitive no-record or cannot
  represent the configured EVM coin type. Official RPC, CCIP, normalization,
  malformed-result, or forward-mismatch failures never fall through.
- Forward search needs a valid name-to-address result. Address display needs a
  primary result: reverse resolution, ENSIP-15 normalization of the returned
  name, and forward resolution for the same coin type back to the original
  address. Custom results remain visibly and structurally distinct from
  official ENS results.
- Go normalization uses the pinned `github.com/adraffy/go-ens-normalize`
  ENSIP-15 implementation. Name hashing and wire encoding use the pinned
  `github.com/wealdtech/go-ens/v3` implementation. The SPA also normalizes user
  input with Viem's `normalize` utility before calling the API; no JavaScript
  runtime is embedded in the Go process.
- Resolution uses the ENSIP-23 Universal Resolver `resolve(bytes,bytes)` and
  `reverse(bytes,uint256)` entrypoints. Every RPC call and CCIP callback in one
  operation stays on one endpoint and exact block hash. Gateway URLs are never
  supplied as non-standard contract arguments; only explicitly configured
  HTTPS URLs present in EIP-3668 `OffchainLookup` responses are contacted
  through the shared SSRF-safe transport. Sender, callback target, URL, ABI,
  depth, time, concurrency, and byte limits are enforced.
- A serialized resolution generation pins the official and optional custom
  source block and endpoint identities for a bounded freshness window. Forward,
  primary, and no-record observations are immutable within that generation.
  Short-lived failures may be retried without changing the source identities.
- Address-name snapshot tokens reference a retained resolution generation, so
  later chunks and pages can resolve newly encountered addresses against the
  same source blocks. Custom generations become unusable immediately when
  their local block detaches; official generations use finalized L1 blocks.
- Search publication advances the existing catalog generation, but dotted
  cursors bind the exact accepted name observation rather than only its
  address. A current no-record result therefore cannot expose a stale name
  document from an older generation.
- Name cache namespaces hash only non-secret source identities and deployment
  policy. RPC URLs and credentials are neither persisted nor logged. Stable
  public states and error codes never include nested upstream errors.

## Boundaries

This decision supersedes ADR-0011 only for external name resolution and name
observation identity. ADR-0011 continues to govern search catalog snapshots,
labels, token/contract search, price adapters, statistics, and maintenance
failure behavior.

ENS names are current presentation identities. They are not reconstructed at a
transaction's historical block, accepted implicitly by unrelated write forms,
or expanded into ownership, registration, avatar, or text-record browsing.
Etherscan-compatible responses remain unchanged.

## Consequences

- The same current primary name is shown consistently across a bounded page
  traversal while exact addresses remain visible and actionable.
- Official absence can intentionally expose a configured custom deployment,
  but official uncertainty cannot silently change identity providers.
- Correct multi-chain and CCIP-aware resolution adds an optional Ethereum L1
  RPC and configured gateway boundary to API-capable processes only.
- Name schema and configuration replace the unused generic HTTPS name adapter;
  active development provides no legacy adapter alias, row backfill, or
  compatibility state.
