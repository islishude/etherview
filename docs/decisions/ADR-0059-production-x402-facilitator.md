# ADR-0059: Production multi-chain x402 facilitator

Status: accepted

## Decision

Etherview operates a separate, authenticated exact-EVM facilitator. It is not a
serve role. Each configured network has an explicit RPC, signing identity, token,
method and recipient allowlist and transaction fee bounds. Network identity is
checked at startup and before signing. Only authorization EIP-3009 and standard
Permit2 settlement are supported; smart-wallet deployment and approval sponsorship
are disabled. Signing uses encrypted Ethereum keystore files or AWS KMS secp256k1
keys, with secrets scoped exclusively to the facilitator process.

PostgreSQL owns authorization identity, request digest and signed transactions.
Signed bytes and transaction hash are committed before broadcast. Duplicate
requests reconcile the same transaction; conflicting authorization reuse fails.
Unknown broadcast outcomes never cause a new transaction or nonce allocation.
A singleton database advisory lock excludes concurrent facilitator processes.
All journal mutations use the lock-owning physical connection, serialized without
holding transactions across external calls. Lock loss stops new work and signals
fatal shutdown within the process shutdown budget; a durable unique network/account/nonce constraint and
immutable signed bytes prevent a replacement process from issuing another
transaction for a reserved nonce. No database snapshot spans RPC or KMS calls.
The signing accounts must not be used by another transaction sender.

The facilitator does not credit user accounts. Existing API-side one-shot payment
and PostgreSQL-fenced account publication remain authoritative. Receipt checks
must establish successful expected transfers on the configured chain before
reporting success; uncertain outcomes retain their exact transaction hash.

Preview uses the same service against two local chains and standard Permit2
contracts. Production requires TLS and bearer authentication; plaintext is an
explicit local-only exception. Runtime chain failures are isolated. Readiness
is separate from liveness and reports dependency degradation.

## Acceptance

P60-T26 owns automated local delivery. P60-T27 owns local end-to-end acceptance
using a pinned Rustack KMS emulator and two independent Anvil networks, without
Geth or real AWS/public-testnet prerequisites. Actual AWS SDK HTTP signing
compatibility must pass; fake KMS and keystore tests cannot substitute for it.
Both standard payment methods must pass on each network in all/split topologies,
with recovery coverage and independent chain/writer reconciliation.

The dual-Anvil facility is dedicated to T27 and does not replace Preview's
Geth/Anvil topology. Local credentials and emulator endpoints remain test-scoped.
Simulation does not establish real AWS IAM enforcement, production key isolation
or public-network production readiness. P70 release gates remain separate.
Existing funded testnet evidence does not cover this new facilitator implementation.

## Read-only submission progress

Authenticated `POST /progress` accepts the same bounded payment request as
settlement and matches both authorization identity and canonical request digest.
It reads PostgreSQL only, without acquiring the settlement gate, signing,
broadcasting or reconciling. An absent reservation reports `submitting`; a
signed transaction with no confirmed broadcast reports `unknown` with its hash.
A successful RPC broadcast or matching confirmed receipt durably records broadcast
evidence and permits `confirming`. Journaled signed bytes alone never imply
submission. A successful settlement is also confirmation evidence. Progress
responses contain only the closed stage and optional transaction hash, never
authorization bodies, credentials or raw transactions.

Progress uses the existing authenticated service transport and is observational;
failures do not cancel settlement or authorize another attempt. Operator exact-byte
rebroadcast also records confirmed broadcast evidence on success.

## Configuration and lifecycle boundaries

Configuration separates `server`, `database`, `auth`, `signers` and `chains`.
Ordinary RPC destinations use `chains[].rpc.url`; credential-bearing destinations
use the mutually exclusive `url_file`. Local plaintext RPC permission belongs
to each RPC configuration, independently of the server's local plaintext exception.
There are no legacy aliases, general environment overrides or YAML interpolation.
Only `X402_DATABASE_URL` / `X402_DATABASE_URL_FILE` and `X402_BEARER_TOKEN` /
`X402_BEARER_TOKEN_FILE` can override their YAML file sources. Presence of both
members of a pair is an error, even when empty. A nonblank environment value wins;
a blank direct value or readable blank environment file falls back to YAML. An unreadable
or oversized selected source fails without fallback. Both startup and offline
checks apply the same bounded, redacted resolution rules.
AWS SDK workload identity remains the KMS credential source.

Database URLs and bearer credentials are process-scoped environment or file inputs;
encrypted keystores/passwords and TLS private keys remain file inputs. Public TLS certificates and ordinary
addresses are configuration, not secrets. URL path credentials cannot be inferred
reliably: operators must select file injection for sensitive URLs.

Strict decoding and bounded local-file resolution precede external initialization.
`--check-config` performs those offline checks without decrypting signing keys or
contacting dependencies; it is not readiness evidence. Errors identify only a
stable code and schema-owned field path. HTTP transport calls a separate settlement
operation, which uses a narrow journal interface implemented by generated sqlc
queries. Startup owns resource acquisition; failure and idempotent shutdown release
resources in reverse order after stopping monitors. Existing singleton, nonce,
journal, protocol and API-side credit semantics remain unchanged.

## Capacity refusals

Submission admission is separate from the execution gate shared with background
reconciliation. A payment waits for background journal scans within its existing
deadline; maintenance alone must not create a durable capacity refusal. Cancellation
while waiting leaves no reservation, refusal, signature or broadcast. Concurrent
payment submissions retain the capacity-refusal behavior below.

A new authorization refused because its chain is busy or has unresolved work is
atomically inserted as a failed journal entry with an empty transaction and a
standard settlement rejection. This fences later submission of the refused
authorization and lets the API finish its one-shot attempt as failed rather than
unknown. Insert conflicts never overwrite an existing owner: digest conflicts
fail, recorded outcomes replay, signed transactions return their original pending
hash, and unsigned in-flight reservations remain uncertain. Database failures
still preserve uncertainty. A submission that loses its reservation to a concurrent
refusal replays that refusal and never signs. No automatic settlement retry is added.

## Authorization reconciliation and result identity

P60-T32 separates immutable outgoing transaction identity from payment evidence.
Unsigned termination is conditional on reserved state and absent nonce, raw bytes
and hash; zero affected rows require a fresh read. Signed bytes and nonce uniqueness
remain immutable. A confirmed external payment does not release an outstanding
local transaction from nonce admission or exact-byte operator recovery.

A consumed authorization or reverted local transaction is not proof of nonpayment.
For standard direct EIP-3009 and Permit2 proxy calls, reconciliation validates the
signed authorization, complete call data, chain, token, payer, recipient, amount,
execution-time validity, authorization/settlement event and Transfer in a successful
canonical receipt with configured confirmations. Proxy-internal calls are not
traced; insufficient evidence remains pending. Per-cycle historical and forward
scans cover at most 2,048 blocks and 32 candidate receipts, checkpoint coverage,
shrink oversized pages without skipping blocks, and reset on canonical mismatch.
Page attempts alternate historical and forward work using a persisted direction,
so continuous head growth cannot starve historical evidence across restarts. Failed
pages retain coverage positions while allowing the other direction to proceed.
The recovery queue orders by last update and rotates each attempted authorization
before external work, so unresolved entries cannot monopolize its 32-row batch.
Only an expired, confirmed-unused authorization permits post-submission failure.

Authenticated POST /result reads the authorization ID and exact request digest,
returning independent original transaction and payment evidence. It cannot sign,
submit or reconcile. POST /progress remains observational. Successful settlement
returns the actual payment hash; confirmed failure retains the original hash.
The separate result contract carries the provenance needed by the API publisher.
Missing protocol defaults mean eip3009/authorization; present invalid values fail.
Only explicit business error codes may be exposed. RPC failures remain unavailable,
including SDK errors that otherwise resemble invalid signatures. Consumed valid
signatures report authorization_reconciliation_required for the one-shot settle
path, even when their execution deadline has since elapsed.

Liveness fails on permanent singleton loss; an independent lock monitor signals
the main process to exit nonzero. Transient chain failures remain chain-local.
The current signature-format policy remains 65-byte signatures; this change does
not introduce wallet deployment or arbitrary smart-wallet signature support.
