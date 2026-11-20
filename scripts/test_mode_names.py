"""Unit tests of the run-mode name check (scripts/bundle_checks/docs.py).

Forbidden samples are built by concatenation so this file passes the check it tests.
"""

from __future__ import annotations

from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))

from bundle_checks import docs  # noqa: E402

P = "personal"


class ModeNameTests(unittest.TestCase):
    def test_old_names_in_any_case(self):
        for text in (P + " mode", P.capitalize() + " Mode", P + "-mode start", "개인" + " 모드에서",
                     "~/.config/codeye/" + P + ".json", "examples/" + P.upper() + ".JSON"):
            with self.subTest(text=text):
                self.assertTrue(docs.mode_names(text), text)

    def test_other_words_pass(self):
        for text in ("config.json", "admin.json", "the service", "personal prefixes", "개인 사용과 GitHub.com 연동", "서비스 모드 없음"):
            with self.subTest(text=text):
                self.assertEqual(docs.mode_names(text), [], text)

    def test_history_files_are_exempt_and_others_fail(self):
        self.assertEqual(docs.check_mode_names(["CHANGELOG.md", "docs/DECISIONS.md"]), 0)
        with self.assertRaises(ValueError) as caught:
            docs.check_mode_names(["examples/" + P + ".json"])
        self.assertIn("path", str(caught.exception))

    def test_repository_passes(self):
        self.assertGreater(docs.check_mode_names(), 1000)


if __name__ == "__main__":
    unittest.main()
