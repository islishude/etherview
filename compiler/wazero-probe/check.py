"""P30-T100: compare diagnostic executions to checked-in compiler outputs.
Usage: python check.py /absolute/probe /absolute/artifact-root /absolute/repo
Writes report.json and bounded per-case stdout/stderr beneath artifact-root/results.
"""
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import time

exe, root, repo = Path(sys.argv[1]), Path(sys.argv[2]), Path(sys.argv[3])
out = root / 'results'
out.mkdir(exist_ok=True)
results = []
def run(name, args, data, expected=None):
    start = time.monotonic()
    try:
        p = subprocess.run([str(exe), *map(str,args)], input=data, capture_output=True, timeout=70)
        stdout,stderr,code=p.stdout,p.stderr,p.returncode
    except subprocess.TimeoutExpired as e:
        stdout,stderr,code=e.stdout or b'',e.stderr or b'',124
    (out/(name+'.stdout')).write_bytes(stdout[:16<<20])
    (out/(name+'.stderr')).write_bytes(stderr[:1<<20])
    item={'case':name,'exit_code':code,'seconds':round(time.monotonic()-start,3),'stdout_sha256':hashlib.sha256(stdout).hexdigest()}
    if expected is not None:
        try: item['matches_reference']=code==0 and json.loads(stdout)==json.loads(expected)
        except ValueError: item['matches_reference']=False
    results.append(item)
    (root/'report.json').write_text(json.dumps(results,indent=2)+'\n')
    print(json.dumps(item),flush=True)
    return stdout

run('python-unmodified-import',['python',root/'python','-c','import sys; print(sys.version); import vyper; print(vyper.__version__)'],b'')
run('python-fallback-dependencies',['python',root/'python','-c','import immutables,cbor2; print(immutables.Map.__module__,cbor2.CBOREncoder.__module__); print(cbor2.loads(cbor2.dumps(dict(immutables.Map(a=1)))))'],b'')
for p in sorted((repo/'internal/verify/testdata/compiler/vyper').glob('*.json')):
    if '.output.' in p.name: continue
    expected=p.with_name(p.stem+'.output.json')
    if not expected.exists():continue
    run('vyper-'+p.stem,['python',root/'python','/probe/vyper_probe.py'],p.read_bytes(),expected.read_bytes())

plain=json.loads((repo/'internal/verify/testdata/compiler/vyper/plain.input.json').read_text())
for label,source in [('invalid','@external\ndef broken( -> uint256:\n    return 1\n'),('missing-import','import absent\n')]:
    value=json.loads(json.dumps(plain))
    next(iter(value['sources'].values()))['content']=source
    raw=json.dumps(value).encode()
    native='import json,sys; from vyper.cli.vyper_json import compile_json,exc_handler_to_dict; print(json.dumps(compile_json(json.load(sys.stdin),exc_handler=exc_handler_to_dict),default=str))'
    ref=subprocess.run([str(repo/'.local/vyper/venv/bin/python'),'-c',native],input=raw,capture_output=True,check=True,timeout=60).stdout
    run('vyper-'+label,['python',root/'python','/probe/vyper_probe.py'],raw,ref)

for p in sorted((repo/'internal/verify/testdata/compiler/solidity').glob('input.*.json')):
    run('solc30-'+p.stem,['solc',root/'solc30'],p.read_bytes(),p.with_name(p.name.replace('input.','output.',1)).read_bytes())

cases={
 'solidity-basic':{'language':'Solidity','sources':{'A.sol':{'content':'pragma solidity ^0.8.0; contract A { function value() external pure returns(uint256) { return 42; } }'}},'settings':{'outputSelection':{'*':{'*':['abi','evm.bytecode.object','evm.deployedBytecode.object']}}}},
 'yul':{'language':'Yul','sources':{'A.yul':{'content':'object "A" { code { datacopy(0, dataoffset("runtime"), datasize("runtime")) return(0, datasize("runtime")) } object "runtime" { code { mstore(0, 42) return(0, 32) } } }'}},'settings':{'optimizer':{'enabled':True},'outputSelection':{'*':{'*':['evm.bytecode.object']}}}},
 'solidity-invalid':{'language':'Solidity','sources':{'A.sol':{'content':'pragma solidity ^0.8.0; contract {'}}},
 'solidity-missing-import':{'language':'Solidity','sources':{'A.sol':{'content':'pragma solidity ^0.8.0; import "absent.sol"; contract A {}'}}},
 'yul-invalid':{'language':'Yul','sources':{'A.yul':{'content':'object "A" { code { invalid syntax } }'}}},
}
for version,path,directory in [('30',repo/'e2e/hardhat3/node_modules/solc/soljson.js','solc30'),('36',repo/'compiler/node_modules/solc/soljson.js','solc36'),('official30',root/'official-0.8.30.js','official30'),('official36',root/'official-0.8.36.js','official36')]:
    for name,data in cases.items():
        raw=json.dumps(data).encode()
        (out/(name+'.input.json')).write_bytes(raw)
        script='const fs=require("fs");const w=require(process.argv[1]);const c=w(require(process.argv[2]));process.stdout.write(c.compile(fs.readFileSync(0,"utf8")));'
        ref=subprocess.run(['node','-e',script,str(repo/'compiler/node_modules/solc/wrapper.js'),str(path)],input=raw,capture_output=True,check=True,timeout=60).stdout
        (out/('solc'+version+'-'+name+'.reference.json')).write_bytes(ref)
        run('solc'+version+'-'+name,['solc',root/directory],raw,ref)
(root/'report.json').write_text(json.dumps(results,indent=2)+'\n')
# Feasibility checks only. Known unsupported solc error paths stay visibly false
# in report.json; they are never accepted as production compiler behavior.
required=[r for r in results if r['case'].startswith('vyper-') or '-input.' in r['case'] or r['case'].endswith('-solidity-basic') or r['case'].endswith('-yul')]
assert all(r.get('matches_reference') for r in required), 'successful compilation or Vyper diagnostic parity regressed'
assert results[0]['exit_code']!=0 and '_ctypes' in (out/'python-unmodified-import.stderr').read_text()
