"""Regression checks for threshold and immutable-baseline behavior; no services."""
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("arch_lint", Path(__file__).with_name("arch-lint.py"))
lint = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lint)


def rule(legacy=None):
    return {"id": "source-file-size", "type": "max_file_lines", "globs": ["**/*.go"],
            "max_lines": 1000, "warn_lines": 500, "message": "Split cohesive responsibilities.",
            "legacy": legacy or {}}


def entry(lines):
    return {"lines": lines, "reason": "Existing player.", "plan": "Extract player modules."}


class ArchitectureTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()
        self.source = self.root / "source.go"

    def write(self, lines):
        self.source.write_text("// source\n" * lines, encoding="utf-8")

    def test_threshold_boundaries(self):
        for count, severity in [(500, None), (501, "warning"), (1000, "warning"), (1001, "error")]:
            with self.subTest(count=count):
                self.write(count)
                findings = lint.lint(self.root, [rule()])
                self.assertEqual([f.get("severity", "error") for f in findings], [] if severity is None else [severity])

    def test_existing_exception_is_a_non_growing_cap(self):
        policy = rule({"source.go": entry(1002)})
        self.write(1002)
        self.assertTrue(all(f.get("severity") == "warning" for f in lint.lint(self.root, [policy])))
        self.write(1003)
        self.assertTrue(any(f.get("severity") != "warning" for f in lint.lint(self.root, [policy])))

    def test_shrinking_and_removed_files_require_exception_cleanup(self):
        policy = rule({"source.go": entry(1002)})
        self.write(1001)
        self.assertIn("stale", lint.lint(self.root, [policy])[0]["message"])
        self.source.unlink()
        self.assertIn("removed", lint.lint(self.root, [policy])[0]["message"])

    def test_legacy_requires_exact_paths_and_explanation(self):
        for legacy in [{"*.go": entry(1002)}, {"source.go": {"lines": 1002}}, {"source.go": entry(True)}]:
            with self.assertRaises(lint.ConfigError):
                lint.validate_rule(rule(legacy), 0)

    def git(self, *args):
        return subprocess.check_output(["git", *args], cwd=self.root, text=True, stderr=subprocess.DEVNULL).strip()

    def commit(self):
        self.git("add", ".")
        self.git("-c", "user.name=Synthetic", "-c", "user.email=synthetic@example.invalid", "commit", "-m", "baseline")
        return self.git("rev-parse", "HEAD")

    def baseline(self, policy):
        self.git("init")
        config = self.root / "rules.json"
        config.write_text(json.dumps({"version": 1, "rules": [policy]}), encoding="utf-8")
        return config, self.commit()

    def test_initial_exception_must_match_existing_source(self):
        self.write(1002)
        config, ref = self.baseline(rule())
        lint.validate_baseline(self.root, [rule({"source.go": entry(1002)})], config, ref)
        with self.assertRaises(lint.ConfigError):
            lint.validate_baseline(self.root, [rule({"source.go": entry(1003)})], config, ref)
        with self.assertRaises(lint.ConfigError):
            lint.validate_baseline(self.root, [rule({"new.go": entry(1002)})], config, ref)

    def test_existing_exception_cannot_grow_in_a_later_change(self):
        self.write(1002)
        config, ref = self.baseline(rule({"source.go": entry(1002)}))
        with self.assertRaises(lint.ConfigError):
            lint.validate_baseline(self.root, [rule({"source.go": entry(1003)})], config, ref)

    def test_parent_must_be_an_available_immutable_commit(self):
        self.write(1002)
        config, _ = self.baseline(rule())
        for ref in ["main", "a" * 40]:
            with self.assertRaises(lint.ConfigError):
                lint.validate_baseline(self.root, [rule()], config, ref)


if __name__ == "__main__":
    unittest.main()
