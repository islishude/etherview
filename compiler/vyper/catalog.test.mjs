import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createHash, generateKeyPairSync, verify } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { executorDigest, fixtureIdentity, sharedSHA256 } from './wasm/release.mjs';
const script = fileURLToPath(new URL('./catalog.mjs', import.meta.url));
const versions = JSON.parse(readFileSync(new URL('./versions/index.json', import.meta.url))).versions;
const sha = (bytes) => createHash('sha256').update(bytes).digest('hex');
for (const failure of ['', 'missing-architecture', 'stale-acceptance', 'corrupt-archive', 'missing-matrix', 'partial-matrix', 'stale-fixtures', 'wrong-host', 'wrong-shared', 'non-native']) {
  test(`signed catalog ${failure || 'round trip'}`, () => {
    const root = mkdtempSync(join(tmpdir(), 'vyper-catalog-'));
    try {
      const keys = generateKeyPairSync('ed25519');
      writeFileSync(join(root, 'key.pem'), keys.privateKey.export({ type: 'pkcs8', format: 'pem' }), { mode: 0o600 });
      const acceptance = {};
      for (const platform of ['linux-amd64', 'linux-arm64']) {
        if (failure === 'missing-architecture' && platform === 'linux-arm64') continue;
        acceptance[platform] = { native_linux: true, monolith: true, split: true, descriptors: {}, matrix: {},
          shared_sha256: sharedSHA256, host_sha256: sha(platform) };
      }
      for (const entry of versions) {
        const name = `vyper-${entry.version}-emscripten-wasm32`;
        const bytes = Buffer.from('test-only archive');
        const descriptor = JSON.stringify({ version: entry.version, compiler_sha256: entry.compiler_sha256,
          platform: 'emscripten-wasm32', shared_sha256: sharedSHA256,
          sha256: sha(bytes), manifest_sha256: '1'.repeat(64), max_bytes: bytes.length,
          protocol: 'etherview-vyper-wasm-package-v1', archive: `${name}.tar.gz` });
        writeFileSync(join(root, `${name}.tar.gz`), failure === 'corrupt-archive' ? 'changed' : bytes);
        writeFileSync(join(root, `${name}.json`), descriptor);
        for (const evidence of Object.values(acceptance)) {
          evidence.descriptors[`${name}.json`] = failure === 'stale-acceptance' ? '0'.repeat(64) : sha(descriptor);
          evidence.matrix[entry.version] = { ...fixtureIdentity(entry.version), package_sha256: '1'.repeat(64),
            shared_sha256: sharedSHA256,
            executor_sha256: executorDigest(evidence.host_sha256, sharedSHA256, '1'.repeat(64)) };
          if (failure === 'missing-matrix') delete evidence.matrix[entry.version];
          if (failure === 'partial-matrix') evidence.matrix[entry.version].cases = 6;
          if (failure === 'stale-fixtures') evidence.matrix[entry.version].fixtures_sha256 = '2'.repeat(64);
        }
      }
      if (failure === 'non-native') acceptance['linux-arm64'].native_linux = false;
      if (failure === 'wrong-host') acceptance['linux-arm64'].host_sha256 = '3'.repeat(64);
      if (failure === 'wrong-shared') acceptance['linux-arm64'].shared_sha256 = '3'.repeat(64);
      writeFileSync(join(root, 'matrix-arm64.json'), JSON.stringify({ unrelated: 'evidence file' }));
      writeFileSync(join(root, 'acceptance.txt'), JSON.stringify(acceptance));
      const result = spawnSync(process.execPath, [script, '--artifacts', root, '--origin', 'https://compilers.example/vyper/',
        '--acceptance', join(root, 'acceptance.txt'), '--key-file', join(root, 'key.pem'),
        '--expires-at', new Date(Date.now() + 3600000).toISOString(), '--output', join(root, 'catalog.txt')], { encoding: 'utf8' });
      if (failure) { assert.notEqual(result.status, 0); return; }
      assert.equal(result.status, 0, result.stderr);
      const envelope = JSON.parse(readFileSync(join(root, 'catalog.txt')));
      const payload = Buffer.from(envelope.payload, 'base64');
      assert.equal(verify(null, payload, keys.publicKey, Buffer.from(envelope.signature, 'base64')), true);
      const catalog = JSON.parse(payload);
      assert.equal(catalog.schema, 'etherview-vyper-catalog-v2');
      assert.equal(catalog.builds.length, versions.length);
      for (const build of catalog.builds) {
        assert.equal(build.runtimes.length, 1);
        assert.equal(Object.keys(build.runtimes[0].executor_digests).length, 2);
      }
    } finally { rmSync(root, { recursive: true, force: true }); }
  });
}
