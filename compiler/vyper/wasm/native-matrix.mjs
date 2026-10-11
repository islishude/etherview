// Run both native Go matrices on the same deterministic version partition.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';
import { assignments, shardOptions, versions } from './production-shards.mjs';

const tests = ['TestVyperDynamicRuntimeMatrix', 'TestVyperStableVersionMatrix'];
export function nativeTestPattern(selected) {
  assert(selected.length > 0 && new Set(selected).size === selected.length);
  for (const version of selected) assert(/^\d+\.\d+\.\d+$/.test(version));
  return `^TestVyper(DynamicRuntimeMatrix|StableVersionMatrix)$/^(${selected.map(v => v.replaceAll('.', '\\.')).join('|')})$`;
}

export function validateNativeResults(output, selected) {
  const passed = new Set();
  for (const line of output.trim().split('\n')) {
    const event = JSON.parse(line);
    assert(!['fail', 'skip'].includes(event.Action), `native matrix ${event.Action}: ${event.Test ?? event.Package}`);
    if (event.Action === 'pass' && event.Test) {
      assert(!passed.has(event.Test), 'duplicate native matrix result');
      passed.add(event.Test);
    }
  }
  for (const test of tests) {
    assert(passed.has(test), `missing ${test}`);
    const actual = [...passed].filter(name => name.startsWith(`${test}/`) && name.split('/').length === 2);
    assert.deepEqual(actual.sort(), selected.map(version => `${test}/${version}`).sort(), 'incomplete native version coverage');
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const shard = shardOptions(process.argv.slice(2));
  const selected = shard ? assignments(shard.count)[shard.index] : versions.map(item => item.version);
  assert(process.env.ETHERVIEW_TEST_VYPER_MATRIX_ROOT, 'native matrix package root is required');
  const result = spawnSync(process.env.GO ?? 'go', ['test', './internal/verify', '-json', '-count=1', '-timeout=30m', '-run', nativeTestPattern(selected)],
    { encoding: 'utf8', timeout: 31 * 60 * 1000, maxBuffer: 32 * 1024 * 1024 });
  process.stdout.write(result.stdout ?? '');
  process.stderr.write(result.stderr ?? '');
  assert.ifError(result.error);
  assert.equal(result.status, 0, 'native Go matrix failed');
  validateNativeResults(result.stdout, selected);
  console.log(`Native matrix: PASS (${selected.length} versions, both tests, no skipped versions)`);
}
