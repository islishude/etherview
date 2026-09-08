"""Build a pinned directory runtime. Run inside the hash-locked build venv."""
import hashlib
import importlib.metadata
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

HERE = Path(__file__).resolve().parent
WHEEL_SHA256 = "3b9671727c888363740dc678e60336759871487d0e4e9fdd973048fa9635c4fd"


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    if sys.version_info[:3] != (3, 13, 15) or importlib.metadata.version("pyinstaller") != "6.22.2":
        raise SystemExit("pinned Python/PyInstaller required")
    if importlib.metadata.version("vyper") != "0.4.3":
        raise SystemExit("pinned Vyper required")
    destination = Path(sys.argv[1]).resolve()
    if destination.exists():
        raise SystemExit("runtime output must not exist")
    with tempfile.TemporaryDirectory(prefix="etherview-vyper-build-") as work:
        work = Path(work)
        subprocess.run([
            sys.executable, "-m", "PyInstaller", "--noconfirm", "--onedir",
            "--name", "etherview-vyper", "--python-option", "hash_seed=0",
            "--distpath", str(work / "dist"), "--workpath", str(work / "build"),
            "--specpath", str(work), "--collect-data", "vyper", "--collect-submodules", "vyper",
            str(HERE / "helper.py"),
        ], check=True, cwd=work)
        shutil.copytree(work / "dist/etherview-vyper", destination, symlinks=False)
    notices = destination / "licenses"
    notices.mkdir()
    dependencies = {}
    locked = {line.split("==")[0].lower() for line in (HERE / "requirements.lock").read_text().splitlines() if "==" in line and not line.startswith(" ")}
    for dist in sorted(importlib.metadata.distributions(), key=lambda d: d.metadata["Name"].lower()):
        name = dist.metadata["Name"]
        if name.lower() not in locked:
            continue
        dependencies[name] = dist.version
        for file in dist.files or []:
            if any(part.lower().startswith(("license", "copying")) for part in file.parts):
                source = Path(dist.locate_file(file))
                if source.is_file():
                    (notices / (name + "-" + str(file).replace("/", "_"))).write_bytes(source.read_bytes())
    python_license = Path(sys.base_prefix) / "lib/python3.13/LICENSE.txt"
    if not python_license.is_file():
        raise RuntimeError("CPython license missing")
    shutil.copyfile(python_license, notices / "Python-LICENSE.txt")
    elf = {}
    if len(sys.argv) > 2:
        from linux_runtime import assemble
        elf = assemble(destination, Path(sys.argv[2]), notices)
    files = []
    for path in sorted(destination.rglob("*")):
        if path.is_file():
            path.chmod(0o555 if path.stat().st_mode & 0o111 else 0o444)
            files.append({"path": path.relative_to(destination).as_posix(), "sha256": sha(path)})
    manifest = {
        "schema": "etherview-vyper-runtime-v2", "python": "3.13.15", "vyper": "0.4.3",
        "pyinstaller": "6.22.2", "compiler_sha256": WHEEL_SHA256,
        "lock_sha256": sha(HERE / "requirements.lock"), "dependencies": dependencies, "files": files, "elf_dependencies": elf,
    }
    manifest_path = destination / "runtime-manifest.json"
    manifest_path.write_text(json.dumps(manifest, sort_keys=True, separators=(",", ":")) + "\n")
    manifest_path.chmod(0o444)
    for directory in sorted(destination.rglob("*"), reverse=True):
        if directory.is_dir():
            directory.chmod(0o555)
    destination.chmod(0o555)
    subprocess.run([str(destination / "etherview-vyper"), "--self-test"], check=True, env={})


if __name__ == "__main__":
    main()
