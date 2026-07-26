# P66 — x402 API Billing

Status: `in_progress`

## Outcome

Selected bounded native GET operations can require one exact-EVM x402 v2
payment. PostgreSQL prevents replay across API replicas, and Etherview releases
the captured resource response only after settlement and its durable final
state commit.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0003](../decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md)
- [ADR-0021](../decisions/ADR-0021-x402-request-billing.md)
- [x402 v2 specification](https://github.com/x402-foundation/x402/blob/main/specs/x402-specification-v2.md)
- [Exact EVM scheme](https://github.com/x402-foundation/x402/blob/main/specs/schemes/exact/scheme_exact_evm.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P66-T01 | done | P40, P60 | ADR, operation catalog, OpenAPI 402 contract, and configuration schema | `make generate-check plan-check`, config and contract tests |
| P66-T02 | done | P65-T01, P66-T01 | PostgreSQL payment ledger, events, reservation owner, and state machine | concurrency, replay, expiry, and migration tests |
| P66-T03 | done | P66-T01 | Reviewed x402 v2 types, exact-EVM validation, fingerprinting, and bounded facilitator client | protocol vectors and hostile transport tests |
| P66-T04 | done | P66-T02, P66-T03 | Coarse limiter, route billing middleware, bounded response capture, settlement gate, and CORS | policy and failure-injection matrix |
| P66-T05 | done | P66-T04 | Billing config/history/admin APIs and operator inspection/reconciliation CLI | generated-client, authorization, pagination, and CLI tests |
| P66-T06 | done | P65-T03, P65-T05, P66-T05 | Optional payer/user association plus account/admin payment views | attribution, bilingual UI, Vitest, and browser tests |
| P66-T07 | done | P66-T04, P66-T05 | Metrics, alerts, Secret/CIDR deployment, operations guide, and failure runbook | role parity, race, security, Helm, and outage tests |
| P66-T08 | blocked | P66-T07 | Opt-in real-client and compatible-facilitator testnet conformance | Base Sepolia transaction and ledger reconciliation evidence |

## Acceptance

- [x] An unpaid priced operation returns native HTTP 402 with request ID and a
      valid `PAYMENT-REQUIRED` header.
- [x] A valid exact-EVM authorization unlocks exactly one matching request and
      exposes `PAYMENT-RESPONSE` only after settlement and durable commit.
- [x] An authorization cannot be replayed across operations, resources, query
      variants, prices, recipients, assets, or concurrent replicas.
- [x] Invalid, oversized, expired, mismatched, duplicate, or malformed payment
      headers fail before the protected handler.
- [x] Handler, verification, settlement, or ledger failure never releases a
      successful protected response.
- [x] A possibly delivered settlement remains `settling` with a stable unknown
      code, is never retried automatically, and requires operator
      reconciliation without replaying the response.
- [x] Free routes and existing API-key behavior remain compatible; disabling
      billing requires no billing Secret, network egress, or facilitator call.
- [x] Final payer, amount, asset, network, operation, transaction, and state are
      reconcilable without retaining the raw authorization.
- [x] Logs and metrics retain closed labels and never expose authorization,
      credentials, hostile remote bodies, or high-cardinality wallet data.
- [x] Monolith and split API replicas share PostgreSQL replay and settlement
      semantics.

## Current Blockers

P66-T08's explicit real-payment harness and offline conformance regressions are
complete. The item is blocked on operator-provided Base Sepolia funds, a payer
key, one priced route backed by a compatible staging facilitator, the matching
staging writer, an independent Base Sepolia RPC endpoint, and the target
deployment image/build digest. Supplying those inputs and preserving one
successful 402 → signature → settlement → writer/chain report clears the
blocker. No such live credentials, funding, or staging deployment are stored
in this repository, so `make test-x402-testnet` has intentionally not been
executed against a live target.

## Evidence

- P66-T01 governance and catalog: ADR-0021 plus the repository invariant fix
  settlement-after-capture, persistent replay fencing, and manual convergence
  for unknown settlement. `internal/apiops` contains every native operation,
  the closed 19-operation eligible inventory, exact query keys, ServeMux
  patterns, and response bounds.
- P66-T01 contract and configuration: the OpenAPI exposes the shared
  `PAYMENT-SIGNATURE`, `PAYMENT-REQUIRED`, `PAYMENT-RESPONSE`, native 402
  envelope, public billing config, user history, and administrator ledger and
  summary operations. Feature-off defaults, exact eligible route policies,
  canonical public/facilitator origins, CIDRs, bounds, role-scoped Secret
  loading, and redacted facilitator header parsing are covered by config and
  contract tests.
- P66-T01 verification: `go test ./internal/api ./internal/apiops
  ./internal/config -count=1`, `make generate-check`, and `make plan-check`
  pass. The API generator's locked transitive overrides keep its dependency
  audit at zero high-severity vulnerabilities.
- P66-T02 implementation: migration `0024_x402_billing.sql` adds only the
  payment ledger and append-only event stream, with global fingerprint
  uniqueness, an undisclosed owner fence, fixed v2/exact financial bindings,
  immutable terminal facts, and the closed
  `reserved→verified→settling→settled|failed` plus expiry state machine.
  Handler start is a one-winner CAS; `settling` never expires or reclaims, and
  only a recorded `settlement_unknown` can be manually reconciled. User
  attribution is accepted only by the post-verification transition, never at
  reservation time.
- P66-T02 verification: `go test ./internal/billing ./internal/db
  ./internal/store -count=1` passes. PostgreSQL 18 executes the integration
  replay, concurrent reservation/handler, expiry, unknown reconciliation,
  premature operator reconciliation rejection, immutable settlement, and
  append-only event tests as part of the passing full `make test-integration`.
- P66-T03 implementation: `internal/billing/x402wire` strictly decodes the
  official v2 exact-EVM structures and interoperates with the pinned
  `x402/go/v2 v2.19.0` types without adopting its general HTTP middleware.
  Requirements and facilitator I/O are canonical, bounded, redirect-free,
  proxy-free, CIDR-checked on every dial, and mapped only to stable failure
  classes. The unique HMAC replay identity covers the normalized EIP-3009
  authorization and EIP-712 network/asset domain but excludes the replaceable
  proof and unsigned outer request fields. PostgreSQL separately compares the
  operation, method, resource, requirement, amount, and recipient, so reusing
  one authorization across any of those bindings cannot acquire a second
  reservation owner.
- P66-T03 verification: official SDK header/type round trips, EIP-3009 vectors,
  strict JSON, alternate-proof, cross-resource replay, loopback-resource,
  `/supported`, DNS/CIDR, redirect/proxy, TLS, header, timeout, response-limit,
  verification, and settlement-unknown regressions pass under
  `go test ./internal/billing/x402wire ./internal/jsonstrict -count=1` and the
  corresponding race run. Scoped `go vet`, `go mod verify`, and
  `git diff --check` also pass.
- P66-T04 implementation: the trusted-proxy-aware coarse limiter precedes API
  key parsing, then the catalog-selected policy chooses the unchanged API-key
  quota path or one replay-fenced paid path. Canonical resources are entirely
  data-driven from the unique catalog parameter schema. Only one owner can
  verify, invoke the bounded non-streaming capture, cross the settlement CAS,
  commit the terminal writer row/event, and release the 2xx response.
- P66-T04 failure semantics: malformed or mismatched payment fields fail before
  the handler; panic, cancellation, non-2xx, body/header overflow, unsupported
  response interfaces, verify/settle/ledger failures, and uncertain settlement
  discard the protected buffer. A pre-existing `settling` row always returns
  stable 503 without rerunning the handler or facilitator. Billing-enabled HTTP
  construction fails closed when its writer-backed dispatcher is absent.
- P66-T04 verification: all 19 eligible actual ServeMux routes require 402
  without payment and bypass quota only on the paid path. Cross-binding and
  concurrent authorization, catalog/config response bounds, failure injection,
  free/API-key/compatibility/header rejection, targeted race, full Go,
  generation, vet, plan, and PostgreSQL integration gates pass.
- P66-T05 HTTP contract: the free configuration surface exposes only sorted
  v2/exact route prices and EIP-55 public identities. User and administrator
  history use Cookie authority plus writer-only reads, strict bounded query
  parsing, scope/filter/chain-bound opaque cursors, and `limit+1` pagination.
  Summary intervals default to 24 hours, stop at 31 days, and use a dedicated
  97-digit aggregate string boundary without weakening individual uint256
  amounts.
- P66-T05 operator contract: `admin billing inspect` returns a non-secret
  payment/event projection. `reconcile` accepts only an explicit settled
  transaction hash or failed outcome, works while both features are off, and
  loads as a maintenance role so session, fingerprint, and facilitator-header
  Secrets are not read. An unknown settlement is immediately reconcilable; a
  crash-window `settling` row without its unknown marker requires two minutes.
  Both paths bind the chain, forbid time regression, atomically append the
  operator event, and race through one writer CAS.
- P66-T05 verification: `make generate-check`, focused unit and race tests for
  API/config/billing/HTTP/app/CLI, and `git diff --check` pass. Against the
  disposable PostgreSQL 18 writer,
  `go test -race -tags=integration ./internal/billing ./internal/integration
  -run Billing -count=1` passes, including pagination/filter/summary integrity,
  fresh/stale/terminal reconciliation, chain isolation, CLI feature-off and
  unavailable-reader behavior, and runtime/operator races.
- P66-T06 attribution: payer association runs only after facilitator
  verification and only while both authentication and billing are enabled.
  The writer accepts an optional user only when its chain and 20-byte address
  exactly match the verified payer; active and disabled users can retain
  financial attribution, accountless payment remains valid, cross-chain or
  mismatched users fail closed, and historical accountless rows are not
  backfilled. User history rechecks its attribution while administrator
  history retains the full nullable ledger.
- P66-T06 browser surface: the account page exposes only the current
  Cookie-authenticated user's cursor-bound payment history, while the
  administrator page provides string-safe summaries and filtered ledger
  pagination. Both languages distinguish payment states and preserve opaque
  cursors verbatim; the generated same-origin client never sends
  `PAYMENT-SIGNATURE`.
- P66-T06 verification: focused billing/app/HTTP unit and PostgreSQL race
  integration tests cover active, disabled, missing, cross-chain, mismatched,
  and no-backfill attribution. `npm --prefix web test`, lint, and build pass
  with 17 files and 116 tests. The production distribution served by the Go
  `go:embed` harness passes all 8 Chromium flows, including SIWE, personal and
  administrator payment views, exact `personal_sign` parameters, wallet
  session ABA handling, bilingual rendering, and WCAG scans.
- P66-T07 runtime observability: the dispatcher records exactly one bounded
  outcome for every paid request, with operation labels limited to the
  eligible catalog. The PostgreSQL writer snapshot reports current
  `settlement_unknown` rows immediately and unmarked `settling` rows only
  after the same fixed crash-reconciliation delay used by the operator CLI.
  Failed refreshes retain the prior snapshot. Alerts cover facilitator
  unavailability, ledger failures, and stale settling facts without payer,
  payment, transaction, resource, or remote-error labels.
- P66-T07 diagnostics and outage isolation: `doctor` checks the configured
  facilitator `/supported` capability only for runnable API/all roles while
  billing is enabled. Feature-off and non-API roles neither load billing
  Secrets nor call the facilitator. A runtime verify outage returns stable 503
  only on priced routes; free status and billing configuration remain
  available.
- P66-T07 production expiry: billing-enabled deployments register one
  maintenance-role, writer-only supervisor component that immediately and
  periodically expires bounded batches of timed-out `reserved`/`verified`
  payments through chain-scoped `SKIP LOCKED` candidates, the existing ledger
  transaction, and append-only events. Feature-off registers nothing,
  `settling` is never touched, and transient failures retain readiness while
  logging only
  `x402_billing_expiry_failed`.
- P66-T07 deployment: Compose and Helm inject the fingerprint pepper and
  optional facilitator headers only into API/all. Helm requires dedicated
  facilitator CIDRs and API/all-only TCP/443 NetworkPolicies, while separate
  explicit `runtimeHTTPSCIDRs` retain reviewed HTTPS RPC/adapter access for
  sync and worker roles without granting facilitator access. It rejects
  global HTTPS, internet-wide runtime CIDRs, exact facilitator/runtime reuse,
  non-443 facilitator origins, and every `additionalEgress` TCP rule whose
  numeric range could include 443. This closes additive-policy bypasses
  through omitted targets, empty targets, IPv4/IPv6 default routes, implicit
  protocols, named ports, or ranges while retaining explicit non-443 egress.
  The runbook requires CIDR-set overlap review and covers support checks,
  gradual enablement, outage isolation, Secret rotation with drain, and manual
  unknown/crash-window convergence without restoring the discarded response.
- P66-T07 verification: focused app/config/billing/HTTP/observability/CLI unit,
  race, and vet suites pass. PostgreSQL 18 integration verifies the durable
  stale-settling snapshot, partial index, refresh retention, and real operator
  reconciliation. `make test`, `make compose-check`, `make helm-check`, the
  role-scoped Secret and failure render matrix, and `git diff --check` pass.
  Focused app supervisor and production-graph regressions additionally cover
  periodic execution, bounded batches, graceful cancellation, redacted retry,
  feature-off omission, and monolith/split-role parity. PostgreSQL race
  coverage proves one chain's sweeper cannot expire another chain's payment.
  `make security-check`, `make license-check`, `make deployment-check`, and a
  final `make check` pass; writable ephemeral lint and Buildx cache locations
  were used only to satisfy the local sandbox.
- P66-T08 one-shot boundary: `cmd/x402testnet` is absent from ordinary
  `check`/CI and rejects every missing public expectation before opening three
  file-only `0600` Secrets. Its running binary must match the exact clean local
  Git VCS revision; the report labels that fact `harness_revision` and requires
  a separately recorded staging image/build digest. Preflight fixes Base
  Sepolia, checks both free configuration endpoints, and requires exactly one
  reviewed priced route before the payer key is used.
- P66-T08 payment path: the pinned official v2.19 client performs one raw 402
  probe, one wrapper 402 recheck, and at most one signed request through a
  proxy-free, redirect-free, cookie-free, HTTP/1 connection-close transport.
  Both 402 responses require the strict native `payment_required` JSON
  envelope, request ID, Content-Type, identical canonical requirement/resource
  digests, and identical raw challenge header. The final response must be exact
  HTTP 200 with a strict native success envelope and is recorded only as a
  bounded length/SHA-256 digest. Any error after authorization is a stable
  paid-unknown boundary; any error or panic after confirmed settlement is
  `x402_testnet_paid_reconciliation_incomplete`, and neither is retryable.
- P66-T08 reconciliation: the writer verifier requires one exact settled row,
  full method/operation/resource/requirement/network/asset/amount/recipient/
  payer binding, no API-key attribution, and the exact five runtime events.
  Independent Base Sepolia RPC verification binds the returned transaction
  hash to the asset call, zero native value, the current authorization's full
  292-byte EIP-3009 VRS calldata prefix (including nonce and signature),
  successful block/hash receipt, and payer-to-recipient Transfer total. Fixed
  official ABI vectors pin selector encoding, prefix digest, and
  `v=0/1→27/28`; old transfers and another authorization cannot satisfy the
  evidence.
- P66-T08 offline verification: `go test ./internal/x402testnet
  ./cmd/x402testnet -count=1`, `go test -race ./internal/x402testnet
  -count=1`, scoped `go vet`, and the repository `make test` pass. A
  disposable PostgreSQL 18 writer passes
  `go test -race -tags=integration ./internal/x402testnet -run
  TestPostgresLedgerVerifierFenceAndFullBinding -count=1` and the full
  `make test-integration`, proving the real migration/query/event fence.
  These offline results do not substitute for the blocked Base Sepolia
  transaction evidence.
