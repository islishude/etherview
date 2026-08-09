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
- `abi@3` decodes functions and logs only from an exact execution identity (or
  the existing explicit conservative log fallback). `CREATE` and `CREATE2`
  constructor inputs require the created address's canonical code observation,
  exact verified artifact, full creation match, and persisted constructor
  argument suffix. The ABI arguments must unpack and re-encode byte-for-byte.
  Constructors have no successful outputs; direct failure retains independent
  custom-error or builtin `Error`/`Panic` decoding when the required ABI exists.
- Trace object storage schema v3 contains only normalized raw frames. Current
  delegation, constructor, and ABI projections are attached from a PostgreSQL
  repeatable-read snapshot. The current delegation endpoint is served through
  the writer and exact canonical-tip RPC state; every delegated-account wallet
  operation re-fetches and compares chain, authority, delegate, and code hash.
  Reads may observe a newer canonical tip when that identity is unchanged;
  writes additionally require the exact block number and hash captured by the
  interaction fence before submission.

## Consequences

Historical support requires an explicit bounded `state_diff` reindex, followed
by `trace`, `proxy`, and `abi` replay. Migration `0040` creates storage and
changes version contracts but never enqueues unbounded history. Authorization
creation/signing/revocation, opcode/raw trace persistence, EIP-7851, EIP-8202,
and proxy authority inferred from arbitrary delegatecall remain out of scope.
