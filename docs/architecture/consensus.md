# Protocol projections, consensus and blob archives

[Architecture overview](overview.md). Accepted [ADRs](../decisions/index.md) own the decisions behind these contracts.

This topic owns optional protocol observations and durable archives.
See [operator procedures](../operations/chain-data.md) and
[acceptance requirements](../testing/chain-data.md) for execution and validation.

- [Amsterdam protocol projections](#amsterdam-protocol-projections)
- [Optional consensus observations and blob archives](#optional-consensus-observations-and-blob-archives)

## Amsterdam protocol projections

[ADR-0052](../decisions/ADR-0052-amsterdam-explorer-support.md) defines the
paired execution slot/BAL-hash projection, EIP-7778/EIP-8037 gas accounting and
native_transfer@1. No BAL body or Engine API connection is required. The
native-transfer processor reads authenticated stored receipts and publishes
rows, its journal and its lease generation atomically. PostgreSQL multirange
coverage follows canonical/outbox changes and exact publication state; it never
infers readiness from output rows alone. API reads use the writer snapshot to
avoid replica-lagged publication or reorg visibility. Address and transaction
panels keep protocol ETH transfers separate from traces, fees and withdrawals.

Transaction Logs join the exact published native transfer identity in their read
snapshot and expose EIP-7708 protocol arguments without ABI or trace provenance.
Missing publication preserves raw logs with unavailable decoding. The shared Web
address identity labels `0xfffffffffffffffffffffffffffffffffffffffe` as System
Address in both locales while retaining the address, navigation and copying.

## Optional consensus observations and blob archives

[ADR-0055](../decisions/ADR-0055-consensus-and-blob-archives.md) defines the
optional Beacon boundary. Independent enrich workers publish `consensus@1`
observations and `blob@1` manifests through the same generation/lease protocol
as other stages. Discovery supplements already-published core outbox history.
A leased overview publisher pins the head and state roots and atomically stores
its observation with a runtime invalidation event. PostgreSQL remains the
identity/publication authority; the dedicated blob bucket durably owns S2-compressed
binary bodies with raw-byte manifest identities. Objects are KZG-bound to authenticated transaction hashes and
independently read back before their manifest can publish. No object operation
holds a database snapshot. Reorgs retain immutable content and orphan metadata.

Block models select consensus slots when enabled and published, with explicit
execution-header fallback and `slot_number_source`. Consensus and blob HTTP
metadata reads do not contact upstream nodes. Exact-hash content reads decompress and validate the object, then recheck the
published manifest. V2 `blob.getblob` uses API-key prepaid billing with weight 5.
Anonymous V1 content access is unavailable; the free browser-only content GET
requires SIWE and CSRF, without Turnstile. Origin may be omitted; when supplied
it must match. Sec-Fetch-Site, when supplied, must be `same-origin`. The expanded content details
provide a bottom-right download icon that saves the already loaded bytes.
Preview and file saving share a session-scoped in-memory byte buffer. Core execution readiness and fork
choice never depend on either optional upstream.

Consensus body decoding verifies the complete fork-specific SSZ root before the
execution hash can establish an association. Replay may reuse retained authenticated
observations and manifests, but still rechecks network identity, generation,
lease and canonicality. Existing archived bytes are independently revalidated;
upstream pruning does not revoke intact archived history. Core reorgs rewind
persistent discovery even while the feature is disabled, and a cursor generation
prevents an older scan from overwriting the rewind. Canonical-attach replay keys
are shared with the outbox dispatcher.

### Execution request publications

`requests@1` independently authenticates and publishes the five EIP-7685 request
types when consensus is enabled. Electra/Fulu use the Beacon body; Gloas uses the
root-addressed payload envelope with bid commitment and execution-header checks.
PostgreSQL stores immutable manifests and ordered rows, with canonicality inherited
from the manifest and visibility fenced by stage publication. Discovery, reindex,
reorg replay and runtime parity follow the existing optional consensus lifecycle.
The generated API pages requests within a repeatable-read snapshot. The block
Requests tab loads by execution hash, provides type filters and expandable exact
fields, and loads its bilingual resources with the feature page.
