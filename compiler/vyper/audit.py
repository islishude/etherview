"""Audit the pinned compiler dependencies with version-scoped upstream corrections."""
import hashlib
import json
from pathlib import Path
import subprocess
import sys

HERE = Path(__file__).resolve().parent
CACHE = HERE.parents[1] / ".local/vyper/audit"
WHEEL = "3b9671727c888363740dc678e60336759871487d0e4e9fdd973048fa9635c4fd"
# See README.md: both records have incorrect unbounded affected-version ranges.
# These exceptions apply ONLY to this exact official Vyper 0.4.3 distribution.
CORRECTIONS = {"PYSEC-2023-142", "GHSA-vgf2-gvx8-xwc3"}


def main():
    lock = (HERE / "audit.lock").read_bytes()
    identity = hashlib.sha256(lock).hexdigest()
    if not (CACHE / "bin/python").exists():
        subprocess.run([sys.executable, "-m", "venv", str(CACHE)], check=True)
    python = str(CACHE / "bin/python")
    stamp = CACHE / "lock-identity"
    if not stamp.exists() or stamp.read_text() != identity:
        subprocess.run([python, "-m", "pip", "install", "--disable-pip-version-check", "--require-hashes", "--only-binary=:all:", "-r", str(HERE / "audit.lock")], check=True)
        stamp.write_text(identity)
    result = subprocess.run([python, "-m", "pip_audit", "--no-deps", "--disable-pip", "-r", str(HERE / "requirements.lock"), "-f", "json"], capture_output=True, text=True, timeout=120)
    if result.returncode not in (0, 1):
        raise RuntimeError("Python dependency audit failed")
    report = json.loads(result.stdout)
    (CACHE / "report.json").write_text(json.dumps(report, indent=2) + "\n")
    # Do not accept an empty/partial report as a clean dependency audit.
    expected = json.loads(subprocess.check_output([python, "-c", """
import json, pathlib, sys
from packaging.requirements import Requirement
selected = {}
for line in pathlib.Path(sys.argv[1]).read_text().splitlines():
    if not line or line[0].isspace() or line.startswith('#') or '==' not in line:
        continue
    requirement = Requirement(line.rstrip().rstrip(chr(92)).strip())
    if requirement.marker is None or requirement.marker.evaluate():
        selected[requirement.name.lower().replace('_', '-')] = str(requirement.specifier).removeprefix('==')
print(json.dumps(selected))
""", str(HERE / "requirements.lock")], text=True, timeout=10))
    actual = {package["name"].lower().replace("_", "-"): package.get("version") for package in report["dependencies"]}
    if actual != expected or len(actual) != len(report["dependencies"]):
        raise RuntimeError("Python dependency audit report is incomplete or inconsistent")
    problems = []
    corrected = []
    for package in report["dependencies"]:
        if "skip_reason" in package:
            raise RuntimeError("Python dependency audit skipped a package")
        for vulnerability in package.get("vulns", []):
            if package["name"] == "vyper" and package["version"] == "0.4.3" and WHEEL in (HERE / "requirements.lock").read_text() and vulnerability["id"] in CORRECTIONS:
                corrected.append(vulnerability["id"])
            else:
                problems.append((package["name"], package["version"], vulnerability["id"]))
    if problems:
        raise RuntimeError("unresolved Python advisories: " + repr(problems))
    print(f"vyper dependency audit: PASS ({len(report['dependencies'])} packages; upstream version-range corrections: {', '.join(corrected) or 'none'})")


if __name__ == "__main__":
    main()
