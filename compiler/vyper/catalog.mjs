// Assemble a signed catalog only from complete native release acceptance.
import { readFileSync, writeFileSync, readdirSync } from 'node:fs';
import { resolve, basename } from 'node:path';
import { createHash, createPrivateKey, sign } from 'node:crypto';
import { parseArgs } from 'node:util';
const { values } = parseArgs({ options: {
  artifacts: { type: 'string' }, origin: { type: 'string' },
  acceptance: { type: 'string' }, 'key-file': { type: 'string' },
  'expires-at': { type: 'string' }, output: { type: 'string' },
}});
for (const name of ['artifacts', 'origin', 'acceptance', 'key-file', 'expires-at', 'output']) {
  if (!values[name]) throw new Error(`--${name} is required`);
}
const origin = new URL(values.origin);
if (origin.protocol !== 'https:' || origin.username || origin.password || origin.search || origin.hash) throw new Error('HTTPS artifact base required');
if (!origin.pathname.endsWith('/')) origin.pathname += '/';
const expiry = new Date(values['expires-at']);
if (!Number.isFinite(expiry.getTime()) || expiry <= new Date()) throw new Error('future catalog expiry required');
const index = JSON.parse(readFileSync(new URL('./versions/index.json', import.meta.url))).versions;
const acceptance = JSON.parse(readFileSync(values.acceptance));
const sha256 = (data) => createHash('sha256').update(data).digest('hex');
const runtimes = new Map();
for (const filename of readdirSync(values.artifacts).filter((name) => name.endsWith('.json'))) {
  const raw = readFileSync(resolve(values.artifacts, filename));
  const artifact = JSON.parse(raw);
  if (!['linux-amd64', 'linux-arm64'].includes(artifact.platform)) continue;
  const evidence = acceptance[artifact.platform];
  if (!evidence || evidence.monolith !== true || evidence.split !== true ||
      evidence.descriptors?.[filename] !== sha256(raw)) throw new Error('native production acceptance is missing or stale');
  const locked = index.find((entry) => entry.version === artifact.version);
  if (!locked || locked.compiler_sha256 !== artifact.compiler_sha256 || artifact.fixture_cases < 6 ||
      artifact.protocol !== 'etherview-vyper-runtime-v3' || !/^[0-9a-f]{64}$/.test(artifact.manifest_sha256)) throw new Error('invalid runtime descriptor');
  if (basename(artifact.archive) !== artifact.archive) throw new Error('invalid archive path');
  const bytes = readFileSync(resolve(values.artifacts, artifact.archive));
  if (sha256(bytes) !== artifact.sha256 || bytes.length !== artifact.max_bytes || bytes.length > 200 * 1024 * 1024) throw new Error('runtime archive identity mismatch');
  const key = `${artifact.version}/${artifact.platform}`;
  if (runtimes.has(key)) throw new Error('duplicate runtime');
  runtimes.set(key, {
    platform: artifact.platform, url: new URL(artifact.archive, origin).href,
    sha256: artifact.sha256, manifest_sha256: artifact.manifest_sha256,
    max_bytes: artifact.max_bytes, protocol: artifact.protocol,
  });
}
const builds = index.map((entry) => {
  const artifacts = ['linux-amd64', 'linux-arm64'].map((platform) => {
    const artifact = runtimes.get(`${entry.version}/${platform}`);
    if (!artifact) throw new Error(`missing native runtime ${entry.version}/${platform}`);
    return artifact;
  });
  return { version: entry.version, compiler_sha256: entry.compiler_sha256, withdrawn: false, runtimes: artifacts };
});
const key = createPrivateKey(readFileSync(values['key-file']));
if (key.asymmetricKeyType !== 'ed25519') throw new Error('Ed25519 signing key required');
const payload = Buffer.from(JSON.stringify({ schema: 'etherview-vyper-catalog-v1', expires_at: expiry.toISOString(), builds }));
writeFileSync(values.output, JSON.stringify({ payload: payload.toString('base64'), signature: sign(null, payload, key).toString('base64') }) + '\n', { flag: 'wx', mode: 0o644 });
