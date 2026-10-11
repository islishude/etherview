# P30-T119 WASM security and performance review

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

## Artifact and advisory coverage

The previous `audit.py` inspected only the retained native 0.4.3 reference
lock. It missed historical compiler packages and setuptools vendors. The new
[`wasm/audit.py`](../../compiler/vyper/wasm/audit.py) inventories the actual
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
discarded. [`audit-review.json`](../../compiler/vyper/wasm/audit-review.json)
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
Build-time `packages.py` reads digest-checked source archives with `extractfile`
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
