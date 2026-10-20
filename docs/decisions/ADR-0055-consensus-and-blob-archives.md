# ADR-0055: Optional Consensus Enrichment and Durable Blob Archives

Status: accepted

## Decision

Beacon REST observations supplement execution facts; they never drive canonical,
safe or finalized execution state. Consensus and blob archive switches default
off. Blob archival requires consensus and a separate durable object bucket.

The enrich role owns independent consensus/blob workers and bounded discovery.
Genesis/spec and execution-chain identity are checked before binding a Beacon
network to an authenticated local execution hash. Requests pin one Beacon root.
The complete fork-specific body must hash to the pinned header's SSZ body root
before an execution-hash association can bind or publish. Beacon body decoding
uses pinned go-eth2-client protocol types and dynamic-ssz for configurable preset
limits and Gloas progressive SSZ, which geth's Beacon wrapper does not provide.
Geth remains authoritative for execution types, Beacon headers and KZG. Unknown
forks, malformed responses and unmatched execution hashes are not facts.
Time-derived slots locate candidates only. Public slot_number prefers the
associated consensus slot when enabled and otherwise uses the execution header;
slot_number_source identifies the selected source. Raw execution facts remain
immutable. Source disagreement is diagnostic, not an implicit overwrite.

PostgreSQL owns network identity, manifests, jobs, generations, leases and
publication. Consensus and blob outputs use the existing atomic stage publisher.
Orphan observations survive; reattachment and replay are idempotent. A fixed
head/state snapshot supplies checkpoint observations with explicit freshness.
Capability-unavailable outcomes finish through the ordinary lease-fenced terminal
path; they never escape as supervisor failures. Transport failures do not withdraw
execution readiness. Core reorg transactions rewind existing discovery cursors
and advance their generation even while collection is disabled. Scanner progress
uses that generation as a compare-and-set; discovery reuses canonical-attach
replay identities so same-hash reattachments and dispatcher races are idempotent.

Blob objects are a durable exception to ADR-0015's disposable cache contract.
Their dedicated bucket has no expiry policy. The same AWS SDK transport and
credential rules apply, but trace cache objects and archive objects never share
a bucket. Each object binds chain identity, versioned hash and SHA-256. KZG
commitments bind bytes to execution transaction references and Beacon metadata.
Conditional creation never overwrites an existing object. Independent bounded
readback verifies bytes before a fenced database transaction publishes references.
Retained observations and manifests are private replay evidence, not publication
authority. Replays check the current network identity and exact block references,
then independently read and validate retained objects before contacting the
upstream for missing bodies. Intact archives can be republished after historical
Beacon bodies have been pruned. Upload-before-commit failures leave reusable
unpublished objects, not public manifests. Reorganizations do not delete content. Database and archive backups
are both required; pruned upstream data may be impossible to recover.

API processes read metadata from PostgreSQL, close snapshots before object
access, validate object integrity, then recheck publication identity before
returning bytes. Downloads use exact block hashes and same-origin HTTP. Credentials
and object URLs are never public. Enrich has archive read/write permissions;
API has read permissions. Optional-service outages affect only their capability.

## Consequences

Execution-only deployments retain PostgreSQL as their sole required external
service. Enabling archives adds durable storage responsibility, without changing
execution correctness or making optional observations a core readiness condition.
Historical availability is explicit, not inferred from a 404 or a scanned range.
Fulu nodes without full-blob retrieval cannot satisfy archive acquisition; this
feature does not reconstruct PeerDAS data columns.
