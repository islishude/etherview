# Load, soak and benchmarks

[Testing index](../testing.md) · Commands run from the repository root.

- [Commands](#commands)
- [Load profile example](#load-profile-example)

## Commands

- `make test-load`: run the bounded public-API driver. Defaults are a 100 RPS,
  30-second smoke with p95, error-rate, throughput, and final core-lag
  thresholds. Set the typed `ETHERVIEW_LOAD_*` environment inputs, encode the
  route mix as a JSON string array in `ETHERVIEW_LOAD_PATHS`, and describe the
  revision, dataset, hardware, and RPC model.
- `make benchmark`: run the raw-first chainbundle decode, validation, and clone
  allocation baselines for empty, typical, and large deterministic blocks.
  Override `BENCHTIME` with a Go benchmark duration or iteration count. These
  microbenchmarks compare implementation revisions; they are not a substitute
  for PostgreSQL or reference-capacity evidence.
- `make benchmark-integration`: use the same owned PostgreSQL 18 lifecycle as
  `make test-integration`, then benchmark idempotent core segment persistence
  plus block and transaction public projections at deterministic typical and
  large block cardinalities. It honors `INTEGRATION_DATABASE_URL` only for an
  explicitly disposable external database and accepts the same `BENCHTIME`
  override. Record the PostgreSQL version and host with results.
- `make test-soak`: run the same driver at the P70 reference defaults of
  500 RPS for 30 minutes. The target fixes a 5-second request timeout, p95
  below 500 ms, error rate below 0.1%, final lag no greater than two blocks,
  and successful throughput of at least 99% of target (495 RPS). The 1%
  throughput allowance covers only the bounded final in-flight drain and
  scheduler jitter; dropped admissions and failed responses remain errors, and
  the former 475 RPS floor cannot pass. This is an executable harness, not
  release evidence by itself; P70-T04 still requires the named reference
  deployment, dataset, hardware, RPC behavior, and independently captured
  resource peaks.

## Load profile example

For example:

```sh
mkdir -p artifacts
ETHERVIEW_LOAD_BASE_URL=https://explorer.example.invalid \
ETHERVIEW_LOAD_REVISION=0123456789abcdef \
ETHERVIEW_LOAD_DATASET=mainnet-snapshot-2026-07-23 \
ETHERVIEW_LOAD_HARDWARE=kubernetes-reference-profile \
ETHERVIEW_LOAD_RPC_BEHAVIOR=isolated-head-history-state \
ETHERVIEW_LOAD_PATHS='["/api/v1/status","/api/v1/blocks?limit=20&sort=desc"]' \
make test-load >artifacts/load.json
```

Use `ETHERVIEW_LOAD_API_KEY_FILE` or the process environment
`ETHERVIEW_LOAD_API_KEY` for an authenticated profile. Never place a key in a
URL, route argument, report metadata, or command-line value.
