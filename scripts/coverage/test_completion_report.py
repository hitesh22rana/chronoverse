"""Absolute policy outcomes and report-only CI integration contracts."""

import csv
import json
from pathlib import Path
import runpy
import tempfile
import unittest
from unittest import mock

from completion_report import evaluate, main, markdown
from test_pr_report import function


def scope(**changes):
    result = {"sources": ["internal/service.go"],
              "inputs": dict.fromkeys(("go_unit", "go_integration", "dashboard", "static", "tooling"), True)}
    result.update(changes)
    return result


class CompletionReportTests(unittest.TestCase):
    def test_absolute_policy_detects_unchanged_and_unmeasured_debt(self):
        report = evaluate([function("unmeasured", coverage=None),
                           function("partial", coverage="99.999", crap="30"),
                           function("complete", coverage="100", crap="29.999")], scope())
        self.assertEqual(report["function_debt"], [
            {"path": "internal/service.go", "namespace": "service", "name": "unmeasured",
             "reasons": ["coverage unavailable"]},
            {"path": "internal/service.go", "namespace": "service", "name": "partial",
             "reasons": ["coverage below 100%", "CRAP at least 30"]}])
        self.assertFalse(report["function_policy_satisfied"])
        self.assertFalse(report["complete"])

    def test_each_absolute_policy_boundary_independently(self):
        for coverage, crap, reasons in (
            (None, "10", ["coverage unavailable"]),
            ("0", "10", ["coverage below 100%"]),
            ("99.999", "10", ["coverage below 100%"]),
            ("100", "30", ["CRAP at least 30"]),
            ("100", "30.001", ["CRAP at least 30"]),
            ("100", "29.999", []),
        ):
            with self.subTest(coverage=coverage, crap=crap):
                report = evaluate([function(coverage=coverage, crap=crap)], scope())
                self.assertEqual(report["scope_blockers"], [])
                self.assertEqual(report["function_policy_satisfied"], not reasons)
                self.assertEqual([debt["reasons"] for debt in report["function_debt"]],
                                 [reasons] if reasons else [])

    def test_each_missing_measurement_input_independently_blocks_success(self):
        for name in ("go_unit", "go_integration", "dashboard", "static", "tooling"):
            for unavailable in (None, False, "true", 1):
                with self.subTest(name=name, unavailable=unavailable):
                    metadata = scope()
                    if unavailable is None:
                        del metadata["inputs"][name]
                    else:
                        metadata["inputs"][name] = unavailable
                    report = evaluate([function(coverage="100")], metadata)
                    self.assertEqual(report["function_debt"], [])
                    self.assertEqual(report["scope_blockers"], [
                        f"Required measurement input unavailable: {name}."])
                    self.assertFalse(report["function_policy_satisfied"])

    def test_function_success_cannot_establish_repository_completion(self):
        report = evaluate([function(coverage="100", crap="4")], scope())
        self.assertTrue(report["function_policy_satisfied"])
        self.assertFalse(report["complete"])
        self.assertEqual(len(report["unavailable_metrics"]), 3)
        self.assertIn("Repository completion: **not established**", markdown(report))

    def test_missing_scope_empty_inventory_and_inputs_remain_blockers(self):
        report = evaluate([], None)
        self.assertEqual(report["scope_blockers"], [
            "Complete source scope metadata is unavailable.", "Function inventory is empty."])
        self.assertFalse(report["function_policy_satisfied"])
        report = evaluate([function()], scope(sources=[], inputs={"static": "true"}))
        self.assertEqual(len(report["scope_blockers"]), 7)
        self.assertIn("Required measurement input unavailable: static.", markdown(report))
        self.assertIn("outside declared source scope", report["scope_blockers"][-2])

    def test_duplicate_function_identity_cannot_satisfy_policy(self):
        report = evaluate([function(coverage="100"), function(coverage="100")], scope())
        self.assertFalse(report["function_policy_satisfied"])
        self.assertEqual(report["scope_blockers"], [
            "Function inventory contains ambiguous duplicate identities."])

    def test_scope_shape_and_duplicates_are_not_silently_accepted(self):
        for invalid in ([], {}, scope(sources=[None]), scope(inputs=[])):
            with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                evaluate([], invalid)
        report = evaluate([], scope(sources=["a", "a"]))
        self.assertIn("Source scope contains duplicate paths.", report["scope_blockers"])

    def test_cli_persists_debt_without_failing_for_existing_debt(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            inventory = root / "inventory.csv"
            fields = ["path", "namespace", "name", "start_line", "end_line", "complexity",
                      "coverage", "coverage_recorded", "crap"]
            with inventory.open("w") as handle:
                writer = csv.DictWriter(handle, fieldnames=fields)
                writer.writeheader()
                writer.writerow(dict(zip(fields, ["internal/service.go", "service", "run", 1,
                                                  2, 4, 50, "yes", 30])))
            scope_file = root / "scope.json"
            args = ["--inventory", str(inventory), "--scope", str(scope_file),
                    "--output", str(root / "reports")]
            self.assertEqual(main(args), 0)
            report = json.loads((root / "reports/completion.json").read_text())
            self.assertEqual(len(report["function_debt"]), 1)
            self.assertIn("scope metadata is unavailable", report["scope_blockers"][0])
            scope_file.write_text(json.dumps(scope()))
            with mock.patch("sys.argv", ["completion_report.py", *args]):
                with self.assertRaises(SystemExit) as raised:
                    runpy.run_module("completion_report", run_name="__main__")
            self.assertEqual(raised.exception.code, 0)
            self.assertEqual(json.loads((root / "reports/completion.json").read_text())[
                "scope_blockers"], [])
            self.assertIn("report-only", (root / "reports/completion.md").read_text())
            scope_file.write_text("null")
            self.assertEqual(main(args), 0)
            scope_file.write_text("{")
            with self.assertRaises(json.JSONDecodeError):
                main(args)
