"""PEP 440 discovery and wheel selection for the Node update orchestrator.

Run through the locked uv tooling project; never used by production.
"""
import hashlib
import json
import re
import subprocess
import sys
import tempfile
import time
import urllib.request
from pathlib import Path
from urllib.parse import urlsplit

from packaging.specifiers import SpecifierSet
from packaging.requirements import Requirement
from packaging.markers import default_environment
from packaging.tags import compatible_tags
from packaging.utils import canonicalize_name, parse_wheel_filename
from packaging.version import InvalidVersion, Version

LIMIT = 32 << 20


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise ValueError("upstream redirect rejected")


def fetch(url, limit=LIMIT):
    parsed = urlsplit(url)
    if parsed.scheme != "https" or parsed.netloc not in {"pypi.org", "files.pythonhosted.org"} or parsed.query or parsed.fragment:
        raise ValueError("unapproved upstream URL")
    for attempt in range(3):
        try:
            request = urllib.request.Request(url, headers={"User-Agent": "etherview-vyper-update"})
            with urllib.request.build_opener(NoRedirect).open(request, timeout=30) as response:
                data = response.read(limit + 1)
                if len(data) > limit:
                    raise ValueError("upstream response exceeds limit")
                return data
        except (OSError, TimeoutError):
            if attempt == 2:
                raise
            time.sleep(attempt + 1)
    raise RuntimeError("unreachable")


def project(name, version=None):
    if not re.fullmatch(r"[A-Za-z0-9_.-]+", name) or (version and not re.fullmatch(r"[A-Za-z0-9_.+!-]+", version)):
        raise ValueError("invalid package identity")
    suffix = f"/{version}" if version else ""
    result = json.loads(fetch(f"https://pypi.org/pypi/{name}{suffix}/json"))
    if canonicalize_name(result['info']['name']) != canonicalize_name(name):
        raise ValueError("package identity mismatch")
    if version and Version(result['info']['version']) != Version(version):
        raise ValueError("package version mismatch")
    return result


def stable(value):
    try:
        version = Version(value)
    except InvalidVersion:
        return False
    # Public compiler identifiers currently accept exactly major.minor.patch.
    return not (version.is_prerelease or version.is_devrelease or version.is_postrelease or version.local or version.epoch) and bool(re.fullmatch(r"\d+\.\d+\.\d+", value)) and str(version) == value


def discover(releases, current):
    highest = max(Version(value) for value in current)
    return sorted((value for value, files in releases.items() if stable(value) and Version(value) > highest and any(f['packagetype'] == 'bdist_wheel' and not f.get('yanked', False) for f in files)), key=Version)


def select_wheel(data, name, version, python):
    candidates = []
    target = Version(python)
    supported = set(compatible_tags((target.major, target.minor), platforms=['any']))
    for item in data['urls']:
        if item['packagetype'] != 'bdist_wheel' or item.get('yanked', False):
            continue
        if item.get('requires_python') and not SpecifierSet(item['requires_python']).contains(python):
            continue
        wheel_name, wheel_version, _, tags = parse_wheel_filename(item['filename'])
        if canonicalize_name(wheel_name) != canonicalize_name(name) or wheel_version != Version(version):
            raise ValueError("wheel identity mismatch")
        if any(tag.abi == 'none' and tag.platform == 'any' and tag in supported for tag in tags):
            candidates.append(item)
    if not candidates:
        raise ValueError(f"no compatible pure Python wheel: {name}=={version}; manual runtime review required")
    item = sorted(candidates, key=lambda item: item['filename'])[0]
    digest = item['digests']['sha256']
    if not re.fullmatch(r'[a-f0-9]{64}', digest) or not isinstance(item['size'], int) or not 0 < item['size'] <= 200 << 20:
        raise ValueError('invalid archive identity')
    raw = fetch(item['url'], 200 << 20)
    if len(raw) != item['size'] or hashlib.sha256(raw).hexdigest() != digest:
        raise ValueError('archive authentication failed')
    return {'name': canonicalize_name(name), 'version': version, 'filename': item['filename'], 'sha256': digest, 'url': item['url'], 'size': len(raw)}


def resolve(request):
    if not stable(request['version']):
        raise ValueError('unsupported compiler version')
    python = request['python']
    if not subprocess.check_output(['uv', '--version'], text=True).startswith('uv 0.12.24 '):
        raise ValueError('uv 0.12.24 required')
    # Pin the shared PyCryptodome version so resolution cannot silently upgrade it.
    with tempfile.TemporaryDirectory(prefix='vyper-resolve-') as temporary:
        source = Path(temporary) / 'requirements.in'
        output = Path(temporary) / 'requirements.lock'
        source.write_text(f"vyper=={request['version']}\npycryptodome=={request['pycryptodome']}\n")
        subprocess.run(['uv', '--no-config', 'pip', 'compile', str(source), '--python-version', python, '--python-platform', 'x86_64-unknown-linux-gnu', '--only-binary', ':all:', '--generate-hashes', '--no-header', '--no-annotate', '--index-url', 'https://pypi.org/simple', '--output-file', str(output)], check=True, timeout=180, stdout=sys.stderr)
        lock = output.read_text()
    pins = re.findall(r'^([A-Za-z0-9_.-]+)==([^\s\\]+)', lock, re.M)
    if not pins or any(line and not line.startswith((' ', '#')) and not re.match(r'^[A-Za-z0-9_.-]+==', line) for line in lock.splitlines()):
        raise ValueError('unexpected resolver output')
    archives = []
    pinned = {canonicalize_name(name): version for name, version in pins}
    environment = dict(default_environment(), python_version='.'.join(python.split('.')[:2]), python_full_version=python, implementation_version=python, sys_platform='emscripten', platform_system='Emscripten', platform_machine='wasm32', extra='')
    for name, version in pins:
        if canonicalize_name(name) == 'pycryptodome':
            if version != request['pycryptodome']:
                raise ValueError('shared runtime dependency changed')
            continue
        data = project(name, version)
        for declaration in data['info'].get('requires_dist') or []:
            requirement = Requirement(declaration)
            if requirement.url:
                raise ValueError('direct dependency URL requires manual review')
            if requirement.marker is None or requirement.marker.evaluate(environment):
                selected = pinned.get(canonicalize_name(requirement.name))
                if selected is None or not requirement.specifier.contains(selected):
                    raise ValueError('WASM dependency closure differs from native resolution; manual review required')
        archives.append(select_wheel(data, name, version, python))
    # Include the selected pure wheel hashes, even if the resolver selected a
    # native wheel for reference installation. Native references remain hash locked.
    for archive in archives:
        pattern = rf"(^\s*{re.escape(archive['name'])}=={re.escape(archive['version'])}[^\n]*)"
        lock = re.sub(pattern, lambda m: m[1].removesuffix('\\').rstrip() + ' \\\n    --hash=sha256:' + archive['sha256'] + ' \\', lock, count=1, flags=re.M)
    return {'archives': sorted(archives, key=lambda a: a['name']), 'lock': lock}


def main():
    request = json.load(sys.stdin)
    if request['mode'] == 'discover':
        result = discover(project('vyper')['releases'], request['current'])
    elif request['mode'] == 'resolve':
        result = resolve(request)
    else:
        raise ValueError('invalid operation')
    json.dump(result, sys.stdout)
    sys.stdout.write('\n')


if __name__ == '__main__':
    main()
