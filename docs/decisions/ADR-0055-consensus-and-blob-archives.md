# ADR-0055: Optional Consensus Enrichment and Durable Blob Archives

Status: accepted

## Decision

Beacon REST observations supplement execution facts; they never drive canonical,
safe or finalized execution state. Consensus and blob archive switches default
off. Blob archival requires consensus and a separate durable object bucket.

The enrich role owns independent workers for consensus, execution requests and
blobs, together with bounded discovery.
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
publication. All three outputs use the existing atomic stage publisher.
Orphan observations survive; reattachment and replay are idempotent. A fixed
head/state snapshot supplies checkpoint observations with explicit freshness.
Capability-unavailable outcomes finish through the ordinary lease-fenced terminal
path; they never escape as supervisor failures. Transport failures do not withdraw
execution readiness. Core reorg transactions rewind existing discovery cursors
and advance their generation even while collection is disabled. Scanner progress
uses that generation as a compare-and-set; discovery reuses canonical-attach
replay identities so same-hash reattachments and dispatcher races are idempotent.

Blob objects are a durable exception to ADR-0015's disposable cache contract.
Their dedicated bucket has no expiry policy. Bodies use klauspost/compress/s2
block encoding with EncodeBetter under `blobs/v2/<chain-namespace>/<versioned-hash>/<raw-sha256>.s2`.
The manifest SHA-256 and size describe the original 131072-byte blob; the S3
upload checksum describes the encoded object. Reads bound encoded size by
`s2.MaxEncodedLen(131072)`, require the declared decoded length to equal 131072,
and verify the decoded length and raw SHA-256 before returning content.
There is no legacy raw-object fallback or automatic conversion. The same AWS SDK transport and
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
returning bytes. Content acquisition uses exact block hashes. Anonymous V1 content
access is unavailable. The free browser-only GET at
`/api/v1/blocks/{hash}/blobs/{index}/content` requires a writer-authoritative
SIWE session and a session-bound CSRF header. Origin may be omitted for
same-origin GETs; if supplied it must match the configured origin.
Sec-Fetch-Site, when supplied, must be `same-origin`. The former POST is removed. Turnstile is not required, even when
globally enabled. API keys cannot replace sessions.
The browser retains content only in session-scoped component memory and reuses
it for preview and local file saving through a download icon at the bottom right
of the expanded content details; changing identity or leaving the blob
page clears it. Responses are no-store, including failures.
API clients use the `blob.getblob` V2 extension governed by ADR-0044, returning
raw bytes as a 0x-prefixed hex result after the same integrity/publication checks. Credentials
and object URLs are never public. Enrich has archive read/write permissions;
API has read permissions. Optional-service outages affect only their capability.

## Consequences

Execution-only deployments retain PostgreSQL as their sole required external
service. Enabling archives adds durable storage responsibility, without changing
execution correctness or making optional observations a core readiness condition.
Historical availability is explicit, not inferred from a 404 or a scanned range.
Fulu nodes without full-blob retrieval cannot satisfy archive acquisition; this
feature does not reconstruct PeerDAS data columns.

## Local Preview topology

Preview enables both optional capabilities against an isolated Geth/Lighthouse
PoS network. Lighthouse Beacon and validator clients use the same pinned
version and a paired EL/CL genesis bundle; Geth dev mining is not used. Generated
JWT/validator material stays outside tracked sources and application mounts.
Recreation preserves chain identity and slashing protection, and changed or
incomplete material requires explicit destruction/reinitialization.

The Preview RustFS bucket is durable across container recreation and separate
from disposable caches. Idempotent provisioning grants API read-only access and
enrich write/read access; the archive client uses conditional creates.
RustFS 1.0.4 still does not enforce the conditional header through IAM, so storage-side
enforcement remains deferred pending evaluation of 1.0.5 with the upstream policy
fix and real-container rejection of unconditional writes and overwrites. See the
[operator limitation](../../preview/docs/operations.md#preview-archive-iam-limitation) for policy
key details and the release acceptance boundary. Administrator credentials stay
in the storage and initialization services.
The one-shot provisioner is outside application startup dependencies; lifecycle
commands run it explicitly and propagate failures without gating core readiness.
Public APIs and production defaults are
unchanged. Acceptance uses real blob transactions and independent object readback,
not simulated Beacon responses.

## Execution requests

Consensus enablement also runs an independent `requests@1` enrich worker pool.
Its failure does not prevent consensus slot or blob publication. Network binding,
root-pinned full-body authentication, execution-hash association and atomic
lease/generation publication use the same boundaries as consensus observations.
Electra/Fulu requests come from the authenticated body's `execution_requests`.
Gloas requests come from the signed payload envelope at the exact Beacon root,
with parent root, builder and execution identities checked against the body bid.
The progressive SSZ requests root must match that authenticated bid; the body's
`parent_execution_requests` must never be attributed to the current execution block.

The accepted record types are validator deposits, withdrawals/exits,
consolidations, builder deposits and builder exits ([EIP-7685](https://eips.ethereum.org/EIPS/eip-7685),
EIP-6110, EIP-7002, EIP-7251 and the pinned
[EIP-8282](https://eips.ethereum.org/EIPS/eip-8282)/Gloas protocol shape). Fixed-width
records use their protocol field order and little-endian uint64 encoding.
Type groups are ordered from 0x00 through 0x04 and preserve each list's order.
Geth's requests hash must match the indexed execution header before publication.
Unknown types/forks, malformed records and inconsistent commitments fail closed.
Response-byte limits, protocol/preset counts and a local ceiling of 8192 records
per type bound decoding and persistence work. Collection records inclusion;
it does not claim successful consensus state-transition processing.

PostgreSQL retains an immutable request-set manifest and ordered request rows.
The manifest owns canonicality and the durable job/generation publication fence;
its rows inherit that authority. Retained sets survive reorgs and are revalidated
against the exact execution header before replay. API reads use a single database
snapshot and never call upstream services. Page cursors bind chain, execution hash,
filter and publication identity. Missing history is unavailable, not an empty set.
A verified empty set is complete; blocks without a requests commitment are not applicable.
