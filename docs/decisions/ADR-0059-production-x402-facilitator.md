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
Lock loss stops new work; a durable unique network/account/nonce constraint and
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

P60-T26 owns automated local delivery. P60-T27 owns real AWS KMS and funded
multi-network acceptance. Neither mocked KMS nor local-chain results establish
production readiness. Existing funded testnet evidence does not cover this new
facilitator implementation.
