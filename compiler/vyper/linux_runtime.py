"""Resolve the frozen runtime's ELF closure against the final distroless root."""
import hashlib
import os
from pathlib import Path
import shutil
import subprocess


def elf_files(root):
    result = []
    for path in root.rglob("*"):
        if path.is_file():
            with path.open("rb") as stream:
                if stream.read(4) == b"\x7fELF":
                    result.append(path)
    return result


def assemble(runtime, target, licenses):
    internal = runtime / "_internal"
    environment = {**os.environ, "LD_LIBRARY_PATH": str(internal)}
    inventory = {}
    for binary in elf_files(runtime):
        listing = subprocess.check_output(["/usr/bin/python3", "/usr/bin/lddtree", "-l", str(binary)], env=environment, text=True)
        for entry in listing.splitlines():
            dependency = Path(entry)
            if not dependency.is_absolute() or not dependency.is_file():
                raise RuntimeError("unresolved ELF dependency")
            if dependency.is_relative_to(runtime):
                continue
            base = target / str(dependency).lstrip("/")
            provider = "base" if base.is_file() else "private"
            if provider == "private":
                copied = internal / dependency.name
                if copied.exists() and copied.read_bytes() != dependency.read_bytes():
                    raise RuntimeError("conflicting ELF provider")
                if not copied.exists():
                    shutil.copyfile(dependency, copied)
            owner = subprocess.run(["dpkg-query", "-S", str(dependency)], capture_output=True, text=True)
            package = next((line.split(": ")[0] for line in owner.stdout.splitlines() if ": " in line and not line.startswith("diversion ")), "")
            if package:
                copyright_file = Path("/usr/share/doc") / package.split(":")[0] / "copyright"
                if not copyright_file.is_file():
                    raise RuntimeError("ELF dependency license missing: " + package + " " + str(dependency))
                shutil.copyfile(copyright_file, licenses / (package.replace(":", "-") + "-copyright"))
            inventory[str(dependency)] = {"provider": provider, "package": package, "sha256": hashlib.sha256(dependency.read_bytes()).hexdigest()}
    installed = target / str(runtime).lstrip("/")
    installed.parent.mkdir(parents=True, exist_ok=True)
    shutil.copytree(runtime, installed)
    library_dirs = {str(path.parent) for path in target.rglob("*.so*") if path.is_file()}
    library_dirs.add(str(installed / "_internal"))
    for binary in elf_files(runtime):
        final_binary = installed / binary.relative_to(runtime)
        result = subprocess.run(["/usr/bin/python3", "/usr/bin/lddtree", "-l", str(final_binary)], env={**os.environ, "LD_LIBRARY_PATH": ":".join(sorted(library_dirs))}, capture_output=True, text=True)
        if result.returncode or "None" in result.stdout:
            raise RuntimeError("incomplete final-root ELF dependency closure: " + result.stdout)
        for line in result.stdout.splitlines():
            dependency = Path(line)
            if dependency.is_relative_to(target):
                continue
            # PT_INTERP is absolute; its target-root counterpart supplies it.
            if dependency.name.startswith("ld-linux-") and (target / line.lstrip("/")).is_file():
                continue
            raise RuntimeError("ELF dependency escaped final root: " + line)
    return inventory
