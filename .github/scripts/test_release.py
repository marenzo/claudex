import unittest
from release import choose, changelog_section, version, release_notes


class ReleasePlanTest(unittest.TestCase):
    def test_minor_bump_above_existing_tags(self):
        self.assertEqual(choose("v1.2.0", ["v1.0.4", "v1.1.0"], []), ("v1.2.0", "v1.1.0"))

    def test_next_main_push_increments_patch(self):
        self.assertEqual(choose("v1.2.0", ["v1.2.9", "v1.2.10", "v2.0.0-rc.1"], []), ("v1.2.11", "v1.2.10"))

    def test_retry_reuses_tag_without_extra_release(self):
        self.assertEqual(choose("v1.2.0", ["v1.1.0", "v1.2.0"], ["v1.2.0"]), ("v1.2.0", "v1.1.0"))

    def test_first_release_and_explicit_major(self):
        self.assertEqual(choose("v1.2.0", [], []), ("v1.2.0", ""))
        self.assertEqual(choose("v2.0.0", ["v1.2.12"], []), ("v2.0.0", "v1.2.12"))

    def test_invalid_version_is_rejected(self):
        for tag in ("1.2.0", "v01.2.0", "v1.2.0\nextra", "v1.2.0-rc.1"):
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                version(tag)

    def test_changed_floor_cannot_reuse_older_head_tag(self):
        with self.assertRaises(ValueError):
            choose("v1.3.0", ["v1.2.0"], ["v1.2.0"])

    def test_publication_requires_versioned_notes(self):
        for text in ("# Changelog\n", "## Unreleased\n- Future\n", "## v1.2.3\n", "## v1.2.3\nTODO\n"):
            with self.subTest(text=text):
                with self.assertRaisesRegex(ValueError, "v1.2.3"):
                    release_notes(text, "v1.2.3")
        self.assertEqual(release_notes("## v1.2.3\n- Fixed\n", "v1.2.3"), "- Fixed")

    def test_notes_only_include_requested_release(self):
        text = "# Changelog\n\n## Unreleased\n\n- Future\n\n## v1.2.0 — 2026-10-23\n\n- Feedback\n\n### Upgrade\nRestart.\n\n## v1.1.0\n- Old\n"
        self.assertEqual(changelog_section(text, "v1.2.0"), "- Feedback\n\n### Upgrade\nRestart.")
        self.assertEqual(changelog_section(text, "v1.2.1"), "")


if __name__ == "__main__":
    unittest.main()
