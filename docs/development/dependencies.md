# Dependencies and license inventory

[Development index](../development.md) · Commands run from the repository root.

- [Dependency maintenance](#dependency-maintenance)
- [License inventory](#license-inventory)

## Dependency maintenance

`.github/dependency-registry.json` owns dependencies that require coordinated
updates or dedicated review. Each entry identifies its public release source,
exact linked version positions, update policy, permitted generated locks and
Dependabot exclusions. Versions stay in their existing owning files; the
registry matches them rather than maintaining a second current-version value.
Missing positions, conflicting versions and duplicate ownership fail the gate.
Add new Makefile `*_VERSION` settings to the registry in the same change.
JSON pins use a `jsonPath` array of object keys and a template for the string
value only; whitespace, property order and surrounding commas are not pins.
Updates serialize JSON with two-space indentation and a final newline, preserving
unregistered field values. Non-JSON pins retain exact text templates and counts.

- `make dependency-check` queries upstream versions without modifying files.
  `DEP=govulncheck` restricts discovery. Output distinguishes available automatic
  candidates, review-only updates, no updates and upstream errors. Network
  discovery is intentionally outside deterministic `make check`. GitHub requests
  use an optional `GH_TOKEN` for authenticated rate limits; the workflow supplies
  its built-in read-only token automatically.
- `make dependency-update DEP=govulncheck VERSION=1.9.0` illustrates preparing
  an exact newer version; use a version newer than the current pin. Versions
  omit a leading `v`. The command requires a clean working tree, prepares an
  isolated temporary checkout, runs package installation/lock generation and
  candidate regressions, then applies the validated patch without committing.
  Failed preparation leaves the original working tree unchanged. An equal
  version, downgrade, prerelease or review-only dependency is rejected.
- `make test-dependency-updates` checks the registry and runs offline regression
  tests. It also runs through `make test-workflow-scripts` and ordinary CI.

Automatic candidates stay within the current major version; `0.x` candidates
stay within the current minor. Registered npm dependencies follow only
`dist-tags.latest`, including explicit version requests; `next` and other tags
are not candidates. Missing, unpublished, prerelease or deprecated `latest`
versions fail discovery without falling back to other versions. A `latest`
version outside the automatic upgrade boundary is reported for review.
One dependency and all its linked locations form
one PR. pnpm and Python locks are regenerated with their package managers.
Only govulncheck is bumped as part of the initial automation installation;
other discovered updates receive separate review.

Dependabot retains monthly ownership of ordinary Go/npm/Actions/container
updates, including nested E2E Dockerfiles and deployment Compose. Registry
exclusions prevent it from independently changing coordinated pins. Node SEA,
Pyodide, Foundry, Reth, the solc wrapper and exact license-review bindings are report-only.
Foundry upgrades also update the release commit assertions and regenerate contract
artifacts; Reth upgrades require the Preview payment-chain acceptance.
The uv entry tracks the supported minimum: setup-uv already resolves a version
inside the configured range, so raising that compatibility floor is a reviewed
policy change. Existing Vyper discovery owns new compiler additions. Historical
compiler locks, Solidity fixture versions, sample inputs and authenticated
hashes are not generic upgrade targets.

The weekly workflow, permissions, PR approval and recovery procedure are in
[operations](../operations/maintenance.md#dependency-update-automation). A successful candidate
check is not full production acceptance; all existing PR CI gates still apply.

## License inventory

Keep maintained project and dependency notices in `licenses/`; the root
`LICENSE` is a symlink for license discovery. Production images copy that directory directly. Preview uses the official
Caddy image and its upstream distribution notices. Builds verify npm/Node, WASM and private
ELF notices without collecting or downloading license texts. When upgrading
dependencies, review and update the corresponding texts alongside their locks.
Run `make license-check`; image builds additionally compare the actual Linux
builder Node and Debian notices against the checked-in inventory.
