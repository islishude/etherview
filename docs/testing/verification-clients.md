# Hardhat and Foundry acceptance

[Testing index](../testing.md) · Commands run from the repository root.

- [Commands](#commands)

## Commands

- `make test-hardhat3-provider-compat`: install the exact dependency-locked
  Hardhat 3 fixture and run its Etherscan-provider flow against a Go-owned real
  compatibility handler with in-memory authentication and a stateful fake
  backend. This is the fast provider-wire regression only; it has no chain,
  database, compiler, or production process. The fixture uses viem for ABI
  encoding/decoding and local EIP-155 signing; its Node regressions also check
  raw transaction recovery, gas pricing, numeric bounds, and key-error redaction.
- `make test-hardhat3-e2e`: rebuild the host-native production application and
  independent Hardhat client images, then run the real Hardhat CLI against independent
  Anvil/PostgreSQL datasets in monolith and complete seven-application-role
  layouts. It compiles and deploys an implementation plus EIP-1967 proxy,
  verifies both sources through the public compatibility API and official
  compiler catalog, polls durable proxy verification, upgrades and rebinds the
  proxy, and checks normalized public/persistent parity. The same pinned
  OpenZeppelin fixture proves constructor-derived verification: verifying the
  `TransparentUpgradeableProxy` after its deployment transaction must
  automatically publish the constructor-created `ProxyAdmin` from the exact
  creator code epoch, without a direct ProxyAdmin address-verification job.
  The gate checks the CREATE trace/transaction/FQN provenance, derived native
  API projection, management availability, and monolith/split parity. The same pinned
  fixture deploys `solady@0.1.26` legacy LibCWIA with packed
  owner/uint256/uint16/bytes arguments, derives its schema from canonical
  helper calls in the dual-compiled Solidity AST, checks exact raw and typed
  dynamic arguments, exercises a delegated storage write, binds it as mechanism
  `cwia`, and proves it has no upgrade history in both topologies. A separate
  verified `MyAccount` deployment shares the implementation runtime code hash;
  before the factory-owned implementation address is verified, the gate
  requires CWIA Method, calldata, emitted event, and root Trace decoding to use
  that artifact without opening proxy writes. The `all` or `api`
  process consumes the durable job, resolves and checksum-validates the
  architecture-independent `emscripten-wasm32` artifact, and runs each bounded
  Standard JSON compilation in a fresh permission-restricted Node SEA subprocess.
  The first real compilation populates the Compose named cache volume; the
  harness force-recreates the compiler-owning service, submits another job for
  the same version, and proves the identical artifact survives without a new
  installation. Final cold-cache installation is serialized by the writer
  PostgreSQL advisory-lock domain; downloads remain outside that lock and a
  validated persistent hit does not acquire it.
  The same production topology also consumes the integrity-pinned official
  `@safe-global/safe-contracts@1.4.1` artifacts, deploys the Safe singleton and
  SafeProxyFactory, and creates a canonical SafeProxy through the factory's
  internal `CREATE2`. The proxy uses an empty initializer so this
  proxy-identity gate does not turn Safe's ordinary setup delegatecall into
  separate Diamond-router evidence. The gate requires the exact 1.4.1 runtime
  fingerprint, published Trace creation fact, generation-fenced `proxy@2`
  evidence, singleton-only detection projection, absence of ordinary implementation interaction
  authority, public API projection, and monolith/split parity. Safe execution
  and setup behavior remain outside this detector gate.
  No application or test client receives a Docker socket or CLI. The test
  fails when the official catalog or compiler cannot be downloaded. CI builds
  and exercises the native production image independently on AMD64 and ARM64;
  no deployment or test field fixes a container platform. CI uses
  `make test-hardhat3-e2e-prebuilt` after loading both images. Its explicit Go
  package timeout matches the existing 30-minute E2E context, rather than Go's
  default 10-minute alarm. Production compiler deadlines and input limits are
  unchanged. Redacted
  Hardhat output, Compose state/logs, and proxy summaries are retained on
  failure. The E2E-only API environment permits Docker fake-IP download
  networks while retaining the exact HTTPS origin, TLS, size, and SHA-256
  checks; the base deployment never receives that escape hatch.
  Shared verification topology scheduling uses a waiting subtest group with two independent
  result slots. Each topology keeps its own Compose project, dynamic ports,
  PostgreSQL and compiler-cache volumes, and diagnostic directory. Both children
  and their cleanup must finish successfully before normalized parity is checked
  and application evidence is written. A failed child does not cancel its sibling;
  the existing shared 30-minute deadline still bounds the run. Cleanup failure,
  missing results, an expired context or a parity mismatch blocks acceptance.

`VERIFICATION_PARALLEL=1` remains the Makefile default pending native AMD64/ARM64
performance acceptance. Native AMD64/ARM64 CI explicitly sets
`VERIFICATION_PARALLEL=2` to run both topologies concurrently. Only `1` and `2` are
accepted. After building the same
application/client images,
measure separately with:

```sh
make test-hardhat3-e2e-prebuilt VERIFICATION_PARALLEL=1
make test-hardhat3-e2e-prebuilt VERIFICATION_PARALLEL=2
```

Run in the order 1/2/2/1 on each matching native runner. Every invocation creates
fresh topology data/cache volumes; keep the images fixed and remove the previous application result file before each run.
Record E2E and per-topology wall time (including cleanup), CPU, peak memory,
OOM/timeouts, leaked resources, full assertion results and whole CI job time.
Change the local Makefile default to 2 only when both parallel runs beat both serial runs on
each architecture without correctness or resource failures. A local ARM64 run
is diagnostic and cannot establish GitHub or AMD64 performance. The four
Vyper production fixture shards run independently of both application suites.

The Docker-free `TestVerificationTopologyScheduling` subprocess regression exercises
both concurrency settings, overlap/completion, either topology failing, missing
results, parity mismatch, deadline expiry and cleanup failure. Run it with
`go test -race -tags='runtimee2e verificatione2e' ./e2e/runtime
-run '^TestVerificationTopologyScheduling$' -count=1`.

- `make test-hardhat3-offline-compile`: run the dependency-locked Hardhat
  client image with no network and compile its production fixture through the
  image's exact `solc@0.8.30` solc-js path. The production verification E2E
  runs this preflight automatically. Product verification still downloads and
  checksum-validates the official catalog artifact through the application;
  only the independent client's fixture compilation is offline.
- `make test-foundry-offline-compile`: run the version-pinned Foundry
  v1.9.0 client image with no network and force a Solidity 0.8.30 rebuild. The
  image build already compiles once online and once offline, while this target
  independently proves the loaded client contains the complete compiler cache.
  Foundry's disposable project build cache is disabled: each Compose command
  runs in a fresh one-shot container, while only the image-owned solc toolchain
  cache is required to survive between the online and offline build checks.
- `make test-foundry-e2e`: rebuild the host-native production application and
  independent Foundry client images, run the offline compiler preflight, and
  exercise source verification against fresh Anvil/PostgreSQL datasets in the
  monolith and complete seven-application-role layouts. These are two separate,
  mutually exclusive Compose-profile runs, not two API services in one run:
  `monolith` starts `etherview` with `roles=all`, while `distributed` starts
  `api` plus the split worker services. The Foundry Compose override therefore
  names both base services so Compose can merge the same verification settings
  into the service selected by the active profile; the client URL selects
  `etherview:8080` for monolith and `api:8080` for distributed. Collapsing the
  two service entries would either drop one production topology or stop the
  test from matching the production service graph. Forge deploys a Factory
  whose constructor creates a second contract with constructor arguments and
  immutable fields, checks that both addresses initially have no verification,
  submits Standard JSON through the exact `/v2/api?chainid=1` custom-verifier
  URL only for the Factory, watches status by POST, and confirms the public
  source and ABI. The child must receive exactly one derived job/result/
  publication/attempt with exact CREATE transaction provenance, decoded
  constructor arguments, runtime immutable references, and
  `verification_origin=factory_derived`; the parent must list that child. A second
  Forge invocation must short-circuit as already verified without creating a
  job. Normalized snapshots require one successful job, result, and
  publication; exact constructor arguments and immutable references; the
  official Solidity 0.8.30 compiler URL and SHA-256; and
  `emscripten-wasm32`, `node_solcjs_v1`, `trusted_subprocess`, and executor
  digest parity. `VERIFIER_API_KEY` is the client's only secret and is passed
  by environment name, never a command argument, URL, Compose value, mount, or
  artifact. The Go harness redacts configured secrets from command and failure
  diagnostics. CI uses `make test-foundry-e2e-prebuilt` after loading both
  images on native AMD64 and ARM64, stays quiet on success, and uploads failed
  `etherview-foundry-e2e-*` bundles for seven days. The existing Hardhat 3
  source/proxy/upgrade gate remains independent and unchanged.
  The same production-topology suite also deploys the pinned ethereum/sys-asm
  EIP-7002 creation bytecode, submits its four-file Geas bundle through the
  native address-verification endpoint, and requires exact runtime/creation
  matches, no catalog generation, `go-module`, `etherview_geas_v1`, empty ABI,
  and monolith/seven-role publication parity.
  Both Hardhat and Foundry verification overlays explicitly disable ENS on
  every application role and construct no ENS workers. Their isolated Anvil
  fixture is verification input, not an ENS source; ENS behavior remains owned
  by `make test-runtime-e2e` and real v1/v2 transactions on the same current-chain Anvil fixture.
