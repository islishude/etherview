# Vyper WASM distribution

[ADR-0053](../../docs/decisions/ADR-0053-vyper-wasm-distribution.md) replaces
native runtime distribution with Pyodide 0.29.3 / CPython 3.13.2 and one
architecture-neutral compiler package per supported version. Node SEA remains
host-native. Production deployment and native release acceptance are tracked
by P30-T119; the local matrix is not production release evidence.

`versions/index.json` authenticates the official compiler artifact; per-version
`.lock` files authenticate dependencies. Version 0.2.0 uses its pinned upstream
source archive. Compiler implementation bytes and reference fixtures remain
unchanged. Historical native Python tooling is retained for reference generation.

`make compiler-install` prepares the shared WASM runtime, the 0.4.3 package and
Node SEA for ordinary local regressions. `make test-vyper-matrix` builds all 26
packages and compares original and modified fixtures using fresh SEA processes.
`make test-vyper-wasm-candidate` runs the warm candidate comparison. Outputs live
under `.local/vyper-wasm/`.

After building the packages, create the common release transports once:

```sh
python3 compiler/vyper/wasm/release.py --output .local/vyper-releases
```

The output directory must not already exist. The builder validates package and
shared identities and atomically publishes the complete set of 26 deterministic
archives and unsigned descriptors. Native AMD64 and ARM64 consumers must use
these same archive bytes. Building transports does not assert matrix or
production acceptance.

The catalog signer requires one acceptance record per Linux architecture with
`native_linux`, `monolith`, `split`, the production `host_sha256`, pinned `shared_sha256`, and
`descriptors` mapping filenames to their SHA-256 digests. Each record's `matrix`
maps all 26 versions to `cases`, `fixtures_sha256`, `package_sha256`,
`shared_sha256` and `executor_sha256`. The signer checks the exact repository
fixture counts and contents and recomputes each composite executor identity.
`wasm/release.mjs` defines these identities. Run
`node compiler/vyper/wasm/production-matrix.mjs` against the built production
image to emit a matrix record; successful Hardhat monolith/split tests add the
production acceptance flags. Non-Linux records remain diagnostic. Only native acceptance jobs may
produce these records after passing the corresponding tests; never synthesize
acceptance from descriptor presence or local candidate results.

Once both native acceptance records are available, merge their architecture
objects without modifying their contents and assemble a signed catalog:

```sh
node compiler/vyper/catalog.mjs --artifacts .local/vyper-releases \
  --origin https://compilers.example/vyper/ --acceptance acceptance.json \
  --key-file /secure/path/vyper-ed25519.pem --expires-at 2026-12-01T00:00:00Z \
  --output catalog.json
```

Choose a future expiry. The v2 signer refuses missing versions or architectures,
incomplete/stale matrices, stale production evidence, digest changes and
non-Ed25519 keys. It never overwrites an existing output. Publish archives before
their signed catalog only under explicit release authorization. Keep artifacts
needed by bound retries. No production signing key is stored in this repository.

## Dependency audit corrections

`make security-check` runs `audit.py` using its separate hash-locked pip-audit
environment. It currently scans the retained native reference dependency lock and fails on unknown
advisories, skipped packages or audit-service failure. The complete WASM dependency inventory remains a P30-T119 release gate. Its retained raw report
is `.local/vyper/audit/report.json`.

Two database records incorrectly include the exact official Vyper 0.4.3 wheel
in an open-ended affected range. Corrections are restricted to package `vyper`,
version `0.4.3`, the pinned wheel SHA-256 and these exact advisory IDs:

- `PYSEC-2023-142`: the [maintainer advisory](https://github.com/vyperlang/vyper/security/advisories/GHSA-5824-cm3x-3c38)
  identifies 0.2.15, 0.2.16 and 0.3.0 as affected; the fix is in 0.3.1.
- `GHSA-vgf2-gvx8-xwc3`: the [maintainer advisory](https://github.com/vyperlang/vyper/security/advisories/GHSA-vgf2-gvx8-xwc3)
  identifies versions through 0.4.0 as affected and 0.4.1 as patched.

These are version-range corrections, not acceptance of vulnerable compilation
or general advisory suppressions. Changing compiler version or adding another
advisory requires a new review; the raw findings remain available.
