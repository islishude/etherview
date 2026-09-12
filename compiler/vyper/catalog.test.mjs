import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createHash, generateKeyPairSync, verify } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
const script = fileURLToPath(new URL('./catalog.mjs', import.meta.url));
const versions = JSON.parse(readFileSync(new URL('./versions/index.json', import.meta.url))).versions;
const sha = (bytes) => createHash('sha256').update(bytes).digest('hex');
for (const failure of ['', 'missing-architecture', 'stale-acceptance', 'corrupt-archive']) {
  test(`signed catalog ${failure || 'round trip'}`, () => {
    const root = mkdtempSync(join(tmpdir(), 'vyper-catalog-'));
    try {
      const keys = generateKeyPairSync('ed25519');
      writeFileSync(join(root, 'key.pem'), keys.privateKey.export({ type: 'pkcs8', format: 'pem' }), { mode: 0o600 });
      const acceptance = {};
      for (const platform of ['linux-amd64', 'linux-arm64']) {
        acceptance[platform] = { monolith: true, split: true, descriptors: {} };
        if (failure === 'missing-architecture' && platform === 'linux-arm64') continue;
        for (const entry of versions) {
          const name = `vyper-${entry.version}-${platform}`;
          const bytes = Buffer.from('test-only archive');
          const descriptor = JSON.stringify({ version: entry.version, compiler_sha256: entry.compiler_sha256, platform,
            sha256: sha(bytes), manifest_sha256: '1'.repeat(64), max_bytes: bytes.length,
            protocol: 'etherview-vyper-runtime-v3', archive: `${name}.tar.gz`, fixture_cases: 6 });
          writeFileSync(join(root, `${name}.tar.gz`), failure === 'corrupt-archive' ? 'changed' : bytes);
          writeFileSync(join(root, `${name}.json`), descriptor);
          acceptance[platform].descriptors[`${name}.json`] = failure === 'stale-acceptance' ? '0'.repeat(64) : sha(descriptor);
        }
      }
      writeFileSync(join(root, 'acceptance.txt'), JSON.stringify(acceptance));
      const result = spawnSync(process.execPath, [script, '--artifacts', root, '--origin', 'https://compilers.example/vyper/',
        '--acceptance', join(root, 'acceptance.txt'), '--key-file', join(root, 'key.pem'),
        '--expires-at', new Date(Date.now() + 3600000).toISOString(), '--output', join(root, 'catalog.txt')], { encoding: 'utf8' });
      if (failure) { assert.notEqual(result.status, 0); return; }
      assert.equal(result.status, 0, result.stderr);
      const envelope = JSON.parse(readFileSync(join(root, 'catalog.txt')));
      const payload = Buffer.from(envelope.payload, 'base64');
      assert.equal(verify(null, payload, keys.publicKey, Buffer.from(envelope.signature, 'base64')), true);
      assert.equal(JSON.parse(payload).builds.length, versions.length);
    } finally { rmSync(root, { recursive: true, force: true }); }
  });
}
