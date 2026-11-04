# Contract verification

[Operations index](../operations.md) · Commands run from the repository root.

- [Sourcify operations](#sourcify-operations)
- [Factory-derived verification rollout and backfill](#factory-derived-verification-rollout-and-backfill)
- [Application-locked Vyper runtimes](#application-locked-vyper-runtimes)
- [Automatic bytecode database source discovery](#automatic-bytecode-database-source-discovery)
- [Browser contract verification](#browser-contract-verification)

## Sourcify operations

Use a supported public chain ID and contract address. Submit compiler
metadata and source files to `POST /api/v1/verifier/sourcify` with an `X-API-Key`
holding `contract:verify`, then poll `GET /api/v1/verifier/jobs/{id}`. Check
`outcome.kind` for `sourcify_success`; job completion alone does not mean
verification succeeded. Sourcify results remain external verification results.

For `POST /api/v1/verifier/sourcify/from-etherscan`, send `chain_id`, `address`
and optionally `etherscan_api_key` in the JSON body. `X-API-Key` is the separate
Etherview API Key; the optional body field is forwarded as `apiKey` to the
configured Sourcify service for that import. Omitting it leaves credential
selection to that service, which may reject an import without an available
Etherscan key. The Web form explains external publication and credential
sharing before confirmation and clears the Etherscan field after submission.
Do not place either key in URLs, shell history, diagnostic payload dumps or
request-body logging at reverse proxies.

The API encrypts Etherscan credentials before PostgreSQL enqueue using
purpose-separated keys derived from `security.api_key_pepper`; database rows
and backups contain ciphertext only, and job reads never return it. Use the
same pepper on every API, verification and all-role replica. Before rotating it, stop accepting
new imports and drain pending/running Sourcify imports that carry credentials.
Restoring those jobs requires the original pepper from a separately secured
secret backup. A missing or mismatched pepper fails decryption; it never falls
back to plaintext or an unauthenticated import. Rotation does not re-encrypt
historical ciphertext. Normal no-key imports need no credential decryption.

To import an already verified contract into Etherview, POST `{"chain_id":"1"}`
with an Etherview `X-API-Key` scoped to `contract:verify` to
`/api/v1/contracts/{address}/verification/sourcify`. Both public verification and
Sourcify must be enabled. `chain_id` selects the Sourcify source chain as a positive
decimal uint64 string; it defaults to the deployment's chain in the Web form.
It may differ from the local chain, but the same address must have identical
runtime bytecode on both chains. The local target chain always comes from the
deployment; the address needs canonical local runtime and creation facts (or
authenticated Genesis provenance).
The API queues a Sourcify import task before remote retrieval; use its ID to
poll at `/api/v1/verifier/jobs/{id}`. Only `verification_success` establishes
local verification and publishes the source/ABI. `sourcify_success` belongs to
the separate external publication workflow. No Etherscan key, file upload or
external publication consent is needed for direct retrieval. A missing upstream
contract returns `sourcify_not_found`; mismatched runtime returns
`sourcify_target_mismatch`; unsupported/invalid input returns
`sourcify_invalid_source`. Unavailable upstreams return `sourcify_unavailable`
without their diagnostic body. The Web offers direct import first and a
separate local import action after external publication.

Sourcify rejects DNS answers containing private, loopback or
special-purpose addresses by default. For local Fake-IP development, set
`sourcify.unsafe_allow_private_networks: true` or
`ETHERVIEW_SOURCIFY_UNSAFE_ALLOW_PRIVATE_NETWORKS=true`. The switch affects only
Sourcify and defaults to `false`. HTTPS, certificate
verification, checked-IP dialing, redirect rejection and request limits remain
enforced; environment HTTP proxies remain disabled. It does not enable automatic
source publication or change the separate metadata, bytecode database or compiler
download policies.

## Factory-derived verification rollout and backfill

Factory-derived verification consumes only PostgreSQL-persisted authenticated
compilation units, canonical `trace@4` CREATE/CREATE2 frames, and canonical code
observations. It does not require archive/debug RPC and never inherits a
submitter's Sourcify consent. Roll out in this order on every `all`/`verification`
replica:

1. Initialize the current baseline with all derived flags false. Successful
   verification starts retaining new authenticated compilation units.
2. Set `ETHERVIEW_DERIVED_VERIFY_ENABLED=true` only. Workers record bounded dry-run
   attempts but do not publish child verification.
3. Review `etherview_derived_verification_total{kind,result}` for unexpected
   ambiguity, runtime mismatch, stale evidence, or backlog growth, then set
   `ETHERVIEW_DERIVED_VERIFY_BACKFILL_ENABLED=true` to publish historical unique matches.
4. Set `ETHERVIEW_DERIVED_VERIFY_FORWARD_ENABLED=true` only after backfill is enabled.
   The post-`trace@4` dispatcher then schedules future and transitive work
   without running the matcher in the trace transaction.

The baseline creates the forward queue with immutable `trace@4`/`proxy@2`
publication-generation events and durable rescan floors. When changing this
queue protocol, stop or replace every `all`/`verification` replica as one rollout; do
not mix worker versions with incompatible queue semantics. Initialization and
startup do not queue failed scans or `pending_runtime` attempts and do not
create a historical workload.

Before recovering an upgraded deployment, record redacted aggregate
state from an operator database session. Preserve the result with the rollout
evidence; do not include connection strings, addresses, hashes, compilation
IDs, or other row-level data:

```sql
SELECT status, count(*) FROM derived_verification_scans
GROUP BY status ORDER BY status;
SELECT count(*) AS scan_count FROM derived_verification_scans;
SELECT status, count(*) FROM derived_verification_attempts
GROUP BY status ORDER BY status;
SELECT status, count(*) FROM derived_verification_forward_blocks
GROUP BY status ORDER BY status;
```

After all new replicas are healthy, explicitly recover with the
existing audited command; migration startup must not substitute for it:

```sh
etherview admin derived-verification backfill \
  --config /etc/etherview/config.yaml \
  --reason "derived verification queue hardening recovery"
```

Then observe only the closed-label
`etherview_derived_verification_total{kind,result}` series. In particular,
review `dispatch=trace_generation|proxy_generation`,
`rewind=trace|pending_runtime`, and `lease=renewed|lost` together with the
aggregate scan/attempt/forward-event counts. Never add addresses, hashes, or
compilation IDs as metric labels. Production defaults remain disabled.

Worker and scan bounds are controlled by `ETHERVIEW_DERIVED_VERIFY_WORKER_COUNT` and
`ETHERVIEW_DERIVED_VERIFY_MAX_TRACES_PER_SCAN`. To request an audited idempotent rescan of
all eligible compilation epochs, or one factory only, use:

```sh
etherview admin derived-verification backfill \
  --config /etc/etherview/config.yaml \
  --reason "reviewed rollout backfill"

etherview admin derived-verification backfill \
  --config /etc/etherview/config.yaml \
  --address 0x0000000000000000000000000000000000000001 \
  --reason "retry factory after runtime observations arrived"
```

The command never deletes attempts or verified artifacts. For each unleased
scan it first resolves the exact canonical creator-code epoch, queues one scan
at that epoch start, and retains an incorrect later-start duplicate as failed
with `superseded_epoch_start` so it cannot be forward-dispatched again.
Existing terminal attempts keep publication idempotent, while pending-runtime
attempts can be reconsidered. A reorg marks attempt provenance stale and
current readers stop resolving the orphaned child until the exact block hash
is reattached.

For Preview, see [verification recovery](../../preview/docs/operations.md#verification-recovery).

## Application-locked Vyper runtimes

Verification/all use the lock embedded in the application. No Vyper catalog URL, public
key, signing service or catalog renewal is required. The supported versions
and their exact dependency choices change only with an application release.
Vyper 0.2.0 is no longer supported. All locked wheels are downloaded from
`https://files.pythonhosted.org`. No PyPI index lookup, pip installation or
native Python fallback occurs during verification.

Remove the retired `verification.vyper_catalog_url` and
`verification.vyper_catalog_public_key` configuration fields, plus their
`ETHERVIEW_VERIFICATION_VYPER_CATALOG_URL` and
`ETHERVIEW_VERIFICATION_VYPER_CATALOG_PUBLIC_KEY` environment settings, before
rolling out the new application.

Keep outbound HTTPS access to that host for cold cache misses. Existing valid
raw archives and assembled packages are reused offline. The existing
`verification.cache_directory` volume is rebuildable performance data: loss can
require downloading several archives per version, and upstream removal prevents
cold reconstruction. Stop all cache owners before manual cleanup. Hash failures
must be resolved by restoring exact bytes, never editing the embedded lock.

The image contains read-only runtimes at `/opt/etherview/python-wasm/modern`
and `/opt/etherview/python-wasm/legacy`, plus the host Node SEA. Vyper
0.4.0–0.4.3 uses Pyodide 314.0.7 / Python 3.14.2; the other 21 baseline versions use
Pyodide 0.29.5 / Python 3.13.2. Both startup probes are required. Every compile
uses a fresh process with V8 old-generation capped at 128 MiB, WASM memory at
384 MiB and configured I/O/time limits. These are not total RSS bounds.

Before any executor-identity rollout, stop new Vyper submissions and drain
queued/running jobs. This is an operator prerequisite; the fresh-database
baseline does not perform an upgrade-time queue check. Terminal evidence in
an already compatible database remains unchanged. Deploy an exact image digest with native
AMD64/ARM64 matrix and monolith/split acceptance, then check readiness and both
runtime probes before resuming. Do not mix different bound executor identities
or rebind jobs. The internal PostgreSQL generation records the application
snapshot and has no remote expiry; Solidity catalog freshness is unchanged.

Release validation uses `compiler/vyper/acceptance.mjs` with both native
acceptance records. It does not sign or publish a catalog. Retain current
assembly/integrity/license evidence. Application releases own recipe changes and
version withdrawal; live upstream metadata never changes an in-flight job.

## Automatic bytecode database source discovery

Set `features.verification: true` and `bytecode_db.enabled: true` to search the
remote Ethereum Bytecode Database for indexed contracts. The feature defaults
to disabled. See [Preview source configuration](../../preview/docs/operations.md#external-source-configuration)
for local defaults and overrides.
`bytecode_db.base_url` defaults to
`https://eth-bytecode-db.services.blockscout.com`; HTTPS search endpoints use
public-network address checks by default. No source is uploaded and no remote compiler
is invoked. Solidity, Yul and locally supported Vyper sources are compiled and
matched locally before source, ABI or verification status is published.

The verification/all role owns discovery, search and local verification. Keep the same
configuration on all verification replicas. Environment overrides use
`ETHERVIEW_BYTECODE_DB_ENABLED`, `BASE_URL`, `TIMEOUT`, `MAX_RESPONSE_BYTES`,
`MAX_CONCURRENCY`, `REQUEST_INTERVAL` and `COOLDOWN` with the same
`ETHERVIEW_BYTECODE_DB_` prefix. Defaults are 20 seconds, 32 MiB, two concurrent
searches, one request per second across replicas and a 24-hour cooldown.
The existing `verification.max_input_bytes` also limits each candidate.

For local Fake-IP or private-network development, explicitly set
`bytecode_db.unsafe_allow_private_networks: true` or
`ETHERVIEW_BYTECODE_DB_UNSAFE_ALLOW_PRIVATE_NETWORKS=true`. This independent
escape hatch defaults to `false`.
It allows the bytecode
search client's resolved private, loopback and special-purpose addresses
(including `198.18.0.0/15`); it does not change metadata or compiler-download
policy. HTTPS, certificate verification, redirect rejection, checked-IP dialing
and request/response bounds remain enforced; environment HTTP proxies remain
disabled. Set the override to `false` to restore public-network checks.

Repeated bounded address sweeps cover old contracts, late creation facts and
reorgs. Ordinary contracts wait for canonical creation facts; authenticated
Genesis predeploys may verify runtime alone. A search hit is not proof: remote
ABI, match labels and artifacts are never published as local evidence. Unknown
compiler versions remain unavailable until local support changes. Vyper release
labels such as `v0.4.3+commit.bff19ea2` select the application-locked `0.4.3`
release; upstream commit labels are not compiler provenance. Automatic
work has lower queue priority than submitted jobs and only one automatic
compilation can run at a time. Successful artifacts retain
`verification_origin: eth_bytecode_db` in the native API and display that
source origin in both locales.

Monitor `etherview_bytecode_db_total{result=...}` and
`etherview_bytecode_db_pending`. `hit` means a candidate was retrieved;
`verified` means a locally verified artifact is available. `waiting_facts`,
`compiler_unavailable`, `candidate_limited`, `rate_limited` and
`verification_failed` explain incomplete coverage without leaking source,
provider URLs or nested upstream errors. Remote failure does not block core
readiness. PostgreSQL owns cooldowns, lookup cache, leases and immutable
candidate/job associations; restarting workers does not erase them.

Compiler artifact downloads retry transport errors, timeouts, HTTP 408/429 and
5xx responses at most three times per compiler resolution or compilation pair, with 1/2-second backoff
and `Retry-After` bounded to 30 seconds. Failed jobs distinguish
`compiler_download_unavailable`, `compiler_download_timeout`,
`compiler_download_rate_limited`, `compiler_download_rejected`,
`compiler_checksum_mismatch` and `compiler_artifact_too_large` without exposing
upstream diagnostics. The first three and `compiler_unavailable` may requeue
after `bytecode_db.cooldown`, retaining the job, source association and bound
compiler identity. Each wave resets the lease attempt budget. Rejected,
checksum, size and deterministic compilation/match failures remain terminal.

For an explicit operator retry of cached searches, use the writer database in
an audited maintenance session. Select the provider by its stored service ID
(`encode(id, 'hex')` from `bytecode_db_services`), then reset only that service's
unleased lookups:

```sql
UPDATE bytecode_db_lookups
SET next_attempt_at = clock_timestamp(), failures = 0, requested = true
WHERE service_id = decode('<service-id-hex>', 'hex') AND lease_token IS NULL;
```

This retries remote discovery; it does not erase deterministic local compilation
failures or alter published verification. Changing the compiler/executor
identity permits a new local attempt. Disable the feature to stop new discovery;
previously queued local verification jobs keep their ordinary durable lifecycle.
This fresh-database baseline requires recreation of older development databases,
as described in the fresh-database initialization procedure.

## Browser contract verification

The More → Verify contract page requires `features.user_auth`,
`features.verification` and `security.public_verification` for submission.
Configure the existing session pepper and canonical `server.public_url` as for
Account login. The API-key pepper remains required by the existing verifier
configuration and encrypted upstream imports; do not distribute it or an
operator API key to the SPA. Browser submission uses the existing SIWE Cookie
and session-bound CSRF token. When user authentication is disabled, the page
explains that browser submission is unavailable; it has no API-key fallback.

The session endpoints are POST `/api/v1/verification-jobs/local`,
`/api/v1/verification-jobs/sourcify-import`,
`/api/v1/verification-jobs/sourcify`, and
`/api/v1/verification-jobs/sourcify-from-etherscan`, plus
GET `/api/v1/verification-jobs/{id}`. The first two POST bodies include
`address`; source import additionally requires the source `chain_id`.
Existing native and Etherscan clients continue to use scoped API keys.

Users can save `/verify?job=<UUID>` to reopen results after login. Switching
back to the form does not cancel a durable task. Refreshing restores the task
result, not the uploaded files. Disabling new submissions leaves authenticated
reads available. Job IDs are not an ownership boundary: any authorized user
with a known ID can read its redacted status and match evidence. There is no
personal job list or schema migration. Sourcify publication remains explicit;
its external success is separate from local compilation and publication.
