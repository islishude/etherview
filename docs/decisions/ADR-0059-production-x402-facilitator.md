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
