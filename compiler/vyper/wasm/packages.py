"""Build reproducible pure-Python bundles from authenticated upstream archives."""
import gzip
import hashlib
import io
import json
from pathlib import Path, PurePosixPath
import re
import tarfile
import urllib.request
import time
import zipfile

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
CACHE = ROOT / '.local/vyper-wasm/downloads'
# These are packaging tools, not compiler imports. setuptools is retained for
# historical pkg_resources imports, including its own vendored dependencies.
OMIT = {'pyinstaller', 'pyinstaller-hooks-contrib', 'altgraph', 'macholib',
        'pefile', 'pywin32-ctypes', 'pytest-runner', 'wheel', 'pycryptodome'}
MAX_ARCHIVE = 200 << 20


def digest(data):
    return hashlib.sha256(data).hexdigest()


def read_url(url, limit):
    for attempt in range(3):
        try:
            with urllib.request.urlopen(url, timeout=60) as response:
                data = response.read(limit + 1)
            if len(data) > limit:
                raise ValueError('download exceeds limit')
            return data
        except OSError:
            if attempt == 2:
                raise
            time.sleep(attempt + 1)


def download(url, expected):
    if not re.fullmatch('[0-9a-f]{64}', expected) or not url.startswith('https://'):
        raise ValueError('invalid authenticated download')
    CACHE.mkdir(parents=True, exist_ok=True)
    path = CACHE / expected
    if path.exists():
        data = path.read_bytes()
    else:
        data = read_url(url, MAX_ARCHIVE)
        if len(data) > MAX_ARCHIVE or digest(data) != expected:
            raise ValueError('artifact identity mismatch')
        path.write_bytes(data)
    if len(data) > MAX_ARCHIVE or digest(data) != expected:
        raise ValueError('cached artifact identity mismatch')
    return data


def locked_packages(path):
    selected = {}
    current = None
    for line in path.read_text().splitlines():
        match = re.match(r'^([\w-]+)==([^\s;\\]+)', line)
        if match:
            current = match[1]
            selected[current] = {'version': match[2], 'hashes': []}
        elif current:
            selected[current]['hashes'].extend(re.findall(r'--hash=sha256:([0-9a-f]{64})', line))
    return selected


def wheel(name, locked):
    CACHE.mkdir(parents=True, exist_ok=True)
    metadata_path = CACHE / (name + '-' + locked['version'] + '.json')
    if metadata_path.exists():
        metadata = json.loads(metadata_path.read_bytes())
    else:
        raw = read_url(f'https://pypi.org/pypi/{name}/{locked["version"]}/json', 8 << 20)
        metadata = json.loads(raw)
        metadata_path.write_bytes(raw)
    choices = [x for x in metadata['urls'] if x['filename'].endswith('.whl')
               and x['digests']['sha256'] in locked['hashes']]
    # Native wheels can contain upstream pure-Python fallbacks. Native members
    # are never copied; this does not execute or patch a foreign-platform wheel.
    choices.sort(key=lambda x: (not x['filename'].endswith('-none-any.whl'), x['filename']))
    if not choices:
        raise ValueError('no authenticated wheel: ' + name)
    chosen = choices[0]
    return download(chosen['url'], chosen['digests']['sha256']), {
        'name': name, 'version': locked['version'], 'sha256': chosen['digests']['sha256'],
        'filename': chosen['filename'],
    }


def safe_member(name):
    path = PurePosixPath(name)
    if not name or '\\' in name or path.is_absolute() or any(x in ('', '.', '..') for x in name.split('/')):
        raise ValueError('unsafe package member')
    return path


def unpack_wheel(data, files):
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        total = 0
        for entry in archive.infolist():
            if entry.is_dir():
                continue
            path = safe_member(entry.filename)
            total += entry.file_size
            if total > MAX_ARCHIVE or (entry.external_attr >> 16) & 0o170000 == 0o120000:
                raise ValueError('unsafe wheel')
            if path.suffix in ('.so', '.pyd', '.dll', '.dylib', '.pyc') or '__pycache__' in path.parts:
                continue
            if path.parts[0].endswith('.data'):
                continue
            value = archive.read(entry)
            if entry.filename in files and files[entry.filename] != value:
                raise ValueError('conflicting package member')
            files[entry.filename] = value


def source_vyper(item, files):
    raw = download(item['source_url'], item['compiler_sha256'])
    if item['source_url'].endswith('.whl'):
        unpack_wheel(raw, files)
        return
    with tarfile.open(fileobj=io.BytesIO(raw), mode='r:gz') as archive:
        total = 0
        for entry in archive:
            if not entry.isfile():
                if not entry.isdir():
                    raise ValueError('unsafe source archive')
                continue
            path = safe_member(entry.name)
            relative = PurePosixPath(*path.parts[1:])
            total += entry.size
            if total > MAX_ARCHIVE:
                raise ValueError('oversized source archive')
            if relative.parts[0] == 'vyper' or relative.name.lower().startswith(('license', 'copying')):
                files[str(relative)] = archive.extractfile(entry).read()
    # Upstream packaging supplies these values for the source-only 0.2.0
    # release. Compiler implementation files remain byte-for-byte upstream.
    commit = item['source_url'].rsplit('/', 1)[1].removesuffix('.tar.gz')
    files['vyper/vyper_git_version.txt'] = commit.encode()
    files['vyper-0.2.0.dist-info/METADATA'] = b'Metadata-Version: 2.1\nName: vyper\nVersion: 0.2.0\n'


def bundle(item, destination):
    files = {}
    identities = []
    locks = locked_packages(HERE.parent / 'versions' / (item['version'] + '.lock'))
    for name, locked in sorted(locks.items()):
        if name in OMIT or name == 'vyper':
            continue
        raw, identity = wheel(name, locked)
        unpack_wheel(raw, files)
        identities.append(identity)
    source_vyper(item, files)
    files['adapter.py'] = (HERE.parent / 'adapter.py').read_bytes()
    destination.mkdir(parents=True, exist_ok=True)
    buffer = io.BytesIO()
    with zipfile.ZipFile(buffer, 'w', compression=zipfile.ZIP_DEFLATED) as archive:
        for name, value in sorted(files.items()):
            entry = zipfile.ZipInfo(name, (1980, 1, 1, 0, 0, 0))
            entry.compress_type = zipfile.ZIP_DEFLATED
            entry.external_attr = 0o100444 << 16
            archive.writestr(entry, value)
    raw = buffer.getvalue()
    (destination / 'packages.zip').write_bytes(raw)
    manifest = {'schema': 'etherview-vyper-wasm-package-v1', 'version': item['version'],
                'compiler_sha256': item['compiler_sha256'], 'pyodide': '0.29.3',
                'python': '3.13.2', 'dependencies': identities,
                'files': [{'path': 'packages.zip', 'sha256': digest(raw)}]}
    (destination / 'package-manifest.json').write_text(json.dumps(manifest, sort_keys=True, separators=(',', ':')) + '\n')
    return manifest


def pack_runtime(runtime, archive_path):
    """Emit identical architecture-neutral release bytes, including gzip headers."""
    names = ['package-manifest.json', 'packages.zip']
    if sorted(path.name for path in runtime.iterdir()) != names:
        raise ValueError('unexpected compiler package files')
    with archive_path.open('wb') as output:
        with gzip.GzipFile(filename='', fileobj=output, mode='wb', mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode='w', format=tarfile.USTAR_FORMAT) as archive:
                for name in names:
                    path = runtime / name
                    if path.is_symlink() or not path.is_file():
                        raise ValueError('invalid compiler package file')
                    info = tarfile.TarInfo(name)
                    info.size = path.stat().st_size
                    info.mode = 0o444
                    with path.open('rb') as stream:
                        archive.addfile(info, stream)


if __name__ == '__main__':
    import argparse
    parser = argparse.ArgumentParser()
    parser.add_argument('--version', action='append')
    args = parser.parse_args()
    for entry in json.loads((HERE.parent / 'versions/index.json').read_text())['versions']:
        if not args.version or entry['version'] in args.version:
            bundle(entry, ROOT / '.local/vyper-wasm/packages' / entry['version'])
            print('packaged', entry['version'], flush=True)
