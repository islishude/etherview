"""Security gates reject incomplete coverage and stale advisory dispositions."""
import copy
import io
import json
import unittest
import zipfile
from pathlib import Path

import audit


class AuditTests(unittest.TestCase):
    def test_vendored_metadata_is_included(self):
        raw = io.BytesIO()
        with zipfile.ZipFile(raw, 'w') as archive:
            archive.writestr('setuptools-1.dist-info/METADATA', 'Name: setuptools\nVersion: 1\n')
            archive.writestr('setuptools/_vendor/wheel-2.dist-info/METADATA', 'Name: wheel\nVersion: 2\n')
        self.assertEqual([(x['name'], x['version']) for x in audit.metadata(raw.getvalue(), 'test')],
                         [('setuptools', '1'), ('wheel', '2')])

    def test_ambiguous_metadata_fails(self):
        raw = io.BytesIO()
        with zipfile.ZipFile(raw, 'w') as archive:
            archive.writestr('x.dist-info/METADATA', 'Name: x\nName: y\nVersion: 1\n')
        with self.assertRaisesRegex(ValueError, 'ambiguous'):
            audit.metadata(raw.getvalue(), 'test')

    def test_vendor_manifest_without_dist_info_and_missing_notice(self):
        def bundle(notice):
            raw = io.BytesIO()
            with zipfile.ZipFile(raw, 'w') as archive:
                archive.writestr('wheel/vendored/vendor.txt', 'packaging==24.0\n')
                archive.writestr('wheel/vendored/packaging/__init__.py', '')
                if notice:
                    archive.writestr('wheel/vendored/packaging/LICENSE', 'upstream notice')
            return raw.getvalue()
        record, = audit.metadata(bundle(True), 'test')
        self.assertEqual((record['name'], record['version']), ('packaging', '24.0'))
        with self.assertRaisesRegex(ValueError, 'missing vendored'):
            audit.metadata(bundle(False), 'test')

    def test_changed_advisory_inventory_and_partial_reports_fail(self):
        actual = {'distributions': [{'name': 'x', 'version': '1'}]}
        finding = {'id': 'CVE-test', 'details': 'reviewed prerequisite'}
        report = [{'component': 'PyPI/x/1', 'findings': [finding]}]
        review = {'inventory_sha256': audit.digest(audit.canonical(actual)), 'core': [],
                  'reasons': {'unreachable': 'specific prerequisite absent'},
                  'findings': {'PyPI/x/CVE-test': {'sha256': [audit.finding_digest(finding)], 'reason': 'unreachable'}}}
        self.assertEqual(audit.evaluate(actual, report, review), 1)
        for reports in ([], report + report):
            with self.assertRaisesRegex(ValueError, 'coverage'):
                audit.evaluate(actual, reports, review)
        for changed in ({'id': 'CVE-new'}, dict(finding, details='new impact'), dict(finding, severity='critical')):
            with self.assertRaisesRegex(ValueError, 'unreviewed or changed'):
                audit.evaluate(actual, [{'component': 'PyPI/x/1', 'findings': [changed]}], review)
        stale = copy.deepcopy(actual)
        stale['distributions'][0]['version'] = '2'
        with self.assertRaisesRegex(ValueError, 'inventory changed'):
            audit.evaluate(stale, report, review)

    def test_reviewed_cpython_tarfile_advisory_revision(self):
        # Exact finding retained by failed CI run 38135537853, not a live fixture.
        here = Path(__file__).parent
        finding = json.loads((here / 'testdata/CVE-2026-82049.json').read_bytes())
        production = json.loads((here / 'audit-review.json').read_bytes())
        actual = {'distributions': []}
        review = dict(production, core=[{'name': 'cpython'}],
                      inventory_sha256=audit.digest(audit.canonical(actual)))

        def evaluate(value):
            return audit.evaluate(actual, [{'component': 'core/cpython', 'findings': [value]}], review)

        self.assertEqual(evaluate(finding), 1)
        self.assertEqual(evaluate(dict(finding, modified='timestamp-only change')), 1)
        previous = copy.deepcopy(review)
        previous['findings']['core/cpython/CVE-2026-82049']['sha256'].remove(audit.finding_digest(finding))
        with self.assertRaisesRegex(ValueError, 'core/cpython/CVE-2026-82049'):
            audit.evaluate(actual, [{'component': 'core/cpython', 'findings': [finding]}], previous)
        for field in ('details', 'affected', 'references', 'database_specific'):
            changed = copy.deepcopy(finding)
            changed[field] = 'unreviewed revision'
            with self.subTest(field=field), self.assertRaisesRegex(ValueError, 'unreviewed or changed'):
                evaluate(changed)


if __name__ == '__main__':
    unittest.main()
