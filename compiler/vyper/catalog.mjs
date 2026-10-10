// Assemble a signed catalog only from complete native release acceptance.
import { readFileSync, writeFileSync, readdirSync, lstatSync } from 'node:fs';
import { resolve, basename } from 'node:path';
import { createPrivateKey, sign } from 'node:crypto';
import { parseArgs } from 'node:util';
import { executorDigest, fixtureIdentity, platforms, sha256, sharedSHA256 } from './wasm/release.mjs';
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
const runtimes = new Map();
for (const filename of readdirSync(values.artifacts).filter((name) => /^vyper-.*\.json$/.test(name))) {
  const descriptorPath = resolve(values.artifacts, filename);
  const descriptorStat = lstatSync(descriptorPath);
  if (!descriptorStat.isFile() || descriptorStat.size > 1024 * 1024) throw new Error('invalid descriptor file');
  const raw = readFileSync(descriptorPath);
  const artifact = JSON.parse(raw);
  const locked = index.find((entry) => entry.version === artifact.version);
  if (!locked || artifact.platform !== 'emscripten-wasm32' ||
      locked.compiler_sha256 !== artifact.compiler_sha256 ||
      artifact.shared_sha256 !== sharedSHA256 ||
      artifact.protocol !== 'etherview-vyper-wasm-package-v1' ||
      !/^[0-9a-f]{64}$/.test(artifact.manifest_sha256)) throw new Error('invalid WASM package descriptor');
  if (typeof artifact.archive !== 'string' || basename(artifact.archive) !== artifact.archive ||
      !/^vyper-[0-9.]+-emscripten-wasm32\.tar\.gz$/.test(artifact.archive)) throw new Error('invalid archive path');
  const archivePath = resolve(values.artifacts, artifact.archive);
  const archiveStat = lstatSync(archivePath);
  if (!archiveStat.isFile() || archiveStat.size > 200 * 1024 * 1024) throw new Error('invalid archive file');
  const bytes = readFileSync(archivePath);
  if (sha256(bytes) !== artifact.sha256 || bytes.length !== artifact.max_bytes ||
      bytes.length > 200 * 1024 * 1024) throw new Error('runtime archive identity mismatch');
  if (runtimes.has(artifact.version)) throw new Error('duplicate package');
  if (filename !== `vyper-${artifact.version}-emscripten-wasm32.json` ||
      artifact.archive !== `vyper-${artifact.version}-emscripten-wasm32.tar.gz`) throw new Error('noncanonical package name');
  const fixtures = fixtureIdentity(artifact.version);
  const executor_digests = {};
  for (const platform of platforms) {
    const evidence = acceptance[platform];
    if (!evidence || evidence.native_linux !== true || evidence.monolith !== true || evidence.split !== true ||
        evidence.shared_sha256 !== sharedSHA256 ||
        evidence.descriptors?.[filename] !== sha256(raw)) throw new Error('native production acceptance is missing or stale');
    const executor = executorDigest(evidence.host_sha256, sharedSHA256, artifact.manifest_sha256);
    const matrix = evidence.matrix?.[artifact.version];
    if (!matrix || matrix.cases !== fixtures.cases || matrix.fixtures_sha256 !== fixtures.fixtures_sha256 ||
        matrix.package_sha256 !== artifact.manifest_sha256 || matrix.shared_sha256 !== sharedSHA256 ||
        matrix.executor_sha256 !== executor) throw new Error('native full matrix acceptance is missing or stale');
    executor_digests[platform] = executor;
  }
  runtimes.set(artifact.version, {
    platform: artifact.platform, url: new URL(artifact.archive, origin).href,
    sha256: artifact.sha256, manifest_sha256: artifact.manifest_sha256,
    shared_sha256: sharedSHA256, executor_digests,
    max_bytes: artifact.max_bytes, protocol: artifact.protocol,
  });
}
const builds = index.map((entry) => {
  const artifact = runtimes.get(entry.version);
  if (!artifact) throw new Error(`missing WASM package ${entry.version}`);
  return { version: entry.version, compiler_sha256: entry.compiler_sha256, withdrawn: false, runtimes: [artifact] };
});
const key = createPrivateKey(readFileSync(values['key-file']));
if (key.asymmetricKeyType !== 'ed25519') throw new Error('Ed25519 signing key required');
const payload = Buffer.from(JSON.stringify({ schema: 'etherview-vyper-catalog-v2', expires_at: expiry.toISOString(), builds }));
writeFileSync(values.output, JSON.stringify({ payload: payload.toString('base64'), signature: sign(null, payload, key).toString('base64') }) + '\n', { flag: 'wx', mode: 0o644 });
