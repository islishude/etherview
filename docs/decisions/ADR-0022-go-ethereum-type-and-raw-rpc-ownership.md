# ADR-0022: Go-Ethereum Type and Raw RPC Ownership

Status: accepted

## Context

Etherview consumes Ethereum execution JSON-RPC and Genesis data while
go-ethereum is already a reviewed direct dependency. Maintaining parallel
quantity, data, address, hash, transaction, receipt, log, withdrawal, and
Genesis models duplicates protocol semantics and can drift as Ethereum adds
fields or transaction formats.

Go-ethereum's exported protocol types are not a complete hostile-input,
JSON-RPC transport, persistence, or public-API boundary. Its typed transaction
decoder intentionally rejects unsupported transaction types, its JSON models
need not retain unknown response fields, and its RPC client does not replace
Etherview's response budgets, batch correlation, stable error classification,
or credential redaction. PostgreSQL raw JSONB is also a compatibility surface:
supported objects retain unknown response fields, while some legacy PoW block
rows contain uncle hashes without the full headers now required to validate
the uncle root.

## Decision

Go-ethereum is the semantic authority for Ethereum protocol values whenever it
exports a matching type. Code uses the module-selected `common`, `hexutil`,
`core/types`, `core.Genesis`, and related exported types directly instead of
declaring repository aliases or replacement protocol models.
`internal/ethrpc` owns bounded execution-RPC access, not an independent
Ethereum type system. Repository-specific pool, capability, scheduling,
availability, persistence, and public-contract concepts belong to their
consuming packages and are not presented as go-ethereum concepts.

RPC ingestion is raw-first:

- The transport obtains a bounded `json.RawMessage` result and validates the
  JSON-RPC envelope, exact request/response correlation, duplicate or unknown
  batch IDs, result cardinality, and trailing input before protocol decoding.
- Existing canonical wire checks remain boundary compatibility rules. Using a
  more permissive upstream decoder does not silently broaden accepted quantity,
  data, hash, address, or identity syntax.
- For transaction types 0 through 4, go-ethereum `types.Transaction` is
  authoritative for the typed payload, protocol fields, and transaction hash.
  RPC-only inclusion and provenance fields remain separate boundary metadata
  and must agree with the requested block and the typed transaction.
- An unsupported future transaction type is not assigned a speculative
  repository transaction model. It produces a stable permanent validation
  error before any part of the block is persisted or durable coverage
  advances. The same atomic rejection applies when the unsupported object
  appears after otherwise valid transactions in the response.
- Other recognized Ethereum objects use reviewed go-ethereum types where those
  types cover the protocol semantics. Any RPC response extension not covered
  by an exported type remains raw boundary data rather than causing a
  repository copy of the upstream model.
- Receipt fields that are not committed by the receipt trie remain explicit
  cross-object checks. Transaction and block identity, transaction type,
  per-receipt gas use, top-level contract creation address, and effective gas
  price must agree with the authenticated transaction order and block header.
  For transaction types 0 and 1, effective gas price equals the transaction gas
  price; for types 2 through 4 it is derived from the fee cap, tip cap, and
  block base fee. A freshly fetched receipt must carry the exact derived value.
  Legacy stored rows may omit it, but a present conflicting value is rejected,
  and a dynamic-fee value read without an authenticated block context is never
  exposed as verified.

Transport remains an explicit hostile-input boundary even when it delegates
wire mechanics to go-ethereum. Etherview retains response-size and time
limits, exact batch accounting, endpoint pinning, no-trailing-value checks,
HTTP status handling, stable retry/unavailable/permanent classifications, and
redaction of response bodies, nested upstream errors, URLs, and credentials.
No geth error or response body crosses logs or public errors without that
classification.

The accepted raw RPC object is preserved independently of the typed
go-ethereum projection. PostgreSQL raw JSONB columns retain unknown fields on
supported objects; typed projections are never re-marshaled as a replacement
for those objects.

`blocks.raw` preserves the original block result's top-level JSON shape so
existing root-level JSONB paths retain their meaning. A validated PoW block
with uncles attaches the full raw uncle headers under the reserved
`_etherviewChainBundle` root field with the versioned
`etherview.chainbundle.uncles.v1` format. `DecodeStoredBlock` removes that
metadata before exposing or decoding the raw block.

A legacy block row with an empty `uncles` array remains directly readable. A
legacy PoW row with non-empty uncle hashes but without the corresponding full
headers cannot authenticate the header list or `sha3Uncles`; decoding returns
the permanent `ErrStoredUncleHeadersUnavailable` and exposes no bundle. Such a
row requires an explicit repair that refetches the exact block and uncle
headers from one endpoint and block view, revalidates the complete bundle, and
only then replaces the stored raw object. Missing headers are never synthesized
and the legacy row is never presented as a verified bundle.

SQL schema, canonical identity, public decimal-string quantities, OpenAPI
models, and generated clients remain their existing contracts; a change to any
of them requires its own reviewed contract change.

Genesis document ownership follows
[ADR-0019](ADR-0019-authenticated-genesis-state-import.md):
`core.Genesis` is the document model and `core.Genesis.ToBlock()` is
authoritative for the allocation root, genesis header semantics, and block
identity. Etherview retains only the source authentication, resource bounds,
canonical comparison, per-account persistence, and capability boundary around
that result.

Any go-ethereum upgrade must rerun focused compatibility vectors for supported
transaction types and permanent atomic rejection of unsupported types, strict
malformed wire cases, receipt cross-object fields, root-preserving block
persistence, legacy empty-uncle reads, permanent repair-required legacy PoW
rows, Genesis roots and hashes, error redaction, security advisories, and
dependency licenses. An upstream behavior change does not implicitly supersede
an Etherview public, persistent, or hostile-input contract.

## Consequences

- Ethereum protocol behavior follows the reviewed dependency instead of
  parallel repository codecs for recognized values.
- Raw-first ingestion preserves unknown fields on supported objects without
  claiming or persisting a transaction format unknown to the selected
  dependency.
- Stored block compatibility is explicit rather than universal: legacy
  empty-uncle rows remain readable, while legacy PoW rows missing required
  uncle headers require exact RPC-backed repair.
- Explicit transport and persistence adapters remain necessary; replacing a
  model does not delegate resource limits, redaction, raw-field retention, or
  public-contract ownership to go-ethereum.
- P70-T09 remains a release dependency until focused compatibility,
  integration, generation, security, license, and common gates establish these
  boundaries.
