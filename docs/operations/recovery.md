# Repair and reindex

[Operations index](../operations.md) · Commands run from the repository root.

- [Repair and reindex](#repair-and-reindex)

## Repair and reindex

Before scheduling work, record the exact chain, inclusive block range, current
canonical hashes, finalized height, and an operator reason. Never use repair as
fork choice.

```sh
etherview repair --config /etc/etherview/config.yaml \
  --from 12000000 --to 12000010 --stage core \
  --reason "replace incomplete receipts from validated history provider"

etherview reindex --config /etc/etherview/config.yaml \
  --from 12000000 --to 12000010 --stage token \
  --reason "rebuild token facts after core repair"

etherview reindex --config /etc/etherview/config.yaml \
  --from 0 --to 12000010 --stage proxy \
  --reason "publish proxy v2 after the OpenZeppelin schema cutover"

etherview reindex --config /etc/etherview/config.yaml \
  --from 0 --to 12000010 --stage abi \
  --reason "publish ABI v5 after trace v4 and proxy v2 are complete"

etherview reindex --config /etc/etherview/config.yaml \
  --from 0 --to 12000010 --stage trace \
  --reason "publish trace v4 execution identities and constructor decoding"

etherview reindex --config /etc/etherview/config.yaml \
  --from 0 --to 12000010 --stage state_diff \
  --reason "publish state_diff v3 complete-prestate execution identities"

etherview reindex --config /etc/etherview/config.yaml \
  --from 12000000 --to 12000010 --stage userop \
  --reason "publish userop@1 for the reviewed EntryPoint registry"

etherview admin repair list --config /etc/etherview/config.yaml --limit 100 --format table
```

`repair --stage core` re-fetches through the history-purpose RPC path. The
database rechecks chain, height, canonical hash, parent, and finality while
holding the chain lock. It cannot move canonicality or checkpoints. A range at
or below finalized height requires `--allow-finalized` plus the recorded
reason; this permits only a same-identity refresh.

`reindex --stage requests|consensus|blob|native_transfer|proxy|abi|token|stats|trace|state_diff|userop|holder` queues work for
the currently canonical block hash. It does not steal queued work or an active
lease. Repair deliberately does not infer a downstream rebuild range; schedule
each required derived stage explicitly and wait for its durable publication
result. After the
OpenZeppelin proxy cutover, schedule `proxy` before `abi`; the ABI worker also
refuses to claim a block until the current `proxy@2` result is published.
`holder@1` also waits for the exact block's `token@2` result and terminal
`proxy@2` result. Authoritative holder reads require a bounded sequence of
Holder reindex requests starting at block zero and continuing through the
current canonical tip; neither migration nor startup schedules this history.
Each request must retain its operator reason. The enrich role uses its
state-purpose endpoint for exact EIP-1898 `balanceOf` and `totalSupply` calls;
missing historical state leaves the affected Holder block unavailable rather
than publishing event-derived balances.
The `trace@4` cutover is an explicit bounded reindex, never a migration-time
historical enqueue. Each completed Trace generation requests the existing
`proxy@2` replay and then `abi@5`; wait for those publications before treating
the range as proxy-interaction complete. Nodes that reject `withLog` with
`-32602` still publish the call tree, but their logs remain visibly on the
conservative address fallback path.

The baseline includes transaction execution-code rows and the current
`state_diff@3` witness. It creates no historical coverage or replay workload.
Run `reindex --stage state_diff` for the chosen canonical range and
wait for `state_diff@3`. Its completion requests `trace@4`; after Trace
publishes, wait for `proxy@2` and then `abi@5`, then rebuild and verify the
affected coverage range. Operators must not substitute block-end or `latest`
code for unavailable transaction prestate.

The baseline also includes partitioned ABI-owned effective execution identities
for `abi@5`; initialization never backfills or queues history.
For a bounded historical or Preview range, first ensure its canonical
`state_diff@3` and `trace@4` publications exist, replay `proxy@2`, then run
`reindex --stage abi` and wait for `abi@5`. The replay uses only stored exact
block evidence. Do not derive an identity from block-end state, `latest`, or
the last delegation observed for an address in the block.

### Proxy detection deployment acceptance

Standard and Solady legacy CWIA detection always run. Published evidence is
returned as `proxy_detection`; there is no separate collection or visibility
switch. Safe and Diamond remain independent, default-off capabilities:

```yaml
features:
  safe_proxy_detection: true
  diamond_proxy_detection: true
```

Use `ETHERVIEW_FEATURE_SAFE_PROXY_DETECTION` and
`ETHERVIEW_FEATURE_DIAMOND_PROXY_DETECTION` for equivalent environment overrides.
Apply the selected family settings consistently to all enrichment processes.

The unified schema and binary are a fresh-baseline cutover. Recreate older
development databases using [fresh database initialization](deployment.md#fresh-database-initialization)
and remove retired configuration keys; strict YAML rejects unknown fields.
Update external clients to `proxy_detection`. There are no wire aliases,
startup conversions or automatic historical scans.

Before serving a new revision to users, run an isolated acceptance deployment
with the exact image, schema and intended detector configuration. Record its
chain and a finalized range of at most 10,001 blocks, including known positives
for each enabled family and 100 deterministic negatives (or the whole smaller
population). Use the
[deployment review template](../reviews/proxy-detection-deployment-review-template.md).
Existing regression/CI results do not establish live-chain acceptance.

For an explicitly approved bounded historical sample, record canonical boundary
hashes and the operator reason, then replay Proxy before ABI:

```sh
etherview reindex --config /etc/etherview/config.yaml \
  --from "$FROM_BLOCK" --to "$TO_BLOCK" --stage proxy \
  --reason "proxy detection deployment acceptance" --allow-finalized
```

Reindex `abi` only after the exact Proxy generations have published. The command
resolves heights to exact hashes; generation fences make retries idempotent.
Replaying older history can establish new authoritative CWIA coverage, so the
range and that effect must be part of the operator's explicit backfill approval.
Migrations and startup never enqueue that work.

Require complete current publications and independently read back canonical
standard/CWIA relations, exact bindings, detector evidence and Diamond Loupe/Cut
maps. Review every unknown, inconsistent and conflicting outcome. Check Safe
shell/slot-0/official-singleton distinctions; use the maintained fixed-mainnet
identities for official provenance claims. Bulk acceptance does not cover
interface-only deep Safe candidates. Confirm that Safe cannot authorize ordinary
implementation writes and that Diamond calls target the Diamond with selector
and code identity checks. Public responses must contain the expected evidence
without changing existing binding and management eligibility.

Compare `etherview_proxy_detection_duration_ms`, RPC call/error counters,
result/conflict counters, Safe counters, allocation/GC and database write volume
against the prior revision for the same sample. The standard RPC path is now
included in the shared context counters; additional summary rows are expected.
No unexplained unknowns, publication gaps or authority changes qualify as a pass.

Disabling `safe_proxy_detection` stops new Safe detection. Disabling
`diamond_proxy_detection` stops new Loupe work and DiamondCut indexing. Neither
removes historical facts or hides already published evidence. There is no global
hide-results or old-detector fallback: withdraw a failing revision from service
and deploy a corrected matching binary/schema. Do not point an older binary at
the new baseline. Keep controlled exports for diagnosis and preserve orphan facts.

The list command is newest-first and bounded to 1–1000 rows. Its default JSON
output and optional `--format table` both report `failure_present` without
returning stored nested error text. Use the stable
maintenance log code and metrics to choose the next investigation, then inspect
PostgreSQL under the deployment's controlled operator-access policy if deeper
forensics are required.

The maintenance role also owns feature-gated PostgreSQL housekeeping. When
wallet authentication is enabled it deletes at most 1,000 expired challenges
and at most 1,000 expired or revoked sessions per sweep. When prepaid billing
is enabled it expires bounded legacy/top-up payment leases, releases expired
usage reservations, and expires untouched top-up intents without changing a
committed debit. All sweeps are chain-scoped, select
candidate rows with `SKIP LOCKED`, use the writer, run once at startup and then
on `maintenance.interval`, and stop with the shared supervisor. A transient
failure retains readiness, logs only
`user_auth_cleanup_failed`, `x402_billing_expiry_failed`, or
`prepaid_billing_expiry_failed`, and retries with
bounded backoff. Disabling a feature registers no corresponding housekeeper.
The split maintenance role must not receive session, fingerprint, or
facilitator-header Secrets.

## Temporarily unreachable execution endpoints

At least one verified endpoint must satisfy each purpose required by enabled
roles. An unreachable backup may start quarantined; `rpc-recovery` retries with
5-second to 1-minute backoff. It logs endpoint names and stable errors, never URLs
or credentials. A successful probe revalidates chain/genesis and capabilities
before reuse. Identity/configuration/invalid-response errors fail startup or the
recovery component; fix the configured endpoint before restarting. Recovery uses
the same supervisor and cancellation path in all and split deployments.
