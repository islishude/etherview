# ADR-0054: Application-Locked Vyper Distribution

Status: accepted

## Decision

P30-T128 replaces ADR-0049/ADR-0053's independently signed catalog and hosted
compiler packages. The complete supported version set, the explicit dual-Pyodide policy,
fresh restricted SEA processes, exact matching and canonical lease-fenced
publication remain mandatory.

The application embeds `compiler/vyper/application-lock.json`, the existing
Python adapter. P30-T130 retires Vyper 0.2.0 and its embedded source archive.
All supported compiler/dependency archives are fetched
from exact `files.pythonhosted.org` HTTPS URLs in the lock. Runtime requests do
not query package indexes, resolve dependencies, invoke pip or execute build
hooks. User input cannot select URLs, packages, runtime or assembly rules.

The lock records exact versions, filenames, URLs, archive sizes and SHA-256,
selected Python/Pyodide versions and the expected complete package manifest.
The same bounded Go assembler runs in build checks and production. It removes
native extensions and installation-only data, rejects unsafe paths, links,
conflicting files and resource-limit violations, and injects the embedded
adapter. The assembled ZIP and manifest must match the application lock before
installation. Locks change only through reviewed application releases; upstream
index changes or yanking never silently alter an existing task.

Raw archives are cached by SHA-256 and assembled packages by manifest digest.
Every use authenticates content. Cold assembly has one overall deadline in
addition to archive, expanded-size and entry-count bounds. Staging is private;
installation rechecks and atomically publishes under the existing PostgreSQL
cache lock. No transaction, snapshot or writer lock spans network requests.
A complete warm cache works offline; missing upstream content never falls back
to another version, host Python or unapproved bytes.

After local runtime self-tests, API/all registers an immutable application
snapshot in the existing PostgreSQL provenance tables. These internal generation
IDs are historical identities, not an independently refreshed distribution
service. The process uses its own registered generation, never another replica's
current head. Vyper versions have no remote catalog freshness or signature
expiry. Solidity catalog semantics remain unchanged.

Submission records the per-version recipe digest and includes it in request
idempotency. The worker rejects requests belonging to another recipe. Binding
still occurs once under the job lease and authenticates compiler, host SEA,
shared runtime and package identities against the registered application
snapshot. PostgreSQL additionally checks the recipe digest and application
identity. Cache loss cannot change these identities; neither can retries.

Drain queued/running Vyper tasks before switching distribution or runtime
identity. Migration 0073 refuses undrained work. Historical terminal provenance
is retained without startup backfill, rebinding or an old execution fallback.
The target is the current fresh-database schema.

## Release and validation

Native AMD64/ARM64 full supported-version differential matrices and monolith/split
production acceptance remain required. The former signing gate becomes an
application release acceptance check; local transport archives are CI evidence
only and are never runtime download inputs. Local or emulated results cannot
replace missing native evidence. Integrity and license checks cover actual assembled contents and both runtime
cores. ADR-0053 retires the custom vulnerability-advisory queries.

Application image/binary provenance now authorizes the lock. Deploy exact image
digests; there is no independent signing key, catalog hosting or renewal. Adding,
revoking or changing a recipe requires an application release. PyPI availability
is required only for cold cache misses, and upstream deletion can prevent a cold
rebuild. Cache persistence/prewarming improves availability without becoming a
correctness authority. Fresh-process initialization and dual compilation costs
remain unchanged.

## Reviewed automatic updates

P30-T130 allows a daily build-time PyPI check to propose new stable versions
above the highest supported release. Existing locks and reference outputs are
immutable to the updater; withdrawn releases do not silently remove support.
The updater tries only the existing modern runtime. A new Python requirement,
unavailable pure wheel or adapter incompatibility requires manual engineering.
Vyper 0.2.0 remains unsupported; historical terminal provenance is retained.

Updates are prepared without repository write credentials in an isolated tree.
Only complete data/generated-code patches that pass the candidate comparison
reach a separate, path-restricted publication job. GITHUB_TOKEN creates the PR;
a maintainer approves CI and reviews the locked dependencies and archive
identities. Automation cannot change integrity checks, adapters, runtime cores
or workflows. Full native
architecture and topology acceptance remains mandatory before application release.
