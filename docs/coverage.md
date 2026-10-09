# Coverage and CRAP reporting

This describes how the coverage reports are produced and how they are turned into CRAP
scores. Nothing here is a gate: no job fails on a score, no release job depends on the
report, and no source file is excluded to make a number look better.

The sources of truth for this pipeline are [`.github/workflows/ci.yaml`](../.github/workflows/ci.yaml),
[`scripts/coverage/crap_report.py`](../scripts/coverage/crap_report.py), and the `Makefile`
coverage targets. Where this page and those files disagree, those files are correct. For how to
read the output and decide what to do about it, see
[crapper-assessment.md](crapper-assessment.md).

## What runs where

| Job | Command | Uploads |
| --- | --- | --- |
| `test` | `make test/short COVERPROFILE=target/coverage-inputs/unit/coverage.out` | `coverage-unit` |
| `integration` | `make test/integration COVERPROFILE=target/coverage-inputs/integration/coverage.out` | `coverage-integration` |
| `dashboard` | `cd dashboard && npm run test:coverage` | `coverage-dashboard` |
| `coverage` | `python scripts/coverage/crap_report.py ...` | `crap-report` |

Coverage rides along with the test runs that already gate the build. The `COVERPROFILE`
variable adds `-covermode=atomic -coverprofile=...` to the same `go test` invocation; there
is no second run of any suite. `npm run test:coverage` is `vitest run --coverage` with the
JSON, LCOV, and text-summary reporters, so it is the existing `npm test` gate plus the LCOV
report. The `coverage` job needs `test`, `integration`, and `dashboard`; it runs the script's
own tests, downloads their three artifacts, and scores the profiles without a Go or Node
toolchain.

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

# 4. Merge, root, and score, using an interpreter that has the pinned crapper:
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

The inventory has a `coverage_recorded` column with `yes` or `no`, and its stdout summary
separates the functions that matched a record from the functions no report mentions.
crapper substitutes 0% for a function no report mentions, which is the right input to the
formula but hides the difference between "measured 0%" and "never measured". The column and
the summary keep them apart.

Intermediate reports land where crapper looks for them by design, so the native
`crapper --use-existing-coverage` command reads the same files:

```
target/coverage/go/coverage.out                      merged Go profile
target/coverage/typescript/dashboard/lcov.info        dashboard LCOV, rooted at the repository
target/coverage-inputs/{unit,integration}/           the raw per-job profiles
```

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

Scope is the tracked sources crapper can parse, minus tests, minus generated code: files
carrying Go's generated-code marker (`// Code generated ... DO NOT EDIT.`) and everything
under `pkg/proto/` are left out. crapper's own skip list removes build output, dependency
trees, and report directories, and `git ls-files` is the starting set, so untracked output
never enters. The snapshot and the inventory are built from that one list, so they always
describe the same scope.

Limits worth stating plainly:

- CRAP is `CC² × (1 − coverage)³ + CC`. It combines one complexity number with one coverage
  number and says nothing else. It is a ranking aid, not a quality grade, and the two inputs
  are not comparable across languages.
- Go coverage is statement coverage from `go test -coverprofile`. TypeScript coverage is LCOV,
  scored from a function's branch records when its span has any and from line hits otherwise.
  A matched Go record can hold zero hits, and a file that matched a report can still contain a
  function with no statements in its span, which reads as 0%.
- Go profiles instrument each package for its own tests by default, so calls from another
  package's tests can remain unrecorded. Cross-package `-coverpkg=./...` instrumentation is not
  used here.
- crapper recognises named functions and methods, plus top-level arrow functions in
  TypeScript. Other nested callbacks stay inside the enclosing function, so one entry can
  stand for a lot of decisions.
- The static site has no test suite, so its functions have no coverage record. Their scores
  are complexity-only and are marked `no` in the inventory. That is an absence of measurement,
  not a failure.
- Not assessed at all: SQL, YAML and Compose/Kubernetes configuration, shell scripts,
  protobuf definitions, MDX content, and CSS.
- Not assessed: runtime performance. Nothing here measures latency, throughput, memory, or
  query plans. Statements about those need workload measurements.

For what to do about a given score, see [crapper-assessment.md](crapper-assessment.md).

No threshold is enforced. A global CRAP limit of 30 can flag necessary guards or functions
with no coverage record and encourage deleting guards rather than testing behaviour.
Judge a change by whether it adds a behaviour test or
removes real duplication, and recompute the numbers.
