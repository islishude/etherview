# Runtime and component boundaries

[Architecture overview](overview.md). Accepted [ADRs](../decisions/index.md) own the decisions behind these contracts.

This topic owns component construction, lifecycle and package boundaries.
See [operator procedures](../operations/deployment.md) and
[acceptance requirements](../testing/runtime.md) for execution and validation.

- [Component assembly and lifecycle](#component-assembly-and-lifecycle)
- [Package boundaries](#package-boundaries)
- [Listener and telemetry boundaries](#listener-and-telemetry-boundaries)
- [Multi-instance ownership](#multi-instance-ownership)

## Component assembly and lifecycle

Etherview is a modular monolith packaged as one Go binary. Components are
selected by roles and are instantiated identically whether all roles share one
process or run in separate deployments.

The feature-aware production component manifest is executable architecture:
startup compares it with the exact deduplicated keys registered by the runtime.
The parity suite also proves that `roles=all` is the union of the split-role
graphs, so adding a component without updating both paths fails before serving.
The manifest remains independent in `internal/app/component_manifest.go`;
typed builders in `runtime_shared.go` and `runtime_<role>.go` own registration
for each subsystem and role. `runtime_assembly.go` only carries their explicit
dependencies and invokes them in lifecycle order, while `serve.go` owns shared
resource acquisition and final supervision.

Configuration has the same separation: `config.go` owns the model, defaults,
and YAML load; environment files own role-scoped overrides and secret loading;
validation files own pure global, role, and subsystem checks. The split does
not introduce alternate precedence, defaults, keys, or role-specific fallback.

The same supervisor owns the lifecycle of those registered services in every
deployment shape. It advertises process readiness only after all selected
services have entered `Run`, withdraws readiness before canceling them, treats
an early clean exit as a process failure, and bounds peer draining with
`server.shutdown_timeout`. The operational probe combines that lifecycle state
with PostgreSQL liveness. The API probe combines it with durable core-index
readiness, so startup, failure, and termination cannot serve a stale ready
signal.

The API server binds every accepted request context to that same lifecycle.
Long-lived SSE handlers therefore exit before graceful HTTP shutdown waits for
active connections. Ordinary responses retain the configured write timeout;
SSE clears the idle deadline and reapplies it around each individual write and
flush, preserving both indefinite idle subscriptions and a bounded slow-client
write.

## Package boundaries

Public read errors, cursor encoding, core reader/status interfaces, and atomic
home snapshot state live in transport-independent `internal/publicquery`.
`internal/query` and `internal/state` cannot import `internal/httpapi`;
`internal/query` also cannot import the enrichment worker package. Shared ABI
provenance/confidence, exact verified calldata, proxy/Diamond vocabulary, and
durable stage/replay/enqueue identities live in `internal/abicontract`,
`internal/abicalldata`, `internal/proxycontract`, and
`internal/stagecontract`, and `internal/enscontract`. Worker implementations may alias those contracts,
but readers and producers import the neutral owner. `make golangci-lint`
enforces these import directions, native database API restrictions, and a
uniform 2,500-physical-line limit for hand-written production and test Go files.
`make source-check` enforces SQL file locations and embedded SQL ownership
through ordinary Go tests. `make lint-go` includes both.

## Listener and telemetry boundaries

The public API listener optionally serves TLS from one startup-loaded
certificate/private-key pair as specified by
[ADR-0027](../decisions/ADR-0027-process-native-api-tls.md). TLS is enabled
only when both absolute certificate paths are configured, fails before binding
on invalid material, and never falls back to HTTP. External Ingress
termination remains supported, and an HTTPS `server.public_url` does not by
itself select the listener protocol. The separate operations listener remains
plain HTTP and never receives the API private key.

Each role also runs the same writer-backed PostgreSQL operational metric
collector. It reads only partial-indexed active durable-job, verification,
repair, and x402 stale-settlement facts, excluding unbounded terminal history,
without making metrics a correctness dependency; refresh failure retains the
last snapshot and exposes its age/failure state. Replicas expose the same
chain snapshot, so current gauges—including
`etherview_x402_stale_settling_payments`—are deduplicated with `max`, while
per-process counters such as `etherview_billing_topups_total` and
`etherview_billing_usage_total` aggregate with `sum`/`rate` or
`sum`/`increase`. Optional OTLP/HTTP tracing starts
only with an explicit collector endpoint, propagates W3C trace context through
HTTP, and flushes within the supervisor's bounded shutdown. Collector or
exporter loss never withdraws readiness. Operator response procedures are in
the [operations runbook](../operations.md).

Structured operational events share stable component and event identities.
Durable workers log exact lease-safe job/block context and typed output
summaries only after the matching transition commits; claim/start and per-RPC
detail remain debug-only. Trace and state-difference failures may carry one
exact failing transaction identity, but request logs and adapter diagnostics
retain the existing authentication, billing, URL, and hostile-error redaction
boundaries.

## Multi-instance ownership

Live sync (including backfill conflict recovery), mempool polling, NFT source
discovery and compiler catalog refresh each reserve a PostgreSQL session before
external work. Publications use the same connection as the advisory lock;
connection loss cannot redirect stale results through a healthy pool session.
Maintenance repair/reindex executors likewise use their existing claim session.
Contenders skip polling and retry normally. Locks do not retain transactions
across external requests; ordinary durable worker queues remain independent.

## RPC recovery and API deadlines

Transient transport/timeout failures quarantine an endpoint at startup only when
verified endpoints still satisfy the enabled roles' required purposes. Identity,
configuration and malformed-response failures remain fatal. The supervised
`rpc-recovery` component retries quarantined endpoints after five seconds with
exponential backoff capped at one minute, revalidating chain, genesis and
capabilities before adding them to the pool. A recovered identity mismatch is a
fatal component error. Shutdown cancels probes through the shared context.

The public listener wraps authentication and business handlers in a configurable
20-second execution context. Pool acquisition, SQL and RPC inherit cancellation.
An uncommitted timeout returns `504 request_timeout`; an already committed response
receives no additional error document. Only exact GET event/home SSE routes are
exempt and retain their individual write deadlines. Compiler execution budgets
are independent.
