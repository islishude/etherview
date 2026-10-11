import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { compile } from '../../compiler/vyper/wasm/production-process.mjs';
const image = execFileSync('docker',['image','inspect','--format','{{.Id}}','busybox:1.37'],{encoding:'utf8'}).trim();
for (const mode of ['failure','timeout','cancel']) {
  const name = `vyper-cleanup-${randomUUID()}`;
  const abort = new AbortController();
  let timer;
  try {
    const args=['run','--rm','--name',name,'-i','--network','none','--read-only',image,'sh','-c',mode==='failure'?'exit 37':'sleep 300'];
    const promise=compile('docker',args,'',abort.signal,mode==='timeout'?2000:30000);
    if(mode==='cancel') timer=setTimeout(()=>abort.abort(),2000);
    await assert.rejects(promise, error => mode==='failure'?error.code===37:mode==='cancel'?error.name==='AbortError':error.killed===true);
    const remaining=execFileSync('docker',['ps','-aq','--filter',`name=^/${name}$`],{encoding:'utf8'}).trim();
    assert.equal(remaining,'',`${mode} left a container`);
    console.log(`${mode}: expected rejection and container cleanup confirmed`);
  } finally {
    clearTimeout(timer);
    execFileSync('docker',['rm','-f',name],{stdio:'ignore'});
  }
}
