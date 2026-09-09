"""Build the pinned compiler bundle. Native Python is a build tool, not payload."""
import argparse
import datetime
import hashlib
import importlib.util
import importlib._bootstrap_external
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tarfile
import tempfile
import zipfile

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
LOCK = json.loads((HERE / 'runtime.lock.json').read_text())

def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

def download(info, directory):
    name = info.get('filename', info['url'].rsplit('/', 1)[-1])
    path = directory / name
    if path.is_file() and sha(path) == info['sha256']:
        return path
    seed = HERE / 'downloads' / name
    if seed.is_file() and sha(seed) == info['sha256']:
        shutil.copyfile(seed, path)
        return path
    print('Downloading pinned input:', name, flush=True)
    temporary = path.with_suffix(path.suffix + '.download')
    subprocess.run(['curl', '-fLsS', '--retry', '2', '--max-time', '300',
                    info['url'], '-o', str(temporary)], check=True)
    if sha(temporary) != info['sha256']:
        raise ValueError('build input checksum mismatch: ' + name)
    temporary.replace(path)
    return path

def unpack(archive, destination):
    if destination.exists():
        shutil.rmtree(destination)
    temporary = destination.with_name(destination.name + '.extract')
    if temporary.exists():
        shutil.rmtree(temporary)
    temporary.mkdir()
    with tarfile.open(archive) as tar:
        tar.extractall(temporary, filter='data')
    roots = list(temporary.iterdir())
    if len(roots) != 1 or not roots[0].is_dir():
        raise ValueError('unexpected source archive layout')
    temporary.replace(destination)
    return next(destination.iterdir())

def run(command, cwd, env, log):
    print('Running:', ' '.join(map(str, command)), flush=True)
    with log.open('w') as output:
        subprocess.run(list(map(str, command)), cwd=cwd, env=env,
                       stdout=output, stderr=subprocess.STDOUT, check=True)

def python_build(source, sdk, cache, env, clean):
    work = source / 'cross-build/wasm32-wasip1'
    if clean and work.exists():
        shutil.rmtree(work)
    (work / 'Modules').mkdir(parents=True, exist_ok=True)
    shutil.copyfile(HERE / 'python/keccak_module.c', source / 'Modules/keccak_module.c')
    (work / 'Modules/Setup.local').write_text('*static*\n_etherview_keccak keccak_module.c\n')
    imports = source / 'Modules/etherview-imports.txt'
    imports.write_text('etherview_keccak256\n')
    build_env = dict(env)
    for key, executable in [('CC', 'clang'), ('CPP', 'clang-cpp'), ('CXX', 'clang++'), ('AR', 'llvm-ar'), ('RANLIB', 'ranlib')]:
        build_env[key] = str(sdk / 'bin' / executable)
        if key in ('CC', 'CPP', 'CXX'):
            build_env[key] += ' --sysroot=' + str(sdk / 'share/wasi-sysroot')
    build_env.update(CONFIG_SITE=str(source / 'Tools/wasm/config.site-wasm32-wasi'),
                     PKG_CONFIG_PATH='', PKG_CONFIG_LIBDIR=str(sdk / 'share/wasi-sysroot/lib/pkgconfig'),
                     WASI_SDK_PATH=str(sdk), CFLAGS='-O2 -g0',
                     LDFLAGS='-Wl,--allow-undefined-file=' + str(imports),
                     HOSTRUNNER=str(cache / 'wasmpython-build') + ' ' + str(source))
    stamp = hashlib.sha256((json.dumps(LOCK, sort_keys=True) + sha(HERE / 'python/keccak_module.c')).encode()).hexdigest()
    if not (work / 'build-input.sha256').exists() or (work / 'build-input.sha256').read_text() != stamp:
        build = subprocess.check_output([str(source / 'config.guess')], text=True).strip()
        run(['../../configure', '--host=' + LOCK['target'], '--build=' + build,
             '--with-build-python=' + sys.executable, '--prefix=/runtime',
             '--disable-test-modules', '--without-ensurepip'], work, build_env, cache / 'configure.log')
    run(['make', '-j', str(min(os.cpu_count() or 1, 8))], work, build_env, cache / 'make.log')
    (work / 'build-input.sha256').write_text(stamp)
    return work / 'python.wasm'

def library_entries(source, packages):
    entries = {}
    for path in sorted((source / 'Lib').rglob('*.py')):
        relative = path.relative_to(source / 'Lib')
        if any(part in ('test', 'tests', '__pycache__', 'site-packages', 'idlelib', 'tkinter', 'ensurepip') for part in relative.parts):
            continue
        entries['lib/python3.13/' + relative.as_posix()] = path.read_bytes()
    for name, archive in packages.items():
        if archive.suffix == '.whl':
            with zipfile.ZipFile(archive) as wheel:
                for entry in wheel.infolist():
                    if not entry.is_dir():
                        entries['site-packages/' + entry.filename] = wheel.read(entry)
        else:
            with tarfile.open(archive) as tar:
                for entry in tar.getmembers():
                    parts = Path(entry.name).parts[1:]
                    if entry.isfile() and len(parts) > 1 and parts[0] == name and entry.name.endswith('.py'):
                        entries['site-packages/' + '/'.join(parts)] = tar.extractfile(entry).read()
    for name in ('entry.py', 'compiler_crypto.py'):
        entries[name] = (HERE / 'python' / name).read_bytes()
    # Preserve every wheel member, adding deterministic, checked-hash bytecode.
    for name, data in list(entries.items()):
        if not name.endswith('.py'):
            continue
        code = compile(data, '/runtime/' + name, 'exec', dont_inherit=True, optimize=0)
        bytecode = importlib._bootstrap_external._code_to_hash_pyc(code, importlib.util.source_hash(data), checked=True)
        path = Path(name)
        cached = path.parent / '__pycache__' / (path.stem + '.cpython-313.pyc')
        entries[cached.as_posix()] = bytecode
    return entries

def make_archive(entries, destination):
    timestamp = datetime.datetime.fromtimestamp(LOCK['source_date_epoch'], datetime.timezone.utc).timetuple()[:6]
    with zipfile.ZipFile(destination, 'w', compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
        for name, data in sorted(entries.items()):
            info = zipfile.ZipInfo(name, timestamp)
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = 0o100444 << 16
            archive.writestr(info, data)

def main():
    if sys.version_info[:3] != (3, 13, 15):
        raise ValueError('Python 3.13.15 is required to build the fixed runtime')
    if os.environ.get('PYTHONHASHSEED') != '0':
        os.execve(sys.executable, [sys.executable, *sys.argv], dict(os.environ, PYTHONHASHSEED='0'))
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--cache', type=Path, default=REPO / '.local/wasm')
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--clean-python', action='store_true')
    parser.add_argument('--tools-directory', type=Path)
    args = parser.parse_args()
    cache, output = args.cache.resolve(), args.output.resolve()
    if output.exists():
        raise ValueError('output must not exist; use a new build directory')
    cache.mkdir(parents=True, exist_ok=True)
    downloads = cache / 'downloads'
    downloads.mkdir(exist_ok=True)
    arch = {'aarch64': 'arm64', 'arm64': 'arm64', 'x86_64': 'x86_64'}[platform.machine()]
    system = {'Darwin': 'macos', 'Linux': 'linux'}[platform.system()]
    sdk_info = LOCK['wasi_sdk']['archives'][arch + '-' + system]
    with tempfile.TemporaryDirectory(prefix='etherview-wasm-source-') as temporary:
        workspace = Path(temporary)
        sdk = unpack(download(sdk_info, downloads), workspace / 'sdk')
        source = unpack(download(LOCK['python'], downloads), workspace / 'source')
        packages = {name: download(info, downloads) for name, info in LOCK['packages'].items()}
        env = dict(os.environ, SOURCE_DATE_EPOCH=str(LOCK['source_date_epoch']), CGO_ENABLED='0', PYTHONHASHSEED='0')
        if args.tools_directory:
            tools = args.tools_directory.resolve()
            shutil.copyfile(tools / 'wasmpython-build', cache / 'wasmpython-build')
            (cache / 'wasmpython-build').chmod(0o755)
        else:
            tools = None
            run(['go', 'build', '-trimpath', '-buildvcs=false', '-o', cache / 'wasmpython-build', './cmd/wasmpython-build'], REPO, env, cache / 'go-build-tool.log')
        binary = python_build(source, sdk, cache, env, args.clean_python)
        run([sys.executable, HERE / 'check_hash.py', cache, source], REPO, env, cache / 'hash-check.log')
        output.mkdir(parents=True)
        shutil.copyfile(binary, output / 'python.wasm')
        make_archive(library_entries(source, packages), output / 'python.zip')
        runtime_lock = dict(LOCK, adapters={name: sha(HERE / name) for name in ('build.py', 'python/entry.py', 'python/compiler_crypto.py', 'python/keccak_module.c')})
        (output / 'runtime.lock.json').write_text(json.dumps(runtime_lock, sort_keys=True, separators=(',', ':')) + '\n')
        licenses = output / 'licenses'
        licenses.mkdir()
        shutil.copyfile(source / 'LICENSE', licenses / 'CPython-LICENSE.txt')
        shutil.copyfile(HERE / 'solc-js-LICENSE.txt', licenses / 'solc-js-LICENSE.txt')
        go_modules = {
            'wazero': 'github.com/tetratelabs/wazero',
            'wabin': 'github.com/tetratelabs/wabin',
            'lz4': 'github.com/pierrec/lz4/v4',
            'x-crypto': 'golang.org/x/crypto',
            'x-sys': 'golang.org/x/sys',
        }
        if tools:
            for path in (tools / 'licenses').iterdir():
                shutil.copyfile(path, licenses / path.name)
        else:
            for name, module in go_modules.items():
                directory = subprocess.check_output(['go', 'list', '-m', '-f', '{{.Dir}}', module], cwd=REPO, text=True).strip()
                shutil.copyfile(Path(directory) / 'LICENSE', licenses / ('go-' + name + '-LICENSE.txt'))
            goroot = subprocess.check_output(['go', 'env', 'GOROOT'], cwd=REPO, text=True).strip()
            candidates = [Path(goroot) / 'LICENSE', Path(goroot).parent / 'LICENSE']
            go_license = next((p for p in candidates if p.is_file()), None)
            if go_license is None:
                raise ValueError('Go toolchain license is missing')
            shutil.copyfile(go_license, licenses / 'Go-LICENSE.txt')

        for name, expected in LOCK['sdk_licenses'].items():
            license_file = HERE / 'licenses' / name
            if sha(license_file) != expected:
                raise ValueError('SDK license digest mismatch: ' + name)
            shutil.copyfile(license_file, licenses / name)
        for name, archive in packages.items():
            if archive.suffix == '.whl':
                with zipfile.ZipFile(archive) as wheel:
                    for entry in wheel.infolist():
                        if not entry.is_dir() and Path(entry.filename).name.lower().startswith(('license', 'copying', 'notice')):
                            (licenses / (name + '-' + entry.filename.replace('/', '_'))).write_bytes(wheel.read(entry))
            else:
                with tarfile.open(archive) as tar:
                    for entry in tar.getmembers():
                        if entry.isfile() and Path(entry.name).name.lower().startswith(('license', 'copying')):
                            (licenses / (name + '-' + Path(entry.name).name)).write_bytes(tar.extractfile(entry).read())
        if tools:
            shutil.copyfile(tools / 'etherview-wasm', output / 'etherview-wasm')
            run([tools / 'wasmpack', output, 'etherview-wasm'], REPO, env, cache / 'seal.log')
        else:
            run(['go', 'build', '-trimpath', '-buildvcs=false', '-o', output / 'etherview-wasm', './cmd/etherview-wasm'], REPO, env, cache / 'go-build-helper.log')
            run(['go', 'run', './cmd/wasmpack', output, 'etherview-wasm'], REPO, env, cache / 'seal.log')
        run([output / 'etherview-wasm', '--self-test'], REPO, env, cache / 'selftest.log')
        run([output / 'etherview-wasm', '--self-test', 'vyper'], REPO, env, cache / 'selftest-vyper.log')
        print('Built', output, flush=True)

if __name__ == '__main__':
    main()
