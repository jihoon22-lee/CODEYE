"""Unit tests of the stage-label check (scripts/bundle_checks/docs.py).

Forbidden samples are built by concatenation so this file passes the check it tests.
"""

from __future__ import annotations

from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))

from bundle_checks import docs  # noqa: E402

W = "w"
R = "r"


class StageLabelTests(unittest.TestCase):
    def test_labels_in_any_case_and_position(self):
        samples = [
            "CODEYE " + W.upper() + "06 core domain schema",
            "#include \"" + W + "16/ring.h\"",
            "#define " + W.upper() + "16_VERSION \"1.0\"",
            "import not_a_real_module_" + W + "17",
            "\"name\": \"@" + W + "18/app\"",
            "/build/" + W + "16-cpp/src",
            "web/src/" + W + "07.ts",
            R.upper() + "1-GO-06",
            "codeye-" + R + "0-qa-relay/1",
            "\"" + R + "0_gateway_error\"",
            "-p 'test_" + R + "0_*.py'",
            "prompts/review-" + R + "1/pack.yaml",
            "codeye agent --review" + "-pack review-" + "v1",
            "scratch review." + R + "1.verify.json",
            "CODEYE_" + R.upper() + "1_PACK",
            "fixtures/" + R + "1-quality/corpus.py",
            "internal/worker/review/role" + "pack.go",
            "the role" + " pack and the legacy" + " pack",
            "agent.model.review" + "_pack",
        ]
        for text in samples:
            with self.subTest(text=text):
                self.assertTrue(docs.stage_labels(text), text)

    def test_keep_list_and_other_versions_pass(self):
        samples = [
            "GET /api/v1/runs/{id}",
            "POST {base_url}/v1/responses",
            "http://127.0.0.1:PORT/v1/chat/completions",
            "https://api.github.com/api/v3/repos",
            "github.com/jackc/pgx/v5 v5.7.1",
            "github.com/spf13/pflag v1.0.5",
            "go-vet-json-v1 and cppcheck-xml-v2",
            "\"schema_version\": \"0.1\"",
            "Q01-before,Q01-after,modules,caller-closes",
            "r1, r2 := run(), run()",
            "x86_64-w64-mingw32-gcc",
            "'POST /api/v1/runs/run-r/retry': run-r2",
            "규칙 팩 v1로 고정한다",
            "TC-100, REQ-027, NFR-007, A52, D12",
        ]
        for text in samples:
            with self.subTest(text=text):
                self.assertEqual(docs.stage_labels(text), [], text)

    def test_lock_file_hashes_are_not_names(self):
        integrity = "\"integrity\": \"sha512-Ab+/" + W + "16/" + R + "1_xYz==\""
        self.assertEqual(docs.stage_labels(integrity), [])
        self.assertEqual(docs.stage_labels("golang.org/x/text v0.21.0 h1:4/" + W + "12+abc="), [])
        self.assertTrue(docs.stage_labels("\"name\": \"" + W + "18-fixture\", " + integrity))

    def test_paths_are_checked_and_sealed_paths_are_never_opened(self):
        with self.assertRaises(ValueError) as caught:
            docs.check_stage_labels(["fixtures/cpp/input/include/" + W + "16/ring.h"])
        self.assertIn("path", str(caught.exception))
        sealed = ["fixtures/" + R + "1-quality/does-not-exist.json", "docs/EVALUATION-RESULTS.md", "scripts/quality_eval.py"]
        self.assertEqual(docs.check_stage_labels(sealed), 0)

    def test_repository_passes(self):
        self.assertGreater(docs.check_stage_labels(), 1000)


if __name__ == "__main__":
    unittest.main()
