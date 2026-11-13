# Chain data and optional archives

[Operations index](../operations.md) · Commands run from the repository root.

- [Genesis account state](#genesis-account-state)
- [ENS primary names](#ens-primary-names)
- [ERC-4337 UserOperation indexing](#erc-4337-useroperation-indexing)
- [Amsterdam protocol data](#amsterdam-protocol-data)
- [Optional consensus and blob archives](#optional-consensus-and-blob-archives)
- [Blob content access and compressed archive format](#blob-content-access-and-compressed-archive-format)
- [Incomplete explorer lists](#incomplete-explorer-lists)

## Genesis account state

Normal RPC indexing always stores block zero, but the header does not enumerate
prefunded EOAs or predeploys. To expose those accounts while
`chain.start_block` is zero, configure exactly one server-only source:

- Set the absolute `chain.genesis_file` path (or
  `ETHERVIEW_CHAIN_GENESIS_FILE`) and mount the chain's authoritative standard
  Genesis JSON read-only into the monolith or sync process.
- Set `chain.genesis_url` (or `ETHERVIEW_CHAIN_GENESIS_URL`) to fetch it once
  from a public HTTPS URL. The URL must use port 443 and cannot contain
  credentials, a query, a fragment, or path-traversal segments. Redirects,
  environment proxies, and private or special-use destinations are rejected.

Both sources are capped at 64 MiB and 500,000 allocation entries. A remote
response must use identity encoding and contain valid JSON. Its media type must
be JSON, vendor `+json`, `application/octet-stream`, or `text/plain`.
Optionally set `chain.genesis_sha256` (or
`ETHERVIEW_CHAIN_GENESIS_SHA256`) to the non-zero lowercase SHA-256 digest of
the exact response bytes. The digest is strict; HTTP checksum headers and
sidecar files are ignored. `chain.genesis_fetch_timeout` controls only the
remote bootstrap request, defaults to 60 seconds, and accepts values from one
second through five minutes. Its environment equivalent is
`ETHERVIEW_CHAIN_GENESIS_FETCH_TIMEOUT`.

The local file is read and parsed when the importer is constructed, then waits
for canonical block zero; the remote source is fetched only after canonical
block zero is present and the per-chain lock is held. The importer computes the
Ethereum account trie, block hash, code hashes, and storage roots, then requires
the computed block hash and state root to match the configured/indexed chain.
The import is one PostgreSQL transaction; mismatch, malformed JSON, duplicate
keys, or a write failure exposes no partial account set. Raw storage slots are
used only for root calculation and are not retained. The first successful
import, including a late import, atomically requests one source-deduplicated
block-zero proxy replay; completed restarts do not request it again. Genesis
code does not classify predeploys as tokens without normal token evidence.

Multiple sync replicas serialize an initial remote fetch with a per-chain
PostgreSQL session advisory lock and recheck completion after acquiring it.
Network I/O, checksum verification, and parsing occur outside the import
transaction. Once a complete import exists, restarts read it without contacting
the remote service; an explicitly configured checksum must still match the
persisted document digest. Transport failures, timeouts, HTTP 429, and 5xx
responses expose a stable redacted unavailable state. Other non-success status,
checksum, invalid-content, and chain-identity failures expose a stable redacted
failed state. Neither response bodies, URLs, nor nested network errors are
published.

Without a configured source, `/api/v1/genesis/accounts` returns the typed
`genesis_state` unavailable capability; this must not be interpreted as an
empty allocation. A successfully authenticated Genesis JSON whose `alloc` is
actually empty returns an empty successful page.

For Compose, mount the file through an override and set
`ETHERVIEW_CHAIN_GENESIS_FILE` to the container path, or set the remote URL,
optional checksum, and timeout environment variables without mounting a file.
For Helm, either place the file in a read-only PVC and set
`genesisState.existingClaim`, `genesisState.key`, and
`genesisState.mountPath`, or configure the mutually exclusive remote source
fields `genesisState.url`, `genesisState.sha256`, and
`genesisState.fetchTimeout`. Only monolith/sync Pods receive Genesis source
configuration. The remote source adds no database migration, public API change,
or SPA protocol change.

## ENS primary names

ENS is disabled by default. Under
[ADR-0058](../decisions/ADR-0058-background-ens-synchronization-and-database-only-reads.md),
`enrich` and `all` run identical bounded ENS workers. `api` reads writer-owned
publications without constructing ENS network clients or enqueueing work.
Enable `features.ens` and set `ens.policy_digest` consistently. Mount one public,
reviewed current-chain manifest using `ETHERVIEW_ENS_MANIFEST_DIR` (Compose) or
`ensManifests` (Helm), and set `ens.manifest` to its container path. The
[manifest directory](../../deploy/ens-manifests/README.md) describes its fields.
The source and target chain must both equal the deployment's current chain.
The manifest binds genesis, protocol profiles, exact deployment block hashes,
code/implementation identities, coin type, registry and Universal Resolver.
Source IDs and manifest/policy digests preserve provenance. There is no official/custom
classification or fallback policy. Registration profiles have independent bounded
jobs and freshness; an unresolved or expired name can remain in the directory.
GET `/api/v1/names/{name}` never imports or resolves a name.

Collection reads core canonical blocks, logs and coverage from PostgreSQL. It
never fills core gaps over a separate RPC. Restore missing history through the
core sync/backfill procedures. Its independent cursor advances in bounded
batches and checks attachment timestamps on restart and reorg. Exact state
resolution uses the current-chain RPC pool, one sticky endpoint and block hash,
outside database transactions. The pool and its credentials follow the existing
current-chain role boundaries; there is no dedicated ENS RPC secret or service.
`ens.max_concurrency` bounds resolution lanes independently of other enrich
workers. Collection has an eight-block batch and one-minute round budget;
`ens.request_timeout` bounds each resolution. Configure `ens.gateways` for
reviewed CCIP origins; public-address, redirect and response-size limits remain
mandatory. Restart enrich/all when changing its manifest or gateway settings.

Exact verified results may publish while historical coverage is incomplete.
Directory coverage never proves resolution validity, and a successful resolver
call never proves directory completeness. Historical projections cannot replace
later observations. Relevant later core logs, canonical detachment, explicit
replay and expiry withdraw results; failed refreshes never extend their lifetime.
Primary names require normalized reverse/forward agreement for the same coin type.
Indexed ownership, name registration and forward aliases are distinct facts.

`/api/v1/status` reports a single source with `coverage`, `coverage_start`,
`target`, `scanned`, resolution `pending`, `last_success` and
`last_published_block`. The last publication is not a coverage watermark.
Names and search results carry their observation block and expiration.
Public integers are decimal strings. Unknown candidates return `not_indexed`;
only exact negative observations return `not_found`. Durable `names` events
share `/api/v1/events`. Snapshot retention never extends observation validity.
The [current-chain acceptance report](../reviews/ens-current-chain-acceptance.md)
records representative Mainnet core-to-publication checks under P30-T151. The
retired independent-role evidence remains in Git; full-history gaps remain
explicit operator state.

Explicit recovery commands use only the writer database and the configured
policy. Import files contain one name or address per line (at most 1 MiB and
1,000 candidates); the same batch ID/content is idempotent, changed content is
rejected. Replay returns `through_candidate_id` for the next bounded page and
records a reason. Replay batch IDs are idempotent per source and reject changed
arguments; use a new ID for each subsequent page. Neither command performs resolution inline.

```sh
etherview ens import --config /etc/etherview/config.yaml --source-id 1 --batch-id reviewed-001 --file /work/candidates.txt
etherview ens replay --config /etc/etherview/config.yaml --source-id 1 --batch-id replay-page-001 --after-candidate-id 0 --limit 100 --reason operator-reviewed-retry
```

For exhausted work, fix endpoint/gateway/deployment evidence first, then replay
only the affected bounded range. Worker restarts retain retry budgets and leases;
source cursor resumption and canonical reattachment are automatic. An accepted
finalized-block conflict halts that source: preserve the database and manifest,
independently reconcile its exact hashes, and provision a separately reviewed
source/policy only after resolving the conflict. Replay cannot clear integrity
halts or invent coverage. P30-T150 owns production/local acceptance; P30-T151
real Mainnet acceptance is separate and is not implied by fixture checks.

Name operation history reports its declared start, scanned block, current core
target and pending event associations independently of profile freshness. The
name page keeps canonical records visible while coverage is partial. History
covers reviewed event ABIs, not arbitrary offchain changes or account-wide
permissions. Earlier writes to an unlinked shared record are not operations on
names linked later. An event omitting a value cannot be repaired by reading the
current resolver value.

Historical association repair runs under the existing collector bounds, with at
most eight attempts separated by 30 seconds. Exhausted or unprovable associations
remain explicit pending coverage; restarts do not reset them. Preserve original
log and block identities when diagnosing missing association evidence. Changes
to reviewed ABI manifests follow the fresh-database policy; there is no startup
backfill for an old schema or manifest. Local history tests do not establish
Mainnet completeness.

## ERC-4337 UserOperation indexing

UserOperation browsing is disabled by default. Enable it only with an explicit
EntryPoint registry whose address, wire version, and inclusive active block
range have been verified for the indexed chain. The four currently supported
official singleton addresses are shown below; replace each `from_block` with
that deployment's exact block on the selected chain, and set `to_block` when a
deployment is no longer accepted there:

```yaml
features:
  user_operations: true

erc4337:
  entry_points:
    - address: "0x5FF137D4b0FDCD49DcA30c7CF57E578a026d2789"
      version: "0.6"
      from_block: 123
      to_block: 456
    - address: "0x0000000071727De22E5E9d8BAf0edAc6f37da032"
      version: "0.7"
      from_block: 457
      to_block: 789
    - address: "0x4337084d9e255ff0702461cf8895ce9e3b5ff108"
      version: "0.8"
      from_block: 790
      to_block: 999
    - address: "0x433709009B8330FDa32311DF1C2AFA402eD8D009"
      version: "0.9"
      from_block: 1000
```

The numeric ranges above are placeholders, not mainnet deployment facts.
Entries for the same address must not overlap; at most 16 entries are accepted.
An operator may instead provide the complete array through the bounded JSON
environment value `ETHERVIEW_ERC4337_ENTRY_POINTS`. Configuration is normalized
into a digest that scopes publications, coverage, readers, and cursors, so an
address/version/range change does not expose results produced under the old
registry.

New canonical heads enter `userop@1` through the ordinary enrichment outbox.
Migrations never schedule historical work. After first enablement or a registry
change, record the canonical range and run an explicit bounded reindex, then
wait for continuous UserOperation completeness in `/api/v1/status`:

```sh
etherview reindex --config /etc/etherview/config.yaml \
  --from 12000000 --to 12010000 --stage userop \
  --reason "publish userop@1 for the reviewed EntryPoint registry"
```

Do not use a successful empty list as evidence beyond the published coverage
range. The index intentionally excludes pending Bundler mempools, submission,
simulation, nested EntryPoint calls, and locally reconstructed UserOperation
hashes.

## Amsterdam protocol data

Amsterdam block responses expose exact decimal `slot_number` and
`block_access_list_hash` when present in the authenticated execution header.
Pre-upgrade blocks omit them and cause no BAL RPC or capability probes.
The always-on `native_balance@1` stage fetches BAL only after authenticating a
header with `BlockAccessListHash`. It requests `eth_getBlockAccessListRaw` by
hash and falls back to `eth_getBlockAccessList` only for method-not-found.
Endpoint selection honors pool health. A transport failure or missing body
tries another state endpoint with the same exact hash; transport failures
remain retryable when no endpoint supplies a valid body.
The body must match the header hash and pass protocol validation before atomic
publication of balance changes and block-end values. Empty valid BALs contribute
coverage; unsupported, missing, malformed and pre-BAL bodies do not.

Address summaries and Etherscan native balance methods use published exact
observations or a baseline followed by uninterrupted BAL coverage. Unknown
addresses and coverage gaps trigger an exact EIP-1898 state request; zero values
are persisted too. This is request-driven, not a periodic balance scan. Token
balances, nonce and code retain their existing state paths. Unsupported BAL
methods are cached per endpoint for one minute without disabling state RPC.
After enabling a BAL-capable endpoint, use `reindex --stage native_balance`
for a reviewed canonical range to retry unavailable blocks. Replay and reorg
revoke coverage; retained authenticated bodies can be replayed without RPC.
No Engine JWT is supplied to Etherview.
Block gas utilization uses the maximum of the execution/state dimensions;
execution fees and base-fee burn use
receipt charged gas. The active enrichment versions are stats@4, token@2,
abi@5 and trace@4.

The always-on native_transfer@1 stage indexes only EIP-7708 protocol ETH logs.
Transaction and address native-transfer tabs expose this separate history,
including exact wei amounts and indexed coverage. Fees, withdrawals and
historical trace transfers are excluded. Missing publication returns unavailable;
replay and reorg revoke coverage until the current generation is published.
An operator may explicitly reindex `--stage native_transfer` for a reviewed
canonical range. No startup reindex or legacy data adapter is installed.

See [Preview genesis updates](../../preview/docs/operations.md#genesis-updates).

## Optional consensus and blob archives

Enable `consensus.enabled` to collect Beacon observations through the enrich
role. Configure `ETHERVIEW_CONSENSUS_URL` and optionally a JSON string-map in
`ETHERVIEW_CONSENSUS_HEADERS`; their `_FILE` variants accept mounted secrets.
Only enrich/all reads these credentials. The endpoint may be a controlled
internal HTTP service; redirects are rejected. API instances read PostgreSQL.

`ETHERVIEW_CONSENSUS_ENABLED`, `ETHERVIEW_CONSENSUS_POLL_INTERVAL` (12s),
`ETHERVIEW_CONSENSUS_REQUEST_TIMEOUT` (10s), `ETHERVIEW_CONSENSUS_WORKER_COUNT` (2),
and `ETHERVIEW_CONSENSUS_MAX_RESPONSE_BYTES` (33554432) override YAML.
Network identity is fixed after matching a local execution block; a different
Beacon genesis is an error requiring operator investigation, never automatic
replacement. Header slots remain available when consensus history is missing.
The collector normalizes `SLOT_DURATION_MS` and older `SECONDS_PER_SLOT`
parameters to the same network identity; conflicting values are rejected.
Full Beacon bodies are checked against the pinned SSZ root, including Gloas
progressive containers. Numeric SSZ preset parameters come from the node spec;
absent parameters use the protocol types' mainnet defaults. Custom presets must
provide their overrides. A wrong preset or unsupported structure fails closed.

Execution request collection is enabled by the same consensus switch, with its
own `requests@1` jobs and worker pool using `consensus.worker_count`. No additional
credentials or blob storage are required. Inspect `/api/v1/blocks/{id}/requests`
or the block Requests tab for collection state; the API reads only PostgreSQL.
Gloas requires `GET /eth/v1/beacon/execution_payload_envelopes/{block_id}` at a
fixed root. Pruned or unsupported envelopes report unavailable without blocking
consensus summaries or blob archival. Only a verified complete response with zero
counts means the block contained no requests. Unknown protocol revisions fail
closed; the five supported types include the pinned builder request draft.

Historical discovery also covers requests. Retry a selected range with
`reindex --stage requests` and the existing reason/finalized-range safeguards.
Retained sets are rehashed against the exact execution header before replay, so
intact PostgreSQL history can be republished after envelope pruning. Current
network identity must still be available. A corrupt retained set requires
restoring verified database data; replay never silently overwrites it. Existing
development databases must be recreated for the changed fresh baseline.

Blob archival is independently enabled by `ETHERVIEW_CONSENSUS_BLOBS_ENABLED`.
Provision a dedicated bucket without expiry or cache eviction and configure
`ETHERVIEW_CONSENSUS_BLOBS_ENDPOINT`, `ETHERVIEW_CONSENSUS_BLOBS_BUCKET`,
`ETHERVIEW_CONSENSUS_BLOBS_REGION`, and `ETHERVIEW_CONSENSUS_BLOBS_PATH_STYLE`.
Optional static secrets `ETHERVIEW_CONSENSUS_BLOBS_ACCESS_KEY`,
`ETHERVIEW_CONSENSUS_BLOBS_SECRET_KEY`, and
`ETHERVIEW_CONSENSUS_BLOBS_SESSION_TOKEN` also support `_FILE`; absent keys use
the AWS SDK credential chain. Enrich/all requires conditional PutObject and
GetObject; API requires GetObject only. Compose uses `ETHERVIEW_CONSENSUS_BLOBS_READ_ACCESS_KEY`,
`ETHERVIEW_CONSENSUS_BLOBS_READ_SECRET_KEY` and
`ETHERVIEW_CONSENSUS_BLOBS_READ_SESSION_TOKEN` for the API role; enrich/all
uses the ordinary archive credential inputs. Helm selects separate
`secrets.blobArchiveRead*Key` entries for API and `secrets.blobArchive*Key`
entries for writers. Alternatively, provision workload identities and select
existing names in `blobArchiveServiceAccounts.api`, `.enrich` and `.all`;
these override the trace-cache ServiceAccount only on archive roles. For nonstandard Beacon/S3 ports, set the Helm network policy's
explicit additional egress rules. Never grant archive credentials to sync,
trace, metadata, maintenance or migration processes.

The archive supports standard full-blob retrieval, and the older sidecar API
for Deneb/Electra. Fulu may require a supernode or provider that reconstructs
full blobs. A normal node's missing historical blobs are reported unavailable;
no client-side data-column reconstruction or fabricated empty result is used.
New blocks take priority over bounded historical discovery. Separate persistent
forward and backward cursors also fill blocks indexed while collection was
disabled, without discarding older scan progress. Reorgs also rewind discovery
while disabled; generation checks prevent an in-flight scan from losing that
rewind. Canonical-attach replay identities deduplicate discovery and live dispatch.
Retry a chosen
range using `reindex --stage consensus` or `reindex --stage blob` with the
normal reason and finalized-range safeguards. A 404 does not prove a missed slot.

Back up PostgreSQL and the archive bucket together. Restore the bucket before
serving blob downloads and validate manifests by replaying the selected range.
Retained manifest references are independently read back and checked before
republication; intact objects do not require historical Beacon blob availability.
Retained consensus observations can also be republished without fetching the
historical body, after checking the current node's network identity. Only missing
objects require upstream retrieval. Immutable keys and checksums make retry after
upload/commit failure safe.
Corrupt existing objects are never silently overwritten: restore the exact
object from a known-good backup, or remove the corrupt object under operator
control and replay while the upstream still retains its bytes. An unavailable
upstream cannot recover lost archived history. Unreferenced uploads and orphan
blob objects are retained; there is no automatic archive garbage collection.

`/status` and `/home` expose the last successful consensus observation and its
freshness. Consensus-node or archive outages affect their own collection and
read capability without changing execution finality or core readiness.

## Blob content access and compressed archive format

The archive writes S2 blocks under `blobs/v2/.../<raw-sha256>.s2` using the pinned
klauspost/compress dependency. Database manifests retain original-byte hashes
and sizes; S3 checksums cover encoded bytes. Both database and archive backups
remain required. Preserve read-only API credentials and enrich read/write
credentials, conditional creation and the no-expiry policy.

This fresh-database format has no old `.bin` reader, startup conversion or
backfill. Reinitialize the development database and recollect into the new
namespace when adopting it; do not delete old archives automatically. Confirm
upstream retention before relying on recollection of historical bodies.

External clients use `/v2/api?chainid=<id>&module=blob&action=getblob&blockhash=<hash>&index=<index>`
with an `api:read` key, preferably in `X-API-Key`. The fixed weight is 5, multiplied
by the configured base atomic price. User keys spend their owner's balance;
operator keys and billing-disabled deployments retain free exceptions.

Anonymous blob content access is unavailable. The former browser POST is removed.
The free browser GET `/api/v1/blocks/{hash}/blobs/{index}/content` requires
SIWE and a session-bound CSRF header. Origin may be omitted; when present it
must match the configured origin. Sec-Fetch-Site, when present, must be
`same-origin`. Blob content does not require Turnstile, even
when the global switch is enabled. The expanded details provide a download
icon that saves the cached complete blob without another request or fee.
The browser saves previously fetched content locally without another charge
or token validation. Account/session changes and navigation discard that buffer.

## Incomplete explorer lists

A successful native browsing response can contain partial published history.
Inspect `meta.browsing.state` and its exact snapshot/range before interpreting
an empty list as absence. `coverage_start` and `coverage_end` are omitted for
incomplete browsing ranges. The UI shows a compact notice; worker failures and
missing capabilities remain diagnosable through operational stage/job status.
An `invalid_cursor` after backfill or replay requires restarting pagination.

Browsing coverage and revisions are maintained inside PostgreSQL publication
transactions, identically for all and split roles. They require the current
fresh-database baseline; no startup backfill or legacy readiness path is added.
Existing balances, holder totals, CSV exports and Etherscan reads retain their
strict exact-state/coverage rules. Partial browsing is not evidence that a
release synchronization or payment acceptance gate has passed.
