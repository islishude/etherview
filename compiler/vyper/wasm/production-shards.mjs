// Shared partition and evidence validation for production fixture acceptance.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { parseArgs } from 'node:util';
import { executorDigest, fixtureIdentity, sha256, sharedSHA256 } from './release.mjs';

export const versions = JSON.parse(readFileSync(new URL('../versions/index.json', import.meta.url))).versions;

export function shardOptions(args, merge = false) {
  const { values } = parseArgs({ args, options: {
    'shard-count': { type: 'string' }, ...(!merge && { 'shard-index': { type: 'string' } }),
  } });
  const count = values['shard-count'];
  const index = values['shard-index'];
  if (!merge && count === undefined && index === undefined) return null;
  assert(count !== undefined && /^[1-9]\d*$/.test(count), 'invalid shard count');
  assert(Number(count) <= versions.length, 'shard count exceeds version count');
  if (merge) return { count: Number(count) };
  assert(index !== undefined && /^(0|[1-9]\d*)$/.test(index) && Number(index) < Number(count), 'invalid shard index');
  return { count: Number(count), index: Number(index) };
}

export function partition(items, count) {
  assert(Number.isSafeInteger(count) && count > 0 && count <= items.length, 'invalid shard count');
  assert.equal(new Set(items.map(item => item.version)).size, items.length, 'duplicate version');
  const shards = Array.from({ length: count }, () => []);
  const loads = Array(count).fill(0);
  const sorted = [...items].sort((a, b) => b.cases - a.cases || a.version.localeCompare(b.version, 'en', { numeric: true }));
  for (const item of sorted) {
    assert(Number.isSafeInteger(item.cases) && item.cases > 0, 'invalid fixture count');
    const index = loads.indexOf(Math.min(...loads));
    shards[index].push(item.version);
    loads[index] += item.cases;
  }
  return shards;
}

export function assignments(count) {
  return partition(versions.map(item => ({ version: item.version, ...fixtureIdentity(item.version) })), count);
}

export function shardFile(architecture, index, count) {
  return `matrix-${architecture}-shard-${index}-of-${count}.json`;
}

export function versionEvidence(item, root, packages, hostSHA256) {
  const name = `vyper-${item.version}-emscripten-wasm32.json`;
  const raw = readFileSync(join(root, name));
  const descriptor = JSON.parse(raw);
  assert.equal(descriptor.compiler_sha256, item.compiler_sha256);
  assert.equal(descriptor.shared_sha256, sharedSHA256);
  assert.equal(sha256(readFileSync(join(root, descriptor.archive))), descriptor.sha256);
  const manifestRaw = readFileSync(join(packages, item.version, 'package-manifest.json'));
  assert.equal(sha256(manifestRaw), descriptor.manifest_sha256);
  const files = JSON.parse(manifestRaw).files;
  assert.equal(files.length, 1);
  for (const file of files) {
    assert.equal(file.path, 'packages.zip');
    assert.equal(sha256(readFileSync(join(packages, item.version, file.path))), file.sha256);
  }
  return { name, digest: sha256(raw), matrix: {
    ...fixtureIdentity(item.version), package_sha256: descriptor.manifest_sha256, shared_sha256: sharedSHA256,
    executor_sha256: executorDigest(hostSHA256, sharedSHA256, descriptor.manifest_sha256),
  } };
}

export function mergeEvidence(records, groups, architecture, identity, expected) {
  assert.equal(records.length, groups.length, 'missing or extra shards');
  const merged = { ...identity, descriptors: {}, matrix: {} };
  const seen = new Set();
  for (const record of records) {
    const shard = record.shard;
    assert(shard && Number.isSafeInteger(shard.index) && shard.index >= 0 && shard.index < groups.length, 'invalid shard index');
    assert.equal(shard.count, groups.length);
    assert.equal(shard.architecture, architecture);
    assert(!seen.has(shard.index), 'duplicate shard');
    seen.add(shard.index);
    for (const [key, value] of Object.entries(identity)) assert.equal(record[key], value, `mismatched ${key}`);
    const selected = groups[shard.index];
    assert.deepEqual(Object.keys(record.matrix).sort(), [...selected].sort(), 'incorrect shard versions');
    assert.deepEqual(Object.keys(record.descriptors).sort(), selected.map(version => expected[version].name).sort(), 'incorrect shard descriptors');
    for (const version of selected) {
      assert(!Object.hasOwn(merged.matrix, version), 'duplicate version');
      const entry = expected[version];
      assert.deepEqual(record.matrix[version], entry.matrix, `stale fixture/package identity: ${version}`);
      assert.equal(record.descriptors[entry.name], entry.digest, `stale descriptor: ${version}`);
      merged.matrix[version] = entry.matrix;
      merged.descriptors[entry.name] = entry.digest;
    }
  }
  assert.deepEqual(Object.keys(merged.matrix).sort(), Object.keys(expected).sort(), 'incomplete version coverage');
  return merged;
}
