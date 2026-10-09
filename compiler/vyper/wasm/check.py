"""Run the candidate's authenticated, offline, full-version differential gate."""
import json
from pathlib import Path
import subprocess

from packages import ROOT, HERE, bundle


def main():
    subprocess.run(['node', str(HERE / 'prepare.mjs')], cwd=ROOT, check=True)
    reports = []
    for item in json.loads((HERE.parent / 'versions/index.json').read_text())['versions']:
        version = item['version']
        destination = ROOT / '.local/vyper-wasm/packages' / version
        bundle(item, destination)
        result = subprocess.run([
            'node', '--permission', '--allow-fs-read=' + str(ROOT), '--no-addons',
            '--max-old-space-size=128', '--wasm-max-mem-pages=6144',
            str(HERE / 'matrix.mjs'), str(ROOT / '.local/vyper-wasm/shared'),
            str(destination), str(ROOT / 'internal/verify/testdata/compiler/vyper/versions' / version), version,
        ], cwd=ROOT, capture_output=True, text=True, timeout=120)
        if result.returncode:
            raise RuntimeError(version + ' WASM differential failed: ' + result.stderr[-3000:])
        report = json.loads(result.stdout)
        reports.append(report)
        print(version, report['cases'], 'cases passed', flush=True)
    (ROOT / '.local/vyper-wasm/matrix-results.json').write_text(json.dumps(reports, indent=2) + '\n')


if __name__ == '__main__':
    main()
