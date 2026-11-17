"""Check synthetic fixture behavior, not the CODEYE application."""
from importlib.util import module_from_spec, spec_from_file_location
from pathlib import Path
import tempfile
import unittest


def load_variant(name: str):
    path = Path(__file__).with_name(name + ".py")
    spec = spec_from_file_location("codeye_fixture_" + name, path)
    if spec is None or spec.loader is None:
        raise RuntimeError("Cannot load fixture: " + str(path))
    module = module_from_spec(spec)
    spec.loader.exec_module(module)
    return module.run_job


buggy = load_variant("before")
fixed = load_variant("after")


class CleanupFixtureTests(unittest.TestCase):
    def test_normal_completion_cleans_both_variants(self):
        for function in (buggy, fixed):
            with self.subTest(function=function.__module__):
                with tempfile.TemporaryDirectory() as directory:
                    root = Path(directory)
                    self.assertEqual(function(root, lambda workspace: 7), 7)
                    self.assertEqual(list(root.iterdir()), [])

    def test_buggy_variant_leaves_directory_on_exception(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            def fail(workspace):
                (workspace / "partial.txt").write_text("partial", encoding="utf-8")
                raise ValueError("callback failed")
            with self.assertRaisesRegex(ValueError, "callback failed"):
                buggy(root, fail)
            remaining = list(root.iterdir())
            self.assertEqual(len(remaining), 1)
            self.assertTrue((remaining[0] / "partial.txt").exists())

    def test_fixed_variant_cleans_on_exception(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            def fail(workspace):
                (workspace / "partial.txt").write_text("partial", encoding="utf-8")
                raise ValueError("callback failed")
            with self.assertRaisesRegex(ValueError, "callback failed"):
                fixed(root, fail)
            self.assertEqual(list(root.iterdir()), [])

    def test_fixed_variant_preserves_result_identity(self):
        with tempfile.TemporaryDirectory() as directory:
            marker = object()
            self.assertIs(fixed(Path(directory), lambda workspace: marker), marker)

    def test_fixed_variant_preserves_original_exception(self):
        with tempfile.TemporaryDirectory() as directory:
            error = ValueError("original")
            def fail(workspace):
                raise error
            with self.assertRaises(ValueError) as caught:
                fixed(Path(directory), fail)
            self.assertIs(caught.exception, error)

    def test_fixed_variant_cleans_on_base_exception(self):
        class Abort(BaseException):
            pass
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            def abort(workspace):
                raise Abort()
            with self.assertRaises(Abort):
                fixed(root, abort)
            self.assertEqual(list(root.iterdir()), [])


if __name__ == "__main__":
    unittest.main()
