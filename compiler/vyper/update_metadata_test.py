"""Offline resolver/discovery regressions; no upstream network needed."""
import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('update_metadata', Path(__file__).with_name('update-metadata.py'))
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


class MetadataTests(unittest.TestCase):
    def test_pep440_and_yanked(self):
        wheel = {'packagetype': 'bdist_wheel', 'yanked': False}
        releases = {v: [wheel] for v in ['0.4.3', '0.4.4', '0.10.0', '0.5.0rc1', '0.5.0.dev1', '0.5.0+local', '0.5.0.post1', 'bad']}
        releases['0.5.0'] = [dict(wheel, yanked=True)]
        self.assertEqual(m.discover(releases, ['0.4.3']), ['0.4.4', '0.10.0'])

    def test_wheel_authentication_and_compatibility(self):
        import hashlib
        wheel = {'packagetype': 'bdist_wheel', 'filename': 'vyper-0.4.4-py3-none-any.whl', 'requires_python': '>=3.14', 'size': 1, 'digests': {'sha256': hashlib.sha256(b'x').hexdigest()}, 'url': 'https://files.pythonhosted.org/a.whl'}
        with patch.object(m, 'fetch', return_value=b'x'):
            first=m.select_wheel({'urls':[wheel]}, 'vyper', '0.4.4', '3.14.2')
            self.assertEqual(first,m.select_wheel({'urls':[wheel]}, 'vyper', '0.4.4', '3.14.2'))
            for change in [{'yanked': True}, {'requires_python': '>=3.15'}, {'filename': 'vyper-0.4.4-cp314-cp314-manylinux_2_17_x86_64.whl'}, {'size': 2}]:
                with self.assertRaises(ValueError):m.select_wheel({'urls':[dict(wheel, **change)]}, 'vyper', '0.4.4', '3.14.2')
        with patch.object(m, 'fetch', return_value=b'z'):
            with self.assertRaises(ValueError):m.select_wheel({'urls':[wheel]}, 'vyper', '0.4.4', '3.14.2')

    def test_fetch_retries_are_bounded(self):
        with patch.object(m.urllib.request, 'build_opener') as opener, patch.object(m.time, 'sleep'):
            opener.return_value.open.side_effect = TimeoutError('offline')
            with self.assertRaises(TimeoutError):m.fetch('https://pypi.org/pypi/vyper/json')
            self.assertEqual(opener.return_value.open.call_count, 3)

    def test_network_failures_are_not_no_updates(self):
        with patch.object(m, 'fetch', side_effect=TimeoutError):
            with self.assertRaises(TimeoutError):m.project('vyper')
        for url in ['http://pypi.org/a', 'https://evil.test/a', 'https://pypi.org@evil.test/a', 'https://pypi.org/a?redirect=1']:
            with self.assertRaises(ValueError):m.fetch(url)


if __name__ == '__main__':
    unittest.main()
