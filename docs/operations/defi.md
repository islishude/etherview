# DeFi indexing

[Operations index](../operations.md) · [ADR-0060](../decisions/ADR-0060-background-defi-positions.md)

## Enable on the configured chain

DeFi is disabled by default. Supply the same reviewed JSON manifest to API and
enrich (or all) processes, and configure:

```yaml
defi:
  enabled: true
  manifest: /etc/etherview/defi/current.json
```

`ETHERVIEW_DEFI_ENABLED` and `ETHERVIEW_DEFI_MANIFEST` override these settings.
Compose mounts `ETHERVIEW_DEFI_MANIFEST_DIR` at `/etc/etherview/defi`; Helm accepts
JSON documents through `defiManifests` and mounts them into API/enrich/all.
RPC credentials remain in the existing server-side RPC configuration. The API
reads PostgreSQL and does not use RPC or enqueue work for DeFi requests.

The version-1 manifest contains `revision`, decimal-string `chain_id`,
`genesis_hash` and up to 32 `deployments`. Each deployment has a unique lowercase
`id`, `protocol`, `start_block`, `start_hash`, and `contracts`. Each contract has
`role`, `address`, and `code_hash`. Proxies additionally require `implementation`,
`implementation_code_hash` and `implementation_slot`; all three must be supplied.
Aave also requires reviewed `receipt_contracts` entries (up to 1024, each with
`role: receipt` and the same code/proxy identity fields) before a receipt can be
valued. Its receipt proxies can upgrade independently of the Pool proxy; missing
or changed implementations produce unsupported positions.
The optional `pool_code_hashes` list selects reviewed Curve pool accounting
implementations. Unknown Curve code or nonzero V4 hooks returns unsupported. Unreviewed Aave
receipts and Curve pools make discovery partial even when no holder transfers
have been found, so incomplete accounting cannot establish an empty portfolio.

| Adapter    | Required contract roles                    |
| ---------- | ------------------------------------------ |
| uniswap-v2 | factory                                    |
| uniswap-v3 | factory, position_manager                  |
| uniswap-v4 | pool_manager, position_manager, state_view |
| aave-v3    | pool, data_provider                        |
| curve      | metaregistry                               |
| lido       | steth, wsteth, withdrawal_queue            |

Verify addresses, ABI compatibility, deployment block hashes and code identities
against the selected network and pinned upstream sources before enabling them.
No production deployment is bundled or implicitly enabled. The start must cover
all relevant position history; a late start cannot establish complete holdings.
Use earliest relevant protocol activity, including older pool activity for Curve
registry additions. Current root code identity does not certify a historical
upgrade path: only reviewed compatible deployment histories may share a manifest.

## Progress and recovery

The worker first enumerates instruments, then scans canonical Transfer evidence,
then reconciles exact quantities. It persists progress after bounded steps and
resumes unfinished work after restart. Every RPC operation selects the fixed
run block hash on one endpoint. The PostgreSQL lease lasts 60 seconds; each
step has a 40-second deadline and releases snapshots before RPC. Processing is
independent across configured deployments. Completed runs schedule the next
check after 60 seconds; this is not a freshness SLA. Partial snapshots can be
reconciled again at the same block, with at most five publications per block;
this permits recovery from temporary position RPC failures without a new head.
A new block starts a new bounded reconciliation budget.

First scans require continuous Core coverage from the configured start through
the target. Uniswap V2 reuses authenticated immutable factory enumeration;
subsequent runs seed still-active candidates and scan newer transfers. Curve
revisits registry instruments and history because registries can add old pools.
All still-active holdings are reconciled again, including interest and rebases
without owner transfers. Old publications remain labelled with their actual
block while a successor progresses. Core reorgs invalidate affected publications
and cursors, including same-hash reattachment.

The endpoint `/api/v1/addresses/{address}/defi-positions` reports per-deployment
scanning, complete, partial, unavailable and not-configured states. Per-position
RPC failures publish explicit unavailable rows without quantities; they do not
prove a zero balance. Inspect `block_number`, `updated_at`, `coverage_start`,
`coverage_end` and `failure_code`, plus stable-code `DeFi step unavailable` logs.
Existing RPC telemetry records endpoint latency and failures. Publication emits
the existing status event; the browser offers explicit refresh.

Five consecutive source-step failures stop retries for that source. Fix the RPC,
coverage or deployment identity first. To request a new explicit full scan,
increment the manifest `revision` and restart API and enrich with the same file.
The digest creates a new fenced source generation; old source data is retained
but cannot be read through the new configuration. Updating only one role leaves
the new API waiting for its matching source. This replay is incremental background
work, not a startup database backfill. Do not delete orphan or zero observations
to repair a source.

## Acceptance boundary

Adapter and RPC-fixture tests are local correctness evidence. A production
activation additionally needs fixed-revision real-contract all/split tests and
independent reconciliation against the configured deployment at the exact same
block. Record the manifest digest, chain/genesis, image/revision, scan range and
expected versus observed owner/quantities. Representative samples do not establish
full historical coverage. Track outstanding acceptance in P30-T158.
