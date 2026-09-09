"""Local fresh-process compiler comparison; not a production capacity gate.
Usage: python benchmark.py BASELINE_SOLC_SEA WASM_RUNTIME OUTPUT_JSON
"""
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import subprocess
import sys
import time
from concurrent.futures import ThreadPoolExecutor

repo=Path(__file__).resolve().parents[2]
sea,wasm,destination=map(Path,sys.argv[1:4])
artifact=repo/'e2e/hardhat3/node_modules/solc/soljson.js'
solidity=(repo/'internal/verify/testdata/compiler/solidity/input.linked.ipfs.json').read_bytes()
vyper=(repo/'internal/verify/testdata/compiler/vyper/plain.input.json').read_bytes()
digest=hashlib.sha256(artifact.read_bytes()).hexdigest()
commands={
 'solidity_sea':[str(sea),'--node-options=--allow-fs-read='+json.dumps(str(artifact)),'--compile',str(artifact),'0.8.30+commit.73712a01'],
 'solidity_wazero':[str(wasm),'--compile','solc',str(artifact),'0.8.30+commit.73712a01',digest,'5242880','67108864','120000'],
 'vyper_native':[str(repo/'.local/vyper/runtime/etherview-vyper'),'--compile','5242880','67108864'],
 'vyper_wazero':[str(wasm),'--compile','vyper',str(wasm.parent),'0.4.3','3b9671727c888363740dc678e60336759871487d0e4e9fdd973048fa9635c4fd','5242880','67108864','120000'],
}
report={'scope':'macOS ARM64, one worker, sequential fresh processes, 120-second timeout, 5 MiB input / 64 MiB output limits; no native-code cache','cases':{}}
report['host']={'platform':platform.platform(),'logical_cpus':os.cpu_count(),'cpu':subprocess.check_output(['/usr/sbin/sysctl','-n','machdep.cpu.brand_string'],text=True).strip(),'memory_bytes':int(subprocess.check_output(['/usr/sbin/sysctl','-n','hw.memsize'],text=True))}
outputs={}
for name,command in commands.items():
 data=vyper if name.startswith('vyper') else solidity
 samples=[]
 for _ in range(3):
  start=time.monotonic()
  run=subprocess.run(['/usr/bin/time','-l',*command],input=data,capture_output=True,timeout=125,env={'HOME':'/nonexistent','TMPDIR':'/tmp','LANG':'C','LC_ALL':'C'})
  if run.returncode:raise RuntimeError(name+': '+run.stderr.decode()[:500])
  result=json.loads(run.stdout);outputs[name]=result
  match=re.search(rb'(\d+)\s+maximum resident set size',run.stderr)
  if not match:raise RuntimeError('peak RSS missing')
  samples.append({'seconds':round(time.monotonic()-start,3),'peak_rss_bytes':int(match[1])})
 report['cases'][name]={'samples':samples,'input_sha256':hashlib.sha256(data).hexdigest()}
 print(name,samples,flush=True)
assert outputs['solidity_sea']==outputs['solidity_wazero']
assert outputs['vyper_native']==outputs['vyper_wazero']
report['outputs_equal']=True
report['concurrency']={}
# A second scenario measures contention without changing either backend's
# per-input limits. Peak RSS is per child, not an aggregate process-tree bound.
for name,command in commands.items():
 data=vyper if name.startswith('vyper') else solidity
 def concurrent_sample(_):
  started=time.monotonic()
  run=subprocess.run(['/usr/bin/time','-l',*command],input=data,capture_output=True,timeout=125,env={'HOME':'/nonexistent','TMPDIR':'/tmp','LANG':'C','LC_ALL':'C'})
  if run.returncode:raise RuntimeError(name+': '+run.stderr.decode()[:500])
  assert json.loads(run.stdout)==outputs[name]
  match=re.search(rb'(\d+)\s+maximum resident set size',run.stderr)
  if not match:raise RuntimeError('peak RSS missing')
  return {'seconds':round(time.monotonic()-started,3),'peak_rss_bytes':int(match[1])}
 started=time.monotonic()
 with ThreadPoolExecutor(max_workers=2) as executor:
  samples=list(executor.map(concurrent_sample,range(6)))
 report['concurrency'][name]={'workers':2,'inputs':6,'batch_seconds':round(time.monotonic()-started,3),'samples':samples}
 print(name,'two workers',report['concurrency'][name],flush=True)
destination.write_text(json.dumps(report,indent=2)+'\n')
