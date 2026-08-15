# ADR-0034: EIP-7702 Execution Identity and Exact Constructor Decoding

Status: accepted

## Context

EIP-7702 keeps an account's address, balance, nonce, and storage context while
making call-like execution load code from the first delegation target. The
delegation list is applied before transaction execution, after the sender nonce
increment, and survives an outer execution revert. A block-end code lookup
cannot reconstruct tuple skips, same-block re-delegations, or the code identity
used by an earlier transaction. Creation input has a separate ambiguity: the
constructor boundary is not recoverable from runtime bytecode equality alone.

## Decision

- `state_diff@3` binds each raw geth type-4 transaction to its exact ordered
  `txHash` item in one endpoint's `debug_traceBlockByHash` pre/post-state
  response. The complete response is rejected before publication if any item
  is missing, reordered, duplicated, malformed, or failed. It uses
  `types.SetCodeAuthorization.Authority` and applies tuples in order, including
  chain ID zero, sender pre-increment, low-s signature rules, nonce checks,
  ordinary-code rejection, zero-address clearing, and repeated authorities.
  Applied nonce/code results must agree with the provider's post-state. Missing
  exact evidence is unavailable; contradictory evidence fails permanently.
- When the diff-mode response omits or cannot resolve a transaction's
  top-level target, the same endpoint receives one additional block-level
  `prestateTracer` request with `diffMode=false`, still bound to the exact block,
  transaction count, order, and hashes. Only that top-level target and a
  present first-hop delegate may supplement execution evidence. A target absent
  from complete prestate has empty code; a delegation designator whose delegate
  account is absent remains unavailable. No height, block-end state, `latest`,
  or per-transaction fallback is permitted.
- Authorization rows retain the raw tuple, recovered authority when available,
  signature status, application status, and a stable skip reason. Transaction
  execution-code rows retain the call context, first-hop code address/hash, and
  whether execution is direct, delegated, empty, or unavailable. Delegation is
  followed once only. A second designator and a precompile provide empty
  executable code under EIP-7702 semantics.
- `trace@3` keeps `to` as the call/storage context. The RPC call target is an
  internal input to resolution; `execution_address` and its exact code hash name
  the code actually loaded for roots and `CALL`, `STATICCALL`, `DELEGATECALL`,
  and `CALLCODE`. Exact log attribution uses that code identity while the
  receipt emitter must equal the frame context. No arbitrary delegatecall is
  interpreted as a durable proxy or management relationship.
- Geth's `prestateTracer` may expose the authorization-applied first-hop
  EIP-7702 delegate address without returning that delegate account's code. In
  this case `trace@3` may retain the exact trace path and execution address for
  a receipt log while the frame resolution remains `unavailable`. `abi@4`
  decodes that log only if the ordinary block-scoped code-identity resolver
  independently finds a canonical historical code hash and range-valid ABI for
  the attributed address; otherwise it remains unavailable. The emitter's ABI,
  current delegation, block height, and `latest` are never substitutes.
- `abi@4` decodes functions and logs only from an exact execution identity (or
  the existing explicit conservative log fallback). `CREATE` and `CREATE2`
  constructor inputs require the created address's canonical code observation,
  exact verified artifact, full creation match, and persisted constructor
  argument suffix. The ABI arguments must unpack and re-encode byte-for-byte.
  Constructors have no successful outputs; direct failure retains independent
  custom-error or builtin `Error`/`Panic` decoding when the required ABI exists.
- A completed canonical `state_diff@3` may still omit an unchanged nested
  direct-call target because complete-prestate supplementation is restricted to
  the transaction's top-level target. It may therefore leave a public Trace
  `CALL` or `STATICCALL` frame with
  `resolution=unavailable` and no execution address or code hash. In that exact
  case only, the read-time projection may use bounded, block-range-covered
  verified function-selector entries for the frame's call-context address. It
  must decode and re-encode the complete calldata byte-for-byte and fail closed
  on selector collisions, overlapping verified code identities, or candidate
  overflow. The frame keeps its unavailable execution evidence; this fallback
  never applies to `DELEGATECALL`, `CALLCODE`, a known-but-unresolved EIP-7702
  delegate, proxy or Diamond routing, same-code reuse, selectorless calldata,
  or an incomplete state-diff publication. A uniquely selected function entry
  may decode its declared successful outputs and decoder-local builtin reverts;
  custom errors still require a complete execution-bound ABI candidate.
- Transaction calldata uses the final execution-code row produced after all
  authorization tuples for that transaction have been applied in order. Thus a
  successful re-delegation of the transaction's `to` authority uses the new
  delegate, a successful zero-address authorization yields `empty` and
  `not_applicable`, and authorizations for other authorities do not affect the
  call. Skipped tuples do not change the identity. The same rule applies to
  later ordinary transactions that call a delegated EOA, and an outer call
  failure or revert does not undo the authorization identity used for decoding.
  A delegated target is followed once; an empty first-hop result does not fall
  back to an older delegate or another delegation designator.
- `state_diff@3` remains the immutable provider-evidence layer. `abi@4`
  separately materializes one effective execution identity for every
  transaction root, keyed by immutable block hash, transaction hash and index,
  and call-context address. An exact `direct`, `eip7702_delegate`, or `empty`
  state-diff result is copied with `prestate_tracer` provenance. Only an
  `unavailable` result that already names its first-hop execution address may
  be recovered; all other unavailable shapes remain unavailable.
- Recovery requires the unique published `trace@3` root for the same block,
  transaction hash and index. Its target, input, context, and first-hop
  execution address must agree with the canonical transaction and raw
  state-diff result. The resolver starts with a canonical code observation
  strictly before the block and applies only continuous code changes whose
  transaction index precedes the current transaction. A change in the current
  or any later transaction cannot affect the identity. Ordinary non-empty code
  yields the exact delegate code hash; proven empty code, a precompile, or a
  second delegation designator yields `empty`. Missing history stays
  unavailable, while inconsistent code history or contradictory exact evidence
  fails the ABI job permanently.
- Effective identities, ABI bindings, transaction calldata decodings, the
  `abi@4` publication, and its canonicality journal commit atomically. Readers
  use the materialized identity only after the exact `abi@4` generation is
  published. Before that publication they retain raw `state_diff@3` semantics
  and never read an old `abi@3` result. Registry and Diamond routing keys bind
  transaction position and exact code hash, so two transactions in one block
  may execute different delegates for the same authority without sharing
  candidates. Trace APIs continue to expose their original trace evidence.
- Once the transaction-time execution identity is fixed, selectorless calldata
  is decoded against that exact code's ABI: empty calldata selects `receive`
  before `fallback`, while incomplete or unmatched selectors may select only a
  declared `fallback`. Current delegation and emitter ABI remain prohibited
  substitutes.
- The transaction-calldata read projection rebuilds recursive parameter shape
  from the same block-, execution-address-, and code-hash-bound ABI candidate
  inside its repeatable-read snapshot. Public inputs carry the selected
  parameter's `name`, canonical `type`, optional validated `internalType`, and
  recursive tuple components while persisted `abi@4` arguments remain the
  value-only decoding fact. A persisted decoded fact without a corresponding
  exact ABI, or a value/signature disagreement after re-decoding the same ABI
  source, is corrupt data. A later higher-confidence candidate may improve the
  read result under ADR-0009. The verified-address selector fallback projects
  shape only from its uniquely selected stored ABI entry. No path consults
  current verification, proxy, or delegation state, and no missing or
  contradictory shape degrades to flat JSON. Trace retains its generic
  `ABIValue` contract.
- Trace object storage schema v3 contains only normalized raw frames. Current
  delegation, constructor, and ABI projections are attached from a PostgreSQL
  repeatable-read snapshot. The current delegation endpoint is served through
  the writer and exact canonical-tip RPC state; every delegated-account wallet
  operation re-fetches and compares chain, authority, delegate, and code hash.
  Reads may observe a newer canonical tip when that identity is unchanged;
  writes additionally require the exact block number and hash captured by the
  interaction fence before submission.
- Address summaries expose whether canonical applied delegation history exists
  at their exact state reference. This writer-authoritative projection includes
  clearing, excludes skipped, orphan, and later authorizations, and fails closed
  if the reference or history cannot be validated. The Web history entry remains
  discoverable after clearing while current binding and artifact reads stay
  lazy unless their panels are opened. Public history is ordered newest first by
  the numeric `(block_number, transaction_index, authorization_index)` tuple;
  text serialization of public quantities never changes that ordering or its
  cursor boundary. When the current binding is not delegated, the retained
  `#code` route is labeled Status and exposes only the current writer-authoritative
  status and canonical snapshot plus a History entry. It does not expose stale
  delegate code or an empty verified-artifact surface, and unavailable state is
  never presented as cleared.

## Consequences

Historical support requires an explicit bounded `state_diff` reindex, followed
by `trace`, `proxy`, and `abi` replay. Migration `0040` creates raw storage;
migration `0047` advances the current witness to `state_diff@3` and invalidates
superseded proxy-interaction coverage. Migration `0048` creates the
transaction-scoped effective-identity partition and advances ABI to `abi@4`;
it does not enqueue or backfill history. Authorization
creation/signing/revocation, opcode/raw trace persistence, EIP-7851, EIP-8202,
and proxy authority inferred from arbitrary delegatecall remain out of scope.
