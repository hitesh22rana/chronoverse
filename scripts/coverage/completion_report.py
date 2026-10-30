#!/usr/bin/env python3
"""Report absolute completion debt without enabling a required global gate."""

from __future__ import annotations

import argparse
import json
from pathlib import Path

from pr_report import read_inventory

REQUIRED_INPUTS = ("go_unit", "go_integration", "dashboard", "static", "tooling")


def scope_blockers(scope: dict | None, functions: list) -> list[str]:
    """Inventory scope and input availability are separate from measured zero."""
    if scope is None:
        return ["Complete source scope metadata is unavailable."]
    if not isinstance(scope, dict):
        raise ValueError("scope must be an object")
    sources = scope.get("sources")
    inputs = scope.get("inputs")
    if not isinstance(sources, list) or not all(isinstance(p, str) for p in sources):
        raise ValueError("scope.sources must be a list of source paths")
    if not isinstance(inputs, dict):
        raise ValueError("scope.inputs must be an input availability map")
    blockers = []
    for name in REQUIRED_INPUTS:
        if inputs.get(name) is not True:
            blockers.append(f"Required measurement input unavailable: {name}.")
    if len(sources) != len(set(sources)):
        blockers.append("Source scope contains duplicate paths.")
    for path in sorted({function.path for function in functions} - set(sources)):
        blockers.append(f"Inventory function is outside declared source scope: {path}.")
    if not sources:
        blockers.append("Source scope is empty.")
    return blockers


def evaluate(functions: list, scope: dict | None) -> dict:
    """Apply absolute per-function policy; never infer unmeasured metric totals."""
    debt = []
    for function in functions:
        reasons = []
        if not function.measured:
            reasons.append("coverage unavailable")
        else:
            if function.coverage != 100:
                reasons.append("coverage below 100%")
            if function.crap >= 30:
                reasons.append("CRAP at least 30")
        if reasons:
            debt.append({"path": function.path, "namespace": function.namespace,
                         "name": function.name, "reasons": reasons})
    blockers = scope_blockers(scope, functions)
    identities = [function.identity for function in functions]
    if len(identities) != len(set(identities)):
        blockers.append("Function inventory contains ambiguous duplicate identities.")
    if not functions:
        blockers.append("Function inventory is empty.")
    # A function's Crapper percentage is not repository statement/branch coverage.
    # Infrastructure contracts also require evidence outside supported languages.
    unavailable = ["repository statement/line totals", "supported branch totals",
                   "infrastructure and unsupported-language contracts"]
    return {"mode": "report-only", "complete": False,
            "function_policy_satisfied": bool(functions) and not debt and not blockers,
            "functions": len(functions), "function_debt": debt,
            "scope_blockers": blockers, "unavailable_metrics": unavailable}


def markdown(report: dict) -> str:
    lines = ["## Complete coverage readiness", "",
             "Mode: **report-only**. Repository completion: **not established**.", "",
             f"Functions inventoried: {report['functions']}; "
             f"functions below the absolute policy: {len(report['function_debt'])}.",
             "Policy: every function measured at 100% coverage and CRAP strictly below 30.", ""]
    for blocker in report["scope_blockers"]:
        lines.append(f"- {blocker}")
    for metric in report["unavailable_metrics"]:
        lines.append(f"- Unavailable completion evidence: {metric}.")
    lines.extend(["", "See completion.json for each function's debt. "
                  "This report does not change the changed-function regression gate.", ""])
    return "\n".join(lines)


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--inventory", type=Path, default=Path(".metrics/inventory.csv"))
    parser.add_argument("--scope", type=Path, default=Path(".metrics/scope.json"))
    parser.add_argument("--output", type=Path, default=Path("target/coverage"))
    args = parser.parse_args(argv)
    scope = json.loads(args.scope.read_text()) if args.scope.exists() else None
    report = evaluate(read_inventory(args.inventory), scope)
    args.output.mkdir(parents=True, exist_ok=True)
    (args.output / "completion.json").write_text(json.dumps(report, indent=2) + "\n")
    (args.output / "completion.md").write_text(markdown(report))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
