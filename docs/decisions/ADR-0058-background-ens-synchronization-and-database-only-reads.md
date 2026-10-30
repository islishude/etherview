# ADR-0058: Current-Chain ENS Indexing and Database-Only Reads

Status: accepted

This decision supersedes ADR-0041 and the external-name provisions of ADR-0011.
ADR-0018 retains writer-authoritative name reads. The current-chain rewrite
replaces this ADR's previous independent ENS role and dual-source design.
P20-T27/T28/T29 own persistence, protocols and publication; P30-T149 owns runtime;
P40-T27/P50-T81 own public reads/display; P30-T150/T151 own local/live acceptance.
Historical reports establish only the implementation and samples they exercised.

## Decision

### One chain and one runtime graph

Core sync is the only owner of block and log acquisition, canonicality and
historical coverage. ENS consumes stored current-chain facts inside `enrich`;
`all` constructs the identical components. There is no standalone `ens` role,
external Mainnet collector, official-first selection or custom fallback.
A deployment configures one manifest and one gateway policy. The manifest binds
the configured chain/genesis, protocol revisions, deployment hashes, code and
implementation identities, roots and coin type. There is no official/custom classification. Source IDs, manifest/policy digests
and verified deployment identities retain provenance without a namespace enum.
All chains use the same manifest identity checks.

Use the current-chain RPC pool for exact-state resolution and code/proxy identity
verification. Never fetch missing historical blocks/logs in ENS. Each operation
pins one endpoint and block hash, with no height/latest fallback. Close database
snapshots before external work and recheck canonical attachment before commit.
Only enrich/all construct ENS clients; API name reads cannot call RPC/gateways.
Bound ENS concurrency separately from other enrichment workers. Recoverable
ENS outages degrade names, not core readiness. Supervisor, readiness, shutdown,
component manifest, deployment and secret scopes remain identical in all/split.

### Core references and derived state

Reference core chain/block/log identities rather than copying raw headers,
log bodies or canonical truth. Keep independent ENS processing cursors,
versioned protocol projections, candidate provenance, bounded jobs, leases,
immutable observations and publication versions. Processing a block does not
mean every name has resolved; acquiring a block does not mean ENS processed it.

Process bounded contiguous batches in transaction/log order. Commit derived
state, candidates and progress atomically. Missing core history remains an
explicit gap; operator imports do not close it. Canonical detach immediately
invalidates dependent reads. Resume with bounded rewind/replay, including after
disabled periods; a same-hash reattachment cannot revive an old worker lease.
Keep orphan evidence and protect active dependencies from maintenance cleanup.
Change the fresh-database baseline directly, without legacy adapters or startup
backfills. Retain validated genesis import provenance without inventing events.

### Protocol completeness within name indexing

Discover normalized plaintext names from verified events, validated reverse
results and bounded explicit operator imports. Hash-only facts never invent a
preimage. Discover address candidates independently of page requests. Forward
names and address primary names are different publications; registration and
ownership never establish a primary label.

Support v1 registries, registrars, controllers, NameWrapper, resolver versions,
record deletion and reverse records. For v2, verify trusted factory/proxy and
implementation identities before decoding standard events. Replay initialization
logs preceding the factory event and records preceding name links. Do not enroll
implementation-only contracts. Unknown implementations invalidate affected
standard projections instead of acquiring trust from matching signatures.

Registry graphs support shared children, multiple paths, bounded depth/fanout
and cycles. Parent-to-child links are authoritative; parent hints supplement
them. Track reservation, registration, renewal, unregistration, transfer and
expiry separately. Preserve token/resource identity through regeneration;
re-registration creates a new lifecycle. Resolver bundles, name links, defaults
and unlinking are separate facts. Shared or ancestor changes invalidate relevant
results before bounded dependent recomputation. Time-based lifecycle transitions
must not wait for an event that will never arrive.

Support premigration, reservations, locked/unlocked and emancipated-subname
migration, WrapperRegistry, v1 routing, ReverseMirror and default reverse records
for the pinned supported deployments. Migration does not require a registrar
registration event. Follow the deployed Universal Resolver entrypoint and retain
ENSIP-15 normalization, matching coin type and reverse-to-forward agreement.
Custom resolvers, wildcard and CCIP-Read use bounded background resolution.
The supported contract revision, code/ABI identities and real-contract fixtures
are explicit; prospective deployments are not evidence of live activation.

Protocol references: [indexing](https://docs.ens.domains/ensv2/indexing/),
[migration](https://docs.ens.domains/ensv2/migration/) and
[reverse resolution](https://docs.ens.domains/ensv2/reverse-resolution/).

### Independent observation validity and directory coverage

An exact resolver observation may publish while historical discovery is behind.
A job pins an already stored canonical block, attachment, manifest/policy and
candidate generation. Publication checks its lease/deadline, immutable block
identity and known invalidations atomically; a newer core head alone does not
invalidate the job. Never require every resolver job to equal the moving scan
target. Exact observations retain their observation block and bounded validity;
they are not claims of exhaustive directory coverage or instantaneous head state.

Event-derived results and RPC-free reuse additionally require complete relevant
history and verified implementation/dependency semantics. Incomplete projections
cannot qualify an observation as event-derived. Older replayed events cannot
replace or repeatedly invalidate a newer exact observation. Changes after the
observation invalidate it and schedule reconciliation; undiscovered changes are
bounded by the observation freshness window. Reorgs always invalidate affected
observations, regardless of scan lag. Definitive negatives require actual proof;
missing rows are unavailable/not_indexed, not not_found.

Retain the existing bounded defaults: 15-minute resolution freshness (Preview:
one minute), 30-minute snapshot retention, 30-second failure delay, eight-second
request timeout, 1 MiB responses, four CCIP levels, 100-address batches and eight
resolution workers. Refresh after half the validity window. Failures never
extend previous validity. Preserve bounded persisted retries and explicit repair
for exhausted work; moving heads must not reset retry budgets indefinitely.
The execution deadline starts at the first claim. Time spent waiting for the
bounded worker pool is reported as backlog and does not exhaust an unattempted
candidate; retries retain the original execution deadline.

Commit observation, job completion, public publication, search catalog version
and names event atomically under lease/generation/canonical fences. Retained
publication versions are immutable. Snapshot retention never extends freshness.

### Public contracts

Address detail, batch names and search read writer-backed read-only snapshots.
They construct no ENS clients and write no ENS candidates, tasks or snapshots.
Authentication/billing writes remain outside this prohibition. Opaque tokens
reference retained publication versions and bind chain and configuration.
Reject expired, invalidated, reorged or pruned snapshots rather than switching
versions during pagination. Unknown dotted search does not enqueue resolution.

OpenAPI owns public interfaces. Report directory coverage, resolution backlog
and most recent successful publication separately. Include observation block and
validity in results; unsafe integers are strings. Keep resolved/not_found/
unavailable and controlled failure codes. Coverage gaps do not hide otherwise
valid exact observations. The SPA uses the generated same-origin client, one
existing SSE stream, atomic visible batches and expiry hiding. Show a compact
incomplete-directory notice. No browser ENS RPC, extra EventSource or list poller.

Coverage catch-up/lag transitions emit `status` with `reason: names_progress`,
not a name invalidation. Source revision/state changes and known dependency
invalidations retain `names` with `invalidated: true`. Publication/directory
`names` events mark changed snapshots without background rereads. True
invalidation, reorg and reconnect withdraw names without starting a request;
invalid snapshots and local expiry still withdraw. Navigation, newly visible
addresses and explicit manual refresh may load names, with each requested batch
replaced atomically. New addresses never renew observations of existing rows.
Refresh failure never extends an observation's deadline.
Unknown or malformed name notifications are treated as invalidations. Scheduler
invalidation of a candidate after known core-log/dependency changes emits one
source invalidation in the same transaction as replacement jobs; ordinary
freshness renewal does not emit a withdrawal.

### Acceptance

P30-T150 requires real pinned contracts and production all/split behavior,
including autonomous discovery/publication before HTTP, partial coverage,
reorgs, restart, expiry, hostile input and database-only API reads. Preserve the
Preview .test genesis; use isolated real-v2 fixtures for protocol lifecycle tests.

P30-T151 separately requires representative Mainnet core ingestion through
background processing, database publication and API readback, reconciled against
an independent RPC at pinned identities. Record image/manifest/block identities,
coverage ranges, provenance of event versus imported candidates and limitations.
Direct resolver success cannot replace publication acceptance. Full historical
coverage is separately reported deployment progress, not a prerequisite for
functional acceptance; neither samples nor imports prove a complete directory.

Avatars/text pages, public resolution submission,
wallet-write name substitution and arbitrary offchain enumeration remain outside
scope. Names are current presentation identities, not historical transaction facts.

### Name registration profiles and directory (P20-T30/P40-T28/P50-T82)

Publish registration profiles independently of forward resolution. Registrant is
the current registration-right holder, including wrapped token ownership, never
a stale registration event or wrapper custodian. Controller describes a proven
single management identity, proven multi-account permissions, unsupported models
or pending evidence; ownership does not imply unrestricted editing rights.
Registrar expiry for wrapped second-level names excludes wrapper grace extension.
Registration lifecycle and observation freshness are separate. Unknown fields
never become zero addresses, empty authoritative records or infinite lifetimes.

Exact background reads pin one verified endpoint and canonical block. Incomplete
event history cannot establish current ownership. Profiles and resolution each
retain immutable observation/publication identity and finite validity, with the
same lease, generation, configuration, dependency and attachment fences. Relevant
transfers, renewals, permissions and migration invalidate and refresh them.

The canonical known-name directory is independent of resolved addresses. Search
includes unresolved and expired names, keyed by normalized name, with summaries
from the same read snapshot. Orphan discovery evidence withdraws a directory
entry; stale profile fields disappear without removing a still-known name.
GET /api/v1/names/{name} is read-only and returns partial known profiles or explicit
not_indexed. Fields distinguish value, pending, not_applicable and unsupported;
Controller additionally supports multi_account. Definitively empty addresses
are not_set. Detail and search preserve independent observation metadata.

The /name/$name route exposes Registrant, Controller, Expiration Time and Resolved
Address. Primary names link to it; reverse/forward agreement remains mandatory.
Use the existing single names SSE stream for notices and withdrawal, manual
refresh for current fields and history, local expiry timers, safe normalized
URLs, bilingual/mobile layouts and independent address navigation/copy actions.
No account-owned-name enumeration or registration management is introduced.

### Name operation history (P20-T31/P40-T29/P50-T83)

History is a separate immutable projection of reviewed contract events, referencing
core logs. It preserves event-time name identity, including v2 registry paths,
token/resource identities and resolver links. Current ownership or a current
resolver link cannot attribute an older event. Global account permissions are
not expanded into all owned names. Namehash preimages may be discovered later
without changing the event's identity. Records written before a name link are
resource history, not operations on that name.

Collection commits history under the existing source revision and canonical
attachment fences. Bounded association repair uses the event's historical
position, never latest state. Orphan facts remain stored but are not publicly
selected. Coverage reports the declared start, scan boundary and unresolved
associations separately from profile validity. Empty incomplete history is not
proof that no operation occurred. Historical events do not expire with profiles.

Read-only name history groups by block and transaction, with separately bounded
event pages. Cursors bind chain, name, policy/manifest, canonical snapshot and
association watermark. Reorgs and historical association changes reject old
traversals; append-only later blocks do not. The API never performs ENS RPC or
writes work. The existing names stream announces history changes. Unknown names
remain not_indexed, and unprovided event values remain unavailable. Text and
binary records are bounded inert display data, never remotely fetched assets.
