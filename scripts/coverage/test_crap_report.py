"""Tests for the coverage infrastructure script.

Run with:
    python3 -m unittest discover -s scripts/coverage -t scripts/coverage
"""

from __future__ import annotations

import contextlib
import io
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

import crap_report


# The module every fixture profile below claims to belong to. The merge refuses a
# block file path no go.mod declares, so each fixture states its module.
MODULE = ("github.com/acme/app",)

GO_UNIT = """mode: atomic
github.com/acme/app/service/user.go:10.30,12.2 2 0
github.com/acme/app/service/user.go:14.2,20.5 6 3
github.com/acme/app/repository/order.go:5.1,7.2 1 1
"""

# Same package set, same covermode: the unit suites left two blocks untouched and
# the integration suites executed them.
GO_INTEGRATION = """mode: atomic
github.com/acme/app/service/user.go:10.30,12.2 2 4
github.com/acme/app/service/user.go:14.2,20.5 6 0
github.com/acme/app/service/audit.go:8.1,9.2 1 2
"""

LCOV = """TN:
SF:src/lib/utils.ts
DA:1,1
DA:2,0
BRDA:2,0,0,1
end_of_record
SF:src/app/page.tsx
DA:7,3
end_of_record
"""

# A work tree matching MainTest: Chosen is executed, Audit is instrumented but
# never entered, and page.tsx is absent from the report entirely.
MAIN_GO_UNIT = """mode: atomic
github.com/acme/app/service/user.go:3.14,5.2 2 2
github.com/acme/app/service/user.go:6.2,7.2 1 0
github.com/acme/app/service/audit.go:3.1,5.2 1 0
"""

MAIN_GO_INTEGRATION = """mode: atomic
github.com/acme/app/service/user.go:3.14,5.2 2 5
github.com/acme/app/service/user.go:6.2,7.2 1 0
github.com/acme/app/service/order.go:9.1,10.2 1 1
"""

MAIN_LCOV = """TN:
SF:src/lib/utils.ts
DA:1,1
DA:2,1
BRDA:2,0,0,1
end_of_record
"""

MAIN_USER_GO = """package service

func Chosen(n int) string {
\tif n > 1 {
\t\treturn "many"
\t}
\treturn "one"
}
"""

MAIN_AUDIT_GO = """package service

func Audit() int {
\treturn 0
}
"""

MAIN_UTILS_TS = """export function cn(value: string) {
  if (value) {
    return value
  }
  return ""
}
"""

MAIN_PAGE_TSX = """export function Panel() {
  if (1) {
    return null
  }
  return null
}
"""


class MergeGoProfilesTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.root = Path(self.dir.name)

    def write(self, name: str, text: str) -> Path:
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")
        return path

    def merged(self, *texts: str) -> dict[str, tuple[int, int]]:
        output = self.root / "out" / "coverage.out"
        inputs = [self.write(f"profile{index}.out", text) for index, text in enumerate(texts)]
        crap_report.merge_go_profiles(inputs, output, MODULE)
        _mode, blocks = crap_report._read_go_profile(output)
        return blocks

    def test_hits_are_summed_and_new_blocks_are_added(self):
        blocks = self.merged(GO_UNIT, GO_INTEGRATION)
        self.assertEqual(
            blocks,
            {
                "github.com/acme/app/service/user.go:10.30,12.2": (2, 4),
                "github.com/acme/app/service/user.go:14.2,20.5": (6, 3),
                "github.com/acme/app/repository/order.go:5.1,7.2": (1, 1),
                "github.com/acme/app/service/audit.go:8.1,9.2": (1, 2),
            },
        )

    def test_block_ranges_are_preserved_exactly(self):
        # The column offsets survive: a rewritten range would not match the block
        # the Go tool itself attributed.
        output = self.root / "out" / "coverage.out"
        inputs = [self.write("p.out", GO_UNIT)]
        crap_report.merge_go_profiles(inputs, output, MODULE)
        self.assertIn("github.com/acme/app/service/user.go:14.2,20.5 6 3", output.read_text())

    def test_merged_profile_reports_block_and_file_counts(self):
        output = self.root / "out" / "coverage.out"
        summary = crap_report.merge_go_profiles(
            [self.write("p.out", GO_UNIT)], output, MODULE
        )
        self.assertEqual(summary, {"blocks": 3, "files": 2})

    def test_covermode_is_written_and_kept(self):
        output = self.root / "out" / "coverage.out"
        inputs = [
            self.write("unit.out", GO_UNIT),
            self.write("integration.out", GO_INTEGRATION),
        ]
        crap_report.merge_go_profiles(inputs, output, MODULE)
        self.assertTrue(output.read_text().startswith("mode: atomic\n"))

    def test_mismatched_covermode_is_rejected(self):
        other = GO_INTEGRATION.replace("mode: atomic", "mode: set")
        with self.assertRaisesRegex(crap_report.CoverageError, "covermode 'set'"):
            self.merged(GO_UNIT, other)

    def test_mismatched_statement_count_is_rejected(self):
        # One block, two statement counts: the profiles came from different builds.
        other = GO_INTEGRATION.replace(":14.2,20.5 6 0", ":14.2,20.5 7 0")
        with self.assertRaisesRegex(crap_report.CoverageError, "6 statements|7 statements"):
            self.merged(GO_UNIT, other)

    def test_self_contradicting_block_in_one_profile_is_rejected(self):
        # Merging across profiles would have caught this; one profile must not pass.
        broken = GO_UNIT + "github.com/acme/app/service/user.go:14.2,20.5 7 1\n"
        with self.assertRaisesRegex(crap_report.CoverageError, "6 statements|7 statements"):
            self.merged(broken)

    def test_repeated_block_with_the_same_count_sums_hits(self):
        repeated = GO_UNIT + "github.com/acme/app/service/user.go:14.2,20.5 6 2\n"
        blocks = self.merged(repeated)
        self.assertEqual(blocks["github.com/acme/app/service/user.go:14.2,20.5"], (6, 5))

    def test_unknown_covermode_is_rejected(self):
        # CI profiles are atomic; any other header means the file is truncated,
        # concatenated, or not a coverprofile at all.
        with self.assertRaisesRegex(crap_report.CoverageError, "covermode 'atomic-'"):
            self.merged(GO_UNIT.replace("mode: atomic", "mode: atomic-"))

    def test_empty_covermode_is_rejected(self):
        with self.assertRaisesRegex(crap_report.CoverageError, "''"):
            self.merged(GO_UNIT.replace("mode: atomic", "mode:"))

    def test_set_and_count_modes_are_accepted(self):
        for mode in ("set", "count"):
            with self.subTest(mode=mode):
                self.merged(GO_UNIT.replace("mode: atomic", f"mode: {mode}"))

    def test_missing_mode_header_is_rejected(self):
        with self.assertRaisesRegex(crap_report.CoverageError, "no 'mode:' header"):
            self.merged("github.com/acme/app/service/user.go:10.30,12.2 2 0\n")

    def test_malformed_line_is_rejected_with_its_line_number(self):
        broken = GO_UNIT + "github.com/acme/app/service/user.go:22.1,23.2 oops\n"
        with self.assertRaises(crap_report.CoverageError) as caught:
            self.merged(broken)
        self.assertIn("profile0.out:5", str(caught.exception))

    def test_unusable_block_fields_are_rejected(self):
        cases = {
            "github.com/acme/app/service/user.go:22.1,23.2 x 0": "invalid literal",
            "github.com/acme/app/service/user.go:22.1 1 0": "malformed block range",
            "github.com/acme/app/service/user.go:22.1,23.2 1 -3": "negative count",
        }
        for line, message in cases.items():
            with self.subTest(line=line):
                with self.assertRaisesRegex(crap_report.CoverageError, message):
                    self.merged(GO_UNIT + line + "\n")

    def test_missing_input_profile_is_rejected(self):
        output = self.root / "out" / "coverage.out"
        with self.assertRaisesRegex(crap_report.CoverageError, "not found"):
            crap_report.merge_go_profiles([self.root / "absent.out"], output, MODULE)

    def test_existing_output_is_replaced_not_appended(self):
        output = self.root / "out" / "coverage.out"
        output.parent.mkdir(parents=True)
        output.write_text("mode: atomic\ngithub.com/acme/stale.go:1.1,2.2 9 9\n", encoding="utf-8")
        crap_report.merge_go_profiles([self.write("unit.out", GO_UNIT)], output, MODULE)
        text = output.read_text()
        self.assertNotIn("stale.go", text)
        self.assertEqual(text.count("mode:"), 1)

    def test_single_profile_round_trips(self):
        _mode, blocks = crap_report._read_go_profile(self.write("unit.out", GO_UNIT))
        self.assertEqual(len(blocks), 3)
        self.assertEqual(
            blocks["github.com/acme/app/service/user.go:14.2,20.5"],
            (6, 3),
        )


class MergeBlockPathsTest(unittest.TestCase):
    """Two go test runs sharing one -coverprofile path interleave their writes."""

    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.root = Path(self.dir.name)
        self.output = self.root / "out" / "coverage.out"

    def merge(self, text: str) -> dict[str, tuple[int, int]]:
        source = self.root / "unit.out"
        source.write_text(text, encoding="utf-8")
        crap_report.merge_go_profiles([source], self.output, MODULE)
        return crap_report._read_go_profile(self.output)[1]

    def test_spliced_path_prefix_is_refused(self):
        # A garbled line still has three fields, so it parses. Merged unchecked it
        # becomes a block no source matches, and the intact block keeps only the
        # other run's hits, under-reporting coverage with nothing to notice it.
        spliced = (
            "mode: atomic\n"
            "github.com/acme/app/service/user.go:10.30,12.2 2 0\n"
            "ggithub.com/acme/app/service/user.go:14.2,20.5 6 3\n"
        )
        with self.assertRaisesRegex(crap_report.CoverageError, "ggithub.com/acme/app"):
            self.merge(spliced)

    def test_refused_merge_writes_no_report(self):
        spliced = "mode: atomic\nsh22rana/acme/app/service/user.go:1.1,2.2 1 0\n"
        with self.assertRaises(crap_report.CoverageError):
            self.merge(spliced)
        self.assertFalse(self.output.exists())

    def test_unrelated_module_path_is_refused(self):
        other = GO_UNIT.replace("github.com/acme/app", "example.com/other")
        with self.assertRaisesRegex(crap_report.CoverageError, "example.com/other"):
            self.merge(other)

    def test_a_prefix_of_the_module_is_not_enough(self):
        # `github.com/acme/application` merely starts with the module string.
        other = GO_UNIT.replace("github.com/acme/app/", "github.com/acme/application/")
        with self.assertRaisesRegex(crap_report.CoverageError, "application"):
            self.merge(other)

    def test_module_path_itself_is_allowed(self):
        blocks = self.merge("mode: atomic\ngithub.com/acme/app:1.1,2.2 1 1\n")
        self.assertIn("github.com/acme/app:1.1,2.2", blocks)

    def test_several_modules_are_each_accepted(self):
        source = "mode: atomic\nother.example/lib.go:1.1,2.2 1 1\n"
        path = self.root / "unit.out"
        path.write_text(source, encoding="utf-8")
        crap_report.merge_go_profiles([path], self.output, MODULE + ("other.example",))
        self.assertIn("other.example/lib.go:1.1,2.2", self.output.read_text())

    def test_unknown_block_files_reports_each_path_once(self):
        blocks = {
            "bad.example/a.go:1.1,2.2": (1, 0),
            "bad.example/a.go:3.1,4.2": (1, 0),
            "bad.example/b.go:1.1,2.2": (1, 0),
        }
        self.assertEqual(
            crap_report.unknown_block_files(blocks, MODULE),
            ["bad.example/a.go", "bad.example/b.go"],
        )

    def test_module_paths_are_read_from_every_go_mod(self):
        (self.root / "go.mod").write_text("// comment\nmodule example.com/app\n\ngo 1.24\n")
        (self.root / "tools").mkdir()
        (self.root / "tools" / "go.mod").write_text("module example.com/tools\n\ngo 1.24\n")
        self.assertEqual(
            crap_report.module_paths(self.root, ["go.mod", "tools/go.mod", "not-a-go-mod.txt"]),
            ("example.com/app", "example.com/tools"),
        )

    def test_missing_go_mod_declares_nothing_rather_than_crashing(self):
        # handwritten_sources reports the tracked-but-missing file, so this stays
        # the one place that names it instead of raising a bare OSError.
        self.assertEqual(crap_report.module_paths(self.root, ["go.mod"]), ())


class PrefixLcovPathsTest(unittest.TestCase):
    def test_every_source_record_is_prefixed(self):
        text = crap_report.prefix_lcov_paths(LCOV, "dashboard")
        self.assertIn("SF:dashboard/src/lib/utils.ts\n", text)
        self.assertIn("SF:dashboard/src/app/page.tsx\n", text)
        self.assertNotIn("SF:src/", text)

    def test_hit_branch_and_end_records_are_untouched(self):
        text = crap_report.prefix_lcov_paths(LCOV, "dashboard")
        for record in ("TN:", "DA:1,1", "DA:2,0", "BRDA:2,0,0,1", "end_of_record"):
            self.assertIn(f"{record}\n", text)

    def test_record_count_is_preserved(self):
        text = crap_report.prefix_lcov_paths(LCOV, "dashboard")
        self.assertEqual(text.count("SF:"), LCOV.count("SF:"))
        self.assertEqual(text.count("end_of_record"), LCOV.count("end_of_record"))

    def test_already_rooted_path_is_not_prefixed_again(self):
        text = crap_report.prefix_lcov_paths("SF:dashboard/src/a.ts\nend_of_record\n", "dashboard")
        self.assertIn("SF:dashboard/src/a.ts\n", text)
        self.assertNotIn("dashboard/dashboard", text)

    def test_absolute_path_is_left_alone(self):
        # An artifact from another checkout must not become
        # dashboard/home/runner/work/...: a name that exists nowhere simply fails
        # to match, and crapper's inventory then reports no coverage record.
        absolute = "/home/runner/work/chronoverse/chronoverse/dashboard/src/a.ts"
        text = crap_report.prefix_lcov_paths(f"SF:{absolute}\nend_of_record\n", "dashboard")
        self.assertIn(f"SF:{absolute}\n", text)
        self.assertNotIn("SF:dashboard/", text)

    def test_leading_and_trailing_slashes_are_normalised(self):
        text = crap_report.prefix_lcov_paths("SF:./src/a.ts\nend_of_record\n", "dashboard/")
        self.assertIn("SF:dashboard/src/a.ts\n", text)

    def test_file_url_prefix_is_normalised(self):
        text = crap_report.prefix_lcov_paths("SF:file:src/a.ts\nend_of_record\n", "dashboard")
        self.assertIn("SF:dashboard/src/a.ts\n", text)

    def test_path_spaces_are_preserved(self):
        text = crap_report.prefix_lcov_paths("SF:src/a b.ts\nend_of_record\n", "dashboard")
        self.assertIn("SF:dashboard/src/a b.ts\n", text)

    def test_roots_are_pure_and_independent_of_order(self):
        self.assertEqual(
            crap_report.root_lcov_path("src/a.ts", "dashboard"),
            crap_report.root_lcov_path("dashboard/src/a.ts", "dashboard"),
        )
        self.assertEqual(crap_report.root_lcov_path("src/a.ts", "static"), "static/src/a.ts")

    def test_report_without_records_is_rejected(self):
        with self.assertRaisesRegex(crap_report.CoverageError, "no SF: records"):
            crap_report.prefix_lcov_paths("TN:\nend_of_record\n", "dashboard")

    def test_truncated_report_is_rejected(self):
        with self.assertRaisesRegex(crap_report.CoverageError, "truncated"):
            crap_report.prefix_lcov_paths("SF:src/a.ts\nDA:1,1\n", "dashboard")

    def test_file_is_written_where_crapper_scans(self):
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            source = root / "lcov.info"
            source.write_text(LCOV, encoding="utf-8")
            output = crap_report.prefix_lcov_report(
                source, root / crap_report.DASHBOARD_LCOV, "dashboard"
            )
            self.assertEqual(
                output, root / "target/coverage/typescript/dashboard/lcov.info"
            )
            self.assertIn("SF:dashboard/src/lib/utils.ts", output.read_text())

    def test_missing_source_is_rejected(self):
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            with self.assertRaisesRegex(crap_report.CoverageError, "not found"):
                crap_report.prefix_lcov_report(
                    root / "absent.info", root / crap_report.DASHBOARD_LCOV, "dashboard"
                )


class HandwrittenSourcesTest(unittest.TestCase):
    FILES = {
        "internal/service/users/user.go": "package users\n\nfunc Get() {}\n",
        "internal/service/users/user_test.go": "package users\n\nfunc TestGet() {}\n",
        "internal/pkg/auth/mock/auth.go": (
            "// Code generated by MockGen. DO NOT EDIT.\n// Source: auth.go\n\npackage auth\n"
        ),
        "pkg/proto/go/users/users.pb.go": (
            "// Code generated by protoc-gen-go. DO NOT EDIT.\n\npackage users\n"
        ),
        "dashboard/src/lib/utils.ts": "export const cn = (a: string) => a\n",
        "dashboard/src/lib/utils.test.ts": "import { expect } from 'vitest'\n",
        "dashboard/vitest.config.ts": "export default {}\n",
        "dashboard/node_modules/pkg/index.js": "module.exports = 1\n",
        "dashboard/.next/build/app/page.js": "module.exports = 2\n",
        "internal/service/users/README.md": "# users\n",
        "internal/service/users/types.d.ts": "export type A = 1\n",
    }

    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.root = Path(self.dir.name)
        for name, text in self.FILES.items():
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(text, encoding="utf-8")

    def select(self, names: list[str] | None = None) -> list[str]:
        found = crap_report.handwritten_sources(self.root, names or list(self.FILES))
        return [path.relative_to(self.root).as_posix() for path in found]

    def test_keeps_handwritten_supported_sources(self):
        self.assertEqual(
            self.select(),
            [
                "dashboard/src/lib/utils.ts",
                "dashboard/vitest.config.ts",
                "internal/service/users/user.go",
            ],
        )

    def test_generated_trees_are_excluded(self):
        self.assertNotIn("pkg/proto/go/users/users.pb.go", self.select())

    def test_mockgen_output_is_excluded_by_its_marker(self):
        self.assertNotIn("internal/pkg/auth/mock/auth.go", self.select())

    def test_test_files_are_excluded(self):
        selected = self.select()
        self.assertNotIn("internal/service/users/user_test.go", selected)
        self.assertNotIn("dashboard/src/lib/utils.test.ts", selected)

    def test_declarations_and_unsupported_files_are_excluded(self):
        selected = self.select()
        self.assertNotIn("internal/service/users/types.d.ts", selected)
        self.assertNotIn("internal/service/users/README.md", selected)

    def test_dependency_and_build_output_are_excluded(self):
        # Exclude tracked dependency and build artifacts: `git ls-files` lists them.
        selected = self.select()
        self.assertNotIn("dashboard/node_modules/pkg/index.js", selected)
        self.assertNotIn("dashboard/.next/build/app/page.js", selected)

    def test_marker_must_follow_go_s_convention(self):
        # https://go.dev/s/generatedcode requires the exact shape in the leading
        # comment block; a mention without the DO NOT EDIT tail, or one below the
        # first line of code, is hand-written and stays in scope.
        self.assertFalse(
            crap_report.is_generated("// A helper.\n// Code generated by a tool.\npackage x\n")
        )
        self.assertFalse(
            crap_report.is_generated("// A helper.\npackage x\n\n// Code generated by x. DO NOT EDIT.\n")
        )
        self.assertTrue(crap_report.is_generated("// Code generated by MockGen. DO NOT EDIT.\npackage auth\n"))

    def test_plain_handwritten_header_is_not_generated(self):
        self.assertFalse(crap_report.is_generated("// Package users holds the service.\npackage users\n"))

    def test_generated_marker_after_code_is_not_generated(self):
        self.assertFalse(crap_report.is_generated("package users\n// Code generated by x\n"))


class TrackedFilesTest(unittest.TestCase):
    def test_git_failure_is_reported_clearly(self):
        with tempfile.TemporaryDirectory() as name:
            with self.assertRaises(crap_report.CoverageError):
                crap_report.tracked_files(Path(name))


class ClearStaleReportsTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.root = Path(self.dir.name).resolve()

    def touch(self, relative: str, text: str = "stale\n") -> Path:
        path = self.root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")
        return path

    def test_removes_the_reports_crapper_reads(self):
        self.touch(crap_report.GO_PROFILE.as_posix(), "mode: atomic\n")
        self.touch("target/coverage/coverage.out", "mode: atomic\n")
        self.touch("coverage.out", "mode: set\n")
        self.touch("target/coverage/typescript/dashboard/lcov.info", "SF:a\nend_of_record\n")
        self.touch("target/coverage/typescript/static/lcov.info", "SF:b\nend_of_record\n")
        self.touch("coverage/typescript/lcov.info", "SF:c\nend_of_record\n")
        # crapper also globs the report location of a nested module.
        self.touch("tooling/target/coverage/go/coverage.out", "mode: set\n")
        self.touch("services/worker/target/coverage/go/coverage.out", "mode: set\n")

        removed = crap_report.clear_stale_reports(self.root)

        self.assertEqual(
            removed,
            sorted(
                [
                    "coverage.out",
                    "coverage/typescript/lcov.info",
                    "services/worker/target/coverage/go/coverage.out",
                    "target/coverage/coverage.out",
                    "target/coverage/go/coverage.out",
                    "target/coverage/typescript/dashboard/lcov.info",
                    "target/coverage/typescript/static/lcov.info",
                    "tooling/target/coverage/go/coverage.out",
                ]
            ),
        )
        self.assertFalse((self.root / crap_report.GO_PROFILE).exists())
        self.assertFalse((self.root / "coverage/typescript/lcov.info").exists())
        self.assertFalse((self.root / "tooling/target/coverage/go/coverage.out").exists())

    def test_staged_inputs_survive(self):
        # CI stages the per-job profiles under INPUT_DIR, inside the same tree
        # crapper merges. Clearing reports must not delete this run's own inputs.
        unit = self.touch(f"{crap_report.INPUT_DIR}/unit/coverage.out", GO_UNIT)
        integration = self.touch(
            f"{crap_report.INPUT_DIR}/integration/coverage.out", GO_INTEGRATION
        )
        dashboard = self.touch("dashboard/coverage/lcov.info", LCOV)
        self.touch(crap_report.GO_PROFILE.as_posix(), "mode: atomic\ngithub.com/acme/stale.go:1.1,2.2 9 9\n")

        crap_report.clear_stale_reports(self.root)

        self.assertEqual(unit.read_text(), GO_UNIT)
        self.assertEqual(integration.read_text(), GO_INTEGRATION)
        self.assertEqual(dashboard.read_text(), LCOV)

    def test_input_directory_is_outside_every_stale_rule(self):
        # A guard against a future edit moving INPUT_DIR under a cleared tree.
        staged = self.touch(f"{crap_report.INPUT_DIR}/unit/coverage.out", GO_UNIT)
        for pattern in crap_report.STALE_PROFILES + crap_report.STALE_LCOV:
            self.assertEqual(list(self.root.glob(pattern)), [], pattern)
        self.assertEqual(staged.read_text(), GO_UNIT)

    def test_missing_reports_are_not_an_error(self):
        self.assertEqual(crap_report.clear_stale_reports(self.root), [])

    def test_input_inside_a_cleared_tree_is_refused_before_deletion(self):
        # Passing a report this run clears would delete the input and then fail.
        planted = self.touch(crap_report.DASHBOARD_LCOV.as_posix(), LCOV)
        with self.assertRaisesRegex(crap_report.CoverageError, "clears"):
            crap_report.resolve_inputs(self.root, [None, crap_report.DASHBOARD_LCOV])
        self.assertEqual(planted.read_text(), LCOV)

    def test_input_outside_every_cleared_tree_is_accepted(self):
        self.touch(crap_report.GO_PROFILE.as_posix(), "mode: atomic\n")
        staged = self.touch(f"{crap_report.INPUT_DIR}/unit/coverage.out", GO_UNIT)
        resolved = crap_report.resolve_inputs(self.root, [staged, None])
        self.assertEqual(resolved, [staged, None])

    def test_relative_input_resolves_against_the_root_not_the_cwd(self):
        # `--repo-root` must govern where a relative input is read from, so a run
        # from another directory still finds the profile the root is holding.
        self.touch(f"{crap_report.INPUT_DIR}/unit/coverage.out", GO_UNIT)
        resolved = crap_report.resolve_inputs(
            self.root, [Path(f"{crap_report.INPUT_DIR}/unit/coverage.out"), None]
        )
        self.assertEqual(resolved, [self.root / crap_report.INPUT_DIR / "unit/coverage.out", None])

    def test_missing_input_is_refused_before_anything_is_cleared(self):
        # Clearing first would destroy the previous run's reports over a typo'd path.
        previous = self.touch(crap_report.GO_PROFILE.as_posix(), "mode: atomic\n")
        with self.assertRaisesRegex(crap_report.CoverageError, "not found"):
            crap_report.resolve_inputs(self.root, [Path("target/coverage-inputs/absent.out")])
        self.assertEqual(previous.read_text(), "mode: atomic\n")


class MainTest(unittest.TestCase):
    """End-to-end: the CLI has to run to completion on a small work tree."""

    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.root = Path(self.dir.name).resolve()
        subprocess.run(["git", "init", "-q", str(self.root)], check=True)
        # The module matches the import path the fixture profiles write, so every
        # block file path is one go.mod declares.
        (self.root / "go.mod").write_text("module github.com/acme/app\n\ngo 1.24\n", encoding="utf-8")
        self.write("service/user.go", MAIN_USER_GO)
        self.write("service/audit.go", MAIN_AUDIT_GO)
        self.write("service/user_test.go", "package service\n\nfunc TestChosen() { Chosen(2) }\n")
        self.write("dashboard/src/lib/utils.ts", MAIN_UTILS_TS)
        self.write("dashboard/src/app/page.tsx", MAIN_PAGE_TSX)
        subprocess.run(["git", "-C", str(self.root), "add", "-A"], check=True)

    def write(self, name: str, text: str) -> Path:
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")
        return path

    def stage(self) -> tuple[Path, Path, Path]:
        unit = self.write(f"{crap_report.INPUT_DIR}/unit/coverage.out", MAIN_GO_UNIT)
        integration = self.write(
            f"{crap_report.INPUT_DIR}/integration/coverage.out", MAIN_GO_INTEGRATION
        )
        dashboard = self.write("dashboard/coverage/lcov.info", MAIN_LCOV)
        return unit, integration, dashboard

    def run_main(self, *extra: str) -> int:
        unit, integration, dashboard = self.stage()
        argv = [
            "--repo-root", str(self.root),
            "--go-unit", str(unit),
            "--go-integration", str(integration),
            "--dashboard-lcov", str(dashboard),
            *extra,
        ]
        return crap_report.main(argv)

    def test_runs_and_writes_all_three_reports(self):
        with contextlib.redirect_stdout(io.StringIO()) as out:
            self.assertEqual(self.run_main(), 0)
        printed = out.getvalue()

        metrics = self.root / ".metrics"
        for name in ("crap.edn", "crap-report.txt", "inventory.csv"):
            self.assertTrue((metrics / name).is_file(), name)
        self.assertIn("CRAP Report", (metrics / "crap-report.txt").read_text())
        self.assertIn(":entries", (metrics / "crap.edn").read_text())
        self.assertIn("Chosen", printed)

    def test_merged_and_prefixed_reports_land_where_crapper_reads_them(self):
        self.run_main()
        go_profile = self.root / crap_report.GO_PROFILE
        lcov = self.root / crap_report.DASHBOARD_LCOV
        self.assertTrue(go_profile.read_text().startswith("mode: atomic\n"))
        self.assertIn("SF:dashboard/src/lib/utils.ts", lcov.read_text())

    def test_stale_reports_are_excluded_but_inputs_survive(self):
        with contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(self.run_main(), 0)
        first = (self.root / crap_report.GO_PROFILE).read_text()

        # A second run must still find its inputs and must not read the merged
        # report the first run left behind: merging that file would double every
        # hit count.
        self.assertTrue((self.root / crap_report.INPUT_DIR / "unit" / "coverage.out").is_file())
        with contextlib.redirect_stdout(io.StringIO()) as out:
            self.assertEqual(self.run_main(), 0)
        self.assertIn("Removed stale reports", out.getvalue())
        self.assertEqual((self.root / crap_report.GO_PROFILE).read_text(), first)

    def test_custom_metrics_dir_receives_the_native_snapshot(self):
        outside = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, outside, ignore_errors=True)
        with contextlib.redirect_stdout(io.StringIO()) as out:
            self.assertEqual(self.run_main("--metrics-dir", str(outside)), 0)

        for name in ("crap.edn", "crap-report.txt", "inventory.csv"):
            self.assertTrue((outside / name).is_file(), name)
        # The bytes are what crapper's own write_metrics produces, so a custom
        # directory cannot drift from the snapshot format.
        from crapper.analyze import analyze_files
        from crapper.metrics import write_metrics

        with tempfile.TemporaryDirectory() as scratch:
            expected_root = Path(scratch)
            files = crap_report.handwritten_sources(self.root, crap_report.tracked_files(self.root))
            entries = analyze_files(files, self.root, crap_report.load_bundle(self.root))
            expected = write_metrics(entries, expected_root).read_text()
        self.assertEqual((outside / "crap.edn").read_text(), expected)
        # A directory outside the work tree is reported by its real path.
        self.assertIn(str(outside), out.getvalue())
        self.assertFalse((self.root / ".metrics").exists())

    def test_default_metrics_dir_matches_crapper(self):
        self.run_main()
        snapshot = self.root / ".metrics" / "crap.edn"
        self.assertTrue(snapshot.is_file())

    def test_inventory_separates_measured_zero_from_no_record(self):
        self.run_main()
        lines = (self.root / ".metrics" / "inventory.csv").read_text().splitlines()
        self.assertEqual(lines[0], ",".join(crap_report.INVENTORY_FIELDS))
        rows = {}
        for line in lines[1:]:
            fields = line.split(",")
            rows[fields[5]] = dict(
                path=fields[0],
                complexity=fields[6],
                coverage=fields[7],
                recorded=fields[8],
            )

        # Instrumented and partly entered by the two suites: a real measurement.
        self.assertEqual(rows["Chosen"]["recorded"], "yes")
        self.assertEqual(rows["Chosen"]["coverage"], "66.6667")
        # Instrumented but never entered: measured zero, not missing.
        self.assertEqual(rows["Audit"]["recorded"], "yes")
        self.assertEqual(rows["Audit"]["coverage"], "0.0000")
        # Absent from the report entirely: no record, and crapper scores it 0%.
        self.assertEqual(rows["Panel"]["recorded"], "no")
        self.assertEqual(rows["Panel"]["coverage"], "")
        # Branch coverage from the dashboard LCOV, matched after prefixing.
        self.assertEqual(rows["cn"]["recorded"], "yes")
        self.assertEqual(rows["cn"]["path"], "dashboard/src/lib/utils.ts")

    def test_generated_and_test_files_are_not_scored(self):
        self.run_main()
        inventory = (self.root / ".metrics" / "inventory.csv").read_text()
        self.assertNotIn("user_test.go", inventory)

    def test_snapshot_matches_crapper_own_analyse_run(self):
        from crapper.analyze import analyze_files

        self.run_main()
        files = crap_report.handwritten_sources(self.root, crap_report.tracked_files(self.root))
        expected = analyze_files(files, self.root, crap_report.load_bundle(self.root))
        rendered = [
            f':name "{entry.name}", :namespace "{entry.namespace}", '
            f":complexity {entry.complexity}, :coverage {entry.coverage}, :crap {entry.crap}"
            for entry in expected
        ]
        snapshot = (self.root / ".metrics" / "crap.edn").read_text()
        for row in rendered:
            self.assertIn(row, snapshot)
        self.assertEqual(snapshot.count(":name \""), len(rendered))

    def test_missing_report_argument_is_reported(self):
        self.stage()
        with self.assertRaisesRegex(crap_report.CoverageError, "not found"):
            crap_report.main(["--repo-root", str(self.root), "--go-unit", str(self.root / "no.out")])

    def test_report_used_as_its_own_input_is_refused_and_left_on_disk(self):
        unit, _integration, _dashboard = self.stage()
        collided = self.write(crap_report.DASHBOARD_LCOV.as_posix(), LCOV)
        with self.assertRaisesRegex(crap_report.CoverageError, "clears"):
            crap_report.main(
                [
                    "--repo-root", str(self.root),
                    "--go-unit", str(unit),
                    "--dashboard-lcov", str(collided),
                ]
            )
        self.assertTrue(collided.is_file())
        self.assertTrue((self.root / crap_report.INPUT_DIR / "unit" / "coverage.out").is_file())


class SummariseTest(unittest.TestCase):
    """The scope block must label each count with what it actually counts."""

    ROWS = [
        {"language": "go", "complexity": "4", "coverage": "50.0000", "coverage_recorded": "yes", "crap": "12.0"},
        {"language": "go", "complexity": "1", "coverage": "0.0000", "coverage_recorded": "yes", "crap": "2.0"},
        {"language": "typescript", "complexity": "20", "coverage": "", "coverage_recorded": "no", "crap": "420.0"},
    ]

    def test_file_and_function_counts_are_labelled_separately(self):
        with tempfile.TemporaryDirectory() as name:
            root = Path(name).resolve()
            (root / "a.go").write_text("package a\n", encoding="utf-8")
            (root / "b.go").write_text("package b\n", encoding="utf-8")
            (root / "c.ts").write_text("export const c = 1\n", encoding="utf-8")
            # Four functions across three files: a file breakdown read as the
            # function totals named more files than the scope holds.
            rows = self.ROWS + [dict(self.ROWS[0], name="extra")]
            text = crap_report.summarise(rows, [root / "a.go", root / "b.go", root / "c.ts"], root)

        self.assertIn("files:      3 (go=2, typescript=1)", text)
        self.assertIn("functions:  4 (go=3, typescript=1)", text)

    def test_empty_scope_is_reported_without_a_breakdown(self):
        with tempfile.TemporaryDirectory() as name:
            text = crap_report.summarise([], [], Path(name).resolve())
        self.assertIn("files:      0 (no supported sources)", text)
        self.assertIn("functions:  0 (no supported sources)", text)

    def test_measured_zero_and_no_record_stay_apart(self):
        with tempfile.TemporaryDirectory() as name:
            root = Path(name).resolve()
            (root / "a.go").write_text("package a\n", encoding="utf-8")
            text = crap_report.summarise(self.ROWS, [root / "a.go"], root)
        self.assertIn("recorded:   2 functions matched a coverage record", text)
        self.assertIn("no record:  1 functions no report mentions", text)
        self.assertIn("zero:       1 of the matched functions measured 0%", text)


class MissingTrackedFileTest(unittest.TestCase):
    def test_tracked_but_missing_file_is_reported_clearly(self):
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            with self.assertRaisesRegex(crap_report.CoverageError, "tracked but missing"):
                crap_report.handwritten_sources(root, ["service/gone.go"])


class DisplayTest(unittest.TestCase):
    def test_relative_when_inside_the_root(self):
        root = Path("/tmp/example")
        self.assertEqual(crap_report.display(root / ".metrics" / "crap.edn", root), ".metrics/crap.edn")

    def test_absolute_when_outside_the_root(self):
        self.assertEqual(
            crap_report.display(Path("/tmp/elsewhere/crap.edn"), Path("/tmp/example")),
            "/tmp/elsewhere/crap.edn",
        )


class MeasureTest(unittest.TestCase):
    """The snapshot has to agree with crapper, and the inventory has to say more."""

    def test_entries_match_crapper_and_no_record_is_kept_separate(self):
        from crapper.analyze import analyze_files

        with tempfile.TemporaryDirectory() as name:
            root = Path(name).resolve()
            source = root / "src" / "math.go"
            source.parent.mkdir(parents=True)
            source.write_text(
                "package math\n\n"
                "func Classified(n int) string {\n"
                '\tif n > 0 {\n\t\treturn "positive"\n\t}\n'
                '\treturn "other"\n}\n',
                encoding="utf-8",
            )
            go_profile = root / crap_report.GO_PROFILE
            go_profile.parent.mkdir(parents=True)
            go_profile.write_text(
                "mode: atomic\n"
                "example.com/app/src/math.go:3.14,5.2 2 2\n"
                "example.com/app/src/math.go:6.2,7.2 1 0\n",
                encoding="utf-8",
            )
            uncovered = root / "src" / "extra.go"
            uncovered.write_text("package math\n\nfunc Never() int {\n\treturn 1\n}\n", "utf-8")

            files = [source.resolve(), uncovered.resolve()]
            bundle = crap_report.load_bundle(root)
            entries, rows = crap_report.measure(files, root, bundle)
            expected = analyze_files(files, root, crap_report.load_bundle(root))

            self.assertEqual(
                [(e.namespace, e.name, e.complexity, e.coverage, e.crap) for e in entries],
                [(e.namespace, e.name, e.complexity, e.coverage, e.crap) for e in expected],
            )

            by_name = {row["name"]: row for row in rows}
            self.assertEqual(by_name["Classified"]["coverage_recorded"], "yes")
            self.assertEqual(by_name["Classified"]["coverage"], "66.6667")
            self.assertEqual(by_name["Never"]["coverage_recorded"], "no")
            self.assertEqual(by_name["Never"]["coverage"], "")
            # crapper scores the unmeasured function as 0% coverage in both places.
            self.assertEqual(by_name["Never"]["crap"], "2.0000")

    def test_inventory_is_sorted_by_source_position(self):
        with tempfile.TemporaryDirectory() as name:
            root = Path(name).resolve()
            paths = []
            for index in (3, 1, 2):
                path = root / "src" / f"file{index}.go"
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(f"package src\n\nfunc F{index}() {{\n\treturn\n}}\n", encoding="utf-8")
                paths.append(path)
            rows = crap_report.measure(paths, root, crap_report.load_bundle(root))[1]
            self.assertEqual([row["path"] for row in rows], sorted(row["path"] for row in rows))


class InventoryFileTest(unittest.TestCase):
    def test_header_and_rows_are_written(self):
        with tempfile.TemporaryDirectory() as name:
            path = crap_report.write_inventory(
                Path(name) / "inventory.csv",
                [
                    {
                        "path": "src/a.go",
                        "start_line": 1,
                        "end_line": 2,
                        "language": "go",
                        "namespace": "app",
                        "name": "A",
                        "complexity": 1,
                        "coverage": "100.0000",
                        "coverage_recorded": "yes",
                        "crap": "1.0",
                    }
                ],
            )
            lines = path.read_text().splitlines()
            self.assertEqual(lines[0], ",".join(crap_report.INVENTORY_FIELDS))
            self.assertIn("src/a.go,1,2,go,app,A,1,100.0000,yes,1.0", lines[1])


if __name__ == "__main__":
    unittest.main()
