# ADR-0021: Durable x402 Per-Request Billing

Status: accepted

## Context

Etherview needs optional per-request payment for selected bounded native reads
without turning API keys into subscriptions, requiring user login, or making
an external facilitator the replay source of truth. Payment verification and
settlement cross a remote boundary, while API replicas may race, restart, or
lose the settlement response after a one-time authorization is consumed.

An in-memory response buffer cannot recover a paid response after a process
crash. Reissuing an exact-EVM settlement whose outcome is unknown may conflict
with an already-consumed authorization. The service must therefore prefer an
explicit unresolved state over fabricated exactly-once delivery.

## Decision

`internal/billing` owns product policy and the PostgreSQL ledger.
`internal/billing/x402` owns only x402 v2 exact-EVM parsing and the restricted
facilitator transport. Neither package is part of API-key authentication.

Every route has one static operation catalog entry containing its method,
ServeMux pattern, OpenAPI operation ID, accepted query parameters, response
bound, and billing eligibility. All routes are free unless configuration
explicitly selects one eligible operation as `x402` or
`api_key_or_x402`. New operations are ineligible by default.

The eligible inventory is the existing bounded native GET set recorded in
P66. Status, public configuration, authentication, billing and administration,
health, metrics, preflight, compatibility, media, streaming, mutation,
fallback, and unknown routes cannot be priced.

Verified-artifact GET and the proxy detail, upgrade-history, and
initialization-history GETs are also deliberately anonymous and free. They are
part of the explorer's basic contract-reading surface and are never admitted
to the pricing allowlist. Verification submissions and verification-job reads
retain their separate authentication policy; making artifact reads free does
not relax either boundary. Configuration and administration surfaces omit the
removed verified-artifact operation, and a stale configuration that still
tries to price it fails validation instead of silently changing policy.

For `api_key_or_x402`, an absent API key selects payment. A valid API key keeps
its existing quota and a quota rejection remains 429; an invalid supplied key
remains 401. A pure `x402` policy requires payment even when a valid key is
present. A payment header on a free or API-key-selected request is rejected
rather than silently ignored.

The request resource is derived only from the normalized configured public
origin plus the matched route and canonical parameters. Host and forwarded
host headers are not authority. Unknown, duplicate, or invalid query inputs
fail before a requirement or reservation is created.

One bounded, strictly decoded v2 authorization is normalized and HMACed with a
billing-only server Secret. PostgreSQL stores the digest and the complete
non-secret binding, never the raw authorization. A unique row and fenced owner
allow at most one replica to verify, execute the handler, and attempt
settlement. Database transactions and row locks never span handler or
facilitator calls.

Only a bounded 2xx handler result may settle. After the handler completes, a
compare-and-set transition publishes `settling` before the outbound settle
call. A successful facilitator response and immutable financial result are
committed with an append-only event before any captured header or body is
released. Any failure before settlement discards or returns the non-success
handler result without charging.

Once a settle request might have reached the facilitator, timeout, disconnect,
5xx, malformed response, or an unconfirmed final database commit leaves the
row in `settling` with `settlement_unknown`. That state is never expired,
reclaimed, or automatically settled again. The same authorization returns a
stable 503 and never reexecutes the handler. An operator may reconcile it to
settled with an external transaction hash or to failed; either outcome appends
an event and never recovers the old response.

Facilitator traffic uses one fixed HTTPS origin, no environment proxy, no
redirect, strict request/response caps and timeouts, bounded secret headers,
and per-dial address validation against an explicit CIDR set. Errors and logs
contain only stable codes. Standard Kubernetes NetworkPolicy enforces the same
CIDR/443 set for API Pods; application origin and dial validation supply the
hostname boundary.

Billing is disabled by default and an empty price list means every operation is
free. Disabled API roles construct no facilitator client, read no billing
Secret, and require no billing egress.

## Consequences

- PostgreSQL, not the facilitator or process memory, is replay and accounting
  authority across replicas.
- The MVP guarantees at most one handler/settlement attempt for an
  authorization, not recovery of a response after payment and process failure.
- User login remains optional. A trusted payer may be associated with an
  existing P65 user after verification without affecting payment validity.
- Revenue APIs use decimal strings and durable rows; Prometheus values are
  operational signals, not a ledger.
- A future durable-response or automatic settlement-recovery design requires a
  facilitator-specific idempotency/status contract and a separate decision.
