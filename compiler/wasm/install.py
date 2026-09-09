"""Install a coherent generated local runtime; never used for live upgrades."""
import fcntl
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
CACHE = REPO / '.local/wasm'

def remove_generated(path):
    for directory, _, _ in os.walk(path):
        os.chmod(directory, 0o755)
    shutil.rmtree(path)

def main():
    CACHE.mkdir(parents=True, exist_ok=True)
    with (CACHE / 'install.lock').open('w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        destination = CACHE / 'runtime'
        stage = Path(tempfile.mkdtemp(prefix='install-', dir=CACHE))
        fresh = stage / 'runtime'
        try:
            subprocess.run([sys.executable, str(HERE / 'build.py'), '--output', str(fresh)], check=True, cwd=REPO)
            if destination.exists():
                subprocess.run(['go', 'run', './cmd/wasmpack', '--check', str(destination / 'etherview-wasm')], check=True, cwd=REPO)
                destination.chmod(0o755)
                try:
                    destination.rename(stage / 'previous')
                except BaseException:
                    destination.chmod(0o555)
                    raise
            fresh.chmod(0o755)
            fresh.rename(destination)
            destination.chmod(0o555)
        except BaseException:
            if not destination.exists() and (stage / "previous").exists():
                (stage / "previous").rename(destination)
                destination.chmod(0o555)
            raise
        finally:
            remove_generated(stage)
    print('Installed', destination)

if __name__ == '__main__':
    main()
