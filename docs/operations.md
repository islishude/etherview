# Operations Runbook

Start with the [deployment guide](../deploy/README.md) for an external-chain
installation, or [Preview documentation](../preview/docs/README.md) for the
disposable local environment. Commands in these runbooks run from the repository
root. Preview lifecycle, local endpoints, fixtures and tools belong in
`preview/docs/`.

## Choose a procedure

| Task                                                                                   | Runbook                                                          |
| -------------------------------------------------------------------------------------- | ---------------------------------------------------------------- |
| Build and brand an image, initialize the database, configure TLS or split verification | [Deployment and builds](operations/deployment.md)                |
| Check readiness, inspect logs, metrics and traces, diagnose polling ownership          | [Health and observability](operations/observability.md)          |
| Size pools and workers, scale replicas, validate HA or failover                        | [Capacity, HA, and failover](operations/capacity.md)             |
| Repair chain state, replay stages or accept proxy detection                            | [Repair and reindex](operations/recovery.md)                     |
| Import genesis, operate ENS/ERC-4337, enable consensus and blob archives               | [Chain data and optional archives](operations/chain-data.md)     |
| Configure RustFS/S3 trace caches and credentials                                       | [Trace cache storage](operations/trace-cache.md)                 |
| Operate Sourcify, local compilers, source discovery and derived verification           | [Contract verification](operations/verification.md)              |
| Administer keys and sessions, browser push, CSV exports or Turnstile                   | [Accounts and browser services](operations/accounts.md)          |
| Configure prepaid credit, payment options and the standalone facilitator               | [Billing and x402 facilitator](operations/billing.md)            |
| Review compiler proposals or recover dependency-update automation                      | [Dependency and compiler maintenance](operations/maintenance.md) |

See [DeFi indexing](operations/defi.md) for protocol manifests, progress and recovery.

## Operational boundaries

PostgreSQL owns chain identity, canonical history, leases and atomic publication.
Keep `serve --roles=all` and split-role deployments on the same schema and
component contracts. Repair and reindex operations must retain exact block-hash
identity; never substitute a height or `latest` for an unavailable hash.

Logs, Prometheus, OTLP, NATS, Redis and the S3 trace cache are diagnostic or
acceleration surfaces. Their loss does not authorize state transitions or
prevent PostgreSQL-backed fallback. The separate durable blob archive owns raw
blob bodies: its loss affects retrieval and requires recovery, while PostgreSQL
retains manifest and publication authority. See
[archive recovery](operations/chain-data.md#optional-consensus-and-blob-archives).

Keep secrets server-side and role-scoped. Follow the
[architecture](architecture/overview.md) and [accepted ADRs](decisions/index.md)
for correctness boundaries, and the [testing guide](testing.md) for acceptance
commands and evidence limits. Milestone status belongs in the
[plan catalog](plans/index.md).
