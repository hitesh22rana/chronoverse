"""Exercise the Makefile's coverage flags without compiling application code."""

import os
from pathlib import Path
import shlex
import shutil
import subprocess
import tempfile
import unittest


# A fake `go` that answers `go list` for one required tree per call, the way the
# Makefile now asks for them. Each call is appended to go-list.log, so a test can
# count how many discoveries a recipe made; any other command exits non-zero, so
# an unexpected invocation cannot pass silently.
#
# FAKEGO_EMPTY_TREE makes that tree exit 0 with nothing on stdout, which is what
# real `go list` does when a pattern resolves nothing.
# FAKEGO_FAIL_TREE makes it print a package and then exit 1, which is what it
# does on a partial discovery.
# FAKEGO_DRIFT answers any tree a second time with a different package, so a
# Makefile that discovers more than once mixes two snapshots into one command.
# FAKEGO_STRAY adds a package no required tree covers, the way a new top-level
# tree would. FAKEGO_EMPTY_TREE and FAKEGO_FAIL_TREE accept `all` for ./...
FAKE_GO = """#!/bin/sh
log="$(dirname "$0")/go-list.log"
seen=no
if [ -f "$log" ] && grep -q -x -F "$*" "$log" 2>/dev/null; then seen=yes; fi
printf '%s\\n' "$*" >>"$log"

# The whole module, one package per line the way go list prints them, carrying a
# node_modules package discovery has to ignore. $stray leads with a newline so it
# appends as a line of its own.
all='example/app/cmd/server
example/app/internal/postgres
example/app/pkg/proto
example/app/dashboard/node_modules/flatted/golang/pkg/flatted'
stray='
example/app/tools/thing'

case "$*" in
'list ./cmd/...') tree=cmd; pkg=example/app/cmd/server ;;
'list ./internal/...') tree=internal; pkg=example/app/internal/postgres ;;
'list ./pkg/...') tree=pkg; pkg=example/app/pkg/proto ;;
'list ./...') tree=all; pkg="$all" ;;
*) printf 'fake go: unexpected command: %s\\n' "$*" >&2; exit 1 ;;
esac

if [ "$seen" = yes ] && [ -n "${FAKEGO_DRIFT:-}" ]; then
\tpkg="example/app/$tree/drifted"
fi

if [ "$tree" = all ] && [ -n "${FAKEGO_STRAY:-}" ]; then
	pkg="$pkg$stray"
fi

case "$tree" in
"${FAKEGO_EMPTY_TREE:-}") exit 0 ;;
"${FAKEGO_FAIL_TREE:-}") printf '%s\\n' "$pkg"; exit 1 ;;
esac

printf '%s\\n' "$pkg"
"""


@unittest.skipUnless(shutil.which("make"), "make is required")
class MakeCoverageTests(unittest.TestCase):
    PACKAGES = [
        "example/app/cmd/server",
        "example/app/internal/postgres",
        "example/app/pkg/proto",
    ]

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        makefile = Path(__file__).resolve().parents[2] / "Makefile"
        shutil.copyfile(makefile, self.root / "Makefile")
        self.go = self.root / "go"
        self.go.write_text(FAKE_GO, encoding="utf-8")
        self.go.chmod(0o755)
        self.log = self.root / "go-list.log"

    def run_make(self, target, coverage=True, **fake):
        args = ["make", "-n", target]
        if coverage:
            args.append("COVERPROFILE=target/coverage-inputs/test.out")
        # Each run is one make process, so start its call log empty and let
        # discoveries() report only what this run did.
        self.log.write_text("", encoding="utf-8")
        env = {**os.environ, "PATH": str(self.root) + os.pathsep + os.environ["PATH"]}
        # The defaults under test must come from the Makefile, not the caller's shell.
        env.pop("INTEGRATION_PARALLELISM", None)
        env.update(fake)
        return subprocess.run(
            args, cwd=self.root, text=True, capture_output=True, env=env,
        )

    def go_test_command(self, result) -> str:
        return next(line for line in result.stdout.splitlines() if line.startswith("go test "))

    def discoveries(self) -> list[str]:
        """Every `go list` the last make run made, in order."""
        return [line for line in self.log.read_text(encoding="utf-8").splitlines() if line]

    def test_both_jobs_measure_identical_application_dependencies(self):
        for target in ("test/short", "test/integration"):
            with self.subTest(target=target):
                result = self.run_make(target)
                self.assertEqual(result.returncode, 0, result.stderr)
                command = self.go_test_command(result)
                self.assertIn("-coverpkg=" + ",".join(self.PACKAGES), command)
                self.assertTrue(command.endswith(" ".join(self.PACKAGES)))
                self.assertNotIn(" ./...", command)

    def test_plain_tests_do_not_instrument_coverage(self):
        result = self.run_make("test/short", coverage=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("-coverpkg", result.stdout)

    def test_integration_serializes_packages_without_weakening_test_flags(self):
        result = self.run_make("test/integration")
        self.assertEqual(result.returncode, 0, result.stderr)
        command = self.go_test_command(result)
        tokens = shlex.split(command)
        self.assertIn("-p", tokens)
        self.assertEqual(tokens[tokens.index("-p") + 1], "1")
        self.assertIn("-run", tokens)
        self.assertEqual(tokens[tokens.index("-run") + 1], "TestIntegration")
        self.assertNotIn("-short", tokens)
        for flag in ("-race", "-count=1", "-timeout=20m", "-covermode=atomic"):
            self.assertIn(flag, command)
        for package in self.PACKAGES:
            self.assertIn(package, command)
        unit = self.run_make("test/short")
        self.assertNotIn("-p", self.go_test_command(unit).split())

    def test_every_required_tree_must_resolve_a_package(self):
        # go list exits 0 and only warns when a pattern matches nothing, so a
        # tree that resolves nothing is the case discovery has to catch itself.
        for tree in ("cmd", "internal", "pkg"):
            with self.subTest(tree=tree):
                result = self.run_make("test/short", FAKEGO_EMPTY_TREE=tree)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("Go package discovery failed", result.stderr)
                self.assertIn(f"./{tree}/... resolved no packages", result.stderr)
                self.assertNotIn("go test", result.stdout)

    def test_failed_discovery_cannot_run_partial_package_list(self):
        # `all` is the ./... listing the stray check reads; a failure there must
        # stop the run just like a failure in one required tree.
        for tree in ("internal", "all"):
            with self.subTest(tree=tree):
                result = self.run_make("test/short", FAKEGO_FAIL_TREE=tree)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("Go package discovery failed", result.stderr)
                self.assertNotIn("go test", result.stdout)

    def test_a_package_outside_the_required_trees_stops_the_run(self):
        # A new top-level tree would otherwise be listed by ./... and tested by
        # nobody, with a green build.
        result = self.run_make("test/short", FAKEGO_STRAY="1")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Go package discovery missed example/app/tools/thing", result.stderr)
        self.assertIn("add its tree to GO_PACKAGES_REQUIRES", result.stderr)
        self.assertNotIn("go test", result.stdout)

    def test_recipe_discovers_each_tree_once_and_reuses_the_snapshot(self):
        # The guard, -coverpkg and the package arguments all have to read one
        # discovery. A second one answers differently here, so mixing snapshots
        # shows up as a drifted package in either place.
        for target in ("test/short", "test/integration"):
            with self.subTest(target=target):
                result = self.run_make(target, FAKEGO_DRIFT="1")
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(
                    self.discoveries(),
                    [
                        "list ./cmd/...",
                        "list ./internal/...",
                        "list ./pkg/...",
                        "list ./...",
                    ],
                )
                self.assertNotIn("drifted", result.stdout)
