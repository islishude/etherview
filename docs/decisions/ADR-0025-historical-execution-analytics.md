# ADR-0025: Historical Execution Analytics

- Status: Accepted
- Date: 2026-07-28

## Context

The existing statistics surface publishes one block-range chart directly from
`block_statistics`. It cannot provide a bounded, stable, long-history
experience, and `stats@2` does not authenticate execution fees, priority fees,
failed transactions, or successful top-level contract creation counts.

Historical analytics combine facts from many canonical blocks. They therefore
cannot use one block's derived-publication identity, and they must not expose a
projection computed from an older canonical generation after a reorganization
or stage replay.

## Decision

### Source facts

`stats@4` is the execution-statistics authority under
[ADR-0052](ADR-0052-amsterdam-explorer-support.md). It retains the analytics
introduced by `stats@3`, using receipt gas for execution fees and base-fee burn,
and stores exact decimal-string source totals for:

- confirmed execution gas fees, calculated for every authenticated receipt as
  `gas_used * effective_gas_price`;
- priority fees, calculated as execution gas fees minus non-negative base-fee
  burn;
- failed transaction count; and
- successful top-level contract creation count.

Average transaction fees are always derived from the exact fee total and
transaction count. No floating-point average is persisted. Blob gas fees
remain separate from execution gas fees. Token transfer analytics continue to
use published `token@2` facts.

### Persistent projection

PostgreSQL stores UTC hourly rollups containing only exact sums and sample
counts. Day, ISO week beginning Monday 00:00 UTC, and calendar-month results
are derived from those hourly rows. Empty time buckets are absent rather than
invented as zero. The current UTC hour is marked partial.

Canonical attach or detach and publication or withdrawal of `stats@4` or
`token@2` mark every affected hour dirty in the same database transaction.
Dirty rows carry a monotonically increasing generation. The maintenance role
uses a chain-scoped PostgreSQL advisory lock, copies a bounded source
generation, recomputes from the exact canonical mapping and published stage
results, and publishes only if that generation is still current. A crash,
duplicate worker, or concurrent source change therefore leaves the hour dirty
instead of publishing stale output.

Backfill state is writer-owned and progresses from newest history toward the
configured start. Only current published stage versions satisfy the projection.
Reindexing `stats` schedules `stats@4`. The current fresh-database baseline
provides no legacy-schema conversion or older-binary rollback guarantee.

### Read contract

The API may read hourly rollups, dirty state, and coverage from the optional
read pool in one repeatable-read snapshot. Recalculation, backfill state, and
all other mutations use the writer.

Each request, including refresh, selects the newest completed canonical
hourly rollup and caps its range at that hour's end. New blocks, including
additions to the same hour, preserve the last completed rollup. Dirty triggers atomically delete an old rollup only when the changed
block is at or below its published end; reorgs and source replay therefore
withdraw affected aggregates before rebuilding. A newer pending suffix does
not block previously completed data. The returned range and canonical snapshot
identify the data used; overview comparison windows end at this cutoff (or now,
if earlier). Missing or withdrawn hours inside the resulting range still
return analytics-pending rather than fabricated aggregates. The overview
reports the available historical range and backfill
progress so recent data can become useful before the full backfill completes.
Every response contains a canonical snapshot and no more than 500 points.

Metric and interval identifiers are closed allowlists. `auto` chooses the
finest interval that fits the point bound; an explicitly over-fine interval is
rejected. Ratios and averages use decimal fixed-point arithmetic without a
`float64` conversion. Public integers outside JavaScript's safe range remain
decimal strings.

### Browser contract

The SPA uses only the generated same-origin `/api/v1` client. `/charts` is a
categorized overview and `/charts/:metric` is a deep-linkable metric detail
route. Range and interval selections are URL state. Canvas charts are an
enhancement over an always-present exact table, and CSV is generated in the
browser from the same response. No external asset, CDN, or download API is
introduced.

## Consequences

- Historical reads are bounded independently of chain age.
- Reorganizations and stage replay temporarily reduce availability instead of
  serving an internally consistent but obsolete projection.
- Storage grows by canonical active hours rather than by every public interval.
- Maintenance becomes responsible for a non-blocking, observable rollup
  component in monolith and split-role deployments.
- A formula or source-stage change requires another reviewed stage or rollup
  contract version; it cannot silently reinterpret published history.
