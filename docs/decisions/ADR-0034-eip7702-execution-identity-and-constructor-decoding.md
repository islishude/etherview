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

- `state_diff@2` binds the raw geth type-4 transaction and one endpoint's exact
  pre/post-state diff to the requested transaction and block. It uses
  `types.SetCodeAuthorization.Authority` and applies tuples in order, including
  chain ID zero, sender pre-increment, low-s signature rules, nonce checks,
  ordinary-code rejection, zero-address clearing, and repeated authorities.
  Applied nonce/code results must agree with the provider's post-state. Missing
  exact evidence is unavailable; contradictory evidence fails permanently.
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
  a receipt log while the frame resolution remains `unavailable`. `abi@3`
  decodes that log only if the ordinary block-scoped code-identity resolver
  independently finds a canonical historical code hash and range-valid ABI for
  the attributed address; otherwise it remains unavailable. The emitter's ABI,
  current delegation, block height, and `latest` are never substitutes.
- `abi@3` decodes functions and logs only from an exact execution identity (or
  the existing explicit conservative log fallback). `CREATE` and `CREATE2`
  constructor inputs require the created address's canonical code observation,
  exact verified artifact, full creation match, and persisted constructor
  argument suffix. The ABI arguments must unpack and re-encode byte-for-byte.
  Constructors have no successful outputs; direct failure retains independent
  custom-error or builtin `Error`/`Panic` decoding when the required ABI exists.
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
- Once the transaction-time execution identity is fixed, selectorless calldata
  is decoded against that exact code's ABI: empty calldata selects `receive`
  before `fallback`, while incomplete or unmatched selectors may select only a
  declared `fallback`. Current delegation and emitter ABI remain prohibited
  substitutes.
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
  lazy unless their panels are opened.

## Consequences

Historical support requires an explicit bounded `state_diff` reindex, followed
by `trace`, `proxy`, and `abi` replay. Migration `0040` creates storage and
changes version contracts but never enqueues unbounded history. Authorization
creation/signing/revocation, opcode/raw trace persistence, EIP-7851, EIP-8202,
and proxy authority inferred from arbitrary delegatecall remain out of scope.
