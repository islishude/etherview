// Native production acceptance: the same common packages, no network, one process/input.
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { readFileSync, writeFileSync, mkdtempSync, rmSync, readdirSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { tmpdir, arch, platform } from 'node:os';
import { executorDigest, fixtureIdentity, sha256, sharedSHA256 } from './release.mjs';
const docker = process.env.DOCKER ?? 'docker';
const image = process.env.IMAGE ?? 'etherview:local';
const root = resolve('.local/vyper-releases');
const packages = resolve('.local/vyper-wasm/packages');
const versions = JSON.parse(readFileSync(new URL('../versions/index.json', import.meta.url))).versions;
const architecture = arch() === 'x64' ? 'amd64' : arch();
// Non-Linux local runs are diagnostic and rejected by the release signer.
assert(['amd64', 'arm64'].includes(architecture));
const imageID = execFileSync(docker, ['image', 'inspect', '--format', '{{.Id}}', image], { encoding: 'utf8' }).trim();
assert.equal(execFileSync(docker, ['image', 'inspect', '--format', '{{.Architecture}}', imageID], { encoding: 'utf8' }).trim(), architecture);
const temporary = mkdtempSync(join(tmpdir(), 'vyper-production-matrix-'));
const container = execFileSync(docker, ['create', imageID], { encoding: 'utf8' }).trim();
const shared = '/opt/etherview/python-wasm';
const exe = '/opt/etherview/solcjs/etherview-solcjs';
const evidence = { native_linux: platform() === 'linux', image_id: imageID, shared_sha256: sharedSHA256, descriptors: {}, matrix: {} };
try {
  execFileSync(docker, ['cp', `${container}:/opt/etherview/solcjs/runtime-manifest.json`, join(temporary, 'host.json')]);
  execFileSync(docker, ['cp', `${container}:${shared}/shared-manifest.json`, join(temporary, 'shared.json')]);
  evidence.host_sha256 = sha256(readFileSync(join(temporary, 'host.json')));
  assert.equal(sha256(readFileSync(join(temporary, 'shared.json'))), sharedSHA256);
  for (const item of versions) {
    const name = `vyper-${item.version}-emscripten-wasm32.json`;
    const raw = readFileSync(join(root, name));
    const descriptor = JSON.parse(raw);
    assert.equal(descriptor.compiler_sha256, item.compiler_sha256);
    assert.equal(descriptor.shared_sha256, sharedSHA256);
    assert.equal(sha256(readFileSync(join(root, descriptor.archive))), descriptor.sha256);
    const pkg = join(packages, item.version);
    const manifestRaw = readFileSync(join(pkg, 'package-manifest.json'));
    assert.equal(sha256(manifestRaw), descriptor.manifest_sha256);
    for (const file of JSON.parse(manifestRaw).files) {
      assert.equal(file.path, 'packages.zip');
      assert.equal(sha256(readFileSync(join(pkg, file.path))), file.sha256);
    }
    const fixtureRoot = new URL(`../../../internal/verify/testdata/compiler/vyper/versions/${item.version}/`, import.meta.url);
    let cases = 0;
    for (const file of readdirSync(fixtureRoot).filter(name => name.endsWith('.input.json')).sort()) {
      const stem = file.slice(0, -'.input.json'.length);
      for (const [input, output] of [['input', 'output'], ['modified', 'modified_output']]) {
        const processName = `vyper-matrix-${process.pid}-${cases}`;
        const result = spawnSync(docker, ['run', '--rm', '--name', processName, '-i', '--network', 'none', '--read-only',
          '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges', '--env', 'LD_LIBRARY_PATH=/opt/etherview/solcjs/lib',
          '--mount', `type=bind,src=${pkg},dst=/compiler,readonly`, '--entrypoint', exe, imageID,
          `--node-options=--max-old-space-size=128 --wasm-max-mem-pages=6144 --allow-fs-read=${shared} --allow-fs-read=/compiler`,
          '--vyper-compile', shared, '/compiler', item.version, '5242880', '33554432'],
        { input: readFileSync(new URL(`${stem}.${input}.json`, fixtureRoot)), encoding: 'utf8', maxBuffer: 33554432, timeout: 30000 });
        if (result.error) {
          execFileSync(docker, ['rm', '-f', processName]);
          throw result.error;
        }
        assert.equal(result.status, 0, `${item.version}/${stem}/${input}: ${result.stderr}`);
        const actual = JSON.parse(result.stdout);
        const expected = JSON.parse(readFileSync(new URL(`${stem}.${output}.json`, fixtureRoot)));
        if (stem.startsWith('invalid')) assert(actual.errors?.some(e => e.severity === 'error' && expected.errors?.some(r => r.type === e.type)));
        else assert.deepEqual(actual.contracts, expected.contracts);
        cases++;
      }
    }
    const fixtures = fixtureIdentity(item.version);
    assert.equal(cases, fixtures.cases);
    evidence.descriptors[name] = sha256(raw);
    evidence.matrix[item.version] = { ...fixtures, package_sha256: descriptor.manifest_sha256, shared_sha256: sharedSHA256,
      executor_sha256: executorDigest(evidence.host_sha256, sharedSHA256, descriptor.manifest_sha256) };
    console.log(`${item.version}: ${cases} production compilations passed`);
  }
  writeFileSync(join(root, `matrix-${architecture}.json`), JSON.stringify(evidence) + '\n', { flag: 'wx' });
} finally {
  execFileSync(docker, ['rm', container]);
  rmSync(temporary, { recursive: true, force: true });
}
