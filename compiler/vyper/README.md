# Pinned Vyper runtime

ADR-0047 owns the boundary. `make compiler-install` builds a dedicated
CPython 3.13.15 / PyInstaller 6.22.2 / Vyper 0.4.3 directory in
`.local/vyper/runtime`. `requirements.lock` pins all compiler/build dependencies
and hashes; `--only-binary` selects authenticated wheels. The Docker stage uses the
`python:3.13.15-slim-trixie` version tag, resolves every ELF against
the final distroless root and tests the helper there as the production user.

The helper accepts `--self-test` or
`--compile <max-input-bytes> <max-output-bytes>` with Standard JSON on stdin.
The Go parent supplies effective configured limits as positive canonical decimal
arguments; source JSON cannot override them. Runtime schema v2 rejects older
helpers during startup, so rebuild the complete runtime and drain bound jobs
when upgrading.
It has no Python CLI or package installer. Compiler sources and interfaces are
in memory. The read-only runtime manifest covers every installed file; Go
validates that tree before startup and every execution. Linux self-test proves
the 512 MiB address-space limit refuses an oversized allocation as well as
file-write, unrelated-read, socket and child-process denial. Audit hooks are
defense in depth for trusted Python/native compiler dependencies.

Real compiler fixtures can be regenerated after `make compiler-install`:

```sh
python3.13 compiler/vyper/fixtures.py \
  .local/vyper/runtime/etherview-vyper internal/verify/testdata/compiler/vyper
```

## Dependency audit corrections

`make security-check` runs `audit.py` using its separate hash-locked pip-audit
environment. It scans every compiler/build dependency and fails on unknown
advisories, skipped packages or audit-service failure. Its retained raw report
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
