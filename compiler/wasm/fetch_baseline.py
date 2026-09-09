"""Fetch the fixed development baseline; never used by production workers.
All bytes, including explicit mirror downloads, must match the official digest.
"""
import argparse
import concurrent.futures
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
from urllib.parse import urljoin, urlparse

HERE = Path(__file__).resolve().parent

def fetch(directory, base_url, entry):
    name = entry['path']
    if Path(name).name != name or not name.endswith('.js'):
        raise ValueError('invalid baseline path')
    destination = directory / name
    expected = entry['sha256'].removeprefix('0x')
    if destination.is_file() and hashlib.sha256(destination.read_bytes()).hexdigest() == expected:
        return
    with tempfile.NamedTemporaryFile(dir=directory, prefix='.download-', delete=False) as f:
        temporary = Path(f.name)
    try:
        subprocess.run(['curl', '--fail', '--silent', '--show-error',
                        '--proto', '=https', '--max-time', '120',
                        '--max-filesize', str(200 << 20),
                        urljoin(base_url, name), '--output', str(temporary)], check=True)
        if temporary.stat().st_size > 200 << 20:
            raise ValueError('compiler artifact too large')
        if hashlib.sha256(temporary.read_bytes()).hexdigest() != expected:
            raise ValueError('compiler artifact checksum mismatch: ' + name)
        temporary.chmod(0o444)
        temporary.replace(destination)
    finally:
        temporary.unlink(missing_ok=True)

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('directory', type=Path)
    parser.add_argument('--base-url', default='https://binaries.soliditylang.org/emscripten-wasm32/')
    args = parser.parse_args()
    url = urlparse(args.base_url)
    if url.scheme != 'https' or not url.netloc or url.username or url.password or url.query or url.fragment or not url.path.endswith('/'):
        raise ValueError('baseline source must be a clean HTTPS directory')
    args.directory.mkdir(parents=True, exist_ok=True, mode=0o750)
    entries = json.loads((HERE / 'solc-baseline.json').read_text())['builds']
    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
        futures = [pool.submit(fetch, args.directory, args.base_url, entry) for entry in entries]
        for future in concurrent.futures.as_completed(futures):
            future.result()
    print(f'Validated {len(entries)} compiler artifacts')

if __name__ == '__main__':
    main()
