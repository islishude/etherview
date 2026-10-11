"""Release transport must preserve authenticated package bytes and identities."""
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import release
from packages import digest


class ReleaseTests(unittest.TestCase):
    def test_authenticated_reproducible_archive_and_tampering(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            package = root / 'package'
            package.mkdir()
            shared = root / 'shared.json'
            shared.write_bytes(b'locked shared manifest')
            item = {'version': '0.4.3', 'compiler_sha256': '1' * 64}
            payload = package / 'packages.zip'
            payload.write_bytes(b'authenticated upstream package')
            manifest = dict(item, schema='etherview-vyper-wasm-package-v1',
                            pyodide='0.29.3', python='3.13.2',
                            files=[{'path': 'packages.zip', 'sha256': digest(payload.read_bytes())}])
            (package / 'package-manifest.json').write_text(json.dumps(manifest))
            first, second = root / 'first', root / 'second'
            first.mkdir()
            second.mkdir()
            with patch.object(release, 'SHARED_SHA256', digest(shared.read_bytes())):
                a = release.descriptor(item, package, shared, first)
                b = release.descriptor(item, package, shared, second)
                self.assertEqual(a, b)
                self.assertEqual((first / a['archive']).read_bytes(), (second / b['archive']).read_bytes())
                self.assertEqual(a['platform'], 'emscripten-wasm32')
                self.assertNotIn('executor_digests', a)
                with self.assertRaisesRegex(ValueError, 'compiler package identity'):
                    release.descriptor(dict(item, version='0.4.2'), package, shared, second)
                payload.write_bytes(b'tampered')
                with self.assertRaisesRegex(ValueError, 'payload mismatch'):
                    release.descriptor(item, package, shared, second)
                shared.write_bytes(b'tampered')
                with self.assertRaisesRegex(ValueError, 'shared runtime identity'):
                    release.descriptor(item, package, shared, second)


if __name__ == '__main__':
    unittest.main()
