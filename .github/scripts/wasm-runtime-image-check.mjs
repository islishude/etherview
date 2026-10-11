import assert from 'node:assert/strict';
import { lstatSync, readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { sha256, sharedSHA256 } from '../../compiler/vyper/wasm/release.mjs';
const [root, licenses] = process.argv.slice(2);
const raw = readFileSync(join(root, 'shared-manifest.json'));
assert.equal(sha256(raw), sharedSHA256, 'unrecognized shared runtime');
const manifest = JSON.parse(raw);
const expected = ['shared-manifest.json', ...manifest.files.map(file => file.path)].sort();
assert.deepEqual(readdirSync(root).sort(), expected, 'unlisted shared runtime content');
assert.equal(lstatSync(root).mode & 0o222, 0, 'writable shared directory');
for (const name of expected) {
  const info = lstatSync(join(root, name));
  assert(info.isFile() && !info.isSymbolicLink(), 'invalid shared runtime file');
  assert.equal(info.mode & 0o333, 0, 'writable/executable runtime data');
}
for (const file of manifest.files) assert.equal(sha256(readFileSync(join(root, file.path))), file.sha256);
const notices = JSON.parse(readFileSync(new URL('../../compiler/vyper/wasm/licenses.lock.json', import.meta.url)));
for (const notice of notices) assert.equal(sha256(readFileSync(join(licenses, notice.path))), notice.sha256);
console.log('WASM image identities and upstream notices: PASS');
