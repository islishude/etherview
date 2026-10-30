# Preview Testing

[Documentation index](README.md). Run shell commands from the repository root.

### Preview gateway regressions

`make test-preview-gateway` runs the shipped Caddyfile against the pinned
`caddy:2.11.7-alpine` image with isolated upstreams and a test-only trusted CA.
It covers domain routing, raw IPFS paths, CORS, TLS, request limits, streaming,
cancellation, deadlines and WSS/SSE idle connections. Origin regressions cover
arbitrary website, extension, null and absent Origins for Geth RPC/preflight/WSS,
plus the unchanged Beacon/Bundler allowlist and its override. These checks do
not establish an actual MetaMask extension connection. The official image is
pulled through `make preview-images`; no custom gateway binary is built.

Preview browser acceptance requires the locked pnpm workspace and Chrome, or
`PLAYWRIGHT_USE_BUNDLED=1` with `pnpm --dir web exec playwright install chromium`.
The metadata gate runs real SIWE, CSRF, wallet URL and node CORS/WSS/SSE browser
checks only after the first mined transaction, beyond the genesis waiting period.
Metadata acceptance requires the explicitly allowlisted local IPFS image to
project as `available` with its exact HTTPS navigation URL while the worker
successfully fetches metadata text. Navigation regressions retain default-empty
and unlisted-local-origin rejection, exact host/port matching, confirmation before
requests, cancellation, and opener-free/no-referrer navigation. Config and app
regressions prove that navigation exceptions never authorize media or metadata
network fetches.
It trusts only the test certificate SPKI and explicitly grants local-network
permission to its test pages; it never disables general TLS verification.

Full acceptance runs `make test-preview-metadata`, `make test-preview-consensus`,
`make test-preview-contracts` and `make test-preview-x402`, plus `make check`, `make test-e2e`,
`make test-runtime-e2e`, and `make test-rustfs`. Preview clients use the trusted
HTTPS virtual hosts with test-owned ephemeral port mappings. The actual app
still owns same-origin browser security and session checks.

`go test ./e2e/previewmetadata -run TestPreviewGenesisSystemContracts` checks
the Preview template against the pinned Geth system contract allocations in the
ordinary Go test suite. Receipt polling retries only null results and Geth's
specific transaction-indexing RPC error within the existing three-minute wait;
other RPC/transport failures and mismatched transaction hashes remain fatal.

## Real Lighthouse Preview acceptance

`make test-preview-metadata` now uses the real PoS Preview network and locally
signed transactions. It preserves the offline Kubo, two-version NFT metadata,
restart, Amsterdam header and exact native-transfer acceptance in split and
all-role layouts. Every test owns its generated network bundle, Compose project,
volumes and ephemeral loopback ports; it never overwrites the daily Preview.

All four Preview E2E gates also own a separate compiler bind directory. They
use the same restricted initialization and cleanup tool as daily Preview,
with UID/GID 65532 and mode 0750. Cleanup stops the Compose project before
removing the private cache and fails the test if either step fails. Reports
remain host-owned and outside this cache. The existing Preview CI gates verify
this lifecycle on native Linux; Docker Desktop evidence alone does not establish
native Linux host cleanup behavior.

`make test-preview-consensus` runs the fixed Geth v1.17.10 / Lighthouse v8.3.2
combination with Gloas at genesis and the durable RustFS archive. Daily Preview,
metadata acceptance and consensus acceptance share three-second slots. Both
acceptance suites verify the live Beacon slot duration and record it in their
reports. The generator is part of the network fingerprint, so older six-second
bundles require explicit reinitialization rather than silent reuse.
It checks real blob transactions with cell proofs, exact Beacon/execution associations,
consensus-first slots, independent Beacon/S3/API byte readback, checksum and
commitment identity, read-only API credentials, client/object-store outages,
core recreation during archive outages in both role layouts, failed and repeated
archive initialization, container recreation and monolith publication.
Writer IAM enforcement of conditional headers remains deferred on RustFS 1.0.4;
the read-only denial check does not establish that property. Reevaluate 1.0.5
against the [IAM clearing conditions](operations.md#preview-archive-iam-limitation);
a passing `make test-rustfs` establishes application conditional-create behavior,
not writer-policy enforcement.
The test waits for a nonzero
finalized epoch rather than treating container readiness as consensus acceptance.
It has a 22-minute scenario budget and a 25-minute test-process deadline.
Diagnostics and `consensus-report.json` remain in the test's temporary artifacts
directory. These local private-network results do not prove public historical
retention, production backup/restore, or native CI execution.

Preview lifecycle helpers and their unit tests live in `preview/scripts/`.
`make preview-network-check` exercises immutable reuse, incomplete material,
configuration drift, tampering, symlinks and rejected single-file overrides.
It also checks Preview env creation, key-pair reuse and validation, operator-setting
preservation and 0600 permissions. Deterministic short-scalar cases verify
32-byte private-key encoding, unchanged public identity and reuse without rotation.
Preview Compose behavior is exercised by the metadata, consensus, contracts
and x402 E2E gates. Production deployment render commands pass
`--project-directory .`; the shared Go Compose helper
anchors tests at the repository root, while the standalone x402 fixture
explicitly retains its own directory. Runtime, compiler, schema and RustFS
tests continue to exercise the external-chain template.
Run the metadata, consensus, contracts and x402 Preview gates, `make check`, `make test-rustfs` and
`make test-runtime-e2e` for changes to this deployment.

## Preview application contracts acceptance

`make preview-contracts-check` uses pinned Anvil 1.8.6 to reproduce the genesis
allocations and exercise registration, commitment age/expiry, pricing, renewal,
transfer/reclaim, permissions and reverse/forward mismatch. Time advancement is
confined to this contract test; it does not establish PoS or Bundler acceptance.

`make test-preview-contracts` builds the production image and pinned Rundler helper,
then runs the Go-owned real Geth/Lighthouse Preview gate. The prebuilt equivalent
is `make test-preview-contracts-prebuilt` after `make docker-build preview-images`.
It checks block-zero code/storage/nonces/balances and authenticated genesis import,
standard Bundler RPC and rejected debug methods, signed successful/reverted
UserOperations, bad signatures, insufficient prefund at submission (Rundler can
estimate unfunded accounts), duplicate submission,
local `.test` registration/renewal/transfer, generated API data and real browser
pages in split/all layouts. Container recreation retains state. PostgreSQL ENS
generations must have no official source. Tests own their project, network and
volumes; pending CLI secret files are separately scoped and removed, while
redacted reports and screenshots remain with the Preview diagnostics.

Changes also require `make generate-check`, `make lint-go`, `make docs-check`,
`make plan-check`, `make check`, `make test-integration-race`, `make test-schema-e2e`,
`make test-runtime-e2e`, `make test-e2e`, and the existing Preview metadata,
consensus and x402 gates. Local evidence cannot close unrelated live payment or
cloud acceptance milestones.

## Metadata gate

- `make test-preview-metadata`: rebuild the host-native production image and
  run a Go-owned local Kubo acceptance gate against a unique full Preview
  Compose project with fresh volumes and random loopback ports. The pinned Kubo
  runs offline with `Gateway.NoFetch=true`; no public
  gateway or P2P retrieval participates. Its startup script imports the reviewed
  CIDv1 directory with `/metadata.json` and verifies the exact CID, 205 bytes and
  SHA-256 `a87d3d327d1a2c7f839000c080e07cd152b49ddf653f1a5afa5144eeec103d8d`.
  The test builds `cmd/ipfs` and exercises wrapped/repeated uploads, empty and
  binary file roundtrips, unknown-CID failure, and gateway restart persistence.
  Trusted TLS must pass, an untrusted CA must fail, and the HTTPS gateway must
  not expose the management API. The reviewed solc 0.8.30 ERC-721/ERC-4906
  creation artifact remains unchanged; the funded Hardhat development account
  signs transactions locally for the real Lighthouse PoS network.
  Both the initial version and no-Transfer `MetadataUpdate(1)` version require
  exactly one successful attempt, exact JSON/content type/hash/size, canonical
  holdings/inventory, source/document/job history, and restart-stable database
  persistence. Every connected/resolved IP must belong to the owned gateway;
  `policy_bypassed=true` is mandatory. A reused connection may omit new DNS
  results. Reports record application/Kubo/Caddy image identities, offline mode,
  CID, content digest and network evidence. Success and failure artifacts are
  retained and their directory is printed. Run `make preview-cert` first (also
  after upgrading a Preview that lacks the wildcard gateway certificate).
  CI runs the same test through `make test-preview-metadata-prebuilt` in the
  independent `Preview metadata, consensus, and blobs E2E` job on Ubuntu AMD64
  for pull requests, main pushes and the daily schedule.
  The job installs pinned mkcert 1.4.4 and uses `make preview-cert` with an
  ephemeral runner-local CA and system trust. The wildcard gateway key remains
  0600 and only Caddy mounts it. The job loads the production application image,
  pulls official Caddy and node images, and uploads reports/diagnostics on success
  or failure for seven days; certificate and CA private keys are never uploaded.
  This gate remains outside `make check` and does not establish public IPFS
  availability. Image pulls/builds require normal registry/toolchain access.

## x402 billing gate

`make test-preview-x402` builds the production image and local helper images,
then exercises the same Preview Geth/Lighthouse deployment in a disposable
project with isolated ports, network material and volumes. The prebuilt form
is `make test-preview-x402-prebuilt` after `make docker-build preview-images`.
It checks both EIP-3009 and Permit2, exact allowance consumption, replay,
chain/credit equality, genesis predeployment, Facilitator recreation, API restart and spending during
Facilitator outage in split and all-role modes. It also pauses the validator,
checks persisted `settlement_pending` with the exact broadcast hash, resumes
mining and reconciles one credit without resending. Controlled RPC tests cover
pre-broadcast rejection, receipt indexing, reverts, wrong hashes, broadcast
uncertainty and receipt errors/timeouts. Its HTTP loopback test origin
isolates payment/runtime acceptance; it does not establish browser wallet or
production Permit2/proxy compatibility. The existing Preview metadata/browser
and consensus gates remain required. The contract fixture
checks genesis token storage, owner balance and Facilitator funding. No local test closes P60-T17.

## Genesis verification inputs

`make preview-verification-check` checks the source bundle checksums, reproduces
local ENS/Local USD compiler inputs, and runs complete-inventory, wrong-identity,
publication, repeat-run, compiler-failure and bounded-timeout regressions.
`make preview-network-check` also runs the helper regressions;
`make preview-contracts-check` also checks generated verification inputs.

`node preview/verification/generate.mjs` explicitly regenerates `local.json`
and `inventory.json` after reviewed contract changes. It uses the same pinned
compiler settings and source hashes as the genesis application manifest and
resolves all imports into inline content. Ordinary startup does not run this
generator. The other JSON bundles retain their upstream source URLs/revision;
Sourcify snapshots provide inputs only, never local verification evidence.
The SimpleAccount implementation shares its factory's compilation unit, using
the pinned account-abstraction revision and OpenZeppelin 5.0.2. The historical
deployment JSON at that revision contains older account sources and a different
OpenZeppelin version; it must not replace the matching checked-in input.
The system assembly bundle records its explicit PUSH3 adaptation for the
historically deployed BeaconRoots bytecode; local compilation must match the
actual genesis bytes exactly.

Real acceptance runs `make verify-preview-genesis` against Geth/Lighthouse and
the production API, requiring all 24 non-empty allocations to publish, then
repeats it to prove reuse. This check is additive to existing Preview chain,
Bundler, ENS and billing acceptance; a mocked helper test does not establish
compiler execution or durable publication.
