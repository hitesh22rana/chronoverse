"""Behavioral tests for changed-function coverage/CRAP reporting."""

import csv
from decimal import Decimal
import json
from pathlib import Path
import tempfile
import unittest

from pr_report import Function, compare, changed_files, escape, main, markdown, read_inventory


def function(name="run", coverage="80", crap="10", complexity=4, start=10, end=20,
             path="internal/service.go", namespace="service"):
    return Function(path, namespace, name, start, end, complexity,
                    Decimal(coverage) if coverage is not None else None,
                    Decimal(crap) if coverage is not None else None, coverage is not None)


def patch(old="12,1", new="12,1", path="internal/service.go"):
    return (f"diff --git a/{path} b/{path}\n--- a/{path}\n+++ b/{path}\n"
            f"@@ -{old} +{new} @@\n-old\n+new\n")


class ChangedFunctionReportTests(unittest.TestCase):
    def test_only_changed_function_is_gated(self):
        base = [function("changed"), function("unrelated", start=30, end=40)]
        head = [function("changed", coverage="90", crap="8"),
                function("unrelated", coverage="0", crap="100", start=30, end=40)]
        result = compare(head, base, patch())
        self.assertEqual(result["gate"], "passed")
        self.assertEqual([r["name"] for r in result["rows"]], ["changed"])
        self.assertEqual(result["totals"]["head"]["crap_ge_30"], 1)

    def test_deletion_inside_function_selects_old_range_without_neighbor(self):
        base = [function(), function("neighbor", start=25, end=30)]
        head = [function(end=19), function("neighbor", coverage="0", crap="100", start=24, end=29)]
        result = compare(head, base, patch(old="19,1", new="18,0"))
        self.assertEqual([r["name"] for r in result["rows"]], ["run"])
        self.assertEqual(result["gate"], "passed")

    def test_insert_between_functions_does_not_select_old_anchor(self):
        base = [function(end=10, start=1)]
        head = [function(end=10, start=1), function("new", coverage="100", crap="2", start=11, end=14)]
        result = compare(head, base, patch(old="10,0", new="11,4"))
        self.assertEqual([(r["name"], r["status"]) for r in result["rows"]], [("new", "added")])

    def test_small_numeric_rounding_is_allowed_but_actual_loss_fails(self):
        allowed = compare([function(coverage="79.99", crap="30.01")], [function(crap="30")], patch())
        self.assertEqual(allowed["gate"], "passed")
        failed = compare([function(coverage="79.9899", crap="30.0101")], [function(crap="30")], patch())
        self.assertEqual(failed["regressions"], 1)
        self.assertEqual(len(failed["rows"][0]["reasons"]), 2)
        self.assertEqual(failed["gate"], "failed")

    def test_below_threshold_crap_increase_is_informational(self):
        result = compare([function(coverage="100", crap="3")],
                         [function(coverage="100", crap="2")], patch())
        self.assertEqual(result["gate"], "passed")
        self.assertIn("informational", result["rows"][0]["note"])
        self.assertIn("increases remaining below 30 do not fail", markdown(result))

    def test_crossing_threshold_or_worsening_high_crap_fails(self):
        for before, after in (("29", "30"), ("40", "41")):
            with self.subTest(before=before, after=after):
                result = compare([function(crap=after)], [function(crap=before)], patch())
                self.assertEqual(result["gate"], "failed")
                self.assertIn("now >= 30", result["rows"][0]["reasons"][0])

    def test_existing_debt_does_not_fail_when_it_improves_or_stays(self):
        for after in ("60", "55"):
            with self.subTest(after=after):
                result = compare([function(crap=after)], [function(crap="60")], patch())
                self.assertEqual(result["gate"], "passed")

    def test_lost_measurement_is_not_silently_zero_coverage(self):
        result = compare([function(coverage=None)], [function()], patch())
        self.assertEqual(result["gate"], "failed")
        self.assertIsNone(result["rows"][0]["after"]["coverage"])
        self.assertIn("lost its coverage record", result["rows"][0]["reasons"][0])
        self.assertEqual(result["totals"]["head"]["missing"], 1)

    def test_new_unmeasured_and_high_crap_fail_but_measured_zero_is_distinct(self):
        for head in (function(coverage=None), function(crap="30")):
            with self.subTest(head=head):
                result = compare([head], [], patch(old="0,0", new="10,11"))
                self.assertEqual(result["gate"], "failed")
                self.assertEqual(result["rows"][0]["status"], "added")
        zero = compare([function(coverage="0", crap="12")], [], patch(old="0,0", new="10,11"))
        self.assertEqual(zero["gate"], "passed")
        self.assertEqual(zero["totals"]["head"]["measured"], 1)

    def test_missing_baseline_is_report_only_and_not_a_pass(self):
        result = compare([function(coverage=None)], None, patch())
        self.assertEqual(result["gate"], "comparison-unavailable")
        self.assertEqual(result["regressions"], 0)
        text = markdown(result)
        self.assertIn("does not establish", text)
        self.assertIn("unmeasured", text)
        self.assertNotIn("No regression.", text)

    def test_baseline_unavailability_reason_is_preserved_and_escaped(self):
        result = compare([function()], None, patch(), baseline_reason="Expired <artifact> | @all")
        self.assertEqual(result["baseline_reason"], "Expired <artifact> | @all")
        text = markdown(result)
        self.assertIn("Expired &lt;artifact&gt;", text)
        self.assertNotIn("@all", text)

    def test_new_measurement_does_not_claim_numeric_baseline_comparison(self):
        result = compare([function()], [function(coverage=None)], patch())
        self.assertIn("measurement added", result["rows"][0]["note"])
        self.assertEqual(result["gate"], "passed")

    def test_duplicate_identity_is_never_paired_by_order_or_lines(self):
        for base, head in (([function(), function(start=30, end=40)], [function()]),
                           ([function()], [function(), function(start=30, end=40)])):
            with self.subTest(base=base):
                result = compare(head, base, patch())
                self.assertEqual(result["gate"], "incomplete")
                self.assertEqual(result["uncomparable"], 1)
                self.assertEqual(result["rows"][0]["status"], "uncomparable")
                self.assertIsNone(result["rows"][0]["before"])
                self.assertIsNone(result["rows"][0]["after"])

    def test_namespace_is_part_of_function_identity(self):
        base = [function(namespace="one"), function(namespace="two", start=30, end=40)]
        head = [function(namespace="one", coverage="100"), function(namespace="two", start=30, end=40)]
        result = compare(head, base, patch())
        self.assertEqual(result["rows"][0]["namespace"], "one")
        self.assertEqual(result["gate"], "passed")

    def test_deleted_function_is_removed_not_an_unmeasured_regression(self):
        result = compare([], [function()], patch(old="10,11", new="9,0"))
        self.assertEqual(result["rows"][0]["status"], "removed")
        self.assertIsNone(result["rows"][0]["after"])
        self.assertEqual(result["gate"], "passed")

    def test_modified_rename_is_not_misclassified_as_new_high_debt(self):
        diff = ("diff --git a/old.go b/new.go\nrename from old.go\nrename to new.go\n"
                "--- a/old.go\n+++ b/new.go\n@@ -12 +12 @@\n-old\n+new\n")
        result = compare([function(path="new.go", crap="60")],
                         [function(path="old.go", crap="60")], diff)
        self.assertEqual(result["gate"], "incomplete")
        self.assertEqual(result["regressions"], 0)
        self.assertEqual(result["uncomparable"], 2)

    def test_summary_counts_are_inventory_counts_not_average_coverage(self):
        result = compare([function(), function("unknown", coverage=None, start=30, end=40)],
                         [function(crap="40")], patch(), "base", "head", "merge")
        self.assertEqual(result["totals"]["head"],
                         {"functions": 2, "measured": 1, "missing": 1, "crap_ge_30": 0})
        text = markdown(result)
        self.assertIn("| Functions | 1 | 2 |", text)
        self.assertIn("| Measured functions | 1 | 1 |", text)
        self.assertIn("not overall statement or branch coverage", text)
        self.assertIn("Tested revision: merge", text)

    def test_markdown_escapes_untrusted_identifiers_and_bounds_table(self):
        unsafe = "<img src=x>|`name`\n@everyone"
        rendered = escape(unsafe)
        self.assertNotIn("<img", rendered)
        self.assertNotIn("\n", rendered)
        self.assertNotIn("@everyone", rendered)
        self.assertIn("\\|", rendered)
        self.assertIn("\\`", rendered)
        result = compare([function(str(n), start=10+n, end=10+n) for n in range(60)],
                         [], patch(old="0,0", new="1,100"))
        text = markdown(result)
        self.assertIn("Showing 50 of 60", text)
        self.assertEqual(text.count("| added |"), 50)
        self.assertEqual(len(result["rows"]), 60)

    def test_source_lines_resembling_diff_headers_cannot_change_paths(self):
        diff = patch().replace("-old\n+new", "--- old\n+++ new")
        result = compare([function(coverage="50")], [function()], diff)
        self.assertEqual(result["gate"], "failed")
        self.assertEqual(result["rows"][0]["path"], "internal/service.go")

    def test_git_octal_utf8_and_control_character_paths_are_decoded(self):
        from pr_report import git_path
        self.assertEqual(git_path(r'"a/caf\303\251.go"'), "café.go")
        self.assertEqual(git_path(r'"a/bell\a.go"'), "bell\a.go")
        self.assertEqual(git_path(r'"a/vertical\v.go"'), "vertical\v.go")

    def test_git_quoted_paths_and_zero_hunks_are_parsed(self):
        diff = ('diff --git "a/a b.go" "b/a b.go"\n--- "a/a b.go"\n+++ "b/a b.go"\n'
                '@@ -1,0 +2,3 @@\n+x\n+y\n+z\n')
        parsed = changed_files(diff)[0]
        self.assertEqual(parsed.before_path, "a b.go")
        self.assertEqual(parsed.before, [])
        self.assertEqual(parsed.after, [(2, 4)])


class InventoryAndCliTests(unittest.TestCase):
    def write_inventory(self, path, rows):
        fields = ["path", "namespace", "name", "start_line", "end_line", "complexity",
                  "coverage", "coverage_recorded", "crap"]
        with path.open("w", newline="", encoding="utf-8") as handle:
            writer = csv.DictWriter(handle, fieldnames=fields)
            writer.writeheader()
            writer.writerows(rows)

    def row(self, measured=True):
        return {"path": "internal/service.go", "namespace": "service", "name": "run",
                "start_line": 10, "end_line": 20, "complexity": 4,
                "coverage": "80" if measured else "", "coverage_recorded": "yes" if measured else "no",
                "crap": "10" if measured else "20"}

    def test_canonical_unmeasured_provisional_crap_is_not_treated_as_measured(self):
        with tempfile.TemporaryDirectory() as directory:
            file = Path(directory)/"inventory.csv"
            self.write_inventory(file, [self.row(False)])
            record = read_inventory(file)[0]
            self.assertIsNone(record.coverage)
            self.assertIsNone(record.crap)
            self.assertFalse(record.measured)

    def test_malformed_numeric_inventory_fails_closed(self):
        with tempfile.TemporaryDirectory() as directory:
            file = Path(directory)/"inventory.csv"
            for value in ("nan", "Infinity", "-1", "101", "garbage"):
                with self.subTest(value=value):
                    row = self.row(); row["coverage"] = value
                    self.write_inventory(file, [row])
                    with self.assertRaises(ValueError):
                        read_inventory(file)

    def test_cli_fails_incomplete_comparison_and_preserves_evidence(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            row = self.row()
            self.write_inventory(root/"base.csv", [row, row])
            self.write_inventory(root/"head.csv", [row, row])
            (root/"diff.patch").write_text(patch())
            args = ["--head", str(root/"head.csv"), "--base", str(root/"base.csv"),
                    "--diff", str(root/"diff.patch"), "--summary", str(root/"summary.md"),
                    "--result", str(root/"result.json"), "--head-sha", "head",
                    "--tested-sha", "merge"]
            self.assertEqual(main(args), 1)
            result = json.loads((root/"result.json").read_text())
            self.assertEqual(result["gate"], "incomplete")
            self.assertEqual(result["regressions"], 0)
            self.assertGreater(result["uncomparable"], 0)
            self.assertIn("Duplicate identity", (root/"summary.md").read_text())
            old = self.row(); old["path"] = "old.go"
            new = self.row(); new["path"] = "new.go"; new["coverage"] = "20"
            self.write_inventory(root/"base.csv", [old])
            self.write_inventory(root/"head.csv", [new])
            (root/"diff.patch").write_text(
                "diff --git a/old.go b/new.go\nrename from old.go\nrename to new.go\n"
                "--- a/old.go\n+++ b/new.go\n@@ -12 +12 @@\n-old\n+new\n")
            self.assertEqual(main(args), 1)
            self.assertEqual(json.loads((root/"result.json").read_text())["gate"], "incomplete")
            self.assertIn("check fails", (root/"summary.md").read_text())

    def test_cli_writes_artifacts_before_returning_regression_exit_code(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.write_inventory(root/"base.csv", [self.row()])
            row = self.row(); row["coverage"] = "50"
            self.write_inventory(root/"head.csv", [row])
            (root/"diff.patch").write_text(patch())
            args = ["--head", str(root/"head.csv"), "--base", str(root/"base.csv"),
                    "--diff", str(root/"diff.patch"), "--summary", str(root/"summary.md"),
                    "--result", str(root/"result.json"), "--base-sha", "base", "--head-sha", "head",
                    "--tested-sha", "merge"]
            self.assertEqual(main(args), 1)
            result = json.loads((root/"result.json").read_text())
            self.assertEqual(result["gate"], "failed")
            self.assertIn("80.00%", (root/"summary.md").read_text())
            self.assertIn("50.00%", (root/"summary.md").read_text())
            args = [value for value in args]
            index = args.index("--base"); del args[index:index+2]
            self.assertEqual(main(args), 0)
            self.assertEqual(json.loads((root/"result.json").read_text())["gate"], "comparison-unavailable")


if __name__ == "__main__":
    unittest.main()
