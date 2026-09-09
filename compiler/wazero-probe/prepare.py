"""Prepare isolated diagnostic inputs; requires existing repository compiler installs.
Usage: python prepare.py /absolute/work-directory
Downloads a pinned third-party CPython WASI build, checks its recorded digest,
and verifies official Solidity artifacts against the upstream catalog.
"""
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import sys
import zipfile

repo=Path(__file__).resolve().parents[2]
root=Path(sys.argv[1]).resolve()
root.mkdir(parents=True,exist_ok=True)
def download(url,path):
    if path.exists(): return
    temporary=path.with_suffix(path.suffix+'.download')
    subprocess.run(['curl','-fsSL','--max-time','120',url,'-o',str(temporary)],check=True)
    temporary.replace(path)
def sha(path): return hashlib.sha256(path.read_bytes()).hexdigest()
url='https://github.com/brettcannon/cpython-wasi-build/releases/download/v3.13.15/python-3.13.15-wasi_sdk-24.zip'
archive=root/'python.zip'
if not archive.exists(): download(url,archive)
# Filled from the exact artifact used for the recorded experiment.
expected='67e1c32a85d5e0600c0939f5fd4023ca0127f1a9e81f5499697467bda67af78d'
if sha(archive)!=expected: raise ValueError('CPython archive digest mismatch')
with zipfile.ZipFile(archive) as z:
    for item in z.infolist():
        target=(root/'python'/item.filename).resolve()
        if not target.is_relative_to(root/'python'): raise ValueError('unsafe archive path')
    z.extractall(root/'python')
site=root/'python-probe/site'
site.mkdir(parents=True,exist_ok=True)
source=repo/'.local/vyper/venv/lib/python3.13/site-packages'
for name in ['vyper','asttokens','packaging','cbor2','immutables','Crypto']:
    shutil.copytree(source/name,site/name,dirs_exist_ok=True,ignore=shutil.ignore_patterns('__pycache__','*.so'))
# Remove native artifacts left by an earlier diagnostic preparation.
for p in site.rglob('*.so'): p.unlink()
for p in site.rglob('*.pyc'): p.unlink()
for name in ['keccak_probe.py','vyper_probe.py']:
    shutil.copyfile(Path(__file__).parent/name,root/'python-probe'/name)
download('https://binaries.soliditylang.org/emscripten-wasm32/list.json',root/'catalog.json')
catalog=json.loads((root/'catalog.json').read_text())
manifest={'python':{'url':url,'sha256':expected},'solc':[],'python_source_files':{}}
for p in sorted(site.rglob('*')):
    if p.is_file():manifest['python_source_files'][str(p.relative_to(site))]=sha(p)
for version,suffix,local in [('0.8.30','30','e2e/hardhat3'),('0.8.36','36','compiler')]:
    path=catalog['releases'][version]
    entry=next(v for v in catalog['builds'] if v['path']==path)
    artifact=root/('official-'+version+'.js')
    download('https://binaries.soliditylang.org/emscripten-wasm32/'+path,artifact)
    if sha(artifact)!=entry['sha256'].removeprefix('0x'):raise ValueError('solc digest mismatch')
    manifest['solc'].append({'version':version,'path':path,'sha256':sha(artifact)})
    for src,dest in [(artifact,root/('official'+suffix)),(repo/local/'node_modules/solc/soljson.js',root/('solc'+suffix))]:
        subprocess.run(['node',str(Path(__file__).parent/'extract.cjs'),str(src),str(dest)],check=True)
(root/'inputs.json').write_text(json.dumps(manifest,indent=2)+'\n')
print('Prepared',root)
