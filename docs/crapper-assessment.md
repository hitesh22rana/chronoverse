# Chronoverse CRAP assessment

Date: 2026-10-05. Baseline revision: `e262fac396edc47509ece1b72a372a3561783b91` (main).
Tool: https://github.com/unclebob/crapper at `9f1bead298b5a9d576bdd6319289fcf426e5b18a`.
See [coverage.md](coverage.md).

## Decision

Keep complexity that guards transactions, idempotency, leases, and outbox atomicity: the
Docker integration tests exercise those paths and the complete profile now shows them
measured. Spend effort on log-stream correctness and on behaviour tests for untested UI
decisions, not on a project-wide refactor, and never remove a guard to satisfy a threshold.

## Scope and method

Crapper scored the complete tree. Generated protobuf and MockGen code (1,092 functions) is
excluded from the actionable scope, leaving 1,371 handwritten functions across 322 files.
Test helpers and configuration functions stay in scope, so handwritten counts are not
exclusively production behaviour.

Baseline profiles, all complete and passing: Go unit `go test -race -short -count=1`, 51
packages reporting passing tests; Go integration the full `TestIntegration*` set under
Docker; dashboard `npm run test:coverage`, 23 files / 174 tests, 52 LCOV sources, imported
files only — see [Dashboard coverage at equal scope](#dashboard-coverage-at-equal-scope) for
the full-source figures. The only integration skip is
`TestIntegrationWorkloadEgressDropsHostListener`, since `CHRONOVERSE_WORKLOAD_FIREWALL` is
unset.

| Area | Handwritten functions | Matched coverage record | CRAP >= 30 | CC > 10 |
| --- | ---: | ---: | ---: | ---: |
| Go | 1,028 | 1,028 | 84 | 76 |
| Dashboard | 251 | 155 | 37 | 23 |
| Static site | 92 | 0 | 19 | 3 |
| Total | 1,371 | 1,183 | 140 | 102 |

Go coverage is statement coverage; TypeScript uses branch coverage where a function has
branch records, line coverage otherwise. A matched Go record can hold zero hits, and 64 of
the 84 high Go scores are exactly that: `cmd/*/main.go` `run`, `registerRoutes`,
`internal/pkg/testkit` helpers and service setup. Rank these by actual behaviour and
maintenance needs; a high score alone does not establish a defect. Default Go profiles
instrument each package for its own tests, so calls from another package's tests can remain
unrecorded as hits. Cross-package `-coverpkg=./...` instrumentation was not used. A missing TypeScript record is
different, since crapper substitutes 0%: 54 of the 140 high scores, 35 dashboard and all 19
static, have no measurement at all. Neither zero proves absence of tests outside these runs.

## Implemented work

Four independent PRs branched from main, unmerged and not tested together, so no combined
"after" figure is reported; see [Independent branches](#independent-branches).

**Docker log streaming**, [PR #180](https://github.com/hitesh22rana/chronoverse/pull/180),
branch `fix/docker-log-stream-correctness`. `streamContainerLogs`
(`internal/pkg/kind/container/docker.go:638` → `:677`):
CC 26 → 12, coverage 78.4% → 100%, CRAP 32.8 → 12.0. Both earlier findings were real:
neither stream inspected `scanner.Err()`, and each loaded and incremented the shared
sequence counter separately, so stdout and stderr could allocate the same value. Assignment
now happens in the single forwarding loop. Event identity also includes stream and retry
attempt, so duplicate values alone never proved event collision; the fix removes the
ordering hazard without changing backpressure or replay identity. `Execute` (line 454 →
472): CC 21 → 17, coverage 80.4% → 94.6%, CRAP 24.3 → 17.0. Full Docker unit and integration
suites pass.

**Workflow UI behaviour tests**, [PR #177](https://github.com/hitesh22rana/chronoverse/pull/177),
branch `test/workflow-ui-behavior`. 225 dashboard tests pass; lint and `tsc` clean, and the
GitHub dashboard build passes.

| Function | Source | CC | Coverage | CRAP |
| --- | --- | ---: | ---: | ---: |
| renderWorkflowActions | `dashboard/src/features/workflows/workflow-details-page.tsx:401 → :369` | 26 → 5 | unrecorded → 100% | 702 → 5 |
| renderWorkflowDetails | `dashboard/src/features/workflows/workflow-details-page.tsx:648 → :459` | 22 → 6 | unrecorded → 100% | 506 → 6 |
| useWorkflowDetailsAndJobsModel | `dashboard/src/features/workflows/workflow-details-page.tsx:81 → :53` | 12 → 11 | unrecorded → 100% | 156 → 11 |
| WorkflowDetailsCard | `dashboard/src/features/workflows/workflow-details-card.tsx:31 → :30` | 17 → 6 | 64.3% → 100% | 30.2 → 6 |
| WorkflowJobsToolbar | `dashboard/src/features/workflows/workflow-jobs-toolbar.tsx:44 → :43` | 15 → 14 | 95.5% → 100% | 15.0 → 14 |
| useWorkflows | `dashboard/src/features/workflows/use-workflows.ts:113` | 20 (same) | unrecorded → 100% | 420 → 20.0 |

The card and toolbar rows compare the previous PR revision; the other rows compare main.

Complexity moved into extracted components rather than disappearing, and the extracted card
and toolbar are now tested in their own right: the details card goes from 17 / 64.3% / 30.2
to 6 / 100% / 6, and the toolbar from 15 / 95.5% / 15.0 to 14 / 100% / 14.

**Representative coverage and reproducible CRAP**,
[PR #178](https://github.com/hitesh22rana/chronoverse/pull/178), branch
`ci/representative-crap-coverage`. Vitest now names every application source through
`coverage.include`, so files no test imports are reported at their real coverage instead of
being left out: 52 → 101 LCOV sources, and the same 174 tests report 921 of 1,797 statements
(51.25%) instead of 921 of 1,050 over imported files only — the same covered statements and
747 further uncovered ones (876 uncovered in total). CI collects the three job
profiles and scores them in one job with crapper pinned by revision; 61 script regression
tests cover the merge, path rooting, and source exclusions. Matched records rise from 1,183
to 1,279 while CRAP >= 30 stays at 140, because the newly recorded functions measure exactly
0% and substituting 0% scores the same as measuring it. The inventory now labels each
function `coverage_recorded` yes or no. No threshold is enforced and no release job depends
on the report.

**Scheduling command coverage**,
[PR #179](https://github.com/hitesh22rana/chronoverse/pull/179), branch
`test/job-scheduling-command-coverage`. `ScheduleJob` (`internal/repository/jobs/jobs.go:124`):
CC 24 unchanged, coverage 62.0% → 90.1%, CRAP 55.7 → 24.6, test-only. A separate
investigation reproduced a non-UTC offset lost through a timezone-less column; callers
currently supply UTC, so it is deferred to a focused UTC-normalization change with an offset
regression test.

## Dashboard coverage at equal scope

Each run below reports every application source under `dashboard/src`, so the percentages
are comparable; the denominators still move slightly where a branch extracts or adds a file.

| Run | Tests | Statements | Branches |
| --- | ---: | ---: | ---: |
| baseline `e262fac` | 174 | 51.25% (921/1797) | 46.22% (624/1350) |
| PR #178, same scope | 174 | 51.25% (921/1797) | 46.29% (625/1350) |
| PR #177 earlier | 222 | 66.42% (1203/1811) | 62.92% (847/1346) |
| PR #177 latest | 225 | 66.44% (1202/1809) | 63.4% (849/1339) |

The baseline's own Vitest configuration reports only imported files, 87.71% statements and
81.38% branches (921/1050, 625/768) over 174 tests. Those numbers come from a denominator of
1,050 statements against 1,797 for the same run with every source named, so the drop to
51.25% is scope, not regression; comparing the two as a trend is meaningless and the
assessment above uses only the full-source figures. The one-branch difference between the two
baseline rows is two runs of the same 174 tests.

## Independent branches

The four PRs branch from main and are not tested together, so their gains are not summed and
no combined "after" figure is claimed. A per-function comparison of each branch against the
baseline reports coverage gains and **zero coverage regressions**.

## Remaining candidates

- **Defended, leave alone.** `useJobLogs` CC 28 / 100% / CRAP 28.0 and `useLogSelection`
  CC 23 / 93.9% / 23.1 are well covered. `ValidateToken` CC 23 / 88.2% / 23.9 enforces
  signing algorithm, key, issuer, audience, expiry, role, and subject checks — preserve
  them, adding a rejection case only when one is identified. `decodeUniqueValue` CC 16 /
  83.3% / 17.2 supports canonical request identity; do not weaken its parsing.
- **Workflow repositories.** `UpdateWorkflow` CC 27 / 75.0% / CRAP 38.4 and `DeleteWorkflow`
  CC 23 / 53.2% / CRAP 77.1 remain candidates for additional domain coverage. Examine the missing domain
  outcomes — conflict, rollback, stale lease or generation, cancellation, commit failure —
  before considering extraction, and keep transaction ownership and mutation/outbox
  atomicity visible. Do not add a generic transaction framework.
- **Remaining UI.** `renderWorkflowListControls` CC 20 and `CreateWorkflowForm` CC 19, both
  unrecorded in the baseline, plus `useLogViewerActions` CC 24 / 74.3% / CRAP 33.8, merit
  behaviour tests when next touched. `renderTooltipItem`
  (`dashboard/src/components/ui/chart.tsx:207`, CC 23 / CRAP 552) is lower priority;
  optional formatting branches naturally inflate it.
- **Static site.** Its 19 high scores assume zero coverage, not measured failures. The
  validators passed for 47 MDX pages, 23 OpenAPI operations, and 3 generated LLM documents.
  Add behaviour tests for OpenAPI dereferencing including cycles and unresolved refs, search
  indexing, and document headings when those utilities change; do not duplicate static
  markup with trivial tests to improve the denominator.
- **Live logs performance, candidate only.** Each arriving SSE log in
  `dashboard/src/features/logs/log-data.ts` copies, deduplicates, and sorts accumulated live
  logs, page flattening and merging happen on render, and live state has no size cap. Before
  capping or batching, measure long-run CPU, heap growth, retained log count, and render
  time. Memoization does not bound retained state, and virtualized rendering alone does not
  bound the data buffer. No benchmark here establishes the size of the benefit.

## Limits

CRAP is `CC² × (1 − coverage)³ + CC`: one complexity number and one coverage number, a
ranking aid rather than a quality grade, and the two inputs are not comparable across
languages. Crapper scores named functions, methods, and top-level arrows; other nested
callbacks are charged to their enclosing function. It does not assess SQL, shell scripts,
protobuf definitions, Compose or Kubernetes YAML, MDX, or CSS, and measures no runtime
performance. Infrastructure behaviour, security configuration, query plans, latency, and
resource use were not comprehensively audited, and static documentation validation is
evidence only for the validators' own checks. There is no evidence here for broad database
index work, Kafka batching, or runtime concurrency tuning. A global threshold of 30 would
fail on 140 functions, 54 of them with no coverage record, and would reward deleting guards.
