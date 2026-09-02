# ADR-0045: Canonical ERC-4337 UserOperation Index

Status: accepted

## Context

ERC-4337 UserOperations are higher-level requests carried inside ordinary
transactions to an EntryPoint contract. They are not Ethereum transaction
types, and an execution transaction may contain multiple operations with
distinct senders, paymasters, factories, aggregators, outcomes, and gas costs.
EIP-7702 transaction support therefore cannot substitute for a UserOperation
index.

The authoritative inputs already exist in PostgreSQL: validated transaction
calldata, receipts, logs, block identity, and canonical history. A Bundler
mempool is incomplete, provider-specific, and not chain consensus. EntryPoint
versions also differ: v0.6 uses the unpacked UserOperation tuple, v0.7-v0.9 use
PackedUserOperation, and v0.8/v0.9 add EIP-7702 and later paymaster/lifecycle
semantics.

## Decision

`features.user_operations` enables a database-only `userop@1` enrichment
stage. It requires an explicit bounded list of EntryPoint address, version,
and inclusive block ranges. There is no event-topic or bytecode autodiscovery.
The normalized, deterministically ordered configuration has a SHA-256 digest;
stage publications, rows, coverage, cursors, readers, and split-role parity
all bind that digest. A configuration change exposes no old-digest output and
requires an explicit bounded reindex.

The stage reconstructs the exact stored block bundle before publication and
processes only successful top-level transactions whose direct target is an
active configured EntryPoint and whose selector is `handleOps` or
`handleAggregatedOps`. It validates all ABI offsets and lengths, bounds work by
the actual calldata, and requires canonical re-encoding equality. A reverted
outer transaction is an unsuccessful bundle attempt and produces no indexed
UserOperation.

For a successful bundle, calldata order defines the operation position. The
EntryPoint's `UserOperationEvent.userOpHash` is the canonical public identity;
the stage correlates it with the decoded operation and requires exact event
count, order, sender, nonce, paymaster, and version-specific semantics. It
does not replace the deployed EntryPoint's hash algorithm with a locally
invented identity. Account deployment, ignored initCode, EIP-7702
initialization, execution revert, postOp revert, prefund, and aggregator facts
are retained in log order. Contradiction or malformed input fails the complete
block stage and cannot publish a partial bundle.

PostgreSQL retains block-local operations, ordered protocol events, and
deduplicated participant roles. Each relation carries chain, block hash,
configuration digest, canonicality, and an exact `userop@1` publication
witness. Derived journals toggle canonicality on detach and reattach while
retaining orphan evidence. Configuration-scoped covered blocks and maximal
contiguous ranges prove authoritative list snapshots without scanning all
history.

Native APIs expose global, detail, transaction, and address resources.
uint256-domain values are decimal strings; raw initCode, callData,
paymasterAndData, signatures, and revert bytes remain hex. Only standard
`Error(string)` and `Panic(uint256)` payloads receive local semantic decoding;
unknown account- or paymaster-specific bytes remain raw. Address participation
distinguishes sender, EntryPoint, outer transaction sender, beneficiary,
factory, paymaster, aggregator, and EIP-7702 delegate.

Pending UserOperations, Bundler RPC dependencies, submission, simulation,
wallet-specific signature or callData interpretation, and EntryPoint calls
nested under another top-level target are excluded. PostgreSQL remains the
only correctness store, and the existing Enrich/API roles own identical
components in monolith and split deployments.

## Consequences

- Deployments opt in with an explicit EntryPoint allowlist; an absent or
  mismatched configuration is visibly unavailable rather than an empty result.
- The API can serve the highest continuous published snapshot while a bounded
  historical reindex continues, but it never claims absence beyond coverage.
- New heads use the normal enrichment outbox. Existing canonical history is
  rebuilt only through operator-approved `reindex --stage userop` ranges.
- Supporting a future EntryPoint wire shape requires a reviewed decoder and a
  stage-version or ADR update; configuration alone cannot authorize unknown
  semantics.
