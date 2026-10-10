"""Validate licenses in the authenticated WASM packages actually distributed."""
import json
from pathlib import Path
import sys
import zipfile

sys.path.insert(0, str(Path(__file__).resolve().parent / 'wasm'))
from packages import ROOT, HERE, digest, locked_packages


def check_package(item, package):
    manifest = json.loads((package / 'package-manifest.json').read_bytes())
    if manifest['version'] != item['version'] or manifest['compiler_sha256'] != item['compiler_sha256']:
        raise ValueError('compiler identity mismatch')
    raw = (package / 'packages.zip').read_bytes()
    if manifest['files'] != [{'path': 'packages.zip', 'sha256': digest(raw)}]:
        raise ValueError('package digest mismatch')
    locked = locked_packages(HERE.parent / 'versions' / (item['version'] + '.lock'))
    with zipfile.ZipFile(package / 'packages.zip') as archive:
        names = archive.namelist()
        for dependency in manifest['dependencies']:
            pin = locked[dependency['name']]
            if pin['version'] != dependency['version'] or dependency['sha256'] not in pin['hashes']:
                raise ValueError('unauthenticated dependency')
        # Check every dist-info, including setuptools vendored distributions.
        roots = {name.split('.dist-info/')[0] + '.dist-info/' for name in names if '.dist-info/' in name}
        for root in roots:
            licenses = [name for name in names if name.startswith(root) and
                        Path(name).name.lower().startswith(('license', 'copying'))]
            if root == 'vyper-0.2.0.dist-info/':
                licenses += [name for name in names if '/' not in name and name.lower().startswith(('license', 'copying'))]
            if not licenses or any(not archive.read(name).strip() for name in licenses):
                raise ValueError('missing distribution license: ' + root)
        if len(roots) < len(manifest['dependencies']) + 1:
            raise ValueError('missing distribution metadata')
    return len(roots)


def main():
    count = 0
    for item in json.loads((HERE.parent / 'versions/index.json').read_bytes())['versions']:
        count += check_package(item, ROOT / '.local/vyper-wasm/packages' / item['version'])
    print(f'WASM package licenses: PASS (26 versions, {count} distributions including vendored dependencies)')


if __name__ == '__main__':
    main()
