import test from 'node:test';
import assert from 'node:assert/strict';
import { validateTopologyMatrix } from './topology-matrix.mjs';
import { sharedSHA256 } from './release.mjs';
const expected = [['0.4.3', { name: 'descriptor.json', digest: 'descriptor-digest', matrix: { cases: 2, fixtures_sha256: 'fixture', package_sha256: 'package', executor_sha256: 'executor' } }]];
const record = { image: 'image', matrix: { native_linux: true, image_id: 'image', shared_sha256: sharedSHA256, monolith: false, split: false,
  descriptors: { 'descriptor.json': 'descriptor-digest' }, matrix: { '0.4.3': expected[0][1].matrix } } };
test('topology matrices reject stale, missing and extra compiler/fixture evidence', () => {
  validateTopologyMatrix(record, expected);
  const changes = [r => { r.matrix.native_linux = false; }, r => { r.matrix.image_id = 'other'; }, r => { r.matrix.shared_sha256 = 'other'; },
    r => { r.matrix.monolith = true; }, r => { r.matrix.split = true; }, r => { r.matrix.descriptors = {}; },
    r => { r.matrix.matrix = {}; }, r => { r.matrix.matrix.extra = {}; }, r => { r.matrix.descriptors.extra = 'extra'; }];
  for (const field of ['cases', 'fixtures_sha256', 'package_sha256', 'executor_sha256']) changes.push(r => { r.matrix.matrix['0.4.3'][field] = 'changed'; });
  for (const change of changes) {
    const revised = structuredClone(record); change(revised);
    assert.throws(() => validateTopologyMatrix(revised, expected));
  }
});
