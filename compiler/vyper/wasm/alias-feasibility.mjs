// Diagnostic only. Does not change production runtime selection.
import assert from 'node:assert/strict';
import {execFileSync, spawn} from 'node:child_process';
import {createRequire} from 'node:module';
import {mkdirSync, readFileSync, writeFileSync, readdirSync, statSync} from 'node:fs';
import {dirname, join, resolve} from 'node:path';
import {platform, arch} from 'node:os';
import {ROOT, HERE, versions, digest} from './tooling.mjs';
import {bundle, download} from './packages.mjs';

import {runtimePolicy} from './runtime-policy.mjs';
const modern = new Set(runtimePolicy.modern.versions);
const supported = new Set(versions().map(x=>x.version));
export function selectRuntime(version) {
  if (!supported.has(version)) throw new Error('unsupported compiler version');
  return modern.has(version) ? 'pyodide' : 'pyodide-legacy';
}
export function compare(actual, expected, invalid) {
  if (invalid) return actual.errors?.some(e=>e.severity==='error' && expected.errors?.some(r=>r.type===e.type)) === true;
  try {assert(expected.contracts); assert.deepEqual(actual.contracts,expected.contracts); return true;} catch {return false;}
}
const json = path=>JSON.parse(readFileSync(path));
const save = (path,value)=>writeFileSync(path,JSON.stringify(value,null,2)+'\n');
const childFile = join(HERE,'alias-feasibility-child.mjs');

export async function invoke(shared, pkg, version, python, other, mode, input='', deadline=30000) {
  const args=['--permission','--disable-sigusr1','--no-addons','--no-global-search-paths',
    '--max-old-space-size=128','--wasm-max-mem-pages=6144',
    ...[shared,pkg,childFile].map(p=>'--allow-fs-read='+p),childFile,shared,pkg,version,mode,python,other];
  const started=Date.now();
  return await new Promise((resolvePromise,reject)=>{
    const child=spawn(process.execPath,args,{cwd:shared,detached:true,
      env:{PATH:process.env.PATH,LANG:'C',TZ:'UTC'},stdio:['pipe','pipe','pipe']});
    let stdout='',stderr='',expired=false,overflow=false,killer;
    const kill = sig=>{try {process.kill(-child.pid,sig);} catch(e) {if(e.code!=='ESRCH') throw e;}};
    const timer=setTimeout(()=>{expired=true;kill('SIGTERM');killer=setTimeout(()=>kill('SIGKILL'),1000);},deadline);
    child.on('error',e=>{clearTimeout(timer);reject(e);});
    child.stdout.on('data',b=>{if(Buffer.byteLength(stdout)+b.length>16<<20){overflow=true;kill('SIGKILL');}else stdout+=b;});
    child.stderr.on('data',b=>{if(Buffer.byteLength(stderr)+b.length>1<<20){overflow=true;kill('SIGKILL');}else stderr+=b;});
    child.stdin.on('error',e=>{if(e.code!=='EPIPE') reject(e);});
    child.stdin.end(input);
    child.on('close',(code,signal)=>{
      clearTimeout(timer);clearTimeout(killer);
      let cleaned=false;
      try {process.kill(-child.pid,0);} catch(e) {if(e.code==='ESRCH') cleaned=true;}
      let result;try {result=JSON.parse(stdout);} catch {result={ok:false,stage:'process',error:stdout};}
      resolvePromise({command:[process.execPath,...args],code,signal,expired,overflow,cleaned,
        seconds:(Date.now()-started)/1000,result,stderr});
    });
  });
}
async function prepare(root) {
  save(join(root,'package.json'),{private:true,dependencies:{pyodide:'314.0.7','pyodide-legacy':'npm:pyodide@0.29.5'}});
  execFileSync('npm',['install','--prefix',root,'--ignore-scripts','--no-audit','--no-fund'],{stdio:'inherit'});
  const loader=createRequire(join(root,'package.json'));
  const runtimes={};
  for(const [alias,version] of Object.entries({pyodide:'314.0.7','pyodide-legacy':'0.29.5'})) {
    const shared=dirname(loader.resolve(alias));
    assert.equal(shared,join(root,'node_modules',alias));
    assert.equal(json(join(shared,'package.json')).version,version);
    const lock=json(join(shared,'pyodide-lock.json')),crypto=lock.packages.pycryptodome;
    const url=`https://cdn.jsdelivr.net/pyodide/v${version}/full/${crypto.file_name}`;
    writeFileSync(join(shared,crypto.file_name),await download(url,crypto.sha256));
    const files=readdirSync(shared).sort().filter(n=>statSync(join(shared,n)).isFile()).map(path=>({path,sha256:digest(readFileSync(join(shared,path)))}));
    const identity={alias,version,info:lock.info,crypto,crypto_url:url,files};
    save(join(shared,'diagnostic-manifest.json'),identity);
    runtimes[alias]={shared,identity};
  }
  return runtimes;
}
async function main() {
  assert.equal(process.version,'v26.12.1');
  if(process.argv.length!==3) throw new Error('usage: node alias-feasibility.mjs NEW_OUTPUT_DIRECTORY');
  const root=resolve(process.argv[2]);mkdirSync(root,{recursive:false});
  const runtimes=await prepare(root), fixtureRoot=join(ROOT,'internal/verify/testdata/compiler/vyper/versions');
  const tasks=[], hashes={},packageIdentities={};
  for(const item of versions()) {
    const pkg=join(root,'packages',item.version);
    packageIdentities[item.version]=await bundle(item,pkg);
    for(const name of readdirSync(join(fixtureRoot,item.version)).filter(n=>n.endsWith('.input.json')).sort()) {
      const stem=name.slice(0,-'.input.json'.length);
      for(const [suffix,expectedSuffix] of [['input','output'],['modified','modified_output']]) {
        const input=join(fixtureRoot,item.version,`${stem}.${suffix}.json`), expected=join(fixtureRoot,item.version,`${stem}.${expectedSuffix}.json`);
        for(const file of [input,expected]) hashes[file.slice(ROOT.length+1)]=digest(readFileSync(file));
        tasks.push({version:item.version,stem,suffix,input,expected,pkg,alias:selectRuntime(item.version)});
      }
    }
  }
  assert.equal(Object.keys(packageIdentities).length,versions().length);assert(tasks.length > 0);
  save(join(root,'packages.json'),packageIdentities);save(join(root,'fixtures.json'),hashes);
  mkdirSync(join(root,'results'));
  const rows=[];
  async function run(task) {
    const {shared,identity}=runtimes[task.alias];
    const other=runtimes[task.alias==='pyodide'?'pyodide-legacy':'pyodide'].shared;
    const result=await invoke(shared,task.pkg,task.version,identity.info.python,other,'compile',readFileSync(task.input));
    result.passed=result.code===0 && !result.expired && !result.overflow && result.cleaned && result.result.ok===true && compare(result.result.output,json(task.expected),task.stem.startsWith('invalid'));
    result.fixture=task.input.slice(ROOT.length+1);result.alias=task.alias;
    save(join(root,'results',`${task.version}-${task.stem}-${task.suffix}.json`),result);
    rows.push({version:task.version,alias:task.alias,fixture:result.fixture,passed:result.passed,seconds:result.seconds});
    if(rows.length%30===0) console.log(rows.length,'/' + tasks.length + ';',rows.filter(x=>x.passed).length,'passed');
  }
  // Four workers, but a distinct child for every original/modified input.
  let cursor=0;
  await Promise.all(Array.from({length:4},async()=>{while(cursor<tasks.length) await run(tasks[cursor++]);}));
  rows.sort((a,b)=>a.fixture.localeCompare(b.fixture));
  const report={host:`${platform()} ${arch()}`,node:process.version,runtimes,packages:packageIdentities,
    npm_lock:json(join(root,'package-lock.json')),fixtures_sha256:digest(readFileSync(join(root,'fixtures.json'))),matrix:rows,probes:{}};
  save(join(root,'report.json'),report);
  for(const [alias,{shared,identity}] of Object.entries(runtimes)) {
    const other=runtimes[alias==='pyodide'?'pyodide-legacy':'pyodide'].shared;
    const pkg=join(root,'packages','0.4.3');
    const probes={};
    for(const mode of ['isolation','timeout','kill']) {
      const result=await invoke(shared,pkg,'0.4.3',identity.info.python,other,mode);
      result.passed=result.cleaned && !result.overflow && (mode==='isolation' ? result.code===0 && result.result.ok===true : result.expired && result.result.ready===true);
      probes[mode]=result;
    }
    const large=json(join(fixtureRoot,'0.4.3','plain.input.json'));
    large.sources['A.vy'].content+='\n#'+'x'.repeat((5<<20)+1024);
    const result=await invoke(shared,pkg,'0.4.3',identity.info.python,other,'compile',JSON.stringify(large));
    result.passed=result.code===0 && result.cleaned && !result.expired && !!result.result.output?.contracts;
    probes.large=result;
    const wrong=await invoke(shared,pkg,'0.4.3',alias==='pyodide'?'3.13.2':'3.14.2',other,'compile',readFileSync(join(fixtureRoot,'0.4.3','plain.input.json')));
    wrong.passed=wrong.cleaned && !wrong.expired && wrong.code!==0 && wrong.result.stage==='loader';
    probes.wrongPython=wrong;
    report.probes[alias]=probes;save(join(root,'report.json'),report);
    console.log(alias,'probes',Object.fromEntries(Object.entries(probes).map(([k,v])=>[k,v.passed])));
  }
  const wrongRoute=await invoke(runtimes.pyodide.shared,join(root,'packages','0.2.1'),'0.2.1','3.14.2',runtimes['pyodide-legacy'].shared,'compile',readFileSync(join(fixtureRoot,'0.2.1','plain.input.json')));
  wrongRoute.passed=wrongRoute.cleaned && !wrongRoute.expired && wrongRoute.result.output?.errors?.some(e=>e.type==='AttributeError' && e.message.includes("has no attribute 'n'"))===true;
  report.wrongRoute=wrongRoute;
  for(const [path,hash] of Object.entries(hashes)) assert.equal(digest(readFileSync(join(ROOT,path))),hash);
  report.passed=rows.every(x=>x.passed) && Object.values(report.probes).every(p=>Object.values(p).every(x=>x.passed)) && wrongRoute.passed;
  save(join(root,'report.json'),report);
  console.log('dual-runtime feasibility:',report.passed?'PASS':'FAIL');
  if(!report.passed) process.exitCode=1;
}
if(import.meta.main) await main();
