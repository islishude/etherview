// Native production acceptance: the same common packages, no network, one process/input.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readFileSync, writeFileSync, mkdtempSync, rmSync, readdirSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { tmpdir, arch, platform } from 'node:os';
import { sha256, sharedRuntimes, runtimeFor } from './release.mjs';
import { assignments, shardOptions, shardFile, versionEvidence, versions } from './production-shards.mjs';
import { randomUUID } from 'node:crypto';
import { compile } from './production-process.mjs';
const started = Date.now();
const shard = shardOptions(process.argv.slice(2));
const selected = shard ? new Set(assignments(shard.count)[shard.index]) : null;
const runID = `${shard?.index ?? 'all'}-${randomUUID()}`;
const abort = new AbortController();
const cancel = () => abort.abort();
process.on('SIGINT', cancel);
process.on('SIGTERM', cancel);
const docker = process.env.DOCKER ?? 'docker';
const image = process.env.IMAGE ?? 'etherview:local';
const root = resolve('.local/vyper-releases');
const packages = resolve('.local/vyper-wasm/packages');
const architecture = arch() === 'x64' ? 'amd64' : arch();
// Non-Linux local runs are diagnostic and rejected by the release signer.
assert(['amd64', 'arm64'].includes(architecture));
const imageID = execFileSync(docker, ['image', 'inspect', '--format', '{{.Id}}', image], { encoding: 'utf8' }).trim();
assert.equal(execFileSync(docker, ['image', 'inspect', '--format', '{{.Architecture}}', imageID], { encoding: 'utf8' }).trim(), architecture);
const temporary = mkdtempSync(join(tmpdir(), `vyper-production-matrix-${runID}-`));
const container = `vyper-matrix-inspect-${runID}`;

const exe = '/opt/etherview/solcjs/etherview-solcjs';
const evidence = { native_linux: platform() === 'linux', image_id: imageID, shared_runtimes: sharedRuntimes, descriptors: {}, matrix: {} };
try {
  execFileSync(docker, ['create', '--name', container, imageID]);
  execFileSync(docker, ['cp', `${container}:/opt/etherview/solcjs/runtime-manifest.json`, join(temporary, 'host.json')]);

  evidence.host_sha256 = sha256(readFileSync(join(temporary, 'host.json')));
  for(const [id,digest] of Object.entries(sharedRuntimes)){
    execFileSync(docker,['cp',`${container}:/opt/etherview/python-wasm/${id}/shared-manifest.json`,join(temporary,id+'.json')]);
    assert.equal(sha256(readFileSync(join(temporary,id+'.json'))),digest);
  }
  for (const item of versions) {
    if (selected && !selected.has(item.version)) continue;
    const expectedEvidence = versionEvidence(item, root, packages, evidence.host_sha256);
    const pkg = join(packages, item.version);
    const shared = '/opt/etherview/python-wasm/'+runtimeFor(item.version).id;
    const fixtureRoot = new URL(`../../../internal/verify/testdata/compiler/vyper/versions/${item.version}/`, import.meta.url);
    let cases = 0;
    for (const file of readdirSync(fixtureRoot).filter(name => name.endsWith('.input.json')).sort()) {
      const stem = file.slice(0, -'.input.json'.length);
      for (const [input, output] of [['input', 'output'], ['modified', 'modified_output']]) {
        const processName = `vyper-matrix-${runID}-${item.version}-${cases}`;
        const result = await compile(docker, ['run', '--rm', '--name', processName, '-i', '--network', 'none', '--read-only',
          '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges', '--env', 'LD_LIBRARY_PATH=/opt/etherview/solcjs/lib',
          '--mount', `type=bind,src=${pkg},dst=/compiler,readonly`, '--entrypoint', exe, imageID,
          `--node-options=--max-old-space-size=128 --wasm-max-mem-pages=6144 --allow-fs-read=${shared} --allow-fs-read=/compiler`,
          '--vyper-compile', shared, '/compiler', item.version, '5242880', '33554432'],
        readFileSync(new URL(`${stem}.${input}.json`, fixtureRoot)), abort.signal);
        const actual = JSON.parse(result.stdout);
        const expected = JSON.parse(readFileSync(new URL(`${stem}.${output}.json`, fixtureRoot)));
        if (stem.startsWith('invalid')) assert(actual.errors?.some(e => e.severity === 'error' && expected.errors?.some(r => r.type === e.type)));
        else assert.deepEqual(actual.contracts, expected.contracts);
        cases++;
      }
    }
    assert.equal(cases, expectedEvidence.matrix.cases);
    evidence.descriptors[expectedEvidence.name] = expectedEvidence.digest;
    evidence.matrix[item.version] = expectedEvidence.matrix;
    console.log(`${item.version}: ${cases} production compilations passed`);
  }
  abort.signal.throwIfAborted();
  if (shard) evidence.shard = { ...shard, architecture };
  writeFileSync(join(root, shard ? shardFile(architecture, shard.index, shard.count) : `matrix-${architecture}.json`), JSON.stringify(evidence) + '\n', { flag: 'wx' });
  console.log(`${architecture} shard ${shard?.index ?? 'all'}: ${Object.keys(evidence.matrix).length} versions, ${(Date.now() - started) / 1000}s`);
} finally {
  try { execFileSync(docker, ['rm', '-f', container], { timeout: 5000, stdio: 'ignore' }); }
  catch { console.error(`failed to clean up ${container}`); process.exitCode = 1; }
  process.off('SIGINT', cancel);
  process.off('SIGTERM', cancel);
  rmSync(temporary, { recursive: true, force: true });
}
