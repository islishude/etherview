# P30-T119 WASM security and performance review

Historical evidence: P30-T134 retired the custom advisory queries, approval
records and CVE fixtures described below. Current validation is defined by
[ADR-0053](../decisions/ADR-0053-vyper-wasm-distribution.md#security-validation).
These historical audit passes do not describe the current CI gate.

Review date: 2026-10-11. Scope: the exact Pyodide 0.29.3 / CPython 3.13.2
runtime, all 26 architecture-neutral Vyper packages, and Node 26.10.0 SEA under
[ADR-0053](../decisions/ADR-0053-vyper-wasm-distribution.md). This is an
engineering applicability review and regression audit, not an independent
penetration-test certification or a claim that old upstream vulnerabilities
have been patched.

The measurements and pass results below remain historical Node 26.10.0
evidence. The Node 26.11.1 upgrade passes all 13 jobs in
[PR #140 CI run 38126174828](https://github.com/islishude/etherview/actions/runs/38126174828)
at `d66cd4d8c92d933f3caa982c57819f157d5438ea`, including security and native
AMD64/ARM64 production acceptance. This does not update the historical
performance measurements below.

The current Pyodide 0.29.5 artifact review is recorded in the
[upgrade review](#pyodide-0295-upgrade-review-2026-10-12). Earlier runtime and
performance results retain their original artifact identities.

## Artifact and advisory coverage

The previous `audit.py` inspected only the retained native 0.4.3 reference
lock. It missed historical compiler packages and setuptools vendors. The new
`wasm/audit.mjs` (now retired) inventories the actual
authenticated payloads: 590 distribution occurrences, 73 distinct Python
name/version pairs, plus 11 core source commits. All package metadata,
including nested `.dist-info` and `vendor.txt`, is included. The latter
identified 14 copies of wheel-vendored packaging 24.0 with no `.dist-info`;
the inventory and license checks now cover them. Direct metadata must agree with
the package manifest. Shared payload digests, all 26 package manifests and
execution-boundary source digests bind the review.

Live PyPI queries cover Python distributions. The source-only Vyper 0.2.0
release uses the OSV version endpoint rather than silently skipping its absent
PyPI release. OSV commit queries cover CPython, Pyodide, Emscripten, Node,
zlib, Expat, bzip2, libffi, hiwire, musl and HACL*. PyCryptodome is one of
the Python pairs. CPython's embedded libmpdec 2.5.1 is covered by its source identity;
LLVM runtime components are covered by Emscripten's source identity. Forked
bzip2 history requires two explicit upstream advisory queries in addition to
its commit query. Commit-query coverage is not proof that an upstream project
has no undisclosed or unindexed vulnerabilities.

The live run reviewed 1,636 version-specific findings, represented by 234
distinct component/advisory dispositions. Raw findings are retained, not
discarded. `audit-review.json` (now retired)
records the exact inventory, source commits, advisory content hashes and
applicability reasons. A new version, changed boundary, missing report,
service failure, new advisory or changed substantive advisory fails the gate.
There is no automatic review-update switch. CI uploads the inventory, raw
responses and timestamped result even when the gate fails.

Primary advisory feeds: [PyPI JSON API](https://docs.pypi.org/api/json/),
[OSV API](https://google.github.io/osv.dev/api/), and the
[PSF advisory database](https://github.com/psf/advisory-database).

## Findings and applicability

| Finding group | Disposition in this exact execution path |
| --- | --- |
| Historical Vyper EVM output bugs | Retained to reproduce deployed bytecode using unchanged official compiler bytes. They concern EVM semantics, not execution of Python in the verifier host. Verification is an identity claim, never a claim that the deployed contract is safe. The two existing 0.4.3 advisory range corrections remain documented in the compiler README. |
| setuptools sdist inclusion, jaraco tarball extraction, vendored wheel unpack | Those build/install APIs never receive runtime input. Historical `pkg_resources` imports remain; compiler packages come from authenticated archives assembled by our builder. |
| CPython HTTP/TLS, email, HTML/XML, plist, CSV, config, base64 and browser APIs | Not invoked with user input by `compile_request`/`compile_input`. User sources are compiler data, never executed Python. Network, host writes and child processes are additionally denied. |
| CPython archive/decompressor and bzip2 findings | Runtime archives are authenticated before invocation and use bounded trusted payloads. User input is inline JSON. No hostile archives or reused failed decompressor are accepted. The bzip2recover utility is absent. |
| CPython `unicode_escape` UAF | Requires ignore/replace error handlers. Compiler/AST decoding is strict; the reviewed compiler source does not use the vulnerable handler combination. |
| CPython loader, VPATH and audit-hook findings | No user Python/pyc, host source mount or user-controlled interpreter search path. Python audit hooks are not an authorization boundary. |
| CPython Windows APIs | Production target is emscripten-wasm32. |
| Expat findings | No XML parsing in the compiler path; xmlwf is absent. The linked library remains inventoried. |
| zlib findings | Uses inflate for authenticated ZIPs; no untgz, Perl, `crc32_combine64`/`crc32_combine_gen64`, or `gzwrite` recovery path. |
| musl EUC-KR iconv overflow | Source arrives as UTF-8 through CPython codecs. The compiler path does not call musl iconv. |

The reachable exception is
[CVE-2026-3276](https://github.com/psf/advisory-database/blob/main/advisories/python/PSF-2026-25.json):
pathological Unicode normalization can consume CPU. Existing input limits,
fresh processes, memory limits, a 30-second invocation deadline and verified
process-group termination contain the work. The new
`TestVyperUnicodeNormalizationWorkIsBounded` exercises alternating combining
classes and then proves successful compilation after cleanup. This is a
bounded residual denial-of-service risk; it is not an upstream fix. The V8 and
WASM caps still do not bound total RSS. No pool, increased deadline, compiler
patch or permission expansion was introduced.

The source review also follows signed catalog v2 authentication, digest-checked
cache extraction, startup and per-invocation identity fences, strict inline
source paths, bounded I/O, clean environments and whole-group cancellation.
Existing regression suites exercise those boundaries; the new audit tests
reject omitted vendors, ambiguous metadata, missing/duplicate reports,
changed inventories and new/changed advisories.

## CPython advisory re-review (2026-10-11)

[CI run 38135537853](https://github.com/islishude/etherview/actions/runs/38135537853/job/114458516156)
correctly rejected an unreviewed revision of
[CVE-2026-82049 / PSF-2026-41](https://github.com/psf/advisory-database/blob/main/advisories/python/PSF-2026-41.json).
Compared with the accepted snapshot, OSV removed generated Vanir signatures,
reduced its affected-version list from 238 to 213 entries and added a fix
reference. The vulnerability description, severity and Git range events are
unchanged; CPython 3.13.2 remains listed as affected. The failed-CI finding and
the live OSV response have the same substantive digest:
`383c542b740deda53c3d0ab723abd2898e84d7fcdadd5e2f09d6ce9689018393`.

The existing authenticated-archives disposition still applies: this vulnerability
requires tarfile extraction of a crafted hard link to a symbolic link. Runtime
`execute.mjs` unpacks authenticated ZIP packages, while the Go runtime cache
extractor rejects all non-regular TAR entries, including hard/symbolic links.
Build-time `packages.mjs` reads digest-checked source archives through bounded
Node archive parsers
after rejecting links; it does not call `extract`/`extractall` or the affected
extraction filters. This is an applicability review, not a CPython patch.

The review now pins both explicitly reviewed snapshots. The checked-in regression
fixture reproduces the rejected CI record and proves that removing its new hash
fails again; further changes to details, affected ranges, references or database
metadata still fail. No hash fields or security gates were relaxed.

## License correction

The four prior core notices did not include all linked component notices.
Eleven additional authenticated upstream notices now cover zlib, bzip2,
Expat, libffi, hiwire, musl, compiler-rt, libc++, libc++abi, HACL* and CPython's
third-party notices (including libmpdec). The production builder and image
checker consume the same expanded lock. All 589 compiler-package distribution
notices remain checked separately; PyCryptodome supplies the 590th occurrence
in the security inventory.

## Performance

Host: Apple M3 Max, 48 GiB RAM, macOS ARM64. The actual SEA binary and shared
and package identities, every input digest and every process sample are in
[`P30-wasm-performance.json`](P30-wasm-performance.json).

Each workload runs an original/perturbed pair followed by three repeated
pairs, always in new restricted processes. “First” is process-cold; OS page
caches are not evicted. Repeated samples reuse disk artifacts, not a Python
interpreter. Timings include process startup, WASM initialization, package
loading and compilation; they exclude network download and Go binding/cache
validation. Peak RSS uses each child's `wait4` high-water mark and accounts
for the Linux/macOS unit difference. Serial processes use the maximum, not
the sum, of the pair's RSS.

| Workload | First dual compilation | Repeated dual median | Maximum process RSS |
| --- | ---: | ---: | ---: |
| All 26 versions, unchanged plain fixtures | 2.539–4.221 s | 2.607–3.024 s | 243.7–264.7 MiB |
| 0.4.3, 17 files / 128 functions / 155.8 KB JSON | 4.791 s | 4.477 s | 277.2 MiB |

All 216 compilations match official reference outputs. The larger generated
input uses 16 imported modules and 128 public functions; its original and
perturbed outputs were independently compiled with the official native 0.4.3
reference interpreter. Its input/output hashes are checked in separately from
existing immutable matrix fixtures. Padding comments expand parser input size;
the executable workload is those modules and functions. The native interpreter
uses CPython 3.13.16; its Vyper implementation files match the pinned official
wheel byte-for-byte. This is a current local reference comparison, not a replay
of the old production image's complete environment.

The serial 0.4.3 native reference pairs measured repeated dual medians of
2.883 s (WASM) versus 0.231 s (native) for plain input, and 4.477 s versus
1.041 s for the larger input. Native peak RSS was 34.7/59.8 MiB versus
260.2/277.2 MiB for WASM. Thus the distribution simplification has a substantial
startup and memory cost: roughly 12.5x/4.3x dual latency in these two workloads.
This records the selected architecture's cost, not a performance improvement.
Every measured process stays within the unchanged deadline; the larger input
produces the same contract output. These local samples do not establish Linux
capacity, concurrent throughput, or a total-RSS guarantee. The first measured
pair can be slower after a SEA rebuild; all raw samples are retained rather
than dropping that cost. The final report's SEA digest matches the actual
locally built binary used for this run.

Reproduce with `make benchmark-vyper-wasm`. To add a native comparison, run
`python3 compiler/vyper/wasm/benchmark.py --version 0.4.3 --native-python
/absolute/path/to/reference/venv/bin/python`. The reference must contain the
official 0.4.3 compiler. Failed runs cannot retain a stale completion marker.

## Acceptance record

Implementation commit `b52deabeb32a90582caab093cff0f7cef5a4d138` passes all 13
jobs in [run 38119209326](https://github.com/islishude/etherview/actions/runs/38119209326).
Downloaded security evidence matches the local inventory and advisory/review
hashes. Both native production records authenticate all 26 packages and 362
fixture compilations per architecture plus monolith/split results; the real
catalog tool consumes them and verifies with a temporary Ed25519 key.
[P30-T119](../plans/P30-contract-verification.md#evidence) records the complete
closure. The following documentation-only update records final benchmark samples
and plan status; documentation/plan gates pass locally. Source delivery,
production catalog signing/publication and deployment remain separate actions.

## Expat advisory re-review (2026-10-11)

CI run 38142676727 failed on `core/expat/CVE-2026-77214`. Its retained
finding and the live OSV response agree: relative to reviewed digest
`2b57dde0fe3f7dc2f86fed118c20c4426f896cc2f67096598ebadca3b02abe18`,
the only substantive JSON addition is `related: [openSUSE-SU-2026:12100-1]`.
The description, affected versions/ranges, severity and fix reference are
unchanged. The new explicitly reviewed digest is
`e4922c1ec54e2292df10d405556089ff0560a616525d575bc805b3ec807aecb8`.

[Upstream's fix](https://github.com/libexpat/libexpat/pull/1393) validates the
caller-supplied length against internal buffer capacity in `XML_ParseBuffer`.
The pinned Expat 2.6.4 remains affected; this review does not claim an upstream
patch. The fixed JSON compiler entrypoints in `protocol.py` and `adapter.py`
do not parse user XML or invoke Expat. The existing `no-xml` disposition still
applies. Keep both exact reviewed snapshots; new or changed substantive content,
including related advisories, continues to require review. The failed-CI finding
is retained as a regression fixture, including rejection when its new digest
is removed and when its impact, affected range, references or severity changes.

The subsequent live audit also received a new CVE-2026-82049 revision at
13:07:11 UTC. It adds only `vanir_signatures` and
`vanir_signatures_modified` under `affected[0].database_specific`, plus the
excluded top-level modification timestamp. The signatures target CPython
link/symlink/rename implementations at commit
`5a57248b22ad3b9aafcaaadae2c304a1923daeca`; the prior description, affected
ranges, versions, severity and references are unchanged. The authenticated
archive/no-untrusted-extraction reasoning in the CPython review above still
applies. Explicitly accept digest
`48d4aa043e5555c02a4b4d53fac9a3ff81db2471c935f1161dca2ba0f2305ee1`
and retain this separate live snapshot as a regression fixture. Signature
metadata remains inside the digest; no fields were newly excluded.

## Node producer inventory review (2026-10-12)

P30-T122 replaces host Python packaging/audit orchestration with Node. An
independent ZIP comparison of all 26 old/new packages found all 17,326 member
names and uncompressed contents identical, including compiler implementations,
adapter bytes, metadata, licenses and vendored distributions. Compiler source
and selected dependency identities are unchanged. The new ZIP compression and
USTAR producers use pinned build-only adm-zip 0.6.1 and tar-stream 3.2.2; their
modules are absent from the production SEA dependency graph. Two full release
builds produced identical 26 archives and 26 descriptors.

The reviewed inventory delta consists only of 26 package-manifest digests and
`compiler/package-lock.json`, which adds those build dependencies. Distribution
records remain exactly equal (590 occurrences). The inventory digest changes
from `041bc04996e0f6de89abebce301cee7be82668b22e46ac8c83440d87a064682e`
to `80dcd370bd3d9b6cdaf78bfef7cc78dd1168fb1a55a5b6573abd1e854b833ff3`.
No advisory digest, reason or applicability disposition changes. The Node audit
passes live PyPI/OSV queries for 84 components and 1,636 reviewed findings.
The native 0.4.3 reference audit independently passes nine remaining packages
with the same two exact-wheel range corrections; its installer no longer
installs PyInstaller or its packaging-only dependencies.

This is local tool-migration evidence. New compressed package bytes invalidate
old package-bound native acceptance and catalog signatures. Neither this
review nor the local matrix promotes packages or asserts new native
AMD64/ARM64 production acceptance.

## Pyodide 0.29.5 upgrade review (2026-10-12)

The [upstream comparison](https://github.com/pyodide/pyodide/compare/0.29.3...0.29.5)
retains CPython 3.13.2, Emscripten 4.0.9 and ABI 2025_0. The intervening
0.29.4 release adds `pyemscripten` wheel tags; 0.29.5 fixes the upstream Python
CLI's Node 26 compatibility. The Pyodide source identity now resolves to
`05804b32e55628ad2269655f3c5a097fb4a966ee`. Other reviewed core source pins
remain unchanged. Upstream core license bytes pass their existing digest locks.

The shared manifest is
`b20a6b86d20d7902197560577f920885d7049e580c10c374b82e1b0725ea4d7b`.
PyCryptodome remains 3.21.0, but its renamed wheel has SHA-256
`13ca0f73475b40696b4a628b6e4424572d4c9f56f3b5d69a97b4622fb4dec961`.
Its rebuilt WASM extensions, WHEEL and RECORD differ; Python source and package
metadata bytes are unchanged. All 26 compiler ZIPs remain byte-identical to the
Node producer's pre-upgrade artifacts. Package manifests now name 0.29.5;
fixture references, compiler sources and adapter/protocol code are unchanged.

The new inventory digest is
`22aceb7fdb0e66e7c4c119b34c7bd23d72a71a2635e4dd75dcc10352e940b75b`.
Inventory changes comprise the shared runtime, package manifests, npm lock,
version/digest checks, core license URL and shared wheel origin name. The live
PyPI/OSV audit covers the same 590 distributions and 84 components, with 1,636
findings accepted by the existing exact-content dispositions. No advisory hash,
reason or exemption was added. This patch does not fix the retained CPython
advisories or establish zero vulnerabilities.

Local candidate/SEA, isolation, performance and common-gate evidence is owned
by the [historical P30-T123 record](https://github.com/islishude/etherview/blob/e665a0b9b5dcee02e6d90d3a2224a76031cbda3d/docs/plans/P30-contract-verification.md#p30-t123--pyodide-0295-upgrade-2026-10-12).
Previous native Linux acceptance cannot authorize promotion of these new
artifacts: both architectures and production topologies must pass again.

The isolated macOS ARM64 0.4.3 benchmark ran one first pair and three warm
pairs for each workload, always in fresh restricted SEA processes. Plain
compilation measured 3.728 s for the first pair and 2.722 s warm median, with
245.6 MiB peak child RSS. The larger 17-file workload measured 4.179 s first
and 4.178 s warm median, with 259.1 MiB peak RSS. Both match their unchanged
official native reference outputs. These are local samples, not an OS-cache
cold experiment or evidence of a performance improvement. Raw output is at
`.local/pyodide-0295-candidate/benchmark.json`; the old published measurements
remain unchanged.

## CPython fix-range re-review (2026-10-12)

[CI run 38198556844](https://github.com/islishude/etherview/actions/runs/38198556844/job/114646592435)
rejected the OSV revision modified at 02:32:56 UTC. Its saved finding and the
[live OSV record](https://api.osv.dev/v1/vulns/CVE-2026-82049) have the same digest:
`066932ef10f754870a25881e1e277dcf8b3c57999d13a9020288a96fa6edc248`.
Compared with the retained October 11 non-Vanir fixture, the only substantive
addition is Git range fix `cccc7ec30212dba2abef53ce4a2bed112ddb690c`.
Relative to the Vanir snapshot, generated signature metadata is also removed.
The description, severity, affected versions, aliases and references are unchanged.

The [added upstream commit](https://github.com/python/cpython/commit/cccc7ec30212dba2abef53ce4a2bed112ddb690c)
is the Python 3.15 backport of the same tarfile hard-link/symlink fix, touching
`Lib/tarfile.py` and its tests. CPython 3.13.2 remains affected. The existing
`authenticated-archives` disposition remains applicable: the runtime loads
authenticated ZIP packages; the Go TAR cache extractor rejects non-regular
entries, and the Node build parser rejects links. This review does not patch
CPython or expand accepted inputs.

Retain the failed-CI record as `CVE-2026-82049-fix-range.json` and accept only
its exact digest in addition to previously reviewed snapshots. Regression tests
prove that removing this digest reproduces the rejection and that changed
impact, affected ranges, references, metadata, related findings or severity
still fail. No additional fields are excluded from advisory hashing.

## Narrow Vanir auxiliary policy (2026-10-12)

The [ADR-0053 policy](../decisions/ADR-0053-vyper-wasm-distribution.md#security-validation)
introduced an exception for only the two validated Vanir fields for an
explicitly opted-in OSV CPython finding. CVE-2026-82049 uses the previously reviewed fix-range
snapshot (`066932ef10f754870a25881e1e277dcf8b3c57999d13a9020288a96fa6edc248`)
as its authenticated baseline. The archive applicability disposition is unchanged.
At that revision, other findings and PyPI records retained exact-content matching.

The historical non-Vanir and Vanir snapshots have identical decision content;
the fix-range revision does not. Thus pure generated-signature changes can pass
with evidence, while the original CI failure's added Git range would still
require review against the older baseline. Full advisory hashes and cross-query
consistency remain strict. That policy accepts no new reference, related
advisory, affected version, range, severity, credit or unknown metadata field.
This policy changes audit classification only; it does not patch CPython or
change compiler packages, runtime execution or public contracts.
