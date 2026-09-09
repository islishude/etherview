"""Check the built CPython C bridge against retained native PyCryptodome vectors."""
import json
from pathlib import Path
import subprocess
import sys

here=Path(__file__).resolve().parent
cache,source=map(lambda value:Path(value).resolve(),sys.argv[1:3])
vectors=json.loads((here/'keccak-vectors.json').read_text())
body='import random,json,_etherview_keccak; r=random.Random('+str(vectors['seed'])+'); print(json.dumps([_etherview_keccak.keccak256(r.randbytes(n)).hex() for n in '+repr(vectors['sizes'])+' for _ in range('+str(vectors['repetitions'])+')]))'
actual=subprocess.check_output([str(cache/'wasmpython-build'),str(source),str(source/'cross-build/wasm32-wasip1/python.wasm'),'-c',body],timeout=120)
if json.loads(actual)!=vectors['hashes']:
    raise RuntimeError('CPython WASI hash bridge differs from native reference')
print('56 CPython WASI C-bridge / native PyCryptodome vectors match')
