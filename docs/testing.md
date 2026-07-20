# Testing and Verification

The Makefile is the command source of truth. Only implemented targets are
listed as runnable commands here; future gates remain in their owning plan
until the Makefile target exists.

## Common Gates

- `make toolchain-check`: require the repository-pinned Go, Node, and npm
  versions before generating or validating artifacts.
- `make plan-check`: validate plan links, IDs, statuses, dependencies, evidence,
  and parent/child state.
- `make generate-check`: regenerate OpenAPI, SQL, and embedded frontend outputs
  and fail on a diff. It snapshots the checked-in baseline in a temporary
  directory before regeneration, so it also works before the repository has an
  initial Git `HEAD`.
- `make test`: Go and frontend unit tests.
- `make test-race`: Go tests with the race detector.
- `make test-e2e`: build the embedded SPA and a temporary Go E2E binary, then
  run Playwright against that embedded distribution. Local runs use installed
  Chrome; CI sets `PLAYWRIGHT_USE_BUNDLED=1` after installing Playwright
  Chromium.
- `make test-integration`: migrations and PostgreSQL integration tests against
  the disposable database named by `INTEGRATION_DATABASE_URL`; the target
  explicitly skips when no URL is supplied.
- `make lint`: Go formatting/vet and TypeScript type checking.
- `make security-check`: `govulncheck`, dependency audit, secret scan, and
  security-focused tests.
- `make license-check`: Go and production frontend dependency license policy.
- `make deployment-check`: Docker build checks, Compose profile validation,
  and Helm lint/render checks.
- `make compose-schema-smoke`: migrate a disposable PostgreSQL Compose volume
  twice and verify schema compatibility.
- `make check`: source, unit/race, security, license, generation, and deployment
  gates. Browser, integration, parity, load, and soak suites run as explicit CI
  targets because they require dedicated services or runtimes.

Runtime parity, load, and soak commands remain tracked by P60 and P70 and will
be added here only with their executable targets.

## Evidence Rules

- Record the exact target/command and a concise pass/fail summary in the child
  plan. Do not paste full logs.
- A targeted test is required for every fixed regression.
- Integration tests that require optional local services must be reproducible
  through documented Compose profiles.
- Load and soak evidence records the revision, dataset, hardware, RPC behavior,
  duration, throughput, latency, error rate, and index lag.
