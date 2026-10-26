import tempfile
from pathlib import Path
import unittest
from check_docs import anchors, check_links


class DocumentationTest(unittest.TestCase):
    def test_links_resolve_relative_to_the_document(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'docs').mkdir()
            page = root / 'docs/guide.md'
            page.write_text('[Policy](../SECURITY.md#reporting)\n')
            (root / 'SECURITY.md').write_text('# Security\n## Reporting\n')
            self.assertEqual(check_links(root, [page]), (1, []))
            (root / 'SECURITY.md').unlink()
            self.assertEqual(len(check_links(root, [page])[1]), 1)

    def test_anchor_errors_and_duplicate_headings(self):
        self.assertEqual(anchors('# API `v2`\n## Usage\n## Usage\n```sh\n# ignored\n```\n'),
                         {'api-v2', 'usage', 'usage-1'})
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            page = root / 'README.md'
            page.write_text('# Guide\n[Missing](#unknown)\n[Web](https://example.com/)\n')
            count, errors = check_links(root, [page])
            self.assertEqual(count, 1)
            self.assertIn('missing anchor', errors[0])

    def test_external_files_do_not_satisfy_package_links(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / 'package'
            root.mkdir()
            (root.parent / 'source.go').write_text('package example\n')
            page = root / 'README.md'
            page.write_text('[Source](../source.go)\n')
            self.assertEqual(len(check_links(root, [page])[1]), 1)


if __name__ == '__main__':
    unittest.main()
