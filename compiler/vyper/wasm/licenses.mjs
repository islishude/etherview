// Build-only notice collection from pinned upstream sources, never host installs.
import { readFileSync, writeFileSync, mkdirSync, existsSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { sha256 } from './release.mjs';
const output = resolve(process.argv[2] ?? '.local/vyper-wasm/licenses');
mkdirSync(output, { recursive: true });
const records = JSON.parse(readFileSync(new URL('./licenses.lock.json', import.meta.url)));
for (const record of records) {
  const path = join(output, record.path);
  let bytes;
  if (existsSync(path)) bytes = readFileSync(path);
  else {
    const response = await fetch(record.url, { signal: AbortSignal.timeout(60000) });
    if (!response.ok) throw new Error('upstream notice download failed');
    bytes = Buffer.from(await response.arrayBuffer());
  }
  if (bytes.length > 1024 * 1024 || sha256(bytes) !== record.sha256) throw new Error('upstream notice identity mismatch');
  writeFileSync(path, bytes);
}
writeFileSync(join(output, 'sources.json'), JSON.stringify(records, null, 2) + '\n');
console.log('WASM core upstream notices: PASS');
