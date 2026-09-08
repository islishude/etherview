"""Check the shipped, reviewed runtime license inventory."""
import json
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1] / ".local/vyper/runtime"
manifest = json.loads((ROOT / "runtime-manifest.json").read_text())
reviewed = {
    "altgraph", "asttokens", "cbor2", "immutables", "lark", "macholib",
    "packaging", "pycryptodome", "pyinstaller", "pyinstaller-hooks-contrib",
    "setuptools", "vyper", "wheel",
}
files = list((ROOT / "licenses").iterdir())
for package in manifest["dependencies"]:
    if package.lower() not in reviewed:
        raise SystemExit("unreviewed Vyper runtime dependency: " + package)
    if not any(file.name.startswith(package + "-") and file.stat().st_size for file in files):
        raise SystemExit("Vyper runtime dependency license missing: " + package)
if not (ROOT / "licenses/Python-LICENSE.txt").is_file():
    raise SystemExit("CPython license missing")
print("vyper runtime licenses: PASS")
