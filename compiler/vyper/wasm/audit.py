"""Audit actual WASM distributions, including vendors and the native core.

Live services are mandatory. No installed host environment or cached clean
report substitutes for the selected artifacts. Reviews are exact-content,
exact-inventory dispositions, not global vulnerability suppressions.
"""
import concurrent.futures
import email
import io
import json
import re
import time
import urllib.request
import zipfile
from datetime import datetime, timezone
from pathlib import Path

from packages import ROOT, HERE, digest, safe_member
from release import SHARED_SHA256

OUTPUT = ROOT / '.local/vyper-wasm/audit'


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':')).encode()


def normalized(name):
    return re.sub(r'[-_.]+', '-', name).lower()


def vendored_metadata(archive, origin):
    records = []
    for name in archive.namelist():
        if not name.endswith(('/vendor.txt', '/vendored.txt')):
            continue
        for line in archive.read(name).decode().splitlines():
            line = line.split('#', 1)[0].strip()
            if not line:
                continue
            match = re.fullmatch(r'([\w.-]+)==([\w.+-]+)', line)
            if not match:
                raise ValueError('unresolved vendored distribution')
            package, version = match.groups()
            prefix = name.rsplit('/', 1)[0] + '/' + package.replace('-', '_') + '/'
            members = [n for n in archive.namelist() if n.startswith(prefix)]
            notices = [n for n in members if Path(n).name.lower().startswith(('license', 'copying'))]
            if not any(n.endswith('.py') for n in members) or not notices or any(not archive.read(n).strip() for n in notices):
                raise ValueError('missing vendored implementation or license')
            records.append({'name': normalized(package), 'version': version, 'origin': origin,
                            'metadata': name + '#' + package})
    return records


def metadata(raw, origin):
    records = []
    with zipfile.ZipFile(io.BytesIO(raw)) as archive:
        names = archive.namelist()
        if len(names) != len(set(names)):
            raise ValueError('duplicate archive member')
        for name in names:
            if name.endswith('/'):
                continue
            safe_member(name)
            if not name.endswith('.dist-info/METADATA'):
                continue
            value = email.message_from_bytes(archive.read(name))
            if len(value.get_all('Name', [])) != 1 or len(value.get_all('Version', [])) != 1:
                raise ValueError('missing or ambiguous distribution identity')
            records.append({'name': normalized(value['Name']), 'version': value['Version'],
                            'origin': origin, 'metadata': name})
        records.extend(vendored_metadata(archive, origin))
    return records


def inventory():
    records, identities = [], {}
    shared = ROOT / '.local/vyper-wasm/shared'
    if shared.is_symlink():
        raise ValueError('invalid shared directory')
    raw = (shared / 'shared-manifest.json').read_bytes()
    if digest(raw) != SHARED_SHA256:
        raise ValueError('unauthenticated shared runtime')
    identities['shared'] = digest(raw)
    files = json.loads(raw)['files']
    if sorted(p.name for p in shared.iterdir()) != sorted(['shared-manifest.json'] + [f['path'] for f in files]):
        raise ValueError('unlisted shared payload')
    for entry in files:
        safe_member(entry['path'])
        path = shared / entry['path']
        if path.is_symlink() or not path.is_file():
            raise ValueError('invalid shared payload')
        data = path.read_bytes()
        if digest(data) != entry['sha256']:
            raise ValueError('shared runtime payload changed')
        if entry['path'].endswith(('.whl', '.zip')):
            records.extend(metadata(data, 'shared/' + entry['path']))
    index = json.loads((HERE.parent / 'versions/index.json').read_bytes())['versions']
    for item in index:
        package = ROOT / '.local/vyper-wasm/packages' / item['version']
        if package.is_symlink() or sorted(p.name for p in package.iterdir()) != ['package-manifest.json', 'packages.zip']:
            raise ValueError('invalid compiler package directory')
        if any(p.is_symlink() or not p.is_file() for p in package.iterdir()):
            raise ValueError('invalid compiler package file')
        raw = (package / 'package-manifest.json').read_bytes()
        manifest = json.loads(raw)
        if manifest['version'] != item['version'] or manifest['compiler_sha256'] != item['compiler_sha256']:
            raise ValueError('compiler identity changed')
        data = (package / 'packages.zip').read_bytes()
        if manifest['files'] != [{'path': 'packages.zip', 'sha256': digest(data)}]:
            raise ValueError('compiler payload changed')
        found = metadata(data, item['version'])
        direct = {(normalized(d['name']), d['version']) for d in manifest['dependencies']}
        direct.add(('vyper', item['version']))
        top_level = {(d['name'], d['version']) for d in found if d['metadata'].count('/') == 1}
        if top_level != direct:
            raise ValueError('distribution inventory does not match package manifest')
        records.extend(found)
        identities[item['version']] = digest(raw)
    if not records:
        raise ValueError('empty distribution inventory')
    # A code/policy change invalidates the reachability review as well.
    for name in ['compiler/vyper/adapter.py', 'compiler/vyper/wasm/protocol.py',
                 'compiler/vyper/wasm/execute.mjs', 'compiler/package-lock.json',
                 'internal/verify/vyper.go', 'internal/verify/vyper_wasm_manifest.go',
                 'compiler/vyper/wasm/licenses.lock.json']:
        identities[name] = digest((ROOT / name).read_bytes())
    return {'identities': identities, 'distributions': sorted(records, key=lambda x: canonical(x))}


def fetch(url, payload=None):
    for attempt in range(3):
        try:
            request = urllib.request.Request(url, data=None if payload is None else canonical(payload),
                                             headers={'Content-Type': 'application/json', 'User-Agent': 'etherview-wasm-audit'})
            with urllib.request.urlopen(request, timeout=60) as response:
                raw = response.read((32 << 20) + 1)
            if len(raw) > 32 << 20:
                raise ValueError('oversized audit response')
            return json.loads(raw)
        except OSError:
            if attempt == 2:
                raise
            time.sleep(attempt + 1)


def osv(query):
    findings = []
    tokens = set()
    while True:
        response = fetch('https://api.osv.dev/v1/query', query)
        if not isinstance(response, dict) or set(response) - {'vulns', 'next_page_token'}:
            raise ValueError('invalid OSV response')
        if not isinstance(response.get('vulns', []), list):
            raise ValueError('invalid OSV findings')
        findings.extend(response.get('vulns', []))
        token = response.get('next_page_token')
        if not token:
            return findings
        if token in tokens:
            raise ValueError('repeated OSV page')
        tokens.add(token)
        query = dict(query, page_token=token)


def query_package(identity):
    name, version = identity
    if (name, version) == ('vyper', '0.2.0'):
        # This official source-only release has no PyPI version endpoint.
        findings = osv({'package': {'ecosystem': 'PyPI', 'name': name}, 'version': version})
    else:
        response = fetch(f'https://pypi.org/pypi/{name}/{version}/json')
        if normalized(response['info']['name']) != name or response['info']['version'] != version:
            raise ValueError('audit-service identity mismatch')
        findings = response['vulnerabilities']
        if not isinstance(findings, list):
            raise ValueError('invalid PyPI findings')
    return {'component': f'PyPI/{name}/{version}', 'findings': findings}


def query_core(component):
    response = osv({'commit': component['commit']})
    findings = {f['id']: f for f in response}
    if len(findings) != len(response):
        raise ValueError('duplicate core advisory')
    # Source-import forks can lack upstream commit ancestry in OSV. Explicit
    # upstream records supplement, never replace, the commit query.
    for identifier in component.get('advisories', []):
        finding = fetch('https://api.osv.dev/v1/vulns/' + identifier)
        if finding.get('id') != identifier:
            raise ValueError('core advisory identity mismatch')
        if identifier in findings and finding_digest(findings[identifier]) != finding_digest(finding):
            raise ValueError('inconsistent core advisory')
        findings[identifier] = finding
    return {'component': 'core/' + component['name'], 'findings': list(findings.values())}


def finding_digest(finding):
    # Pin the substantive advisory, not feed bookkeeping timestamps.
    return digest(canonical({k: v for k, v in finding.items() if k not in ('modified', 'published')}))


def evaluate(actual, reports, review):
    if digest(canonical(actual)) != review['inventory_sha256']:
        raise ValueError('WASM distribution inventory changed; review required')
    expected = {'PyPI/' + d['name'] + '/' + d['version'] for d in actual['distributions']}
    expected.update('core/' + c['name'] for c in review['core'])
    components = [r['component'] for r in reports]
    if set(components) != expected or len(components) != len(expected):
        raise ValueError('audit coverage is incomplete or duplicated')
    problems, count = [], 0
    for report in reports:
        seen = set()
        for finding in report['findings']:
            identifier = finding['id']
            if identifier in seen:
                raise ValueError('duplicate advisory')
            seen.add(identifier)
            # The inventory pins every supported version and its exact bytes.
            # Group identical advisory text across those versions for review.
            scope = report['component'].rsplit('/', 1)[0] if report['component'].startswith('PyPI/') else report['component']
            key = scope + '/' + identifier
            disposition = review['findings'].get(key)
            if (not disposition or finding_digest(finding) not in disposition['sha256']
                    or disposition['reason'] not in review['reasons']):
                problems.append(key)
            count += 1
    if problems:
        raise ValueError('unreviewed or changed advisories: ' + ', '.join(problems))
    return count


def main():
    OUTPUT.mkdir(parents=True, exist_ok=True)
    # Remove a prior success marker before any operation that could fail.
    report_path = OUTPUT / 'report.json'
    report_path.write_text(json.dumps({'status': 'incomplete'}) + '\n')
    for name in ('inventory.json', 'raw.json'):
        (OUTPUT / name).unlink(missing_ok=True)
    actual = inventory()
    review = json.loads((HERE / 'audit-review.json').read_bytes())
    (OUTPUT / 'inventory.json').write_bytes(canonical(actual) + b'\n')
    identities = sorted({(d['name'], d['version']) for d in actual['distributions']})
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
        reports = list(pool.map(query_package, identities))
        reports.extend(pool.map(query_core, review['core']))
    (OUTPUT / 'raw.json').write_bytes(canonical(reports) + b'\n')
    count = evaluate(actual, reports, review)
    result = {'status': 'pass-with-reviewed-findings', 'queried_at': datetime.now(timezone.utc).isoformat(),
              'inventory_sha256': digest(canonical(actual)), 'raw_sha256': digest(canonical(reports)),
              'review_sha256': digest((HERE / 'audit-review.json').read_bytes()),
              'distributions': len(actual['distributions']), 'components': len(reports), 'findings': count}
    report_path.write_text(json.dumps(result, indent=2) + '\n')
    print(f'WASM security audit: PASS ({len(reports)} components, {count} explicitly reviewed findings; not zero vulnerabilities)')


if __name__ == '__main__':
    main()
