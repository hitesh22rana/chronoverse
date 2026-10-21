#!/usr/bin/env python3
"""Compare function inventories for changed source ranges and render a PR report."""

from __future__ import annotations

import argparse
import ast
import csv
from dataclasses import dataclass, field
from decimal import Decimal, InvalidOperation
import html
import json
from pathlib import Path
import re
from collections import defaultdict

SCHEMA_VERSION = 1
TOLERANCE = Decimal("0.01")
HUNK = re.compile(r"^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@")
Identity = tuple[str, str, str]


@dataclass(frozen=True)
class Function:
    path: str
    namespace: str
    name: str
    start_line: int
    end_line: int
    complexity: int
    coverage: Decimal | None
    crap: Decimal | None
    measured: bool

    @property
    def identity(self) -> Identity:
        return self.path, self.namespace, self.name

    def metrics(self) -> dict:
        return {
            "complexity": self.complexity,
            "coverage": float(self.coverage) if self.coverage is not None else None,
            "crap": float(self.crap) if self.crap is not None else None,
            "coverage_recorded": self.measured,
        }


@dataclass
class ChangedFile:
    before_path: str | None = None
    after_path: str | None = None
    before: list[tuple[int, int]] = field(default_factory=list)
    after: list[tuple[int, int]] = field(default_factory=list)
    renamed: bool = False


def number(value: str, name: str) -> Decimal:
    try:
        result = Decimal(value)
    except InvalidOperation as error:
        raise ValueError(f"Invalid {name}: {value!r}") from error
    if not result.is_finite() or result < 0:
        raise ValueError(f"Invalid {name}: {value!r}")
    return result


def read_inventory(path: Path) -> list[Function]:
    required = {
        "path", "namespace", "name", "start_line", "end_line", "complexity",
        "coverage", "coverage_recorded", "crap",
    }
    result = []
    with path.open(encoding="utf-8", newline="") as handle:
        reader = csv.DictReader(handle)
        if not required.issubset(reader.fieldnames or []):
            raise ValueError(f"Incomplete inventory schema: {path}")
        for row in reader:
            if row["coverage_recorded"] not in ("yes", "no"):
                raise ValueError("coverage_recorded must be yes or no")
            measured = row["coverage_recorded"] == "yes"
            coverage = number(row["coverage"], "coverage") if measured else None
            crap = number(row["crap"], "CRAP") if measured else None
            if coverage is not None and coverage > 100:
                raise ValueError("Coverage must be between 0 and 100")
            start, end, complexity = (int(row[x]) for x in ("start_line", "end_line", "complexity"))
            if start < 1 or end < start or complexity < 1:
                raise ValueError("Invalid function range or complexity")
            if not measured and row["coverage"]:
                raise ValueError("Unmeasured inventory rows must not contain coverage")
            if not measured and row["crap"]:
                # Canonical Crapper invents a numeric score from 0% when coverage
                # is absent. Validate that field but do not compare it as measured.
                number(row["crap"], "provisional CRAP")
            result.append(Function(
                row["path"], row["namespace"], row["name"], start, end,
                complexity, coverage, crap, measured,
            ))
    return result


def git_path(value: str) -> str | None:
    """Decode Git's quoted filenames without interpreting them as shell code."""
    value = value.split("\t", 1)[0]
    if value.startswith('"'):
        # Git uses C string escapes, including octal UTF-8 and \a/\v.
        quoted = value
        value = ast.literal_eval(quoted)
        if not isinstance(value, str):
            raise ValueError("Git path is not a quoted string")
        if re.search(r"\\[0-7]{3}", quoted):
            try:
                value = value.encode("latin-1").decode("utf-8")
            except (UnicodeEncodeError, UnicodeDecodeError):
                pass
    if value == "/dev/null":
        return None
    return value[2:] if value.startswith(("a/", "b/")) else value


def changed_files(patch: str) -> list[ChangedFile]:
    result = []
    current = None
    in_hunk = False
    for line in patch.splitlines():
        if line.startswith("diff --git "):
            current = ChangedFile()
            in_hunk = False
            result.append(current)
        elif current is not None:
            if line.startswith("--- ") and not in_hunk:
                current.before_path = git_path(line[4:])
            elif line.startswith("+++ ") and not in_hunk:
                current.after_path = git_path(line[4:])
            elif line.startswith("rename from "):
                current.renamed = True
            elif match := HUNK.match(line):
                in_hunk = True
                old_start, old_count, new_start, new_count = match.groups()
                old_start, new_start = int(old_start), int(new_start)
                old_count = int(old_count) if old_count is not None else 1
                new_count = int(new_count) if new_count is not None else 1
                if old_count:
                    current.before.append((old_start, old_start + old_count - 1))
                if new_count:
                    current.after.append((new_start, new_start + new_count - 1))
    return result


def touched(function: Function, files: list[ChangedFile], side: str) -> bool:
    return any(
        function.path == getattr(item, side + "_path")
        and any(function.start_line <= end and function.end_line >= start
                for start, end in getattr(item, side))
        for item in files
    )


def index(functions: list[Function]) -> dict[Identity, list[Function]]:
    result = defaultdict(list)
    for function in functions:
        result[function.identity].append(function)
    return result


def totals(functions: list[Function]) -> dict:
    measured = sum(function.measured for function in functions)
    return {
        "functions": len(functions), "measured": measured,
        "missing": len(functions) - measured,
        "crap_ge_30": sum(function.crap is not None and function.crap >= 30 for function in functions),
    }


def compare(head: list[Function], base: list[Function] | None, patch: str,
            base_sha: str = "", head_sha: str = "", tested_sha: str = "",
            baseline_reason: str = "") -> dict:
    changes = changed_files(patch)
    old = index(base or [])
    new = index(head)
    selected = {f.identity for f in head if touched(f, changes, "after")}
    selected.update(f.identity for f in base or [] if touched(f, changes, "before"))
    rows = []
    for key in sorted(selected):
        before, after = old.get(key, []), new.get(key, [])
        row = {
            "path": key[0], "namespace": key[1], "name": key[2],
            "status": "changed", "before": None, "after": None,
            "reasons": [], "note": "",
        }
        if len(before) > 1 or len(after) > 1:
            row.update(status="uncomparable", note="Duplicate identity; no line/order pairing attempted.")
        elif any(c.renamed and key[0] in (c.before_path, c.after_path) for c in changes):
            row.update(status="uncomparable", note="File renamed; strict path identity cannot establish a pair.")
            row["before"] = before[0].metrics() if before else None
            row["after"] = after[0].metrics() if after else None
        else:
            previous = before[0] if before else None
            current = after[0] if after else None
            row["before"] = previous.metrics() if previous else None
            row["after"] = current.metrics() if current else None
            if base is None:
                row.update(status="baseline-unavailable", note="No verified base inventory; comparison and regression gate unavailable.")
            elif current is None:
                row["status"] = "removed"
            elif previous is None:
                row["status"] = "added"
                if not current.measured:
                    row["reasons"].append("New function has no coverage record.")
                elif current.crap >= 30:
                    row["reasons"].append("New function has CRAP >= 30.")
            else:
                if previous.measured and not current.measured:
                    row["reasons"].append("Previously measured function lost its coverage record.")
                elif previous.measured and current.measured:
                    if previous.coverage - current.coverage > TOLERANCE:
                        row["reasons"].append("Coverage decreased by more than 0.01 percentage points.")
                    if current.crap - previous.crap > TOLERANCE:
                        if current.crap >= 30:
                            row["reasons"].append("CRAP increased by more than 0.01 and is now >= 30.")
                        else:
                            row["note"] = "CRAP increased below 30; informational only."
                elif not current.measured:
                    row["note"] = "Function remains unmeasured; no numeric comparison is possible."
                else:
                    row["note"] = "Coverage measurement added; no baseline numeric comparison is possible."
        rows.append(row)
    regressions = sum(bool(row["reasons"]) for row in rows)
    uncomparable = sum(row["status"] == "uncomparable" for row in rows)
    gate = ("comparison-unavailable" if base is None else
            "failed" if regressions else "incomplete" if uncomparable else "passed")
    return {
        "schema_version": SCHEMA_VERSION,
        "provenance": {"base_sha": base_sha, "head_sha": head_sha, "tested_sha": tested_sha},
        "comparison": "available" if base is not None else "unavailable",
        "baseline_reason": baseline_reason if base is None else "",
        "gate": gate,
        "totals": {"base": totals(base) if base is not None else None, "head": totals(head)},
        "rows": rows, "regressions": regressions, "uncomparable": uncomparable,
    }


def escape(value: object) -> str:
    text = str(value).replace("\r", " ").replace("\n", " ")
    text = text[:240] + ("…" if len(text) > 240 else "")
    return html.escape(text, quote=False).replace("\\", "\\\\").replace("`", "\\`").replace("|", "\\|").replace("@", "&#64;")


def metric(value: dict | None, field: str, percent: bool = False) -> str:
    if value is None:
        return "—"
    if value[field] is None:
        return "unmeasured"
    return f"{value[field]:.2f}" + ("%" if percent else "") if field != "complexity" else str(value[field])


def markdown(report: dict, limit: int = 50) -> str:
    provenance = report["provenance"]
    lines = [
        "## Coverage and CRAP", "",
        f"Base: {escape(provenance['base_sha']) or 'unavailable'} · "
        f"PR head: {escape(provenance['head_sha'])} · Tested revision: {escape(provenance['tested_sha'])}",
        "", f"Comparison: **{escape(report['gate'])}**. "
        f"Regressions: **{report['regressions']}**; uncomparable identities: **{report['uncomparable']}**.", "",
    ]
    if report["comparison"] == "unavailable":
        lines += ["No verified inventory exists for the exact base revision. This is a report-only run; "
                  "it does not establish that coverage or CRAP did not regress.", ""]
        if report.get("baseline_reason"):
            lines += ["Baseline unavailable: " + escape(report["baseline_reason"]), ""]
    if report["uncomparable"] and report["comparison"] != "unavailable":
        lines += ["The check fails because changed-function identities could not be compared reliably. "
                  "Resolve renamed or duplicate identities before merging.", ""]
    lines += ["| Inventory metric | Base | Tested |", "|---|---:|---:|"]
    for key, label in (("functions", "Functions"), ("measured", "Measured functions"),
                       ("missing", "Functions without coverage records"), ("crap_ge_30", "Measured functions with CRAP ≥ 30")):
        base = report["totals"]["base"]
        lines.append(f"| {label} | {base[key] if base is not None else 'unavailable'} | {report['totals']['head'][key]} |")
    lines += ["", "Function coverage is Crapper's per-function metric. Inventory counts are not "
              "overall statement or branch coverage. The gate rejects coverage losses over 0.01 percentage points, "
              "lost measurements, and CRAP increases over 0.01 when the resulting score is at least 30. "
              "New functions need a coverage record and CRAP below 30. Existing high CRAP alone and "
              "increases remaining below 30 do not fail the gate.", "",
              "### Changed functions", ""]
    if not report["rows"]:
        lines += ["No changed application functions were identified in the diff.", ""]
    else:
        lines += ["| Function | Status | CC before | CC after | Coverage before | Coverage after | CRAP before | CRAP after | Result |",
                  "|---|---|---:|---:|---:|---:|---:|---:|---|"]
        for row in report["rows"][:limit]:
            identity = " :: ".join((row["path"], row["namespace"], row["name"]))
            before, after = row["before"], row["after"]
            result = " ".join(row["reasons"]) or row["note"] or "No regression."
            lines.append("| " + " | ".join((escape(identity), escape(row["status"]),
                metric(before, "complexity"), metric(after, "complexity"),
                metric(before, "coverage", True), metric(after, "coverage", True),
                metric(before, "crap"), metric(after, "crap"), escape(result))) + " |")
        if len(report["rows"]) > limit:
            lines += ["", f"Showing {limit} of {len(report['rows'])} changed-function identities; "
                      "the complete results remain in the report artifact."]
    return "\n".join(lines).rstrip() + "\n"


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--head", type=Path, required=True)
    parser.add_argument("--base", type=Path)
    parser.add_argument("--diff", type=Path, required=True)
    parser.add_argument("--summary", type=Path, required=True)
    parser.add_argument("--result", type=Path, required=True)
    parser.add_argument("--base-sha", default="")
    parser.add_argument("--baseline-reason", default="")
    parser.add_argument("--head-sha", required=True)
    parser.add_argument("--tested-sha", required=True)
    args = parser.parse_args(argv)
    report = compare(read_inventory(args.head), read_inventory(args.base) if args.base else None,
                     args.diff.read_text(encoding="utf-8"), args.base_sha, args.head_sha, args.tested_sha,
                     args.baseline_reason)
    for path in (args.summary, args.result):
        path.parent.mkdir(parents=True, exist_ok=True)
    args.summary.write_text(markdown(report), encoding="utf-8")
    args.result.write_text(json.dumps(report, indent=2, allow_nan=False) + "\n", encoding="utf-8")
    return 1 if report["gate"] in {"failed", "incomplete"} else 0


if __name__ == "__main__":
    raise SystemExit(main())
