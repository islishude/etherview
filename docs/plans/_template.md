# PXX — Plan Name

Status: `planned`

Use only for work meeting the [planning threshold](../development.md#planning-threshold).
Small fixes, dependency bumps, CI repairs and documentation edits do not need
this template. Prefer an existing milestone; allocate IDs using the
[catalog watermarks](index.md#identifier-allocation).

## Outcome

State the observable capability and explicit non-goals.

## References

- [Architecture](../architecture/overview.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| PXX-T01 | todo | — | Independently verifiable capability or migration | Named acceptance gate |

Allowed item states are `todo`, `in_progress`, `blocked`, `done`, `dropped`,
and `superseded`. Implementation, regressions and acceptance belong to the same
milestone, not separate tracking items.

## Acceptance

- [ ] Observable acceptance criterion.
- [ ] Failure and recovery behavior is covered.
- [ ] Monolith and split-role semantics match where applicable.

## Current Blockers

None.

## Evidence

Record concise commands, results, revision/environment and material limits for
the milestone. Link a commit or PR when one exists. Replace stale progress
notes; do not append a maintenance log. After completion, keep the capability
and meaningful acceptance summary, resolving references before pruning records.

