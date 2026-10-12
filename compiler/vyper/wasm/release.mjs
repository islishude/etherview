// Identities used by native acceptance producers and the offline catalog signer.
import { readFileSync, readdirSync } from 'node:fs';
import { createHash } from 'node:crypto';
export const sharedSHA256 = 'b20a6b86d20d7902197560577f920885d7049e580c10c374b82e1b0725ea4d7b';
export const platforms = ['linux-amd64', 'linux-arm64'];
export const sha256 = (data) => createHash('sha256').update(data).digest('hex');
export function executorDigest(host, shared, pkg) {
  for (const digest of [host, shared, pkg]) {
    if (!/^[0-9a-f]{64}$/.test(digest) || /^0+$/.test(digest)) throw new Error('invalid executor component digest');
  }
  return sha256(Buffer.concat([Buffer.from('etherview/node_vyper_wasm_v1\0'),
    ...[host, shared, pkg].map((digest) => Buffer.from(digest, 'hex'))]));
}
export function fixtureIdentity(version) {
  const root = new URL(`../../../internal/verify/testdata/compiler/vyper/versions/${version}/`, import.meta.url);
  const names = readdirSync(root).filter((name) => name.endsWith('.json')).sort();
  const cases = names.filter((name) => name.endsWith('.input.json')).length * 2;
  const files = names.map((name) => [name, sha256(readFileSync(new URL(name, root)))]);
  return { cases, fixtures_sha256: sha256(JSON.stringify(files)) };
}
