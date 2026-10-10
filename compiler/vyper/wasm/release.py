"""Package authenticated WASM bundles once for both native Linux consumers.

This emits unsigned transport descriptors, never release acceptance or signatures.
"""
import argparse
import json
from pathlib import Path
import tempfile

from packages import ROOT, HERE, MAX_ARCHIVE, digest, pack_runtime

SHARED_SHA256 = 'b87574439230ec442792386f2ccd82fa020f735769d55770492b9da727502d24'


def descriptor(item, package, shared, output):
    if shared.is_symlink() or digest(shared.read_bytes()) != SHARED_SHA256:
        raise ValueError('shared runtime identity mismatch')
    if package.is_symlink():
        raise ValueError('invalid package directory')
    raw = (package / 'package-manifest.json').read_bytes()
    manifest = json.loads(raw)
    expected = {'schema': 'etherview-vyper-wasm-package-v1',
                'version': item['version'], 'compiler_sha256': item['compiler_sha256'],
                'pyodide': '0.29.3', 'python': '3.13.2'}
    if any(manifest.get(key) != value for key, value in expected.items()):
        raise ValueError('compiler package identity mismatch')
    payload = package / 'packages.zip'
    if payload.is_symlink() or payload.stat().st_size > MAX_ARCHIVE:
        raise ValueError('invalid package payload')
    if manifest.get('files') != [{'path': 'packages.zip', 'sha256': digest(payload.read_bytes())}]:
        raise ValueError('compiler package payload mismatch')
    name = 'vyper-' + item['version'] + '-emscripten-wasm32'
    archive = output / (name + '.tar.gz')
    pack_runtime(package, archive)
    data = archive.read_bytes()
    if len(data) > MAX_ARCHIVE:
        raise ValueError('release archive exceeds limit')
    record = {'version': item['version'], 'compiler_sha256': item['compiler_sha256'],
              'platform': 'emscripten-wasm32', 'protocol': expected['schema'],
              'manifest_sha256': digest(raw), 'shared_sha256': SHARED_SHA256,
              'archive': archive.name, 'sha256': digest(data), 'max_bytes': len(data)}
    (output / (name + '.json')).write_text(json.dumps(record, sort_keys=True, separators=(',', ':')) + '\n')
    return record


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--packages', type=Path, default=ROOT / '.local/vyper-wasm/packages')
    parser.add_argument('--shared', type=Path, default=ROOT / '.local/vyper-wasm/shared/shared-manifest.json')
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    if args.output.exists():
        raise ValueError('release output must not already exist')
    args.output.parent.mkdir(parents=True, exist_ok=True)
    # Publish the complete 26-package set only after every input passes.
    with tempfile.TemporaryDirectory(dir=args.output.parent) as temporary:
        staging = Path(temporary) / 'release'
        staging.mkdir()
        for item in json.loads((HERE.parent / 'versions/index.json').read_bytes())['versions']:
            descriptor(item, args.packages / item['version'], args.shared, staging)
        staging.rename(args.output)


if __name__ == '__main__':
    main()
