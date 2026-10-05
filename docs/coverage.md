# Coverage and CRAP reporting

This describes how the coverage reports are produced and how they are turned
into CRAP scores. Nothing here is a gate: no job fails on a score, and no source
file is excluded to make a number look better.

## What runs where

| Job | Command | Writes |
| --- | --- | --- |
| `test` | `make test/short COVERPROFILE=target/coverage-inputs/unit/coverage.out` | `coverage-unit` artifact |
| `integration` | `make test/integration COVERPROFILE=target/coverage-inputs/integration/coverage.out` | `coverage-integration` artifact |
| `dashboard` | `cd dashboard && npm run test:coverage` | `coverage-dashboard` artifact |
| `coverage` | `python3 scripts/coverage/crap_report.py ...` | `crap-report` artifact |

Coverage rides along with the test runs that already gate the build. The
`COVERPROFILE` variable adds `-covermode=atomic -coverprofile=...` to the same
`go test` invocation; there is no second run of any suite. `npm run test:coverage`
is `vitest run --coverage ...`, so it is the existing `npm test` gate plus the
LCOV report. The `coverage` job needs `test`, `integration`, and `dashboard`,
downloads their three artifacts, and scores them without a Go or Node
toolchain.

Both Go jobs must use the same `-covermode`. The Makefile fixes `COVERMODE` to
`atomic` because the race detector needs atomic counters, and summing atomic hit
counts is what the merge step defines.

## Local commands

Everything runs against profiles already on disk; nothing here reruns a suite
unless you ask it to.

```sh
# 1. Unit coverage (about three minutes; Docker is not needed).
make test/short COVERPROFILE=target/coverage-inputs/unit/coverage.out

# 2. Integration coverage. Needs a running Docker daemon, about ten minutes.
make test/integration COVERPROFILE=target/coverage-inputs/integration/coverage.out

# 3. Dashboard coverage.
cd dashboard && npm ci && npm run test:coverage && cd ..

# 4. Merge, root, and score. Needs the pinned crapper:
#    python3 -m pip install \
#      "crapper @ git+https://github.com/unclebob/crapper@9f1bead298b5a9d576bdd6319289fcf426e5b18a"
make coverage/crap PYTHON=/path/to/venv/bin/python
```

`make coverage/crap` writes three files and prints the worst scores first:

| Path | Contents |
| --- | --- |
| `.metrics/crap-report.txt` | crapper's own table, worst CRAP first |
| `.metrics/crap.edn` | crapper's snapshot in the shape `uml-viewer` reads |
| `.metrics/inventory.csv` | one row per function, with source lines and whether a coverage record matched |

The inventory has a `coverage_recorded` column with `yes` or `no`. crapper
substitutes 0% for a function no report mentions, which is the right input to
the formula but hides the difference between "measured 0%" and "never
measured". The column keeps them apart.

Intermediate reports land where crapper looks for them by design, so the native
`crapper --use-existing-coverage` command reads the same files:

```
target/coverage/go/coverage.out                    merged Go profile
target/coverage/typescript/dashboard/lcov.info      dashboard LCOV, rooted at the repository
target/coverage-inputs/{unit,integration}/          the raw per-job profiles
```

`target/coverage` and `.metrics` are gitignored. Do not commit them.

## Merging the Go profiles

`scripts/coverage/crap_report.py` refuses to merge profiles it cannot trust:

- every profile must carry a `mode:` header naming one of `set`, `count`,
  `atomic`;
- a block described twice must agree on its statement count, inside one profile
  as much as across two. Disagreement means the profiles came from different
  builds, and summing them would report coverage for code neither build ran;
- hit counts are added across blocks, because CRAP only asks whether a block was
  entered at all;
- the merged report and every LCOV crapper would read are deleted first, so a
  profile left by an earlier run cannot be merged in by accident. The per-job
  inputs are staged under `target/coverage-inputs`, outside everything that is
  cleared.

Run the script's own tests with:

```sh
python3 -m unittest discover -s scripts/coverage -t scripts/coverage
```

## Dashboard coverage scope

Vitest reports only the files a test imported unless `coverage.include` names
them. Left unset, the summary described 52 files and read as 87.71% statements;
the dashboard has 101 non-test sources under `dashboard/src`. `dashboard/vitest.config.ts`
now names `src/**/*.{ts,tsx}` explicitly and excludes only test files, type
declarations, and `__tests__` directories, so every application source is
reported at its real coverage. No production file is excluded to raise a number.

## What the score covers, and what it does not

Scope is the tracked sources crapper can parse, minus tests, minus generated
code: files carrying Go's generated-code marker
(`// Code generated ... DO NOT EDIT.`) and everything under `pkg/proto/` are
left out. crapper's own skip list removes build output, dependency trees, and
report directories, and `git ls-files` is the starting set, so untracked output
never enters. The snapshot and the inventory are built from that one list, so
they always describe the same scope.

Limits worth stating plainly:

- CRAP is `CC² × (1 − coverage)³ + CC`. It combines one complexity number with
  one coverage number and says nothing else. It is a ranking aid, not a quality
  grade, and the two inputs are not comparable across languages.
- Go coverage is statement coverage from `go test -coverprofile`; TypeScript
  coverage is LCOV, using branch records where a function has them and line
  hits otherwise. A matched Go record can contain zero hits.
- crapper recognises named functions and methods, plus top-level arrow functions
  in TypeScript. Other nested callbacks stay inside the enclosing function, so
  one entry can stand for a lot of decisions.
- A score of 1-5 is low risk, 5-30 is worth a look, 30+ is complex and
  under-tested. A high score on a coordinator that owns transaction, lease, and
  idempotency guards often reflects necessary work, not a defect.
- The static site has no test suite, so its functions have no coverage record.
  Their scores are complexity-only and are marked `no` in the inventory. That is
  an absence of measurement, not a failure.
- Not assessed at all: SQL, YAML and Compose/Kubernetes configuration, shell
  scripts, protobuf definitions, MDX content, and CSS.
- Not assessed: runtime performance. Nothing here measures latency, throughput,
  memory, or query plans. Statements about those need workload measurements.
- Generated protobuf and MockGen methods, startup wiring, declaration-heavy UI
  primitives, and static page rendering are poor targets for score reduction.

No threshold is enforced. A global CRAP limit of 30 currently fails on a large
number of functions, many of them with no coverage record at all, and would
reward deleting guards rather than testing behaviour. Judge a change by whether
it adds a behaviour test or removes real duplication, and recompute the numbers.

Pinned tool: <https://github.com/unclebob/crapper> at revision
`9f1bead298b5a9d576bdd6319289fcf426e5b18a`. The revision is part of the
reproducibility contract; bump it deliberately, and expect the scores to move
when you do.
