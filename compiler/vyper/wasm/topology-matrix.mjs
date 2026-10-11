// Independently check both topology matrices against the checked-out fixtures/packages.
import assert from 'node:assert/strict';
import { readFileSync, rmSync } from 'node:fs';
import { arch } from 'node:os';
import { pathToFileURL } from 'node:url';
import { resolve } from 'node:path';
import { versions, versionEvidence } from './production-shards.mjs';
import { sharedSHA256 } from './release.mjs';

export function validateTopologyMatrix(record, expected) {
  const matrix = record.matrix;
  assert.equal(matrix.native_linux, true);
  assert.equal(matrix.image_id, record.image);
  assert.equal(matrix.shared_sha256, sharedSHA256);
  assert.equal(matrix.monolith, false);
  assert.equal(matrix.split, false);
  assert.deepEqual(matrix.descriptors, Object.fromEntries(expected.map(([, e]) => [e.name, e.digest])));
  assert.deepEqual(matrix.matrix, Object.fromEntries(expected.map(([version, e]) => [version, e.matrix])));
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const architecture = arch() === 'x64' ? 'amd64' : arch();
  assert(['amd64', 'arm64'].includes(architecture));
  const root = resolve('.local/vyper-releases');
  const packages = resolve('.local/vyper-wasm/packages');
  rmSync(resolve(root, `acceptance-${architecture}.json`), { force: true });
  for (const mode of ['monolith', 'distributed']) {
    const record = JSON.parse(readFileSync(resolve('.local/topology-results', `hardhat3-${architecture}-${mode}.json`)));
    const expected = versions.map(item => [item.version, versionEvidence(item, root, packages, record.matrix.host_sha256)]);
    validateTopologyMatrix(record, expected);
  }
  console.log('Both topology matrices match all current fixtures, archives and runtime/package identities');
}
