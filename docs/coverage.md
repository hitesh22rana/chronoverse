# Coverage and CRAP reporting

This describes how coverage reports become CRAP scores and pull-request feedback.
CI reports existing debt and checks changed functions for regressions. The scope is
handwritten application and repository tooling code; unsupported formats remain explicitly unscored.

The sources of truth for this pipeline are [`.github/workflows/ci.yaml`](../.github/workflows/ci.yaml),
[`scripts/coverage/crap_report.py`](../scripts/coverage/crap_report.py),
[`scripts/coverage/pr_report.py`](../scripts/coverage/pr_report.py),
[`scripts/coverage/ci_report.py`](../scripts/coverage/ci_report.py), and the `Makefile`
coverage targets. Where this page and those files disagree, those files are correct. For how to
read the output and decide what to do about it, see
[crapper-assessment.md](crapper-assessment.md).

## What runs where

| Job | Command | Uploads |
| --- | --- | --- |
| `test` | `make test/short COVERPROFILE=target/coverage-inputs/unit/coverage.out` | `coverage-unit` |
| `integration` | `make test/integration COVERPROFILE=target/coverage-inputs/integration/coverage.out` | `coverage-integration` |
| `dashboard` | `cd dashboard && npm run test:coverage` | `coverage-dashboard` |
| `static-site` | `cd static && npm run test:coverage` when that script exists | `coverage-static` |
| `coverage` | `make coverage/tooling`, then `python scripts/coverage/crap_report.py ...` | `crap-report`, including raw Python LCOV/JSON |

Coverage rides along with the test runs that already gate the build. The `COVERPROFILE`
variable adds `-covermode=atomic -coverprofile=... -coverpkg=<application packages>` to the
same `go test` invocation; there is no second run of any suite. `npm run test:coverage` is
`vitest run --coverage` with the JSON, LCOV, and text-summary reporters, so it is the
existing `npm test` gate plus the LCOV report. The `coverage` job needs `test`,
`integration`, and `dashboard`; it runs the script's own tests, downloads their three
artifacts, and scores the profiles without a Go or Node toolchain.

The integration launcher gives each package binary its own Testcontainers/Ryuk
session before the binary starts, so parallel packages do not reuse the same
reaper. Local runs default to one package at a time; CI uses four. Override
`INTEGRATION_PARALLELISM` to select a bound suitable for the available Docker
CPU and memory. Python is required for the launcher. Every selected package still
runs with race detection and fresh execution; unit package parallelism is unchanged.

Both Go jobs must use the same `-covermode`: the Makefile defaults `COVERMODE` to `atomic`
because the race detector needs atomic counters, and summing atomic hit counts is what the
merge step defines. Overriding it is possible, but the merge refuses profiles that disagree,
and any two jobs must pass the same value.

## Local commands

Everything runs against profiles already on disk; nothing here reruns a suite unless you ask
it to. Integration coverage needs a running Docker daemon.

```sh
# 1. Unit coverage.
make test/short COVERPROFILE=target/coverage-inputs/unit/coverage.out

# 2. Integration coverage.
make test/integration COVERPROFILE=target/coverage-inputs/integration/coverage.out

# 3. Dashboard coverage.
cd dashboard && npm ci && npm run test:coverage && cd ..

# 4. Tooling coverage (coverage.py is pinned in CI).
make coverage/tooling PYTHON=/path/to/venv/bin/python

# 5. Run static test:coverage when installed; absent input is reported unmeasured.
# 6. Merge, root, and score, using an interpreter that has the pinned crapper:
#    make coverage/crap PYTHON=/path/to/venv/bin/python
```

### Installing the pinned crapper

`crapper` is not vendored, and its revision is part of the reproducibility contract. Read the
revision out of the reporting script instead of copying it into a command, so the install
follows a deliberate bump rather than drifting from it. The `sed` below extracts the 40-character
commit hash from `CRAPPER_REVISION` and passes it as a pip argument rather than evaluating it;
if the constant is renamed or reformatted, `sed` prints nothing and the install fails on an
empty revision rather than silently pinning something else. It needs `git` on `PATH` for the
`git+https` URL, and the `venv` module (`python3-venv` on Debian) to create the environment.
The environment is created outside the work tree, because `.venv` is not in `.gitignore` and an
untracked dependency tree would otherwise show up in `git status`:

```sh
CRAPPER_VENV="${HOME}/.venvs/chronoverse-crapper"
python3 -m venv "$CRAPPER_VENV"
"$CRAPPER_VENV/bin/python" -m pip install \
  "crapper @ git+https://github.com/unclebob/crapper@$(sed -n 's/^CRAPPER_REVISION = "\([0-9a-f]\{40\}\)"$/\1/p' scripts/coverage/crap_report.py)"

make coverage/crap PYTHON="$CRAPPER_VENV/bin/python"
```

If you would rather keep the environment in the work tree, add `.venv` to `.gitignore` first.

Expect the scores to move when the revision is bumped, and bump it deliberately. The revision
appears in two places, this script's `CRAPPER_REVISION` and the install step in
`.github/workflows/ci.yaml`. Nothing asserts that they match, so change both in one commit;
otherwise local runs and CI silently score different code.

## What the report writes

`make coverage/crap` writes three files and prints the worst scores first:

| Path | Contents |
| --- | --- |
| `.metrics/crap-report.txt` | crapper's own table, worst CRAP first |
| `.metrics/crap.edn` | crapper's snapshot in the shape `uml-viewer` reads |
| `.metrics/inventory.csv` | one row per function, with source lines and whether a coverage record matched |
| `.metrics/scope.json` | scored source list, all other tracked files, missing measurements and supplied suite inputs |

The inventory has a `coverage_recorded` column with `yes` or `no`, and its stdout summary
separates the functions that matched a record from the functions no report mentions.
crapper substitutes 0% for a function no report mentions, which is the right input to the
formula but hides the difference between "measured 0%" and "never measured". The column and
the summary keep them apart.

Intermediate reports land where crapper looks for them by design, so the native
`crapper --use-existing-coverage` command reads the same files. Its default source walk
includes the static site and generated code; pass the application source list when comparing
its scores with this pipeline:

```
target/coverage/go/coverage.out                      merged Go profile
target/coverage/typescript/dashboard/lcov.info        dashboard LCOV, rooted at the repository
target/coverage-inputs/{unit,integration}/           the raw per-job profiles
```

To run the native tool with the same source scope, copy the pipeline snapshot aside
first, because the native run overwrites it:

```sh
cp .metrics/crap.edn .metrics/application-crap.edn
"$CRAPPER_VENV/bin/python" - <<'PYTHON'
from pathlib import Path
import subprocess
import sys
sys.path.insert(0, "scripts/coverage")
from crap_report import handwritten_sources, tracked_files
root = Path.cwd()
files = handwritten_sources(root, tracked_files(root))
crapper = str(Path(sys.executable).parent / "crapper")
subprocess.run([crapper, "--use-existing-coverage", *map(str, files)], check=True)
PYTHON
```

`make coverage/crap` is what puts the pipeline snapshot back in `.metrics/crap.edn`, so
run it again before repeating the comparison. The native tool rewrites only
`.metrics/crap.edn`; the inventory and text report remain pipeline outputs. Compare
function identities and values, rather than snapshot ordering.

`target/coverage` and `.metrics` are gitignored. Do not commit them.

## Merging the Go profiles

`scripts/coverage/crap_report.py` refuses to merge profiles it cannot trust:

- every profile must carry a `mode:` header naming one of `set`, `count`, `atomic`, and
  every profile in one merge must agree on it;
- a block described twice must agree on its statement count, inside one profile as much as
  across two. Disagreement means the profiles came from different builds, and summing them
  would report coverage for code neither build ran;
- every block must name a file inside a module some `go.mod` in the work tree declares. Two
  `go test` runs pointed at the same `-coverprofile` path write that file at once, and their
  blocks interleave: a spliced path still has three fields, so it parses, becomes a block no
  source matches, and leaves the intact block holding only one run's hits. That under-reports
  coverage with nothing to notice it, so it is refused instead. Give each run its own output
  file;
- hit counts are added across blocks, because CRAP only asks whether a block was entered at
  all;
- the merged report and every LCOV crapper would read are deleted first, so a profile left by
  an earlier run cannot be merged in by accident. Inputs are resolved against `--repo-root`
  rather than the working directory, and both an input this run would clear and one that does
  not exist are refused *before* anything is deleted, so a mistyped path cannot cost you the
  last report. The per-job inputs are staged under `target/coverage-inputs`, outside
  everything that is cleared.

Run the script's own tests with the same interpreter that has `crapper` installed:

```sh
"$CRAPPER_VENV/bin/python" -m unittest discover -s scripts/coverage -t scripts/coverage
```

An interpreter without `crapper` installed fails to import it, since the script imports it
at module level.

## Dashboard coverage scope

Vitest reports only the files a test imported unless `coverage.include` names them.
`dashboard/vitest.config.ts` names `src/**/*.{ts,tsx}` explicitly and excludes only test
files, type declarations, and `__tests__` directories, so every application source under
`dashboard/src` is reported at its real coverage. Naming the sources raises the denominator
to the whole application instead of the subset a test happened to import, so the headline
percentage can fall with no line of behaviour changing. Compare a percentage only against
another percentage measured over the same set of sources; a scope change and a behaviour
regression look identical from the number alone.

## What the score covers, and what it does not

Scope is the tracked handwritten sources crapper can parse, including the static site and
Python tooling, minus tests,
and generated code: files carrying Go's generated-code marker (`// Code generated ... DO NOT
EDIT.`) and everything under `pkg/proto/` are left out. crapper's own skip list removes
build output, dependency trees, and report directories. The source directories
`scripts/coverage/` and `scripts/test/` are explicitly distinguished from generated
coverage output and test suites; test filenames and nested test/output directories remain excluded.
`git ls-files` is the starting
set, so untracked output never enters. The snapshot and the inventory are built from that
one list, so they always describe the same scope.

Limits worth stating plainly:

- CRAP is `CC² × (1 − coverage)³ + CC`. It combines one complexity number with one coverage
  number and says nothing else. It is a ranking aid, not a quality grade, and the two inputs
  are not comparable across languages.
- Go coverage is statement coverage from `go test -coverprofile`. TypeScript coverage is LCOV,
  scored from a function's branch records when its span has any and from line hits otherwise.
  A matched Go record can hold zero hits, and a file that matched a report can still contain a
  function with no statements in its span, which reads as 0%.
- Both Go jobs test and instrument the same package set: `cmd/`, `internal/`, and `pkg/`. A
  call made from another package's tests is recorded. Broader instrumentation corrects
  measurement rather than adding tests, so a percentage that rises here is not new coverage
  evidence. Discovery stops the build when `go list ./...` reports a package no required tree
  covers, so a new top-level tree cannot land untested; the Go files under `node_modules` are
  the known exception and stay out.
- crapper recognises named functions and methods, plus top-level arrow functions in
  TypeScript. Other nested callbacks stay inside the enclosing function, so one entry can
  stand for a lot of decisions.
- Static-site functions remain in the inventory even when their coverage suite is absent.
  CI collects `static/coverage/lcov.info` once `test:coverage` is present; missing suite input
  means unmeasured, not measured zero or full coverage. Its build/document checks still run.
- Python tooling uses pinned coverage.py with branch tracking and repository-rooted LCOV.
  Its raw JSON preserves statement and branch totals separately; Crapper uses branch records
  when available. Subprocesses need their own instrumentation: an uninstrumented launcher
  remains visible at zero coverage instead of being dropped.
- Not assessed at all: SQL, YAML and Compose/Kubernetes configuration, shell scripts,
  protobuf definitions, MDX content, and CSS.
- Not assessed: runtime performance. Nothing here measures latency, throughput, memory, or
  query plans. Statements about those need workload measurements.

For what to do about a given score, see [crapper-assessment.md](crapper-assessment.md).

There is no global CRAP threshold: unchanged existing debt does not fail a pull request.
Judge improvements by behaviour tests and real simplification, not by removing useful
guards to lower a score.


## Pull-request feedback and regression checks

The CRAP job writes a GitHub Actions summary and uploads the complete inventory,
comparison and raw reports as the `crap-report` artifact. Reports are retained for
90 days. For pull requests from the same repository, it also maintains one bot
comment with before/after metrics and a link to the workflow run. It does not edit
the author's PR description. Forks still receive the summary and artifact; their
read-only tokens do not publish comments.

The baseline must come from a successful CRAP job on a push to `main`
at the PR's exact base commit. Metadata verifies the source revision, scoring
revision, report scope and inventory checksum. The report distinguishes the PR
head from the merge commit actually tested by CI. A stale run cannot replace the
comment for a newer PR head or base.

Changed production line ranges identify affected functions. Tests-only changes do
not gate coverage changes in untouched functions; the complete inventory remains
available for review. Unique path, namespace and name
identities pair before/after results; ambiguous identities and renames are reported
as uncomparable rather than guessed from line order. An incomplete comparison
fails the check and requires resolving the ambiguous identities or rename before
merging. Deleted functions remain
visible without being treated as coverage losses.

For comparable changed functions, CI fails when:

- Measured coverage falls by more than 0.01 percentage points.
- A previously measured function loses its coverage record.
- CRAP rises by more than 0.01 and the resulting score is at least 30.
- A newly added function has no coverage record or has CRAP of at least 30.

A CRAP increase below 30 remains visible without blocking a fully tested change.
Existing high scores that stay unchanged or improve do not fail the gate. These
checks do not replace logical review of tests, unsupported paths or production
behaviour. No score should be improved by deleting required error handling.

If an exact, compatible baseline is unavailable, including during initial rollout
or after artifact expiration, the report explicitly marks comparison unavailable.
It publishes current metrics but makes no claim that regressions were ruled out.
Missing coverage records are distinct from measured zero, and function counts or
averages are not presented as overall statement coverage.

A regression makes the `CRAP report` check fail. Repository rules must require
that check to enforce it before merging; release-job dependencies are unchanged.

Comment publication is best-effort: GitHub API failures cannot turn a successful
comparison into a failed gate. The job summary and artifact retain the results.
New PR runs cancel older runs for that PR; main-branch reports use separate
concurrency groups per commit so later pushes do not cancel baseline collection.

Whole-scope debt tables list high-CRAP, partially covered and unmeasured functions,
including unchanged code. The PR comment and job summary show bounded tables with total
counts; the JSON artifact retains all rows. CI emits the same bounded inventory as a
revision-bound check annotation, readable through the GitHub API when artifact downloads
are unavailable. These debt tables do not change the regression gate.

## Expanding measurement scope

Report metadata versions the measured scope. A scope or scorer change invalidates older
baselines rather than treating newly inventoried functions as previously tested or untested.
The first compatible main report establishes the new baseline; an unavailable comparison
is reported explicitly and does not prove absence of regressions. Full coverage and a global
CRAP limit remain goals under remediation, not claims made by the changed-function gate.
`.metrics/scope.json` keeps unsupported configuration, content, shell and generated/test
files visible for separate contract validation; those files do not acquire a fabricated CRAP score.
