import {constants,readFileSync,writeFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import {isAbsolute,join,normalize} from 'node:path';
import {isSea} from 'node:sea';
import protocol from './protocol.py';

const fixed=['--permission','--disable-sigusr1','--no-addons','--no-global-search-paths','--max-old-space-size=384'];
const limits=['--max-old-space-size=128','--wasm-max-mem-pages=6144'];
const loader=createRequire(process.execPath);

function path(value){
 if(!value||!isAbsolute(value)||normalize(value)!==value||value.includes(','))throw new Error('invalid path');
 return value;
}
function bound(raw){
 if(!/^[1-9][0-9]*$/.test(raw))throw new Error('invalid limit');
 const value=Number(raw);if(!Number.isSafeInteger(value))throw new Error('invalid limit');return value;
}
async function input(max){
 const chunks=[];let size=0;
 for await(const chunk of process.stdin){size+=chunk.length;if(size>max)throw new Error('input limit');chunks.push(chunk);}
 return Buffer.concat(chunks).toString('utf8');
}
async function boot(root){
 const {loadPyodide}=loader(join(root,'pyodide.js'));
 const originalBinding=process.binding;
 // Public constants only. This never grants access to private bindings or files.
 process.binding=name=>name==='constants'?{fs:constants}:originalBinding(name);
 try{return await loadPyodide({indexURL:root+'/',packageCacheDir:root,stdout:()=>{},stderr:()=>{}});}
 finally{process.binding=originalBinding;}
}
function denied(operation){try{operation();return false;}catch(e){return e?.code==='ERR_ACCESS_DENIED';}}

export async function executeVyper(args){
 try{
  const [mode,shared,packageRoot,version,maxInput,maxOutput]=args;
  const selfTest=mode==='--vyper-self-test';
  if(!isSea()||process.version!=='v26.10.0'||(selfTest?args.length!==2:mode!=='--vyper-compile'||args.length!==6))throw new Error('invalid invocation');
  path(shared);
  const reads=[shared];if(!selfTest)reads.push(path(packageRoot));
  const expected=[...fixed,...limits,...reads.map(p=>'--allow-fs-read='+p)];
  if(JSON.stringify(process.execArgv)!==JSON.stringify(expected))throw new Error('invalid execution policy');
  for(const permission of ['fs.write','net','child','worker','addons','ffi','wasi','inspector']){
   if(process.permission.has(permission))throw new Error('excess permission');
  }
  const p=await boot(shared);
  if(p.runPython('import sys; ".".join(map(str,sys.version_info[:3]))')!=='3.13.2')throw new Error('Python identity');
  if(selfTest){
   const memory=p.runPython('try:\n    bytearray(385 << 20)\n    bounded = False\nexcept MemoryError:\n    bounded = True\nbounded');
   let network=false;try{await fetch('http://127.0.0.1:65534');}catch(e){network=e?.cause?.code==='ERR_ACCESS_DENIED';}
   const access=network&&denied(()=>readFileSync('/etc/passwd'))&&denied(()=>writeFileSync(join(process.cwd(),'denied'),'x'))&&denied(()=>loader('node:child_process').spawnSync(process.execPath,['--version']));
   if(!memory||!access)throw new Error('self-test failed');
   process.stdout.write(JSON.stringify({schema:'etherview-vyper-wasm-self-test-v1',python:'3.13.2',pyodide:'0.29.3',access_denied:true,limits:true}));
   return;
  }
  if(!/^\d+\.\d+\.\d+$/.test(version))throw new Error('invalid version');
  const raw=await input(bound(maxInput));const maximum=bound(maxOutput);
  const lock=JSON.parse(readFileSync(join(shared,'pyodide-lock.json')));
  await p.loadPackage(join(shared,lock.packages.pycryptodome.file_name));
  p.unpackArchive(new Uint8Array(readFileSync(join(packageRoot,'packages.zip'))),'zip',{extractDir:'/packages'});
  p.runPython("import sys;sys.path.insert(0,'/packages');import vyper");
  if(p.runPython('vyper.__version__')!==version)throw new Error('compiler identity');
  p.runPython(protocol);p.globals.set('raw_request',raw);
  const output=p.runPython('compile_request(raw_request)');
  if(typeof output!=='string'||Buffer.byteLength(output)>maximum)throw new Error('output limit');
  process.stdout.write(output);
 }catch{
  process.stderr.write('compiler runtime failed\n');process.exitCode=1;
 }
}
