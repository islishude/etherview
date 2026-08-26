# Proxy Detection V2 Shadow Rollout Review Template

Review state: template

Use this redacted aggregate record before enabling
`proxy_detection_v2_public` in one deployment. It is not a source-code, P58,
or release-plan completion record. Controlled raw exports remain outside Git;
record only their SHA-256 digests and reviewed aggregate conclusions here.

## Deployment and cohort

| Field | Value |
| --- | --- |
| Review date | pending |
| Reviewer | pending |
| Git revision | pending |
| Production image digest | pending |
| Configured chain ID | pending |
| Inclusive finalized block range, at most 10,001 blocks | pending |
| First canonical block hash | pending |
| Last canonical block hash | pending |
| Canonical manifest SHA-256 | pending |
| Reindex request ID | pending |
| Reindex requested/completed time | pending |
| Range first processed by a P69-capable revision | pending |
| Separate CWIA backfill approval, if required | not applicable or reference |
| `proxy_detection_v2` | `true` required |
| `safe_proxy_detection` | enabled only when reviewed |
| `diamond_proxy_detection` | enabled only when reviewed |
| `proxy_detection_v2_public` during review | `false` required |

## Publication completeness

| Measure | Required | Observed |
| --- | ---: | ---: |
| Expected blocks | exact inclusive range size | pending |
| Canonical blocks | expected blocks | pending |
| Current complete `proxy@2` publications | expected blocks | pending |
| Missing, stale, failed, unavailable, or pending publications | 0 | pending |

## Detection aggregates

Record the complete resolution-status table and the complete
detector/family/status/confidence table. Include every enabled detector and any
`solady-cwia`/`cwia` results produced by the deployed build.

pending

## Mandatory correctness review

| Review | Required conclusion | Observed conclusion |
| --- | --- | --- |
| Legacy projection | Existing rows are unchanged; older pre-P69 history was not replayed without separate CWIA approval | pending |
| V2/OZ differences | Every difference is explained; no legacy family or implementation regression | pending |
| Unknown outcomes | None remain unexplained; transient rows were replayed only after exact-state RPC health returned | pending |
| Inconsistent outcomes | Every row is evidence-correct hostile/ambiguous input, not a detector or publication defect | pending |
| Resolver conflicts | Every conflict retains all outcomes and cannot change legacy authority | pending |
| Safe bulk | Canonical shell, slot-0 singleton, code identity, and official/custom distinction are correct | pending or disabled |
| Ethereum Mainnet official Safe | Maintained fixed identities, currently at block `25711126`, validate any official-provenance claim | pending or not applicable |
| Diamond | Complete/partial/truncated state is accurate and Cut replay agrees with the latest exact published Loupe map where complete | pending or disabled |
| CWIA | Exact shell/length/implementation evidence is correct and OpenZeppelin does not claim the outcome | pending |
| Negative sample | All 100 deterministic rows, or the full smaller population, are valid negatives | pending |
| Public isolation | Representative API responses omit `proxy_detection_v2` while the public flag is false | pending |

The production replay exercises bulk detection. It does not claim coverage for
deep-only `safe-compatible-proxy`; that path remains covered by fixed-block
regressions.

## Metrics

For the same set of `enrich`/`all` processes, record pre-run values, post-run
values, and deltas for:

- `etherview_proxy_detection_duration_ms`
- `etherview_proxy_detection_rpc_calls_total`
- `etherview_proxy_detection_rpc_errors_total`
- `etherview_proxy_detection_results_total`
- `etherview_proxy_detection_ambiguous_total`
- `etherview_proxy_detection_inconsistent_total`
- `etherview_safe_proxy_fingerprint_match_total`
- `etherview_safe_proxy_compatible_candidate_total`

No new numeric latency or error SLO is introduced by this review. Any sustained
exact-state RPC error, unexplained unknown, or unclassified regression fails
promotion.

pending

## Controlled artifact digests

| Artifact | SHA-256 |
| --- | --- |
| Canonical block manifest | pending |
| Pre-replay legacy projection | pending |
| Post-replay legacy projection | pending |
| Publication completeness export | pending |
| Detection aggregates | pending |
| Mandatory review rows | pending |
| Deterministic negative sample | pending |
| Diamond Loupe/Cut comparison, when enabled | pending or not applicable |
| Pre/post metrics | pending |

## Decision

Decision: `pending`

Only `pass` authorizes setting `proxy_detection_v2_public: true` for this exact
deployment revision, image, chain, detector configuration, and reviewed
cohort. Failure keeps the public flag disabled and uses the documented
detector-specific or complete V2 rollback.
