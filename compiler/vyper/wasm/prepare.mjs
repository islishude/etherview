// Build-time only: production loads local, authenticated files without fetch.
import {createHash} from 'node:crypto';
import {mkdirSync,readFileSync,writeFileSync,copyFileSync,existsSync,unlinkSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {resolve,join} from 'node:path';
const hash=b=>createHash('sha256').update(b).digest('hex');
export function retirePreviousFiles(output,previous,files){
// Retire only digest-checked entries from the previous generated manifest.
// Unlisted files remain visible to the audit's exact-directory check.
if(previous?.schema==='etherview-python-wasm-v1'){
 for(const entry of previous.files??[]){
  if(typeof entry.path!=='string'||!/^[a-zA-Z0-9_.-]+$/.test(entry.path)||entry.path==='.'||entry.path==='..')throw new Error('invalid previous runtime path');
  if(files.some(file=>file.path===entry.path))continue;
  const path=join(output,entry.path);
  if(existsSync(path)){
   if(hash(readFileSync(path))!==entry.sha256)throw new Error('previous runtime payload changed');
   unlinkSync(path);
  }
 }
}
}
async function main(){
const source=fileURLToPath(new URL('../../node_modules/pyodide/',import.meta.url));
const output=resolve(process.argv[2]??'.local/vyper-wasm/shared');
const npm=JSON.parse(readFileSync(join(source,'package.json')));
if(npm.version!=='0.29.5')throw new Error('pinned Pyodide required');
const lock=JSON.parse(readFileSync(join(source,'pyodide-lock.json')));
if(lock.info.python!=='3.13.2')throw new Error('pinned Python required');
mkdirSync(output,{recursive:true});
const manifestPath=join(output,'shared-manifest.json');
const previous=existsSync(manifestPath)?JSON.parse(readFileSync(manifestPath)):null;
const files=[];
for(const name of ['pyodide.js','pyodide.asm.js','pyodide.asm.wasm','python_stdlib.zip','pyodide-lock.json']){
 copyFileSync(join(source,name),join(output,name));files.push({path:name,sha256:hash(readFileSync(join(output,name)))});
}
const crypto=lock.packages.pycryptodome;
let bytes;
const cached=join(output,crypto.file_name);
if(existsSync(cached))bytes=readFileSync(cached);
else{
 const result=await fetch('https://cdn.jsdelivr.net/pyodide/v0.29.5/full/'+crypto.file_name,{signal:AbortSignal.timeout(60000)});
 if(!result.ok)throw new Error('WASM dependency download failed');
 bytes=new Uint8Array(await result.arrayBuffer());
}
if(bytes.length>20<<20||hash(bytes)!==crypto.sha256)throw new Error('WASM dependency identity mismatch');
writeFileSync(join(output,crypto.file_name),bytes);
files.push({path:crypto.file_name,sha256:crypto.sha256});
files.sort((a,b)=>a.path.localeCompare(b.path,'en'));
retirePreviousFiles(output,previous,files);
writeFileSync(manifestPath,JSON.stringify({schema:'etherview-python-wasm-v1',pyodide:'0.29.5',python:'3.13.2',pycryptodome:crypto.version,files})+'\n');
console.log('prepared shared Python WASM runtime');
}
if(import.meta.main)await main();
