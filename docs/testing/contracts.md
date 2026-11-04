# Contract verification acceptance

[Testing index](../testing.md) · Commands run from the repository root.

- [Sourcify transport gates](#sourcify-transport-gates)
- [Unified proxy detection acceptance](#unified-proxy-detection-acceptance)
- [Bytecode database source discovery acceptance](#bytecode-database-source-discovery-acceptance)
- [Independent verification acceptance](#independent-verification-acceptance)

## Sourcify transport gates

Sourcify transport/configuration changes run
`go test -race ./internal/config ./internal/app ./internal/verify -run TestSourcify`.
The private-network cases exercise default rejection, explicit local HTTPS
access through the production dialer, application configuration wiring, strict
boolean overrides and independent client policies. TLS and redirect rejection,
request bounds, hostile responses and explicit source-publication consent remain
covered by the Sourcify regressions. Credential changes also run
`go test ./internal/httpapi -run TestVerifierV2SourcifyEtherscanCredentials`
and `go run ./e2e/cmd/testintegration -packages ./internal/verify -race -run
TestSourcifyCredentialsPostgresWorkerRestart`. These cover separate Etherview
and Etherscan credentials, response/log redaction, authenticated encryption,
active-job deduplication, durable restart, and forwarding to a local HTTPS
fixture without local contract publication. Web tests cover optional omission,
consent reset and clearing completed mutation/form state; bilingual browser
coverage lives in `web/e2e/sourcify.spec.ts`. Source import additionally runs
`go test ./internal/verify ./internal/httpapi -run
'TestSourcifyImportV2|TestVerifierV2SourcifyImport'` and
`go run ./e2e/cmd/testintegration -packages ./internal/verify -race -run
TestSourcifyImportPostgresLocalPublication`. Local HTTPS fixtures verify GET-only
retrieval, exact large source-chain IDs, independent local target identity,
supported compiler preparation and hostile-source
rejection; PostgreSQL verifies dual compilation, local source/ABI publication,
mismatch rejection and reorg fencing. Browser checks cover direct import,
post-publication import, separate source/metadata selection and replacement,
keyboard service cards, 390/1440px layouts and native
select icon proportions. These are local fixtures, not live Sourcify acceptance.
Run `make deployment-check` for its Compose
and Helm configuration surfaces.

## Unified proxy detection acceptance

The immutable `internal/enrich/testdata/proxy-observation-baseline.json` fixture
was captured from the pre-unification implementation at
`deebb7a9b0118df12a3c1b89b663d36e067eede7`. Its test independently
compares authoritative ordinary/clone/CWIA relations, empty code, negative and
Safe facts; acceptance never regenerates it. Exact OpenZeppelin artifact,
immutable admin/Beacon authority, error classification, generation/reorg and
write-fence regressions remain mandatory alongside the unified resolver tests.

Run focused enrich/app/query/config tests, PostgreSQL integration/race,
`make test-e2e`, `make test-runtime-e2e`, `make test-hardhat3-e2e`, and
`make test-foundry-e2e` for the cutover. The production suites exercise the
shared configuration and public contract without framework/visibility flags
in both monolith and split roles. Run
`make test-schema-e2e` for the fresh baseline, and generation/source/docs/plan
and common gates. Preserve independent pre-change evidence, and record current
local results separately from native CI and deployment review.

## Bytecode database source discovery acceptance

Run `go test ./internal/verify ./internal/config -run TestBytecodeDB` for
search-only transport, hostile response, candidate and configuration regressions.
Private-network exception cases cover default rejection, explicit loopback access,
Fake-IP and mixed DNS answers, strict boolean/environment overrides and client
policy isolation. They retain real TLS verification and reject redirects after
opt-in; the ordinary hostile-response suite uses the same production dialer.
The integration-tagged `TestBytecodeDB` cases exercise real PostgreSQL discovery,
per-chain cursor isolation, request admission, retry/cooldown, lease fencing,
local dual-compilation flow,
creation-input fallback, Yul import, source origin, manual priority and the
single automatic compiler slot. The controlled client fixture never grants source publication directly.

`TestWorkerCompilerDownloadRecoveryAndBudget` covers a cold compiler download
recovering after HTTP 503 and bounded exhaustion. Download regressions cover
stable error labels, cancellation, policy rejection and lease loss during
backoff. `TestBytecodeDBCompilerFailureRecovery` verifies cooldown, competing
restarted workers, immutable job/compiler binding, successful publication after
recovery and deterministic checksum-failure retention in PostgreSQL.

Changes require `make check`, `make test-integration-race`, `make test-e2e`,
`make test-schema-e2e`, `make test-runtime-e2e` and affected Hardhat/Vyper
production suites. `make test-hardhat3-e2e` also runs `TestBytecodeDBProductionE2E`: a
test-owned HTTPS provider on an isolated Docker network supplies Solidity and
Vyper fixtures to both production topologies. All-role mode uses strict
public-address checks; split mode explicitly enables the Fake-IP exception on
an isolated `198.18.0.0/15` network. Both retain TLS verification and the
production SEA compiles and publishes each source locally. Public remote
health, actual public-source coverage, production compiler execution and native
CI acceptance are separate evidence.

## Independent verification acceptance

P30-T153 requires API-only admission without an installed executor, verification
component ownership and all/split parity. Run worker wake/polling tests, real
PostgreSQL `TestSourcifyImport` concurrency and immutable-input restart tests,
and NATS accelerator outage/coalescing tests. Production coverage uses at least
two API and two verification instances, tests lookup failures through task
polling, and checks crash takeover and stale-publication rejection. Run common,
generation, lint, deployment, schema, integration race, browser and runtime E2E
and affected Hardhat/Vyper suites. Local results do not close P70 release gates.
