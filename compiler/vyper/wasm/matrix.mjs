// Differential gate. References are read-only and compiler packages are built
// from authenticated official archives, never copied from installed venvs.
import assert from 'node:assert/strict';
import {readFileSync,readdirSync,constants} from 'node:fs';
import {createRequire} from 'node:module';
import {resolve,join} from 'node:path';
import {createHash} from 'node:crypto';
const [sharedArg,packagesArg,fixturesArg,version]=process.argv.slice(2);
const shared=resolve(sharedArg),packages=resolve(packagesArg),fixtures=resolve(fixturesArg);
const hash=b=>createHash('sha256').update(b).digest('hex');
function manifest(root,name){const m=JSON.parse(readFileSync(join(root,name)));for(const f of m.files)assert.equal(hash(readFileSync(join(root,f.path))),f.sha256);return m;}
const rt=manifest(shared,'shared-manifest.json');const pkg=manifest(packages,'package-manifest.json');
assert.equal(rt.pyodide,'0.29.3');assert.equal(rt.python,'3.13.2');assert.equal(pkg.version,version);
const require=createRequire(import.meta.url);
const {loadPyodide}=require(join(shared,'pyodide.js'));
// Emscripten's NODEFS initialization asks for fs constants through an obsolete
// private API. Supply only the public constants; all other bindings stay denied.
const originalBinding=process.binding;
process.binding=name=>name==='constants'?{fs:constants}:originalBinding(name);
let p;
try{p=await loadPyodide({indexURL:shared+'/',packageCacheDir:shared,stdout:()=>{},stderr:()=>{}});}
finally{process.binding=originalBinding;}
const lock=JSON.parse(readFileSync(join(shared,'pyodide-lock.json')));
await p.loadPackage(join(shared,lock.packages.pycryptodome.file_name));
p.unpackArchive(new Uint8Array(readFileSync(join(packages,'packages.zip'))),'zip',{extractDir:'/packages'});
p.runPython("import sys; sys.path.insert(0, '/packages'); import json, vyper; from adapter import compile_input");
assert.equal(p.runPython('vyper.__version__'),version);
let count=0;
for(const filename of readdirSync(fixtures).filter(n=>n.endsWith('.input.json')).sort()){
 const name=filename.slice(0,-'.input.json'.length);
 for(const suffix of ['input','modified']){
  p.globals.set('raw',readFileSync(join(fixtures,`${name}.${suffix}.json`),'utf8'));
  const actual=JSON.parse(p.runPython('json.dumps(compile_input(json.loads(raw)),default=str)'));
  const expected=JSON.parse(readFileSync(join(fixtures,`${name}.${suffix==='input'?'output':'modified_output'}.json`)));
  if(name.startsWith('invalid')){
   assert(actual.errors?.some(e=>e.severity==='error'&&expected.errors?.some(r=>r.type===e.type)),`${version}/${name}/${suffix}: diagnostic mismatch`);
  }else assert.deepEqual(actual.contracts,expected.contracts,`${version}/${name}/${suffix}: output mismatch`);
  count++;
 }
}
assert(count>=6);
console.log(JSON.stringify({version,cases:count,package_sha256:hash(readFileSync(join(packages,'package-manifest.json'))),shared_sha256:hash(readFileSync(join(shared,'shared-manifest.json')))}));
