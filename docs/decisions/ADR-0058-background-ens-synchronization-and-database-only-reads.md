# ADR-0058: Background ENS Synchronization and Database-Only Reads

Status: accepted

Implementation: P20-T23 supplies the persistence foundation and neutral read
contracts, and P20-T24 supplies bounded official v1/custom collection and
autonomous discovery. P20-T25 adds pinned v2 dynamic discovery and protocol
projections. P20-T26 adds background resolution and effective/catalog publication.
P30-T146 implements the shared ENS runtime and operations; P40-T26 implements
writer-authoritative database-only reads. P50-T80 provides publication-driven
Web display. [P20](../plans/P20-enrichment.md),
[P30](../plans/P30-contract-verification.md), [P40](../plans/P40-api.md), and
[P50](../plans/P50-web.md) retain acceptance status. Local production and real
Mainnet acceptance remain separate gates; P30-T148 is in progress.

This decision supersedes [ADR-0041](ADR-0041-snapshot-stable-ens-primary-names.md).
It also replaces the external-name provisions of
[ADR-0011](ADR-0011-snapshot-search-stats-and-bounded-adapters.md), while retaining
that ADR's general catalog, price, statistics, and housekeeping contracts.
[ADR-0018](ADR-0018-api-read-replica-routing.md) continues to govern database
routing; name reads retain writer authority without request-time name writes.

## Context

Request-triggered ENS resolution makes first reads depend on an RPC or CCIP
gateway, discovers names only when queried, and keeps synchronization work in
the API process. The address-detail path also resolves names, and the batch
path creates database snapshots. Moving only the search resolver would leave
those network and write dependencies intact.

The explorer needs proactive name discovery and independently observable source
coverage. Registration ownership, forward address records, and a verified
address primary name are different facts. Events can establish standard record
state, but arbitrary resolvers and offchain names still require resolution and
cannot all be enumerated. Neither missing rows nor a caught-up event scanner
prove that a particular name has no record.

## Decision

### One background role and two source paths

Add `serve --roles=ens` to the existing binary and shared supervisor. Its
source collectors, projectors, candidate discovery, resolution workers, and
publisher are the same components constructed by `roles=all`. Extend the
independent production component manifest, role validation, readiness,
shutdown, Compose, Helm, and production parity tests together. This is not a
separate service repository or a database shared across unrelated deployments.

PostgreSQL is authoritative for progress, coverage, candidates, jobs, leases,
observations, and publication. Optional notifications only wake durable work.
Only `ens` and `all` load ENS RPC/gateway credentials and construct ENS network
clients. API feature configuration still controls display and routes; it does
not grant network access to the API. Disabling collection issues no source
calls; re-enabling it resumes from durable, canonicality-checked progress.
Recoverable source outages degrade name capability, not core readiness.

- **Official source:** independently verify Mainnet chain ID and genesis,
  retain source headers and exact finalized targets, and scan bounded log
  ranges from the configured deployments through those targets. Store source
  coverage separately from the explored chain's core tables. When exploring
  Mainnet itself, reuse matching, continuously covered core facts where
  available; the same ENS collector fills missing ranges without changing
  source identity or idempotency.
- **Custom source:** consume the explored chain's existing canonical blocks
  and stored logs with an independent ENS cursor. Do not start another core
  synchronizer. A nonzero core start or missing history is an explicit
  coverage gap until core history is supplied or a verified baseline is
  imported. Preview's genesis deployment is an explicit deployment baseline,
  not fabricated registration logs.

A reviewed deployment manifest binds chain/genesis, protocol, contract address,
deployment block/hash, ABI/source revision, and recognized implementation
identity. It includes historical official contracts required for the declared
coverage, not just the latest registrar. Validate dynamically discovered
contracts and proxy implementations before using standard event semantics.
Unknown upgrades invalidate affected projections and enter explicit unsupported
or background-resolution handling; matching an event signature is insufficient.
Manifest changes have a new policy digest and require bounded replay before
their coverage can be advertised. RPC URLs and secrets are not manifest data.

### Protocol projections

ENS v1 and the current Preview deployment track Registry relationships,
registrations/renewals, ownership, relevant NameWrapper changes, resolver
selection, address records, and reverse names. Handle record deletion/version
changes as well as assignment. Preserve registrar ownership separately from
registry control and resolution targets. Registration or transfer alone never
selects an address label.

ENSv2 discovery starts from trusted roots and factories, then follows registry
and resolver links. Backfill pre-link history and earlier logs in a deployment
transaction; exclude implementation-only contracts. Model the registry graph
with multiple parent paths, cycle/depth bounds, registration lifecycles, and
token regeneration without treating each token ID as a new name. Store resolver
record bundles separately from name links and default records. A shared-record
or ancestor change invalidates dependent versions before bounded fanout work
rebuilds them. See the [official indexing specification](https://docs.ens.domains/ensv2/indexing/).

Treat v1 and v2 as versions of the official `ens` namespace, not competing
sources. Follow the deployed resolver routing and migration relationships;
reservation is not ownership, and migration need not emit a registrar
registration event. Preserve existing reverse-record bridging and apply
forward verification through the selected official entrypoint. See
[migration](https://docs.ens.domains/ensv2/migration/) and
[reverse resolution](https://docs.ens.domains/ensv2/reverse-resolution/).

Record names only from validated plaintext events, reverse results, or explicit
candidate imports, checking normalization and the claimed hashes. A hash-only
record remains a hash-only fact; do not guess its preimage. Apply each protocol's
time-dependent semantics using the source block timestamp and scheduled
reconciliation. Expiry, grace, ownership, and resolution validity are distinct;
the absence of a new event does not freeze them forever.

### Candidate discovery and background resolution

Candidate discovery runs independently of HTTP traffic. Sources are ENS events,
the explored chain's indexed addresses (including authenticated genesis,
transaction participants, and contracts), and an operator's explicit bounded
name/address import. The import is idempotent and records candidate provenance;
it never directly grants a public name or ownership. Add an ENS-specific
bounded import/replay CLI boundary, documented as executable only when delivered.
Search, address display, and pagination never enqueue candidates.

Standard event projections may compute forward/reverse records only when the
contract profile and complete relevant history establish their semantics.
Incomplete records, arbitrary resolvers, wildcard resolution, and CCIP-Read use
background Universal Resolver calls. Materialize successful forward results
and primary results separately; primary display requires ENSIP-15 normalization,
the same coin type, reverse-to-forward address agreement, and consistent
resolver provenance. Retain the existing Go normalization/hashing libraries
and the SPA's Viem normalization. Ethereum uses coin type 60; supported other
EVM chains use ENSIP-11. Custom deployments remain visibly `custom_ens`.

Preserve `official_first` and `custom_only`. Only a definitive official negative
or the established unsupported-coin-type policy permits custom fallback.
Unindexed, pending, stale, failed, or unsupported official deployments are not
negative results. `custom_only` constructs no official pool; an empty gateway
allowlist denies every CCIP request.

Jobs bind source, candidate, target block, policy, and requested generation.
Coalesce repeated invalidations; prioritize changed records and local address
candidates while reserving bounded capacity for historical discovery and due
refreshes. Persist retry deadlines, bounded attempt budgets, and terminal
failure reasons. Never repeatedly create fresh jobs to bypass exhaustion;
a new source generation or explicit replay can authorize new work.

Copy bounded inputs and close database snapshots before external calls. Each
operation and CCIP callback stays on one endpoint and exact block hash, never
height or `latest`. Keep existing sender/callback checks, configured HTTPS
gateway intersection, SSRF policy, response/depth/concurrency/time limits, and
redacted typed failures. Callback data is processed by the contract before
publication; a gateway response alone is not a verified address.

### Storage, publication, and recovery

The fresh schema separates these responsibilities:

| Data                 | Identity and purpose                                                                                                                   |
| -------------------- | -------------------------------------------------------------------------------------------------------------------------------------- |
| Sources and coverage | Source chain/genesis, namespace, protocol/deployment policy, target, contiguous scanned ranges, source health and publication progress |
| Events               | Source chain, exact block hash, emitting contract, transaction/log identity, raw facts and canonical/orphan history                    |
| Protocol projections | Registration/control, graph links, resolver records, name preimages and dependency versions                                            |
| Candidates and jobs  | Discovery provenance, lookup direction/coin type, requested generation, lease, retry budget and refresh deadline                       |
| Observations         | Immutable resolution result, exact source blocks/endpoints, dependency versions, observed time and validity                            |
| Publications         | Effective official/custom selection, address-primary and forward views, catalog generation and durable public change event             |

Source chain and explored/target chain are separate identities. Different
offchain observations at the same source block have distinct observation
versions; block identity alone cannot freeze dynamic data. Retain historical
observations without treating them as current display identities.

Lease-fenced publication commits the verified result, effective-name views,
catalog changes, completed job generation, and public change event atomically.
Recheck exact source canonicality, deployment/dependency versions, lease and
requested generation at commit. A stale worker cannot publish after a record
change, migration, policy update, replay request, or lease loss. Ordinary search
cannot bypass the effective-name selection and expose a stale catalog document.

Custom canonical detach immediately makes dependent publications and snapshots
unreadable, retaining orphan facts and rewinding discovery for replay. Recheck
on re-enable even if collection was disabled during the reorg. A conflict in
previously accepted Mainnet finalized identity halts that source's publication
with a typed integrity failure and requires explicit operator repair; do not
silently replace finalized history. These failures do not alter core chain truth.

Housekeeping remains PostgreSQL-only under maintenance, with bounded deletion
and retained-version floors. It cannot remove active task dependencies or
reorgable facts. Protocol history can be compacted only into a validated
finalized baseline sufficient for supported replay; snapshots are not a
substitute for that baseline.

### Freshness and database-only public reads

Retain the current configurable defaults: dynamic resolution freshness 15
minutes (Preview: one minute), snapshot retention 30 minutes, failure delay 30
seconds, request timeout eight seconds, response bound 1 MiB, CCIP depth four,
100 addresses per batch, and eight concurrent resolutions. Schedule dynamic
successes and definitive negatives for refresh after half their freshness
window. These are background deadlines, not HTTP-triggered refreshes.

Standard event-derived results are valid while their dependency versions,
protocol validity, contiguous source coverage, and source health remain valid;
do not poll every unchanged standard name through RPC. Source health also has
a bounded freshness window, using the configured resolution freshness by
default. Refreshing a health timestamp alone cannot make a source current when
its target has advanced past an unprocessed gap. Dynamic results additionally
require their own unexpired successful observation. Dirty or invalidated
identities become pending immediately; failures never extend prior validity.

Existing address detail, `/address-names`, and search routes read only published
database views through neutral query contracts. They neither construct ENS
clients nor write candidates, jobs, observations, or snapshot rows. Existing
authentication, billing, and request accounting are outside this ENS-only write
prohibition. Use read-only transactions on the writer for ENS reads and
ENS-enabled search, retaining immediate canonical/publication checks; a new
read-replica consistency protocol is outside scope.

Keep the address-name states `resolved`, `not_found`, and `unavailable`.
Controlled codes distinguish `not_indexed`, `sync_pending`,
`resolution_pending`, `stale`, `unsupported_source`, and existing typed resolver
failures. A missing row is not `not_found`: require a definitive negative
observation or a complete standard projection that proves absence. Dotted
search uses the same distinction and never resolves or queues an unknown name.
General search filters unavailable/expired name documents while retaining
independently matching non-name sources.

API-generated bounded opaque snapshot tokens reference already retained
immutable publication versions; issuing a token performs no database write.
Bind tokens to chain, policy and exact published identity, and validate them
against database retention and source/dependency validity. Retention is not
freshness: expiry or reorg rejects reuse rather than extending a label's life.
Return bounded validity metadata so the SPA can hide names when their validity
ends even if no server event arrives. Workers alone advance published versions.

Extend the existing status contract with per-source/protocol target, scanned
and published positions, coverage, pending work, last success and failure code.
Separate log coverage from resolution completion. Serialize unsafe public
integers as strings. Add a durable `names` event to the existing SSE stream,
carrying publication identity rather than unbounded address lists; commit it
with publication and preserve replay/version-floor behavior.

The SPA keeps exact addresses visible and uses the generated same-origin
client. A `names` event triggers a fresh snapshot and atomic replacement of
visible-address batches; it never mixes versions. Search pagination never
silently switches versions. Expiry hides labels, while reconnect and view
entry re-read database status. No new browser RPC or gateway calls, separate
EventSource, or periodic list polling are introduced.

## Delivery and acceptance boundaries

Change the current declarative fresh-database baseline. Do not build old-cache
conversion, startup schema/data backfills, dual readers, or request-time RPC
fallback. History synchronization is ordinary bounded resumable worker work,
not a migration or a startup transaction. Replace all three API resolution
paths and request-created snapshots together at the API cutover.

Keep the existing `.test` Preview genesis; use a separate isolated environment
with pinned real v2 contracts for v2 lifecycle and migration acceptance. Local
and production-image fixtures prove implementation, not public network health.

The initial design review found prelaunch wording in the official deployment
documentation. The subsequent 2026-10-29 [live read-only report](../reviews/P30-ens-mainnet-acceptance.md)
verified the public Universal Resolver proxy chain, `isENSv2()`, root registry,
actual registrations and representative records at finalized block 26297715.
Pin and independently verify chain state rather than inferring activation or
migration from documentation or deployed code. P30-T148 retains that positive
activation evidence separately from its remaining full historical coverage and
database-publication reconciliation gate; bounded live samples do not close it.

This change does not add ownership/registration browsing, avatars/text-record
pages, a public resolution-submission API, a third-party indexing authority,
or implicit names in wallet write forms. It cannot enumerate every possible
offchain/wildcard name. ENS names remain presentation identities rather than
historical transaction facts, and Etherscan compatibility responses are unchanged.

## Consequences

- Newly registered names can be discovered and published without a page visit.
- ENS read latency and availability are separated from upstream resolution;
  the cost moves into explicit background capacity and coverage management.
- Strictly read-only search cannot discover an unknown offchain name by itself;
  users see a controlled unindexed state until background discovery or an
  operator import supplies a candidate.
- Overdue or invalidated labels disappear instead of continuing as current
  identities. Monitoring must distinguish healthy source polling from a
  growing parsing, fanout, or resolution backlog.
- The role and protocol expansion requires real all/split, reorg, restart,
  malicious-input and publication-race acceptance before implementation is done.
