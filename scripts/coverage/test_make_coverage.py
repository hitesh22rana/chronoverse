"""Exercise the Makefile's coverage flags without compiling application code."""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


@unittest.skipUnless(shutil.which("make"), "make is required")
class MakeCoverageTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        makefile = Path(__file__).resolve().parents[2] / "Makefile"
        shutil.copyfile(makefile, self.root / "Makefile")
        self.go = self.root / "go"
        self.go.write_text(
            '#!/bin/sh\n'
            'if [ "$*" != "list ./cmd/... ./internal/... ./pkg/..." ]; then exit 1; fi\n'
            'printf "example/app/cmd/server\\nexample/app/internal/postgres\\n"\n',
            encoding="utf-8",
        )
        self.go.chmod(0o755)

    def run_make(self, target, coverage=True):
        args = ["make", "-n", target]
        if coverage:
            args.append("COVERPROFILE=target/coverage-inputs/test.out")
        return subprocess.run(
            args, cwd=self.root, text=True, capture_output=True,
            env={**os.environ, "PATH": str(self.root) + os.pathsep + os.environ["PATH"]},
        )

    def test_both_jobs_measure_identical_application_dependencies(self):
        for target in ("test/short", "test/integration"):
            with self.subTest(target=target):
                result = self.run_make(target)
                self.assertEqual(result.returncode, 0, result.stderr)
                command = next(line for line in result.stdout.splitlines() if line.startswith("go test "))
                self.assertIn("-coverpkg=example/app/cmd/server,example/app/internal/postgres", command)
                self.assertTrue(command.endswith("example/app/cmd/server example/app/internal/postgres"))
                self.assertNotIn(" ./...", command)

    def test_plain_tests_do_not_instrument_coverage(self):
        result = self.run_make("test/short", coverage=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("-coverpkg", result.stdout)

    def test_failed_discovery_cannot_run_partial_package_list(self):
        self.go.write_text('#!/bin/sh\nprintf "example/app/cmd/server\\n"\nexit 1\n', encoding="utf-8")
        result = self.run_make("test/short")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Go package discovery failed", result.stderr)
