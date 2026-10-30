# ENS name directory and registration profiles

Recorded 2026-10-30 for P20-T30, P40-T28 and P50-T82 under ADR-0058.
This extends the current-chain indexer with independently published registration
profiles, database-only name details, and search results keyed by normalized name.
It does not reopen the original rewrite or its historical acceptance records.

## Delivered behavior

- `/name/$name` displays Registrant, Controller, Expiration Time and Resolved
  Address. Primary-name links open this page; address links and copying remain
  separate. English and Chinese layouts support 390px screens and keyboard use.
- Known names remain searchable without a successful forward resolution, even
  after registration expiry. Two names resolving to one address remain separate
  directory entries. Unknown names return `not_indexed`; only verified negative
  resolution produces `not_set` for the address field.
- Background profile jobs use the current-chain RPC pool at one canonical block
  hash and endpoint. Registration and forward observations have independent
  publication identities and validity. API reads use PostgreSQL only and neither
  enqueue ENS work nor write ENS snapshots.
- Real pinned v1/v2 contracts cover registrar versus registry ownership, wrapped
  ownership and registrar expiry, subnames, premigration, migration,
  WrapperRegistry paths, renewal, token regeneration, grace, expiry and
  reregistration. Unreviewed implementations and resolver permission models do
  not invent a single controller account.
- Canonical discovery, lease/generation/configuration fences and immutable
  publication references govern reads. Search cursors reject changed or expired
  snapshots, including registration expiry before observation expiry. An orphaned
  event-only name loses its directory entry; operator imports retain their
  distinct provenance.
- Official/custom classifications were removed from manifest/runtime/schema,
  public contracts, cursor metadata, metrics and presentation. Source identity,
  manifest/policy digests and reviewed deployment/code identities remain.

## Validation

The targeted ENS/API/query units and PostgreSQL ENS race regressions passed.
`make test-integration-race` also passed across application, billing, genesis,
integration, authentication and verifier packages.
`make generate-check`, `make lint-go`, `make web-lint`, `make web-test`,
`make docs-check` and `make plan-check` passed. Web tests include 760 unit cases.
The full browser suite passed **157 tests**; the two new bilingual mobile cases
were then rerun with keyboard activation and passed. Initial assets remain within
unchanged budgets: 992439 raw bytes and 309441 gzip bytes.

`make test-schema-e2e`, `make test-runtime-e2e` and
`make test-preview-contracts` passed. Final reruns used their documented
`-prebuilt` variants after `make docker-build`, against Linux arm64 image
`sha256:6d6b6b53162f67801b508ab94388c1e3abee7218f3d550e89d0f8cccec8cf246`. The runtime and Preview assertions include
independent registration publication followed by the public database-only detail
and name-search reads, alongside the existing restart/recovery and topology tests.
The local x402 all/split regressions also passed.

## Evidence boundary

These are local development checks, not remote CI, full deployment history or
release capacity/security/soak evidence. The earlier
[Mainnet publication report](ens-current-chain-acceptance.md) describes the
original rewrite and does **not** validate these new registration fields on
Mainnet. Full historical coverage remains independently reported deployment
progress. P60/P70 live-payment, security, capacity, recovery and release gates
remain unchanged.

## Review follow-up

The pre-push review removed the retired request-time search interface, resolved-
address cursor gate and SQL, obsolete public search validity fields, the unused
forward-read facade and getter artifact, and the multi-source runtime loop.
Preview procedures now describe the current-chain model without source badges.
Superseded ADRs and historical reports retain their original facts.

The review also corrected three profile boundaries: transient proxy storage RPC
failures remain retryable; premigration fallback retains the exact job block
instead of considering future deployments; and v1 registration rights follow the
configured Registry, its authoritative parent owner and the actual Wrapper
holder, rather than manifest ordering. Pinned real-contract regressions cover
these cases.

Review validation passed `make check`, the full owned PostgreSQL race runner,
and four bilingual ENS publication/detail browser cases. Schema, all/split
runtime (including local x402) and Preview contracts passed using image
`sha256:053532cc3d2561b91bc2fa2941831e6fb2e2d458876fbf64f002a04e7f97aaae`.
The final SQL-only cleanup removes unused validity/block columns and their
joins; API/query race units, PostgreSQL ENS/search race regressions, generation
and Go lint passed afterward.
The security scanner's public-address false positive is constrained by exact
report path, field and value, with regressions retaining detection elsewhere.
