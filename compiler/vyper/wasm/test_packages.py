"""Authenticated package assembly rejects tampering and unsafe members."""
import io
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch
import zipfile

import packages


class PackageTests(unittest.TestCase):
    def test_release_archive_is_reproducible(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            source = root / 'package'
            source.mkdir()
            for name in ('package-manifest.json', 'packages.zip'):
                (source / name).write_bytes(b'locked bytes')
            first, second = root / 'first.tar.gz', root / 'second.tar.gz'
            packages.pack_runtime(source, first)
            (source / 'packages.zip').chmod(0o755)
            packages.pack_runtime(source, second)
            self.assertEqual(first.read_bytes(), second.read_bytes())
            (source / 'unexpected').write_bytes(b'extra')
            with self.assertRaises(ValueError):
                packages.pack_runtime(source, second)

    def test_cache_digest_is_checked_again(self):
        with tempfile.TemporaryDirectory() as root:
            with patch.object(packages, 'CACHE', Path(root)):
                expected = packages.digest(b'original')
                (Path(root) / expected).write_bytes(b'tampered')
                with self.assertRaisesRegex(ValueError, 'cached artifact identity'):
                    packages.download('https://example.invalid/file', expected)

    def test_parent_and_absolute_members_are_rejected(self):
        for name in ('../file.py', '/file.py', 'a/../file.py', 'a\\file.py'):
            with self.subTest(name=name):
                with self.assertRaises(ValueError):
                    packages.safe_member(name)

    def test_native_code_is_not_copied(self):
        raw = io.BytesIO()
        with zipfile.ZipFile(raw, 'w') as archive:
            archive.writestr('fallback/map.py', b'upstream python')
            archive.writestr('fallback/map.abi3.so', b'native code')
            archive.writestr('fallback/__pycache__/map.pyc', b'bytecode')
        result = {}
        packages.unpack_wheel(raw.getvalue(), result)
        self.assertEqual(result, {'fallback/map.py': b'upstream python'})

    def test_conflicting_packages_are_rejected(self):
        raw = io.BytesIO()
        with zipfile.ZipFile(raw, 'w') as archive:
            archive.writestr('module.py', b'other')
        with self.assertRaisesRegex(ValueError, 'conflicting'):
            packages.unpack_wheel(raw.getvalue(), {'module.py': b'original'})

    def test_symlinks_are_rejected(self):
        raw = io.BytesIO()
        with zipfile.ZipFile(raw, 'w') as archive:
            entry = zipfile.ZipInfo('link')
            entry.external_attr = 0o120777 << 16
            archive.writestr(entry, '../escape')
        with self.assertRaisesRegex(ValueError, 'unsafe wheel'):
            packages.unpack_wheel(raw.getvalue(), {})


if __name__ == '__main__':
    unittest.main()
