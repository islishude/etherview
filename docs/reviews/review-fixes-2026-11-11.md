# Review fixes — 2026-11-11

Implementation baseline: `d06b1ae5957a6b3a42c044bfb8088caf687c51bf` on
`codex/review-fixes`. This report describes the working tree, not a published
release. All applicable local gates below passed.

## Delivered behavior

| Item | Change and regression evidence                                                                                                                                                                                                                                                                                                                                                                |
| ---- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1    | One compact source body; deterministic settings-only variants. The Service → PostgreSQL → worker path covers 3 MiB Standard JSON and 1.5 MiB multipart inputs.                                                                                                                                                                                                                                |
| 2    | Separate HTTP, compiler, and compact-job limits. Every variant and the second perturbation compile are checked before admission; predictable overflow is a typed client error.                                                                                                                                                                                                                |
| 3    | PostgreSQL atomically deduplicates, checks principal/global capacity and creates jobs. Browser sessions and owned keys share the user principal. Unknown imports reserve the input ceiling; binding replaces the reservation with actual bytes. Retry/takeover retain one allocation; terminal transitions release it. Native/compatible 429 and 503 responses carry `Retry-After: 30`.       |
| 4    | Shared all/split cleanup retains failed/cancelled input for seven days, deletes at most 100 per pass and preserves metadata/digests and successful/referenced evidence. Regression checks two batches and recent failures.                                                                                                                                                                    |
| 5    | Stored target/outcome projections keep polling source-free, including successful jobs. Native complete-result compatibility uses a separate evidence read. A 3 MiB successful input retains its full native result while its status projection stays below 4 KiB.                                                                                                                             |
| 6    | Browsing queues transaction-local identities, ignores heartbeat-only changes and refreshes only relevant stages. Deferred proxy coverage, chart source and browsing use a common lock order. Concurrency regression covers the formerly inverted order.                                                                                                                                       |
| 7    | Source/block/hour/chain projections update atomically. Idle progress reads summaries; completed prefixes survive later incomplete suffixes. Reconciliation checks at most 1,000 identities per 15 minutes and resumes from a durable cursor without overwriting intervening publication.                                                                                                      |
| 8    | Claim/exhaustion inspect at most 32 candidates, try advisory ownership before nonblocking row ownership and skip busy candidates. Replacement lease/publication fencing remains tested.                                                                                                                                                                                                       |
| 9    | Canonical segment and both boundary mappings are locked in one query under the chain lock; hash/parent validation stays in memory. Existing boundary conflict and atomic publication regressions remain applicable.                                                                                                                                                                           |
| 10   | Native-transfer query and pagination identities include the overview block hash. Empty and nonempty mismatched snapshots both hide data/pagination and show the identity-change message.                                                                                                                                                                                                      |
| 11   | Cancelled initial browsing requests stop loading and offer keyboard-accessible manual refresh, without automatic retry. Bilingual/light/dark browser cases cover recovery.                                                                                                                                                                                                                    |
| 12   | Block, transaction, address, token and NFT routes have separate dynamic entries. Shared status/log components no longer import transaction detail implementation. Production static asset closures and actual emitted module ownership enforce route isolation and the pre-change size ceiling.                                                                                               |
| 13   | Holder summaries use the count endpoint. Lists run only on the active holders tab, show their own snapshot and preserve a hidden cursor across another tab's page refresh.                                                                                                                                                                                                                    |
| 14   | Head polling validates a lightweight header and fetches changed bundles/receipts by exact hash on the same endpoint. Transient startup failures quarantine endpoints with supervised identity/capability revalidation. A pre-authentication 20-second API deadline propagates through database/external work, returns typed 504 before response commitment and explicitly exempts SSE routes. |

Defaults and operator behavior are maintained in the
[verification runbook](../operations/verification.md),
[recovery runbook](../operations/recovery.md),
[configuration example](../../deploy/config.example.yaml) and affected ADRs.
The database changes target fresh databases; no legacy adapter or startup backfill
was added.

## Local performance evidence

The [machine-readable record](review-fixes-performance-2026-11-11.json) retains
`EXPLAIN (ANALYZE, BUFFERS)` plans, measured-source SHA-256 identities and timing
samples. Host: ARM64 Mac15,9, 16 logical CPUs, 48 GiB RAM; Go 1.27.4, Docker
29.9.3, owned PostgreSQL 18 Compose databases. Other acceptance gates were running
on the host. These are diagnostic samples, without an isolated-host throughput
claim or an assumed improvement percentage.

Analytics datasets contain real canonical bundles plus three published
jobs/results/journals per block:

| Blocks | Jobs/results/journals each | Canonical batch | Publication p95, three stages | Idle worker |
| ------ | -------------------------- | --------------- | ----------------------------- | ----------- |
| 16     | 48                         | 145.000 ms      | 106.480 ms                    | 2.735 ms    |
| 128    | 384                        | 482.559 ms      | 86.061 ms                     | 3.539 ms    |

Both idle SQL plans avoid canonical blocks, jobs, stage results and journals.
The functional ten-year hourly-range test remains separate from these publication
samples. Reconciliation's occasional bounded scan is not counted as ordinary idle
work.

Verification uses five concurrent submitters with five distinct tasks each, 25
renewals, and 100 status reads per source size:

| Source bytes/task | Queued input bytes | Admission p95 | Renewal p95 | Sampled maximum lock wait | Status read | Allocated bytes/read |
| ----------------- | ------------------ | ------------- | ----------- | ------------------------- | ----------- | -------------------- |
| 1,024             | 49,865             | 41.416 ms     | 1.647 ms    | 34.439 ms                 | 0.220 ms    | 2,328                |
| 1,572,864         | 39,345,865         | 348.511 ms    | 8.653 ms    | 291.643 ms                | 0.233 ms    | 2,330                |
| 3,145,728         | 78,667,465         | 487.151 ms    | 17.490 ms   | 367.094 ms                | 0.416 ms    | 2,389                |

All status SQL plans use the job primary key, return one row, hit three shared
buffers and read zero buffers from storage. No submitted source is fetched.
These timing samples cover queued/running status. Successful-source exclusion is
also checked against the stored outcome projection. Returning the native API's
full successful evidence intentionally retains response cost proportional to that
evidence; it is not presented as a constant-size operation.

Reproduce the samples with:

```sh
GOFLAGS=-v go run ./e2e/cmd/testintegration -root . \
  -packages ./internal/integration \
  -run TestIncrementalAnalyticsIdlePlansAndPublicationCost
go run ./e2e/cmd/testintegration -root . -packages ./internal/verify \
  -benchmark BenchmarkVerificationCompactAdmissionAndStatus -benchtime 100x
```

## Route resource checks

Static import closures include shared dependencies and CSS, while separately
selected dynamic features remain outside the initial closure. Each route stays
below the measured pre-change combined detail entry ceiling of 1,366,770 raw bytes
and 391,509 gzip bytes. Emitted-module ownership checks also reject foreign
entity implementation hidden inside a shared chunk.

| Route       | Raw bytes | Gzip bytes |
| ----------- | --------- | ---------- |
| Block       | 1,110,327 | 333,725    |
| Transaction | 1,161,381 | 344,278    |
| Address     | 1,277,593 | 376,111    |
| Token       | 1,085,469 | 324,974    |
| NFT         | 1,064,400 | 317,654    |

The initial shell remains within its existing budget: 995,580 raw / 298,249 gzip
bytes, with no relaxation of the 1,000,000 / 310,000-byte limits.

## Acceptance

Targeted unit/race and owned PostgreSQL regressions pass for capacity, imported
input reconstruction, lightweight status, bounded retention/reconciliation,
publication lock order, heartbeat, queue skipping, request cancellation and RPC
endpoint pinning. The complete gates passed:

| Gate                                                                    | Result                                                                                                                                                                                |
| ----------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `make check GO_LICENSES="$PWD/.local/review-tools/go-licenses"`         | Passed generation, lint, unit/race, security, licenses and deployment checks; 1,030 frontend unit tests.                                                                              |
| `make test-integration test-integration-race`                           | Both complete owned PostgreSQL suites passed, including the database-wide zero-deadlock guard and cleanup.                                                                            |
| `make test-e2e`                                                         | All 270 embedded-browser cases passed after pre-PR review. Four full-page 390px cancellation/recovery screenshots were visually checked across English/Chinese and light/dark themes. |
| `make test-schema-e2e test-runtime-e2e IMAGE=etherview:review-fixes-ui` | Fresh schema and embedded SPA at runtime acceptance; all/split runtime, outage/restart, SSE, TLS, bounded load and x402 passed.                                                       |
| `make test-hardhat3-e2e IMAGE=etherview:review-fixes-final`             | Real Hardhat compilation, native/compatibility verification, BytecodeDB and proxy upgrade/rebinding passed in all/split layouts.                                                      |
| `make test-foundry-e2e test-vyper-e2e IMAGE=etherview:review-fixes-ui`  | Real Forge and Geas verification plus every application-locked Vyper version passed in both layouts.                                                                                  |

The license scanner was rebuilt at the repository-pinned `v2.0.1` using the
current Go toolchain, because the existing host binary referenced an older Go
standard-library installation. No license rules or allowlists were relaxed.
Hardhat's image contains the final backend; the subsequent shared-component
extraction changes only frontend dependencies and is covered by the final UI
image, complete browser suite and generation/resource gates.

Final documentation/plan validation and Go lint also pass. P20-T33 and P30-T156
are complete; ordinary defect fixes did not reopen earlier completed milestones.

Pre-PR review found and corrected a missing list-specific snapshot label on the
holders tab and a stale in-progress sentence in the root plan. Empty/nonempty
holder regressions first reproduced the omission with a summary at block 42 and
a list at block 43, then passed with the list's own snapshot link. The correction
reuses existing typography, translated labels and block links, without changing
the API or adding styling dependencies. `make web-test web-lint generate-check`
passed with 1,032 frontend tests and the resource sizes above. Backend measured
source hashes remain unchanged. Four 390px holder-list screenshots were visually
checked in English/Chinese and light/dark themes; the list snapshot remains clear
and its links and existing horizontal table scrolling remain accessible. This final
frontend-only label correction postdates the runtime/compiler acceptance images;
it is covered by the production embedded-browser build and generation/resource
gates, and does not change backend or compiler behavior.

P70-T04 remains blocked on its specified reference environment and independent
500 RPS / 30-minute release acceptance. Local benchmarks and bounded runtime load
checks do not clear that blocker.
