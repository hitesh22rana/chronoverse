#!/usr/bin/env python3
"""Merge the CI coverage profiles, then score the handwritten sources with crapper.

The test jobs already run the suites that gate the build; COVERPROFILE only adds a
profile to those runs. crapper reads fixed report locations (GO_PROFILE,
DASHBOARD_LCOV), so this merges the Go profiles, roots the dashboard LCOV at the
repository, and scores the tracked sources a person wrote.

crapper decides complexity, coverage, and CRAP; kept here is only whether a function
had a coverage record at all, since crapper substitutes 0% for a function no report
mentions, hiding "measured zero" from "never measured".

Pinned tool: https://github.com/unclebob/crapper at 9f1bead298b5a9d576bdd6319289fcf426e5b18a.
Invocation and caveats: docs/coverage.md.
"""

from __future__ import annotations

import argparse
import csv
import re
import subprocess
import sys
from pathlib import Path

from crapper.coverage import load_bundle, normalize_path
from crapper.crap import make_entry, sort_entries
from crapper.discover import SKIP_DIRS, is_test_file, language_of
from crapper.languages import functions_in_file
from crapper.metrics import render_edn
from crapper.report import format_report

CRAPPER_REVISION = "9f1bead298b5a9d576bdd6319289fcf426e5b18a"

# Where crapper looks for each report: its own native layout, so
# `--use-existing-coverage` finds them with no extra flags.
GO_PROFILE = Path("target/coverage/go/coverage.out")
DASHBOARD_LCOV = Path("target/coverage/typescript/dashboard/lcov.info")

# The reports crapper's loader reads, as the globs it scans. Only these are cleared:
# CI stages its inputs under INPUT_DIR, which a wholesale delete would destroy.
STALE_PROFILES = (
    "target/coverage/go/coverage.out",
    "target/coverage/coverage.out",
    "coverage.out",
    "*/target/coverage/go/coverage.out",
    "*/*/target/coverage/go/coverage.out",
)
STALE_LCOV = ("target/coverage/**/lcov.info", "coverage/**/lcov.info")

# CI stages the per-job reports here, outside every rule above.
INPUT_DIR = Path("target/coverage-inputs")

# `go test -covermode` accepts exactly these three.
COVER_MODES = ("set", "count", "atomic")

# Go's generated-code convention (https://go.dev/s/generatedcode), which protoc-gen-go
# and MockGen emit verbatim in the block above the package clause.
GENERATED_MARKER = re.compile(r"^// Code generated .* DO NOT EDIT\.$")
# The project's declared generated-code location, kept as a second net.
GENERATED_TREES = ("pkg/proto/",)

# Enough for that leading comment block, so no source is read in full.
HEADER_BYTES = 512

INVENTORY_FIELDS = (
    "path",
    "start_line",
    "end_line",
    "language",
    "namespace",
    "name",
    "complexity",
    "coverage",
    "coverage_recorded",
    "crap",
)


class CoverageError(Exception):
    """A report is missing, malformed, or incompatible with another report."""


def _add_block(
    blocks: dict[str, tuple[int, int]],
    location: str,
    statements: int,
    hits: int,
    source: str,
) -> None:
    """Add one profile block, refusing one that contradicts itself.

    A block's statement count is fixed by the instrumented source, so a mismatch, in
    one profile or across two, means different builds; summing them would report
    coverage for code neither build ran.
    """

    known = blocks.get(location)
    if known is None:
        blocks[location] = (statements, hits)
        return
    if known[0] != statements:
        raise CoverageError(
            f"{source}: {location} has {statements} statements but the same block already "
            f"has {known[0]}. These profiles come from different builds."
        )
    blocks[location] = (known[0], known[1] + hits)


def _read_go_profile(path: Path) -> tuple[str, dict[str, tuple[int, int]]]:
    """Return the covermode and `{block location: (statements, hits)}`."""

    mode: str | None = None
    blocks: dict[str, tuple[int, int]] = {}
    for number, raw in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        line = raw.strip()
        if not line:
            continue
        if line.startswith("mode:"):
            if mode is not None:
                raise CoverageError(f"{path}:{number}: second mode line {line!r}")
            mode = line[len("mode:") :].strip()
            if mode not in COVER_MODES:
                raise CoverageError(
                    f"{path}:{number}: covermode {mode!r} is not one of "
                    f"{', '.join(COVER_MODES)}; this is not a Go coverprofile"
                )
            continue
        fields = line.split()
        if len(fields) != 3:
            raise CoverageError(
                f"{path}:{number}: want 'file:range statements hits', got {raw!r}"
            )
        location, statements, hits = fields
        if ":" not in location or "," not in location:
            raise CoverageError(f"{path}:{number}: malformed block range {location!r}")
        try:
            counts = (int(statements), int(hits))
        except ValueError as error:
            raise CoverageError(f"{path}:{number}: {error}") from error
        if counts[0] < 0 or counts[1] < 0:
            raise CoverageError(f"{path}:{number}: negative count in {raw!r}")
        _add_block(blocks, location, counts[0], counts[1], f"{path}:{number}")
    if mode is None:
        raise CoverageError(f"{path}: no 'mode:' header, so this is not a Go coverprofile")
    return mode, blocks


def module_paths(root: Path, tracked: list[str]) -> tuple[str, ...]:
    """The import path every tracked go.mod declares.

    A go.mod missing from the work tree declares nothing here; `handwritten_sources`
    reports it as a tracked-but-missing file.
    """

    declared: list[str] = []
    for relative in sorted(tracked):
        path = root / relative
        if Path(relative).name != "go.mod" or not path.is_file():
            continue
        for line in path.read_text(encoding="utf-8").splitlines():
            text = line.strip()
            if text.startswith("module "):
                declared.append(text.split(None, 1)[1].strip())
                break
    return tuple(declared)


def unknown_block_files(blocks: dict[str, tuple[int, int]], modules: tuple[str, ...]) -> list[str]:
    """The block file paths that no declared module can contain.

    Every block a `go test ./... -coverprofile` run writes names a file inside a
    module of this work tree.
    """

    unknown: set[str] = set()
    for location in blocks:
        part = location.split(":", 1)[0]
        if not any(part == module or part.startswith(f"{module}/") for module in modules):
            unknown.add(part)
    return sorted(unknown)


def merge_go_profiles(
    inputs: list[Path], output: Path, modules: tuple[str, ...]
) -> dict[str, int]:
    """Sum the hit counts of compatible profiles into one coverprofile.

    Compatible means the same covermode, the same block with the same statement
    count, and a file path some go.mod declares. Hits are added, so a block run by
    both suites counts twice; CRAP only asks whether the count is above zero.
    Returns the merged block and file counts.
    """

    mode: str | None = None
    blocks: dict[str, tuple[int, int]] = {}
    for path in inputs:
        if not path.is_file():
            raise CoverageError(f"Go coverprofile not found: {path}")
        found_mode, found = _read_go_profile(path)
        if mode is None:
            mode = found_mode
        elif found_mode != mode:
            raise CoverageError(
                f"{path}: covermode {found_mode!r} but an earlier profile used {mode!r}. "
                "Both jobs must pass the same -covermode, which the Makefile fixes to atomic."
            )
        for location, (statements, hits) in found.items():
            _add_block(blocks, location, statements, hits, str(path))
    if mode is None:
        raise CoverageError("no Go coverprofile given")

    unknown = unknown_block_files(blocks, modules)
    if unknown:
        shown = ", ".join(unknown[:5])
        more = f" (and {len(unknown) - 5} more)" if len(unknown) > 5 else ""
        raise CoverageError(
            f"no go.mod declares these block file paths: {shown}{more}. Two go test runs "
            "sharing one -coverprofile path interleave and splice a path's prefix; the "
            "damaged line still parses, so it would land as a block no source matches "
            "and leave the intact block with only one run's hits. Give each run its own "
            "output file."
        )

    lines = [f"mode: {mode}"]
    for location, (statements, hits) in sorted(blocks.items()):
        lines.append(f"{location} {statements} {hits}")
    write_report(output, "\n".join(lines) + "\n")
    files = {location.split(":", 1)[0] for location in blocks}
    return {"blocks": len(blocks), "files": len(files)}


def root_lcov_path(value: str, prefix: str) -> str:
    """One `SF:` value, made relative to the repository root.

    Vitest writes `SF:src/...` relative to `dashboard/`, so the module directory is
    prepended. An already-prefixed or absolute value is left alone: prefixing it names
    a path that exists nowhere, and a non-matching absolute one reports no record.
    """

    path = normalize_path(value)
    if path.startswith(f"{prefix}/") or path.startswith("/"):
        return path
    return f"{prefix}/{path}"


def prefix_lcov_paths(text: str, prefix: str) -> str:
    """Root every `SF:` path in an LCOV report at the repository.

    Otherwise `SF:src/lib/utils.ts` from the dashboard is indistinguishable from the
    static site's own `src/` when crapper runs from the root. Paths pass through
    crapper's own `normalize_path`.
    """

    prefix = prefix.strip("/")
    lines: list[str] = []
    records = 0
    open_record = False
    for raw in text.splitlines():
        line = raw.strip()
        if line.startswith("SF:"):
            records += 1
            open_record = True
            lines.append(f"SF:{root_lcov_path(line[len('SF:') :], prefix)}")
            continue
        lines.append(raw)
        if line == "end_of_record":
            open_record = False
    if not records:
        raise CoverageError("LCOV report has no SF: records; was the coverage reporter enabled?")
    if open_record:
        raise CoverageError("LCOV report ends inside a record; the file is truncated")
    return "\n".join(lines) + "\n"


def prefix_lcov_report(source: Path, output: Path, prefix: str) -> Path:
    """Copy an LCOV report to `output` with `SF:` paths rooted at the repository."""

    if not source.is_file():
        raise CoverageError(f"LCOV report not found: {source}")
    write_report(output, prefix_lcov_paths(source.read_text(encoding="utf-8"), prefix))
    return output


def is_generated(header: str) -> bool:
    """True when the file opens with Go's generated-code marker.

    Only the comment block above the first line of code counts, so a hand-written
    mention further down does not remove the file from scope.
    """

    for line in header.splitlines():
        text = line.strip()
        if text and not text.startswith("//"):
            return False
        if GENERATED_MARKER.match(text):
            return True
    return False


def is_output_path(relative: str) -> bool:
    """True for build output, dependency trees, and report directories.

    crapper's own skip list, so the scope stays the set its walk would reach.
    """

    return any(part in SKIP_DIRS for part in Path(relative).parts)


def handwritten_sources(root: Path, paths: list[str]) -> list[Path]:
    """The tracked sources crapper can score and a person wrote.

    `git ls-files` starts the scope; crapper's extension, skip-directory and test-name
    rules drop what it cannot usefully score, and the generated marker drops protoc-gen-go
    and MockGen output whose complexity would drown the report.
    """

    found: list[Path] = []
    for relative in sorted(paths):
        if language_of(relative) is None or is_test_file(relative) or is_output_path(relative):
            continue
        text = Path(relative).as_posix()
        if any(text.startswith(tree) for tree in GENERATED_TREES):
            continue
        path = root / relative
        if not path.is_file():
            raise CoverageError(
                f"{relative} is tracked but missing from the work tree. Restore it, or stage "
                "the deletion, before scoring: git ls-files still lists it."
            )
        with path.open("rb") as handle:
            header = handle.read(HEADER_BYTES)
        if is_generated(header.decode("utf-8", errors="replace")):
            continue
        found.append((root / relative).resolve())
    return found


def tracked_files(root: Path) -> list[str]:
    """Paths git tracks, or a clear error when this is not a work tree."""

    try:
        result = subprocess.run(
            ["git", "-C", str(root), "ls-files", "-z"],
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
    except OSError as error:
        raise CoverageError(f"git is required to list the tracked sources: {error}") from error
    if result.returncode != 0:
        raise CoverageError(
            f"git ls-files failed in {root}: {result.stderr.strip() or result.returncode}"
        )
    return [entry for entry in result.stdout.split("\0") if entry]


def write_report(path: Path, text: str) -> Path:
    """Replace a report, so a failed write never leaves an older one behind."""

    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")
    return path


def stale_reports(root: Path) -> list[Path]:
    """The Go and LCOV reports crapper's loader reads and this run would clear."""

    found: list[Path] = []
    for pattern in STALE_PROFILES + STALE_LCOV:
        found.extend(sorted(root.glob(pattern)))
    return [path for path in found if path.is_file()]


def clear_stale_reports(root: Path) -> list[str]:
    """Remove the reports crapper reads, and only those.

    Covers the globs its loader scans, so a report from an earlier run cannot leak in.
    Inputs under `INPUT_DIR` are left alone. Returns the removed relative paths.
    """

    removed = []
    for target in stale_reports(root):
        removed.append(target.relative_to(root).as_posix())
        target.unlink()
    return sorted(removed)


def measure(files: list[Path], root: Path, bundle) -> tuple[list, list[dict]]:
    """Score every function, keeping crapper's no-record case distinguishable.

    crapper's `analyze_files` turns a missing record into 0%: the right input to the
    formula and the snapshot, so entries match `crapper --use-existing-coverage` while
    the inventory separately records whether a record matched at all.
    """

    relative = [file.relative_to(root).as_posix() for file in files]
    bundle.bind_sources(relative)
    entries = []
    rows = []
    for file, name in zip(files, relative):
        language = language_of(file)
        source = file.read_text(encoding="utf-8", errors="replace")
        for function in functions_in_file(language, source, name, str(root)):
            recorded = bundle.percent_for(function)
            entry = make_entry(function, 0.0 if recorded is None else recorded)
            entries.append(entry)
            rows.append(
                {
                    "path": name,
                    "start_line": function.start_line,
                    "end_line": function.end_line,
                    "language": function.language,
                    "namespace": function.namespace,
                    "name": function.name,
                    "complexity": function.complexity,
                    "coverage": "" if recorded is None else f"{recorded:.4f}",
                    "coverage_recorded": "yes" if recorded is not None else "no",
                    "crap": "" if entry.crap is None else f"{entry.crap:.4f}",
                }
            )
    rows.sort(key=lambda row: (row["path"], row["start_line"], row["name"]))
    return sort_entries(entries), rows


def write_inventory(path: Path, rows: list[dict]) -> Path:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", encoding="utf-8", newline="") as handle:
        writer = csv.DictWriter(handle, fieldnames=INVENTORY_FIELDS)
        writer.writeheader()
        writer.writerows(rows)
    return path


def language_file_counts(files: list[Path], root: Path) -> dict[str, int]:
    """How many scoped files each language contributes."""

    counts: dict[str, int] = {}
    for file in files:
        language = language_of(file.relative_to(root))
        counts[language] = counts.get(language, 0) + 1
    return counts


def function_language_counts(rows: list[dict]) -> dict[str, int]:
    """How many scored functions each language contributes."""

    counts: dict[str, int] = {}
    for row in rows:
        counts[row["language"]] = counts.get(row["language"], 0) + 1
    return counts


def breakdown(counts: dict[str, int]) -> str:
    return ", ".join(f"{name}={count}" for name, count in sorted(counts.items()))


def summarise(rows: list[dict], files: list[Path], root: Path) -> str:
    """Counts that keep the two kinds of zero apart."""

    measured = sum(row["coverage_recorded"] == "yes" for row in rows)
    zero = sum(
        row["coverage_recorded"] == "yes" and float(row["coverage"]) == 0.0 for row in rows
    )
    file_areas = breakdown(language_file_counts(files, root))
    function_areas = breakdown(function_language_counts(rows))
    return "\n".join(
        [
            "",
            "Scope",
            f"  files:      {len(files)} ({file_areas or 'no supported sources'})",
            f"  functions:  {len(rows)} ({function_areas or 'no supported sources'})",
            f"  recorded:   {measured} functions matched a coverage record",
            f"  no record:  {len(rows) - measured} functions no report mentions",
            f"  zero:       {zero} of the matched functions measured 0%",
            f"  CRAP >= 30: {sum(bool(row['crap']) and float(row['crap']) >= 30 for row in rows)}",
            f"  CC > 10:    {sum(int(row['complexity']) > 10 for row in rows)}",
        ]
    )


def display(path: Path, root: Path) -> str:
    """A repository-relative path when possible, absolute otherwise.

    `--metrics-dir` may sit outside the work tree, where a relative path would not help.
    """

    resolved = path.resolve()
    try:
        return resolved.relative_to(root).as_posix()
    except ValueError:
        return str(resolved)


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Merge coverage profiles and score the handwritten sources with crapper.",
    )
    parser.add_argument("--repo-root", default=".", type=Path, help="repository root (default: .)")
    parser.add_argument(
        "--go-unit", type=Path, help="coverprofile from `make test/short COVERPROFILE=...`"
    )
    parser.add_argument(
        "--go-integration",
        type=Path,
        help="coverprofile from `make test/integration COVERPROFILE=...`",
    )
    parser.add_argument(
        "--dashboard-lcov",
        type=Path,
        help="lcov.info from the dashboard `npm run test:coverage`",
    )
    parser.add_argument(
        "--dashboard-prefix",
        default="dashboard",
        help="module directory the dashboard LCOV paths are relative to (default: dashboard)",
    )
    parser.add_argument(
        "--metrics-dir",
        default=Path(".metrics"),
        type=Path,
        help=(
            "where crap.edn, crap-report.txt, and inventory.csv are written "
            "(default: .metrics, the location crapper itself uses)"
        ),
    )
    return parser.parse_args(argv)


def check_inputs_survive_clearing(root: Path, inputs: list[Path | None]) -> None:
    """Refuse an input this run would delete before clearing anything."""

    stale = {path.resolve() for path in stale_reports(root)}
    for path in inputs:
        if path is None:
            continue
        candidate = path if path.is_absolute() else root / path
        if candidate.resolve() in stale:
            raise CoverageError(
                f"{path} is a report this run clears, so clearing it would destroy an "
                f"input. Stage the per-job reports outside those trees, e.g. {INPUT_DIR}."
            )


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    root = args.repo_root.resolve()
    metrics = args.metrics_dir
    if not metrics.is_absolute():
        metrics = root / metrics

    check_inputs_survive_clearing(root, [args.go_unit, args.go_integration, args.dashboard_lcov])
    removed = clear_stale_reports(root)
    if removed:
        print(f"Removed stale reports: {', '.join(removed)}")

    tracked = tracked_files(root)
    if args.go_unit or args.go_integration:
        profiles = [path for path in (args.go_unit, args.go_integration) if path]
        merged = merge_go_profiles(profiles, root / GO_PROFILE, module_paths(root, tracked))
        print(
            f"Merged {len(profiles)} Go profile(s) into {GO_PROFILE} "
            f"({merged['blocks']} blocks across {merged['files']} files)"
        )
    if args.dashboard_lcov:
        prefix_lcov_report(args.dashboard_lcov, root / DASHBOARD_LCOV, args.dashboard_prefix)
        print(f"Wrote {DASHBOARD_LCOV} with SF: paths rooted at {args.dashboard_prefix}/")

    files = handwritten_sources(root, tracked)
    bundle = load_bundle(root)
    entries, rows = measure(files, root, bundle)

    # crapper's own EDN renderer, so the bytes are what `crapper
    # --use-existing-coverage` writes; only the destination follows --metrics-dir.
    snapshot = write_report(metrics / "crap.edn", render_edn(entries))
    report = write_report(metrics / "crap-report.txt", format_report(entries))
    inventory = write_inventory(metrics / "inventory.csv", rows)

    print(format_report(entries), end="")
    print(summarise(rows, files, root))
    print(f"\nWrote {display(snapshot, root)} (crapper {CRAPPER_REVISION})")
    print(f"Wrote {display(report, root)}")
    print(f"Wrote {display(inventory, root)}")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except CoverageError as error:
        print(f"error: {error}", file=sys.stderr)
        sys.exit(2)
