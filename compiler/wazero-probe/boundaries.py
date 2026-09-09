"""Diagnostic checks of configured WASI capabilities, limits and hash fallback."""
import json
from pathlib import Path
import random
import subprocess
import sys
import time

exe,root,repo=map(Path,sys.argv[1:4])
sys.path.insert(0,str(repo/'.local/vyper/venv/lib/python3.13/site-packages'))
from Crypto.Hash import keccak
import keccak_probe
rng=random.Random(100)
for length in [0,1,2,31,32,64,135,136,137,271,272,273,1024,4096]:
    for _ in range(4):
        value=rng.randbytes(length)
        assert keccak_probe.digest(value)==keccak.new(digest_bits=256,data=value).digest()
results={'keccak_differential_vectors':56}
code='''
import json
results = {}
for name, operation in [
 ('host_file_unavailable', lambda: open('/etc/passwd').read()),
 ('write_denied', lambda: open('/probe/forbidden', 'w')),
 ('memory_limit', lambda: bytearray(600 * 1024 * 1024)),
]:
 try:
  operation()
 except (OSError, MemoryError) as exc:
  results[name] = type(exc).__name__
 else:
  raise AssertionError(name)
print(json.dumps(results))
'''
p=subprocess.run([str(exe),'python',str(root/'python'),'-c',code],capture_output=True,text=True,timeout=70,check=True)
results['wasi']=json.loads(p.stdout)
start=time.monotonic()
p=subprocess.run([str(exe),'python',str(root/'python'),'-c','print("entered", flush=True)\nwhile True: pass'],env={'PROBE_TIMEOUT':'5s'},capture_output=True,text=True,timeout=15)
assert 'entered' in p.stdout and p.returncode!=0 and 'context deadline exceeded' in p.stderr
results['loop_cancel_seconds']=round(time.monotonic()-start,3)
(root/'boundaries.json').write_text(json.dumps(results,indent=2)+'\n')
print(json.dumps(results))
