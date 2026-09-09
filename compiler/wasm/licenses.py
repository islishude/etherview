"""Validate the actual WASM bundle license and dependency inventory."""
import json
from pathlib import Path

here = Path(__file__).resolve().parent
root = here.parents[1] / '.local/wasm/runtime'
lock = json.loads((root / 'runtime.lock.json').read_text())
files = list((root / 'licenses').iterdir())
for package in lock['packages']:
    if package not in {'vyper','asttokens','packaging','cbor2','immutables'}:
        raise SystemExit('unreviewed Python runtime package: ' + package)
    if not any(p.name.startswith(package + '-') and p.stat().st_size for p in files):
        raise SystemExit('missing Python runtime license: ' + package)
for name in ['CPython-LICENSE.txt','solc-js-LICENSE.txt','Go-LICENSE.txt','go-wazero-LICENSE.txt','go-wabin-LICENSE.txt','go-lz4-LICENSE.txt','go-x-crypto-LICENSE.txt','go-x-sys-LICENSE.txt']:
    if not (root / 'licenses' / name).is_file():
        raise SystemExit('missing runtime license: ' + name)
for name in lock['sdk_licenses']:
    if not (root / 'licenses' / name).is_file():
        raise SystemExit('missing WASI SDK/target library license: ' + name)
print('WASM runtime licenses: PASS')
