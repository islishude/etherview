"""Measure the actual restricted SEA; every sample uses a fresh process.

The first pair is process-cold, not an OS page-cache eviction experiment.
Warm samples reuse on-disk artifacts, never a Python interpreter or worker.
"""
import argparse
import copy
import json
import os
import platform
import signal
import statistics
import tempfile
import time
import subprocess
import zipfile
from pathlib import Path

import hashlib

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]

def digest(data):
    return hashlib.sha256(data).hexdigest()


def measure(command, value, cwd, timeout=30):
    raw = json.dumps(value, sort_keys=True).encode()
    with tempfile.TemporaryFile() as stdin, tempfile.TemporaryFile() as stdout, tempfile.TemporaryFile() as stderr:
        stdin.write(raw)
        stdin.seek(0)
        start = time.monotonic()
        child = subprocess.Popen(command, stdin=stdin, stdout=stdout, stderr=stderr,
                                 cwd=cwd, env={'LANG': 'C', 'TZ': 'UTC'}, start_new_session=True)
        while True:
            pid, status, usage = os.wait4(child.pid, os.WNOHANG)
            if pid:
                child.returncode = os.waitstatus_to_exitcode(status)
                break
            if time.monotonic() - start > timeout:
                os.killpg(child.pid, signal.SIGKILL)
                child.wait()
                raise RuntimeError('benchmark exceeded the production 30-second deadline')
            time.sleep(0.005)
        elapsed = time.monotonic() - start
        stdout.seek(0)
        stderr.seek(0)
        if child.returncode:
            raise RuntimeError('benchmark compiler failed: ' + stderr.read(4096).decode(errors='replace'))
        result = json.load(stdout)
        if any(e.get('severity') == 'error' for e in result.get('errors', [])) or not result.get('contracts'):
            raise RuntimeError('benchmark did not produce a valid contract: ' + repr(result.get('errors')))
        rss = usage.ru_maxrss / (1024 * 1024 if platform.system() == 'Darwin' else 1024)
        return {'seconds': elapsed, 'peak_rss_mib': rss}, result['contracts']


def large_input():
    value = json.loads((ROOT / 'internal/verify/testdata/compiler/vyper/versions/0.4.3/plain.input.json').read_bytes())
    # Sixteen imported modules, each used by the entrypoint, plus 128 public
    # functions. Comments bring the parser input above 128 KiB without making
    # deployed bytecode exceed EIP-170's limit.
    sources = {}
    for i in range(16):
        sources[f'lib{i}.vy'] = {'content': '# workload padding\n' * 450 +
                                f'@internal\n@pure\ndef add(x: uint256) -> uint256:\n    return x + {i + 1}\n'}
    main = ''.join(f'import lib{i} as lib{i}\n' for i in range(16))
    for i in range(128):
        main += f'@external\n@pure\ndef f{i}(x: uint256) -> uint256:\n    return lib{i % 16}.add(x)\n'
    sources['A.vy'] = {'content': main}
    value['sources'] = sources
    modified = copy.deepcopy(value)
    modified['sources']['A.vy']['content'] += '\n# deterministic source perturbation\n'
    return value, modified


def native_identity(executable):
    information = json.loads(subprocess.check_output([str(executable.absolute()), '-I', '-c',
        'import json,sys,pathlib,vyper,importlib.metadata as m;print(json.dumps(dict('
        'python=sys.version,version=vyper.__version__,root=str(pathlib.Path(vyper.__file__).parent),'
        'distributions=sorted((d.metadata["Name"],d.version) for d in m.distributions()))))'], timeout=10))
    item = next(x for x in json.loads((HERE.parent / 'versions/index.json').read_bytes())['versions'] if x['version'] == '0.4.3')
    wheel = ROOT / '.local/vyper-wasm/downloads' / item['compiler_sha256']
    if information['version'] != '0.4.3' or digest(wheel.read_bytes()) != item['compiler_sha256']:
        raise RuntimeError('unauthenticated native reference compiler')
    with zipfile.ZipFile(wheel) as archive:
        for name in archive.namelist():
            if name.startswith('vyper/') and name.endswith('.py'):
                installed = Path(information['root']) / name.removeprefix('vyper/')
                if installed.read_bytes() != archive.read(name):
                    raise RuntimeError('native compiler implementation changed')
    information.pop('root')
    information['compiler_sha256'] = item['compiler_sha256']
    return information


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--samples', type=int, default=3)
    parser.add_argument('--version', action='append')
    parser.add_argument('--native-python', type=Path, help='optional official Vyper 0.4.3 reference interpreter')
    parser.add_argument('--output', type=Path, default=ROOT / '.local/vyper-wasm/benchmark.json')
    args = parser.parse_args()
    if args.samples < 3:
        parser.error('at least three warm pairs are required')
    shared = ROOT / '.local/vyper-wasm/shared'
    exe = ROOT / '.local/vyper-wasm/sea/etherview-solcjs'
    versions = [x['version'] for x in json.loads((HERE.parent / 'versions/index.json').read_bytes())['versions']]
    if args.version and not set(args.version) <= set(versions):
        parser.error('unknown compiler version')
    workloads = [(v, 'plain') for v in versions if not args.version or v in args.version]
    if not args.version or '0.4.3' in args.version:
        workloads.append(('0.4.3', 'large-multifile'))
    report = {'schema': 'etherview-vyper-wasm-benchmark-v1', 'system': platform.platform(),
              'machine': platform.machine(), 'cpu': platform.processor(),
              'sea_sha256': digest(exe.read_bytes()),
              'shared_sha256': digest((shared / 'shared-manifest.json').read_bytes()),
              'method': 'first fresh-process pair then repeated fresh-process pairs; OS caches not evicted; no concurrent workload',
              'limits': {'timeout_seconds': 30, 'v8_old_mib': 128, 'wasm_mib': 384}, 'complete': False, 'workloads': []}
    if args.native_python:
        report['native_reference'] = native_identity(args.native_python)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2) + '\n')
    for version, workload in workloads:
        package = ROOT / '.local/vyper-wasm/packages' / version
        fixtures = ROOT / 'internal/verify/testdata/compiler/vyper/versions' / version
        if workload == 'plain':
            inputs = [json.loads((fixtures / f'plain.{suffix}.json').read_bytes()) for suffix in ('input', 'modified')]
            expected = [json.loads((fixtures / f'plain.{suffix}.json').read_bytes())['contracts'] for suffix in ('output', 'modified_output')]
        else:
            inputs = large_input()
            reference = json.loads((HERE / 'benchmark-reference.json').read_bytes())
            if reference['input_sha256'] != [digest(json.dumps(v, sort_keys=True).encode()) for v in inputs]:
                raise RuntimeError('larger workload changed; official reference regeneration required')
            expected = reference['contracts_sha256']
        command = [str(exe), f'--node-options=--max-old-space-size=128 --wasm-max-mem-pages=6144 --allow-fs-read={shared} --allow-fs-read={package}',
                   '--vyper-compile', str(shared), str(package), version, '5242880', '33554432']
        record = {'version': version, 'workload': workload,
                  'package_sha256': digest((package / 'package-manifest.json').read_bytes()),
                  'input_sha256': [digest(json.dumps(v, sort_keys=True).encode()) for v in inputs],
                  'input_bytes': [len(json.dumps(v, sort_keys=True).encode()) for v in inputs],
                  'source_files': len(inputs[0]['sources']), 'pairs': []}
        with tempfile.TemporaryDirectory(prefix='vyper-benchmark-') as directory:
            for sample in range(args.samples + 1):
                pair, contracts = [], []
                for value in inputs:
                    measurement, output = measure(command, value, directory)
                    pair.append(measurement)
                    contracts.append(output)
                actual = contracts if workload == 'plain' else [digest(json.dumps(c, sort_keys=True, separators=(',', ':')).encode()) for c in contracts]
                if actual != expected:
                    raise RuntimeError('benchmark output differs from official compiler reference')
                record['pairs'].append({'phase': 'first' if sample == 0 else 'warm', 'processes': pair,
                                        'dual_seconds': sum(x['seconds'] for x in pair),
                                        'peak_rss_mib': max(x['peak_rss_mib'] for x in pair)})
            if args.native_python and version == '0.4.3':
                native_command = [str(args.native_python.absolute()), '-I', '-c',
                    'import sys,json,vyper;assert vyper.__version__=="0.4.3";sys.path.insert(0,sys.argv[1]);'
                    'from adapter import compile_input;print(json.dumps(compile_input(json.load(sys.stdin)),default=str))', str(HERE.parent)]
                record['native_reference_pairs'] = []
                for _ in range(args.samples + 1):
                    pair = []
                    for value, reference_output in zip(inputs, expected):
                        measurement, output = measure(native_command, value, directory)
                        actual = output if workload == 'plain' else digest(json.dumps(output, sort_keys=True, separators=(',', ':')).encode())
                        if actual != reference_output:
                            raise RuntimeError('native reference output mismatch')
                        pair.append(measurement)
                    record['native_reference_pairs'].append(pair)
        warm = record['pairs'][1:]
        record['warm_dual_median_seconds'] = statistics.median(p['dual_seconds'] for p in warm)
        record['max_peak_rss_mib'] = max(p['peak_rss_mib'] for p in record['pairs'])
        report['workloads'].append(record)
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(report, indent=2) + '\n')
        print(f'{version}/{workload}: first dual {record["pairs"][0]["dual_seconds"]:.3f}s, '
              f'warm dual median {record["warm_dual_median_seconds"]:.3f}s, peak {record["max_peak_rss_mib"]:.1f} MiB', flush=True)
    report['complete'] = True
    args.output.write_text(json.dumps(report, indent=2) + '\n')


if __name__ == '__main__':
    main()
