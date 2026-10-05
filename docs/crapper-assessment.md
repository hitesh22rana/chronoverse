# Chronoverse CRAP assessment

Date: 2026-10-05. Baseline revision: `e262fac396edc47509ece1b72a372a3561783b91` (main).
Tool: https://github.com/unclebob/crapper at `9f1bead298b5a9d576bdd6319289fcf426e5b18a`.
Supersedes the unit-only figures in the first version; where the two disagree, the numbers
here are the complete ones. See [coverage.md](coverage.md).

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
Docker; dashboard `npm run test:coverage`, 23 files / 174 tests, statements 87.71%,
branches 81.25%, 52 LCOV sources. The only integration skip is
`TestIntegrationWorkloadEgressDropsHostListener`, since `CHRONOVERSE_WORKLOAD_FIREWALL` is
unset. An earlier integration run that appeared to finish silently skipped jobs
container-start failures and is not used here.

| Area | Handwritten functions | Matched coverage record | CRAP >= 30 | CC > 10 |
| --- | ---: | ---: | ---: | ---: |
| Go | 1,028 | 1,028 | 84 | 76 |
| Dashboard | 251 | 155 | 37 | 23 |
| Static site | 92 | 0 | 19 | 3 |
| Total | 1,371 | 1,183 | 140 | 102 |

Go coverage is statement coverage; TypeScript uses branch coverage where a function has
branch records, line coverage otherwise. A matched Go record can hold zero hits, and 64 of
the 84 high Go scores are exactly that: `cmd/*/main.go` `run`, `registerRoutes`,
`internal/pkg/testkit` helpers, ClickHouse/Kafka/Redis/Postgres setup — wiring with no
unit-level behaviour to test, not a defect candidate. A missing TypeScript record is
different, since crapper substitutes 0%: 54 of the 140 high scores, 35 dashboard and all 19
static, have no measurement at all. Neither zero proves absence of tests outside these runs.

## Implemented work

Four independent PRs branched from main, unmerged and not tested together, so no combined
"after" figure is reported.

**Docker log streaming**, branch `fix/docker-log-stream-correctness`
([link](https://github.com/hitesh22rana/chronoverse/tree/fix/docker-log-stream-correctness),
PR number pending). `streamContainerLogs` (`internal/pkg/kind/container/docker.go:638`):
CC 26 → 12, coverage 78.4% → 100%, CRAP 32.8 → 12.0. Both earlier findings were real:
neither stream inspected `scanner.Err()`, and each loaded and incremented the shared
sequence counter separately, so stdout and stderr could allocate the same value. Assignment
now happens in the single forwarding loop. Event identity also includes stream and retry
attempt, so duplicate values alone never proved event collision; the fix removes the
ordering hazard without changing backpressure or replay identity. `Execute` (line 454): CC 21
→ 17, coverage unchanged at 80.4%, CRAP 24.3 → 19.2. Full Docker unit and integration suites
pass.

**Workflow UI behaviour tests**, [PR #177](https://github.com/hitesh22rana/chronoverse/pull/177),
branch `test/workflow-ui-behavior`. 222 dashboard tests pass; lint and `tsc` clean.

| Function | Source | CC | Coverage | CRAP |
| --- | --- | ---: | ---: | ---: |
| renderWorkflowActions | `dashboard/src/features/workflows/workflow-details-page.tsx:401` | 26 → 5 | unrecorded → 100% | 702 → 5 |
| renderWorkflowDetails | `dashboard/src/features/workflows/workflow-details-page.tsx:648` | 22 → 6 | unrecorded → 100% | 506 → 6 |
| useWorkflows | `dashboard/src/features/workflows/use-workflows.ts:113` | 20 (same) | unrecorded → 95.5% | 420 → 20.0 |

Complexity moved into extracted components rather than disappearing: the extracted details
card remains at 64.3% coverage and CRAP 30.2. The production build was not verified — it
stops at an unchanged Google Fonts Poppins fetch the environment cannot reach.

**Representative coverage and reproducible CRAP**,
[PR #178](https://github.com/hitesh22rana/chronoverse/pull/178), branch
`ci/representative-crap-coverage`. Vitest now names every application source through
`coverage.include`, so files no test imports are reported at their real coverage instead of
being left out: 52 → 101 LCOV sources, and the same 174 tests move from statements 87.71%
(921/1050) to 51.25% (921/1797), branches 81.25% → 46.29% — same covered statements, 876 more
that no test reaches. CI collects the three job profiles and scores them in one job with
crapper pinned by revision; 57 script regression tests cover the merge, path rooting, and
source exclusions. Matched records rise from 1,183 to 1,279 while CRAP >= 30 stays at 140,
because the newly recorded functions measure exactly 0% and substituting 0% scores the same
as measuring it. The inventory now labels each function `coverage_recorded` yes or no. No
threshold is enforced and no release job depends on the report.

**Scheduling command coverage**,
[PR #179](https://github.com/hitesh22rana/chronoverse/pull/179), branch
`test/job-scheduling-command-coverage`. `ScheduleJob` (`internal/repository/jobs/jobs.go:124`):
CC 24 unchanged, coverage 62.0% → 88.7%, CRAP 55.7 → 24.8, test-only. A separate
investigation reproduced a non-UTC offset lost through a timezone-less column; callers
currently supply UTC, so it is deferred to a focused UTC-normalization change with an offset
regression test.

## Remaining candidates

- **Defended, leave alone.** `useJobLogs` CC 28 / 100% / CRAP 28.0 and `useLogSelection`
  CC 23 / 93.9% / 23.1 are well covered. `ValidateToken` CC 23 / 88.2% / 23.9 enforces
  signing algorithm, key, issuer, audience, expiry, role, and subject checks — preserve
  them, adding a rejection case only when one is identified. `decodeUniqueValue` CC 16 /
  83.3% / 17.2 supports canonical request identity; do not weaken its parsing.
- **Workflow repositories.** `UpdateWorkflow` CC 27 / 75.0% / CRAP 38.4 and `DeleteWorkflow`
  CC 23 / 53.2% / CRAP 77.1 are the two largest remaining. Examine the missing domain
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