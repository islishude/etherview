# Dependency and compiler maintenance

[Operations index](../operations.md) · Commands run from the repository root.

- [Vyper compiler update proposals](#vyper-compiler-update-proposals)
- [Dependency update automation](#dependency-update-automation)

## Vyper compiler update proposals

The `Vyper compiler updates` GitHub workflow checks PyPI daily at 09:37
Asia/Shanghai. See the [compiler maintenance guide](../../compiler/vyper/README.md#proposed-updates-from-pypi)
for manual discovery/generation, pinned tooling and PR lifecycle rules. Enable
Actions PR creation in repository settings; approve the generated PR's CI runs.
A maintainer must review changed dependency and archive identities before merging, and native
AMD64/ARM64 plus monolith/split acceptance remains mandatory before release.

Vyper 0.2.0 is no longer listed or accepted by the new application. Stop new
Vyper submissions and drain queued/running jobs before deploying this change,
then roll all roles to the same application identity and verify both runtime
probes (legacy uses 0.2.1). Do not rebind jobs, delete historical verification
records, or clear existing caches as part of this rollout. Historical terminal
results remain readable. The scheduled workflow does not deploy the application.

## Dependency update automation

The Dependency updates workflow checks the default branch every Monday at
09:47 Asia/Shanghai (01:47 UTC); GitHub may delay scheduled execution. It also
supports manual dispatch with optional registry dependency ID and exact stable
version (without `v`). An exact version requires a dependency ID. Discovery
reports are retained as artifacts for 14 days; successful candidate patches and
validation reports are retained for 7 days. Existing Dependabot monthly schedules
and the dedicated Vyper update workflow retain their separate responsibilities.

Enable **Settings → Actions → General → Workflow permissions → Allow GitHub
Actions to create and approve pull requests**. Keep the default token permission
read-only. The workflow grants contents/PR write permissions only to its publishing
job and uses `GITHUB_TOKEN`; no PAT or App private key is required. It never
approves workflows, approves reviews, merges PRs or force-pushes branches.
An organization policy that prohibits this setting blocks publication and must
be resolved by an administrator; the job must not silently skip that failure.

When a bot PR appears, select **Approve workflows to run** and inspect the complete
existing CI results before manually merging. Its preparation evidence is limited
to the listed candidate checks. Review-only entries report their candidate and
reason in the discovery summary; they do not silently install new runtime bytes.

A PR uses `deps/<dependency>-<version>`. Repeat runs retain open,
closed and merged PRs; closing a PR suppresses that exact candidate. For a new
attempt at a rejected version, a maintainer can reopen the existing PR after
review. If the branch was pushed but PR creation failed, retrying from the same
base can create the missing PR only when its bot identity, parent and tree match
exactly. Human changes or a different base/tree stop recovery. Inspect and preserve
needed work before manually removing an obsolete bot branch; then dispatch again.

Upstream timeouts, rate limits, invalid responses, unexpected file changes and
candidate validation failures are visible failures. Inspect the failing job and
rerun after resolving the cause. No partial candidate is published; a failure in
one preparation job prevents publication of that batch. Ordinary CI remains
independent of live release discovery.

For Node SEA/Pyodide changes, follow ADR-0040/0053/0054 and regenerate the runtime
identities and application locks with native AMD64/ARM64 acceptance. License-bound
Go dependencies require fresh notice/source/checksum review. The solc wrapper and
historical compiler fixtures require their compiler-specific acceptance; never
replace historical inputs or hand-edit authenticated digests. uv minimum changes
require reference-resolver policy tests. The dedicated Vyper workflow remains the
entry point for adding supported Vyper releases.
