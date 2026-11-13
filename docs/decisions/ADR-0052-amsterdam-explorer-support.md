# ADR-0052: Amsterdam Explorer Support

Status: accepted

## Decision

The selected Geth protocol types remain authoritative. Amsterdam is identified
by the paired slot number and block access list hash in the authenticated header;
a partial pair is invalid. Slot zero is present, not absent. BAL fetching is permitted only after an authenticated header has a non-nil
`BlockAccessListHash`; pre-fork blocks perform no BAL request or capability
probe. No Engine API credentials are added.

Before Amsterdam, receipt cumulative gas equals header gas. Amsterdam uses
EIP-7778 refund accounting and EIP-8037 execution/state dimensions: header gas
is max(total execution gas, total state gas), while receipts charge their combined
net amount. Receipt gas can therefore be either above or below header gas, but
cannot exceed twice header gas. This bound is checked without uint64 overflow. Receipt
cumulative deltas, transaction limits, receipt root, bloom and header gas limit
remain checked. This is ingestion validation, not independent EVM re-execution.
Statistics use header gas for utilization and receipt gas for execution fee and
base-fee burn. The current stage becomes stats@4.

Only authenticated post-Amsterdam receipt logs with the EIP-7708 system emitter,
exact Transfer topic and canonical address/value layout are protocol ETH
transfers. Token@2 excludes them. ABI@5 excludes them from contract ABI provenance; the native transfer index supplies their protocol decoding.
Transaction Logs project protocol decoding from the exact published native transfer
row in the same read snapshot. The public decoding has `protocol: eip7708` and
`attribution.mode: protocol`, without contract ABI source or execution identity.
Unpublished protocol transfers remain unavailable without ABI fallback; malformed
protocol logs or published rows inconsistent with receipts fail as corrupt data.
The Web labels the exact system address independently of stage availability.

Trace@4 distinguishes protocol logs from EVM logs: ordinary logs retain exact
matching and provenance checks; protocol logs never acquire a fabricated ABI
execution identity. Existing trace browsing remains independent.

Native_transfer@1 publishes exact block/transaction/log identities and wei
amounts from stored receipts, with no external RPC. Output, canonicality journal,
stage result and lease completion share the existing publication transaction.
Orphan facts are retained and replay is idempotent. PostgreSQL is authoritative.

Native transfer APIs use canonical snapshot-bound keyset pages and the existing
page limits. Missing stage coverage is pending/unavailable, never an empty
success. Coverage is explicitly limited to protocol logs after Amsterdam; old
transactions and traces are not synthesized into this index. New transaction
and address panels do not merge these rows with traces or top-level transaction
value. Notification, CSV and Etherscan transfer contracts are unchanged.

## References

- [Raw RPC ownership](ADR-0022-go-ethereum-type-and-raw-rpc-ownership.md)
- [Lease-fenced publication](ADR-0012-lease-fenced-derived-publication.md)
- [EIP-8037](https://eips.ethereum.org/EIPS/eip-8037)
- [EIP-7778](https://eips.ethereum.org/EIPS/eip-7778)
- [EIP-7708](https://eips.ethereum.org/EIPS/eip-7708)

## Consequences

Fresh-schema migrations and generated query/API outputs implement these
contracts. All roles use identical processors and persistence. Preview activation
is tested using new disposable volumes without resetting existing operator data.
Local fixtures do not establish live network or remote CI acceptance.

## Native balance publication

`native_balance@1` fetches by exact hash through ordinary execution RPC, preferring
`eth_getBlockAccessListRaw` and using `eth_getBlockAccessList` only when the raw
method is unsupported. Geth BAL types validate canonical encoding, limits and
the header commitment. Raw bodies, indexed post-balances and block-end
observations commit with the existing lease/generation publication fence.
Not-applicable blocks never establish BAL coverage or endpoint incapability.

Publication-aware browsing coverage tracks this stage independently. A known
canonical balance may carry forward only through continuously published BAL
blocks. Exact EIP-1898 RPC observations (including zero) and authenticated
Genesis allocations can seed balances; absence is never zero. Conflicting
observations fail closed. Reorgs retain orphan facts and withdraw publications.
Address and Etherscan reads share this index; nonce/code and token state remain
separate. There is no periodic account scan or new history API.

Preview uses Reth v2.9.0 with Lighthouse for the primary 48815 PoS chain; the
31337 Reth payment fixture remains independent. Existing Geth databases are not
converted or automatically erased. Fresh isolated volumes establish acceptance.
