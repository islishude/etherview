"""Install the locked development/test runtime in the ignored project cache."""
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import sys

here = Path(__file__).resolve().parent
cache = here.parents[1] / ".local/vyper"
runtime = cache / "runtime"
identity = hashlib.sha256(b"".join((here / name).read_bytes() for name in ["helper.py", "build.py", "linux_runtime.py", "requirements.lock"])).hexdigest()
if (runtime / "runtime-manifest.json").exists() and (cache / "build-identity").exists():
    if (cache / "build-identity").read_text() == identity:
        subprocess.run([str(runtime / "etherview-vyper"), "--self-test"], check=True, env={}, stdout=subprocess.DEVNULL)
        raise SystemExit(0)
if sys.version_info[:3] != (3, 13, 15):
    raise SystemExit("Vyper runtime build requires Python 3.13.15")
cache.mkdir(parents=True, exist_ok=True)
venv = cache / "venv"
if not venv.exists():
    subprocess.run([sys.executable, "-m", "venv", str(venv)], check=True)
python = str(venv / "bin/python")
subprocess.run([python, "-m", "pip", "install", "--disable-pip-version-check", "--require-hashes", "--only-binary=:all:", "-r", str(here / "requirements.lock")], check=True)
if runtime.is_symlink():
    raise SystemExit("runtime cache must not be a symlink")
if runtime.exists():
    runtime.chmod(0o755)
    for directory in runtime.rglob("*"):
        if directory.is_dir() and not directory.is_symlink():
            directory.chmod(0o755)
    shutil.rmtree(runtime)
subprocess.run([python, str(here / "build.py"), str(runtime)], check=True)
(cache / "build-identity").write_text(identity)
