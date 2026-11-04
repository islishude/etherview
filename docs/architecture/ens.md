# Background ENS discovery and publication

[Architecture overview](overview.md). Accepted [ADRs](../decisions/index.md) own the decisions behind these contracts.

This topic owns ENS discovery, publication and read validity.
See [operator procedures](../operations/chain-data.md) and
[acceptance requirements](../testing/chain-data.md) for execution and validation.

- [Runtime integration](#runtime-integration)
- [Background ENS persistence foundation](#background-ens-persistence-foundation)
- [Public reads and invalidation](#public-reads-and-invalidation)

## Runtime integration

[ADR-0058](../decisions/ADR-0058-background-ens-synchronization-and-database-only-reads.md)
defines ENS background workers under enrich in this shared component graph.
P20-T27 owns core-backed discovery, resolution, registration profiles and history;
P40-T27 owns database-only reads and P50-T81 owns manual browsing with expiry
withdrawal. P30-T150/P30-T151 retain separate local and representative Mainnet
acceptance; full directory coverage remains deployment progress.

## Background ENS persistence foundation

The current-chain indexer under `internal/enssync` consumes core canonical
blocks, log identities and coverage. `ens_sync_block_refs` and
`ens_sync_event_refs` retain references and attachment evidence; their read
views do not store a second copy of headers, raw logs or canonical truth.
Protocol projections, discovery origins, independent cursors, retry budgets,
leases and observations remain PostgreSQL-owned. A reviewed manifest binds the
current chain/genesis, v1/v2 revisions, deployments, implementations and coin type.

The bounded collector authenticates factories and proxies before replaying
same-transaction initialization logs. Implementations are never instances.
Parent-to-child links determine shared registry paths; parent hints do not.
Registry token/resource lifecycle, reserved names, migration/WrapperRegistry,
record bundles, links, defaults and reverse mirroring remain separate facts.
Depth, cycles, fanout, hostile input and batch work are bounded.

Workers close database snapshots before exact RPC/CCIP calls. Core canonical
attachment, configuration, generation, lease and known invalidation fences are
rechecked on publication. Exact observations can publish before history catches
up; event-only reuse additionally requires complete relevant projections. Old
events cannot invalidate newer exact reads. Relevant later core logs and reorgs
withdraw results independently of the scan cursor. Finite validity, half-period
refresh and no renewal on failure bound unobserved changes.

`enrich` and `all` construct the same workers using the current-chain RPC pool.
Source publication, immutable catalog version and `names` outbox event commit
atomically. API reads are database-only and retain exact snapshot identity.
One configured source retains manifest/policy and deployment identity without
official/custom classification. Unknown is distinct from not found. Independent
profile jobs publish current registration rights, management semantics and protocol
expiry without requiring a successful forward address. Canonical discovery owns
name directory membership, so expired and unresolved names remain searchable.
Name detail and search compose immutable profile and resolution references in
one read-only snapshot; their validity windows are independent. Names SSE and
local expiry withdraw stale fields without manufacturing a new negative.
Coverage transitions emit `status` with `reason: names_progress`; they do not
withdraw valid names. Publication/directory events mark snapshots for manual
refresh. Explicit invalidation, reorg and reconnect withdraw names without
rereading; local expiry still applies. Explicit refresh replaces requested
batches atomically and failed reads never extend observation validity.

Name history references core logs and captures event-position protocol state
under the collector's canonical/source fence. Node identities and event-time v2
registry paths or record links select relevant operations. Resolver permission
resources describe setter arguments, not record IDs, and are not expanded to
names. Historical text values are inert, bounded event evidence. Unknown
preimages do not hide directly matching namehash events. Unlinked record writes
are not retrospectively attributed after a link.

History reads use writer read-only snapshots, group by block/transaction and
page expanded logs separately. Their opaque cursors freeze a canonical block,
source revision and name-association watermark. Newer blocks preserve a page;
reorgs or late associations reject the old traversal. History remains available
when current registration/resolution observations expire. The compact name page
uses the existing stream; `names` with `history=true, invalidated=false` requests
a manual-refresh notice without withdrawing current profile fields.

## Public reads and invalidation

- ENS workers
  consume current-chain core facts under enrich and publish independently valid
  exact observations. The one configured source has no fallback namespace.
  Sticky exact-hash RPC and reverse/forward agreement remain mandatory;
  external calls occur outside PostgreSQL snapshots.

- API detail, batch and search paths read the writer using read-only transactions
  and neutral contracts. They construct no ENS clients and write no candidates,
  jobs, observations or snapshots. Opaque tokens reference retained effective
  publications, including immutable empty snapshots; dependencies, canonicality,
  known invalidations and observation freshness are checked independently of retention.

- Durable `names` events share the existing SSE stream and mark changed snapshots
  for manual refresh. Explicit reads replace visible address batches atomically;
  old responses cannot restore stale labels. Local validity timers hide expired names without a network request.
  Source coverage and resolution status remain separate from core readiness.
