// Image transport is independent of all three acceptance lanes.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';

const [mode, directory, architecture, ...extra] = process.argv.slice(2);
assert(['save', 'load'].includes(mode) && directory && extra.length === 0,
  'usage: vyper-production-bundle.mjs save|load DIRECTORY amd64|arm64');
assert(['amd64', 'arm64'].includes(architecture), 'unsupported architecture');
const bundle = resolve(directory);
const image = process.env.IMAGE ?? 'etherview:local';
const docker = (...args) => execFileSync(process.env.DOCKER ?? 'docker', args, { encoding: 'utf8' });
const revision = process.env.GITHUB_SHA ?? execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim();
assert.match(revision, /^[a-f0-9]{40}$/, 'invalid source revision');
let expected;
if (mode === 'load') {
  expected = JSON.parse(readFileSync(join(bundle, 'identity.json'), 'utf8'));
  assert.equal(expected.schema, 'etherview-production-image-v1');
  assert.equal(expected.revision, revision, 'image source revision mismatch');
  assert.equal(expected.architecture, architecture, 'image architecture mismatch');
  assert.match(expected.image_id, /^sha256:[a-f0-9]{64}$/);
  docker('image', 'load', '--input', join(bundle, 'production-image.tar'));
}
const [actual] = JSON.parse(docker('image', 'inspect', image));
assert.equal(actual.Architecture, architecture, 'production image architecture mismatch');
assert.equal(actual.Os, 'linux', 'production image must target Linux');
assert.match(actual.Id, /^sha256:[a-f0-9]{64}$/);
if (mode === 'save') {
  mkdirSync(bundle, { recursive: true });
  docker('image', 'save', '--output', join(bundle, 'production-image.tar'), image);
  writeFileSync(join(bundle, 'identity.json'), JSON.stringify({
    schema: 'etherview-production-image-v1', revision, architecture, image_id: actual.Id,
  })+'\n', { flag: 'wx' });
} else {
  assert.equal(actual.Id, expected.image_id, 'loaded image identity mismatch');
}
console.log(`${mode}: ${architecture} ${actual.Id}`);
