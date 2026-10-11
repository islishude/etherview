import test from 'node:test';
import assert from 'node:assert/strict';
import { nativeTestPattern, validateNativeResults } from './native-matrix.mjs';
import { assignments, versions } from './production-shards.mjs';
const tests = ['TestVyperDynamicRuntimeMatrix', 'TestVyperStableVersionMatrix'];
const selected = ['0.2.0', '0.4.3'];
const complete = tests.flatMap(Test => [Test, ...selected.map(v => `${Test}/${v}`)]).map(Test => ({Action: 'pass', Test}));
const output = events => events.map(e => JSON.stringify(e)).join('\n');
test('native partitions cover all versions once and select both Go tests', () => {
  const groups = assignments(4);
  assert.deepEqual(groups.flat().sort(), versions.map(v => v.version).sort());
  for (const group of groups) {
    const [root, child] = nativeTestPattern(group).split('/').map(pattern => new RegExp(pattern));
    for (const name of tests) assert(root.test(name));
    assert(!root.test('TestVyperVersionSettings'));
    for (const { version } of versions) assert.equal(child.test(version), group.includes(version));
  }
  assert.throws(() => nativeTestPattern([]));
  assert.throws(() => nativeTestPattern(['0.4.3', '0.4.3']));
  assert.throws(() => nativeTestPattern(['.*']));
});
test('native completion rejects skips, missing/duplicate versions, extra versions and failures', () => {
  validateNativeResults(output(complete), selected);
  for (const events of [complete.slice(1), complete.slice(0,-1), [...complete, complete[0]],
    [...complete, {Action:'skip',Test:tests[0]}], [...complete, {Action:'fail'}],
    [...complete, {Action:'pass',Test:`${tests[0]}/0.3.10`}]]) {
    assert.throws(() => validateNativeResults(output(events), selected));
  }
});
