"""License collection must cover vendored metadata and authenticated payloads."""
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
import zipfile

from packages import HERE, digest

spec = importlib.util.spec_from_file_location('vyper_license_check', HERE.parent / 'licenses.py')
licenses = importlib.util.module_from_spec(spec)
spec.loader.exec_module(licenses)


class LicenseTests(unittest.TestCase):
    def test_missing_vendored_notice_and_tampering(self):
        item = {'version': '0.4.3', 'compiler_sha256': '1' * 64}
        with tempfile.TemporaryDirectory() as directory:
            package = Path(directory)
            def write_package(vendored):
                buffer = io.BytesIO()
                with zipfile.ZipFile(buffer, 'w') as archive:
                    archive.writestr('vyper-0.4.3.dist-info/LICENSE', b'upstream notice')
                    archive.writestr('vendor/dependency-1.dist-info/METADATA', b'Name: dependency')
                    if vendored:
                        archive.writestr('vendor/dependency-1.dist-info/licenses/LICENSE', b'vendored notice')
                raw = buffer.getvalue()
                (package / 'packages.zip').write_bytes(raw)
                (package / 'package-manifest.json').write_text(json.dumps(dict(item, dependencies=[],
                    files=[{'path': 'packages.zip', 'sha256': digest(raw)}])))
            write_package(False)
            with self.assertRaisesRegex(ValueError, 'missing distribution license'):
                licenses.check_package(item, package)
            write_package(True)
            self.assertEqual(licenses.check_package(item, package), 2)
            (package / 'packages.zip').write_bytes(b'changed')
            with self.assertRaisesRegex(ValueError, 'package digest mismatch'):
                licenses.check_package(item, package)


if __name__ == '__main__':
    unittest.main()
