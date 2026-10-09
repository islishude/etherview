// Build-time only: production loads local, authenticated files without fetch.
import {createHash} from 'node:crypto';
import {mkdirSync,readFileSync,writeFileSync,copyFileSync,existsSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {resolve,join} from 'node:path';
const source=fileURLToPath(new URL('../../node_modules/pyodide/',import.meta.url));
const output=resolve(process.argv[2]??'.local/vyper-wasm/shared');
const npm=JSON.parse(readFileSync(join(source,'package.json')));
if(npm.version!=='0.29.3')throw new Error('pinned Pyodide required');
const lock=JSON.parse(readFileSync(join(source,'pyodide-lock.json')));
if(lock.info.python!=='3.13.2')throw new Error('pinned Python required');
mkdirSync(output,{recursive:true});
const hash=b=>createHash('sha256').update(b).digest('hex');
const files=[];
for(const name of ['pyodide.js','pyodide.asm.js','pyodide.asm.wasm','python_stdlib.zip','pyodide-lock.json']){
 copyFileSync(join(source,name),join(output,name));files.push({path:name,sha256:hash(readFileSync(join(output,name)))});
}
const crypto=lock.packages.pycryptodome;
let bytes;
const cached=join(output,crypto.file_name);
if(existsSync(cached))bytes=readFileSync(cached);
else{
 const result=await fetch('https://cdn.jsdelivr.net/pyodide/v0.29.3/full/'+crypto.file_name,{signal:AbortSignal.timeout(60000)});
 if(!result.ok)throw new Error('WASM dependency download failed');
 bytes=new Uint8Array(await result.arrayBuffer());
}
if(bytes.length>20<<20||hash(bytes)!==crypto.sha256)throw new Error('WASM dependency identity mismatch');
writeFileSync(join(output,crypto.file_name),bytes);
files.push({path:crypto.file_name,sha256:crypto.sha256});
files.sort((a,b)=>a.path.localeCompare(b.path,'en'));
writeFileSync(join(output,'shared-manifest.json'),JSON.stringify({schema:'etherview-python-wasm-v1',pyodide:'0.29.3',python:'3.13.2',pycryptodome:crypto.version,files})+'\n');
console.log('prepared shared Python WASM runtime');
