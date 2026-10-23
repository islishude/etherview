# Proxy Detection Deployment Review Template

Review state: template

Use this redacted aggregate record in an isolated acceptance deployment before
serving a unified detector revision to users. Evidence is public by design in
that deployment. This template is not itself acceptance evidence. Controlled
raw exports remain outside Git; record their SHA-256 and reviewed conclusions.

## Deployment and cohort

| Field                                                  | Value                       |
| ------------------------------------------------------ | --------------------------- |
| Review date                                            | pending                     |
| Reviewer                                               | pending                     |
| Git revision                                           | pending                     |
| Production image digest                                | pending                     |
| Configured chain ID                                    | pending                     |
| Inclusive finalized block range, at most 10,001 blocks | pending                     |
| First canonical block hash                             | pending                     |
| Last canonical block hash                              | pending                     |
| Canonical manifest SHA-256                             | pending                     |
| Reindex request ID                                     | pending                     |
| Reindex requested/completed time                       | pending                     |
| Range first processed by a CWIA-capable revision       | pending                     |
| Separate CWIA backfill approval, if required           | not applicable or reference |
| `safe_proxy_detection`                                 | enabled only when reviewed  |
| `diamond_proxy_detection`                              | enabled only when reviewed  |

## Publication completeness

| Measure                                                      |                   Required | Observed |
| ------------------------------------------------------------ | -------------------------: | -------: |
| Expected blocks                                              | exact inclusive range size |  pending |
| Canonical blocks                                             |            expected blocks |  pending |
| Current complete `proxy@2` publications                      |            expected blocks |  pending |
| Missing, stale, failed, unavailable, or pending publications |                          0 |  pending |

## Detection aggregates

Record the complete resolution-status table and the complete
detector/family/status/confidence table. Include every enabled detector and any
`solady-cwia`/`cwia` results produced by the deployed build.

pending

## Mandatory correctness review

| Review                         | Required conclusion                                                                                                         | Observed conclusion       |
| ------------------------------ | --------------------------------------------------------------------------------------------------------------------------- | ------------------------- |
| Authoritative relations        | Existing rows are unchanged; older pre-CWIA history was not replayed without separate CWIA approval                         | pending                   |
| Baseline comparison            | Every difference is explained; no standard family or implementation regression                                              | pending                   |
| Unknown outcomes               | None remain unexplained; transient rows were replayed only after exact-state RPC health returned                            | pending                   |
| Inconsistent outcomes          | Every row is evidence-correct hostile/ambiguous input, not a detector or publication defect                                 | pending                   |
| Resolver conflicts             | Every conflict retains all outcomes and cannot change binding or write authority                                            | pending                   |
| Safe bulk                      | Canonical shell, slot-0 singleton, code identity, and official/custom distinction are correct                               | pending or disabled       |
| Ethereum Mainnet official Safe | Maintained fixed identities, currently at block `25711126`, validate any official-provenance claim                          | pending or not applicable |
| Diamond                        | Complete/partial/truncated state is accurate and Cut replay agrees with the latest exact published Loupe map where complete | pending or disabled       |
| CWIA                           | Exact shell/length/implementation evidence is correct and OpenZeppelin does not claim the outcome                           | pending                   |
| Public contract                | Published `proxy_detection` matches evidence; Safe grants no ordinary writes and Diamond targets are selector-fenced        | pending                   |
| Negative sample                | All 100 deterministic rows, or the full smaller population, are valid negatives                                             | pending                   |

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
deployment acceptance.

pending

## Controlled artifact digests

| Artifact                                   | SHA-256                   |
| ------------------------------------------ | ------------------------- |
| Canonical block manifest                   | pending                   |
| Baseline authoritative relations           | pending                   |
| Candidate authoritative relations          | pending                   |
| Publication completeness export            | pending                   |
| Detection aggregates                       | pending                   |
| Mandatory review rows                      | pending                   |
| Deterministic negative sample              | pending                   |
| Diamond Loupe/Cut comparison, when enabled | pending or not applicable |
| Pre/post metrics                           | pending                   |

## Decision

Decision: `pending`

Only a complete `pass` supports serving this exact revision, image, fresh schema,
chain and family configuration to users. Failure keeps that revision out of
service. Disabling a family stops new work but does not hide historical results.
There is no global visibility gate or old-detector fallback.
