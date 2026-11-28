# ADR-0061: Sync-owned chain data acquisition

Status: accepted

## Decision

The sync role owns execution-node and Beacon-node connections for the explorer.
A processing role declares a bounded, typed demand for missing inputs in
PostgreSQL. Sync claims the demand under a lease and records the response
against its exact chain and block identity. Consumers independently validate
canonicality, stage generation and publication leases. Retained inputs support
replay during a node outage. Compound reads requiring one endpoint preserve
that boundary throughout collection.

Interactive readers reuse available evidence and wait for missing inputs within
their request budget. Cancelling a waiter leaves shared acquisition intact.
Core head work has a separate worker budget from on-demand reads. Both runtime
layouts use the same durable path. Browser wallets and the standalone payment
facilitator own their existing independent connections.

Stage consumers release their leases while a prerequisite is pending. A retry uses
the same group of endpoint-pinned inputs, including a retained revert or empty
response. Sync records the Beacon header, root-pinned block and, when required,
the execution payload envelope. The consensus and execution-request processors
authenticate those inputs against the indexed execution block before publication.
Blob bodies are stored in the durable archive under ADR-0055.

P10-T08 tracks migration of existing callers, deployment secrets and acceptance.
