# Test-audit discovery ledger — chronoverse (read-only, no edits)

Base SHA: `f5b8c9bb` — clean checkout, no source/test edits in this pass.
Scope: full repo. 111 `*_test.go` + ~20 `dashboard/src/**/*.test.{ts,tsx}`.
- Go unit gate: `make test/short` = `go test -race -short ./...` (Docker integration self-skips)
- Go integration gate: `make test/integration` = `go test -race -run 'TestIntegration' ./...`
- Dashboard gate: `dashboard/vitest.config.ts` (no include filter) + `.github/workflows/ci.yaml` `dashboard` job (`npm ci` → `lint` → `tsc --noEmit` → `npm test`=`vitest run` → `build`)
- `static/`, `scripts/`, `infra/k8s`: zero vitest suites; covered by `compose/validate`, `static npm run check`, kustomize render/dry-run targets.
- No root or scoped `AGENTS.md` found. Skill validation refs to `scripts/run-vitest.mjs`, `scripts/check-changed.mjs`, `$openclaw-*`, `$crabbox` do not exist in this repo — Go/dashboard gates above are canonical.

Conventions: each candidate lists all 7 evidence fields. Status is `candidate` (not approved for deletion). No production/test edits made.

---

## A. Exact source-grep on shell source — `TestFirewallNeverDeletesInputDrop`

- exact test: `TestFirewallNeverDeletesInputDrop` — `internal/pkg/kind/container/firewall_script_test.go:285`
- detectable failure: only literal `-D` + `CHAIN_IN` co-occurring on one line of `compose/firewall/workload-firewall.sh`. Passes if firewall semantically broken (wrong chain, missing terminal DROP, bad order). Fails on behavior-preserving rename of shell var.
- non-test callers of seam: no Go callers. Only `compose.dev.yaml:372`, `compose.prod.yaml:423` mounts + test `runFirewallFile`.
- stronger proof: `TestFirewallReapplyKeepsOrderAndNoDuplicates` (`:248`, stub-iptables exact INPUT order + no-dup) + `TestFirewallProbePassesAfterApply` / `TestFirewallProbeFailsWithoutRules`.
- history: `eb7154e4 fix: workload egress firewall and admission gating (#150)` — single commit.
- deletion unlocked: test-only (~15 lines + `runtime.Caller` boilerplate). No prod change.
- risk + validation: Low. `go test ./internal/pkg/kind/container -run 'TestFirewall(NeverDeletesInputDrop|ReapplyKeepsOrderAndNoDuplicates|ProbePassesAfterApply|ProbeFailsWithoutRules)' -count=1`
- lane: 1 + 4 (dup merged).

## B. Private predicate duplicated at real boundary — `TestIsAllowedContainerRegistry`

- exact test: `TestIsAllowedContainerRegistry` — `internal/pkg/kind/container/registry_test.go:19`
- detectable failure: direct-call divergence of private `isAllowedContainerRegistry(host)` only. Misses validator wiring break (`ExtractAndValidateContainerDetails` stops calling helper, `reference.Domain` misuse, payload extraction bug).
- non-test callers: `isAllowed...` ← single prod caller `validateContainerImage` (`registry.go:32`). Public owner `ExtractAndValidateContainerDetails` ← 5 prod callers (`idempotency.go:47`, `workflows.go:146,239`, `build_workflow.go:218`, `helpers.go:60`, `container_executor.go:35`).
- stronger proof: `TestExtractAndValidateContainerDetailsRegistryGuard` (`registry_test.go:53`, 11 allowed + 5 rejected incl `docker.io.evil.com`, `localhost:5000`, `%%%`, case-insensitive AzureCR).
- history: `4dafe215 fix: P4 heartbeat + registry guard (#148)`.
- deletion unlocked: test-only (~32 lines + dup allow/reject tables). Prod helper stays.
- risk + validation: Low. `go test ./internal/pkg/kind/container -run 'TestIsAllowedContainerRegistry|TestExtractAndValidateContainerDetailsRegistryGuard' -count=1`

## C. Bundled-dictionary pin — `TestCommonPasswordsWired`

- exact test: `TestCommonPasswordsWired` — `internal/service/users/password_test.go:8` (whole 19-line file)
- detectable failure: only upstream `zxcvbn/frequency` list losing literals `password/12345678/qwerty123` or empty map. Passes if `weakPassword` stops checking map or `RegisterUser` stops calling it. Fails on harmless dictionary refresh still caught by `Score<3`.
- non-test callers: `commonPasswords` ← only `weakPassword`; `weakPassword` ← once in `Service.RegisterUser` (`users.go:127`).
- stronger proof: `TestRegisterUser` (`users_test.go:21`, weak/letters-digits/sequential cases `:109-140` assert error with repo never called).
- history: `0d1f6806 fix: P2 input validation + allowlists (#146)`.
- deletion unlocked: delete `password_test.go` (19 lines). No prod change.
- risk + validation: Low-medium. `go test ./internal/service/users -run 'TestCommonPasswordsWired|TestRegisterUser' -count=1`

## D. Copied TLS manifest twins — `TestNewSurfacesTLSBuildError` / `TestNewWithoutTLSUnchanged`

- exact tests: `internal/pkg/kafka/tls_test.go:14,:32` + twin `internal/pkg/meilisearch/tls_test.go:14,:32` (both 42-line files, identical except owner)
- detectable failure: `SurfacesTLSBuildError` (manifest `/nonexistent/*.crt/*.key/ca.crt`) only detects error code ≠ `Internal` on missing files. `WithoutTLSUnchanged` only detects missing-brokers/missing-uri `InvalidArgument` — passes for unrelated reason, not TLS wiring. Neither detects wrong `RootCAs`/`MinVersion`/`DialTLSConfig` vs `WithCustomClientWithTLS`, or silently-skipped TLS (original #158 shape).
- non-test callers: `kafka.New+WithTLS` ← 5 `cmd` mains (execution-worker:79, analytics-processor:59, joblogs-processor:88, outbox-relay:57, workflow-worker:94); `meilisearch.New+WithTLS` ← 4 mains (database-migration:83, jobs-service:87, joblogs-processor:75, workflow-worker:81). `newTLSConfig` 20 lines byte-identical in both owners.
- stronger proof: keep one twin as representative; shared logic covered by `TestLoadTLSConfig` (`internal/pkg/grpcserver/tls_test.go:25`, real certs + live `httptest` handshake, MinVersion/trusted/missing/untrusted + 4 invalid-path).
- history: `4d818701 fix(pkg): surface Kafka/Meilisearch TLS build errors (#158)` — both files single commit.
- deletion unlocked: test — delete one twin file (e.g. whole `meilisearch/tls_test.go` 42 lines). Prod follow-up (not this pass): extract shared `newTLSConfig` to kill `kafka.go:131-150` vs `meilisearch.go:105-124` dup.
- risk + validation: Low. `go test ./internal/pkg/kafka ./internal/pkg/meilisearch ./internal/pkg/grpcserver -run 'TestNewSurfacesTLSBuildError|TestNewWithoutTLSUnchanged|TestLoadTLSConfig' -count=1`

## E. Self-generated expected — `TestVerifyCSRFToken` success half

- exact test: `TestVerifyCSRFToken` — `internal/server/csrf_test.go:14` (lines 20-27 roundtrip)
- detectable failure: roundtrip (`token:=generateCSRFToken(...); verifyCSRFToken(token,...)`) only detects crash; passes if both share systematic bug (delimiter, HMAC input, timestamp format). Only tamper half (`:29-33` zeroed HMAC → reject) has teeth.
- non-test callers: `generate` ← 3 prod handlers (`users_handlers.go:68,119,178`); `verify` ← prod middleware (`middlewares.go:176`) + reuse check (`users_handlers.go:169`).
- stronger proof: `TestHandleGetCSRFToken` (`:36`, handler-issued token via verify + cookie flags) + `TestHandleGetCSRFTokenReusesValidCookie` + `TestVerifyCSRFMiddlewareRequiresHeader` (`middlewares_test.go:170`, 403/403/200).
- history: `c5500197 fix: P1 gateway auth hardening (#145)`, `8c26cbc3 (#103)`.
- deletion unlocked: test-only — shrink to tamper-only (delete lines 20-27) or delete whole test keeping handler/middleware.
- risk + validation: Low. `go test ./internal/server -run 'TestVerifyCSRFToken|TestHandleGetCSRFToken|TestVerifyCSRFMiddlewareRequiresHeader' -count=1`

## F. Duplicate Redis image-pull integration (same contract twice)

- exact test: `TestIntegrationImagePullLockSerializesBuilds` — `internal/repository/workflow/workflow_integration_test.go:18` + twin `internal/repository/executor/executor_integration_test.go:23` (bodies line-for-line identical: `testkit.NewFakeContainerSvc(200ms)`, 3× concurrent `Build`, `MaxConcurrentBuilds==1`, post-release `<2s`)
- detectable failure: only Redis `imagepull.Ensure` serialization break. No workflow/executor-specific logic (both wrappers `return imagepull.Ensure(...)`).
- non-test callers: `workflowrepo.NewImagePullLockedContainerSvc` ← `cmd/workflow-worker/main.go:140`; `executorrepo....` ← `cmd/execution-worker/main.go:181`. Wrappers stay; only one test fn redundant.
- stronger proof: `internal/pkg/imagepull/imagepull_test.go:16-189` (8 unit tests, fake lock-store) + `workflow/image_pull_lock_test.go:14` delegation proof. One integration copy suffices for real-Redis path.
- history: `5a9347a7 (#108)` introduced both; `90f01136 (#83)` shared Ensure; `44e7cb60 (#126)`, `5987c651 (#125)` touched wrappers.
- deletion unlocked: delete one copy (prefer workflow copy; its wrapper has extra untested `ResolveImageDigest`). `TestMain` (`WithRedis, WithKafka`) unchanged.
- risk + validation: Low. `go test -short ./internal/pkg/imagepull/... ./internal/repository/workflow/... -count=1` + `go test -run TestIntegrationImagePullLockSerializesBuilds ./internal/repository/executor/... -count=1`

## G. Outboxrelay trivial-helper trio

- exact tests: `TestLeaseAndBackoffDefaults` (`:62`), `TestPostgresInterval` (`:10`), `TestClaimTokenIncludesWorkerAndIsUnique` (`:41`) — all in `internal/repository/outboxrelay/outboxrelay_test.go` (73-line file)
- detectable failure: only literal edits (`30s`/`5s`/`"%d milliseconds"`/`"%s:%d:%s"` in `outboxrelay.go:230-261`). Real claim/publish break (bad interval SQL, bad `locked_by`, wrong lease) fails integration first.
- non-test callers: all three helpers ← only `outboxrelay.go:111,155,220` + this test file.
- stronger proof: `outboxrelay_integration_test.go:22 TestIntegrationPublishTopic`, `:95 PreservesOrderingAcrossKeys`, `:156 CleanupCommandIdempotencyKeys` (real Postgres+Kafka, `InsertTx` → `PublishTopic` → `SELECT status` + `WaitForRecord` + per-key ordering + second-run no-op).
- history: `f73664f1 (#81)` introduced prod+unit+integration together.
- deletion unlocked: all 3 fns (file becomes empty/deletable). Prod untouched.
- risk + validation: Low. `go test -short ./internal/repository/outboxrelay/... -count=1` + `go test -run 'TestIntegrationPublishTopic|TestIntegrationCleanup' ./internal/repository/outboxrelay/... -count=1`

## H. Executor config floor duplicate

- exact test: `TestNormalizeConcurrencyFallbackHasFloor` — `internal/repository/executor/config_test.go:71` (strict subset of `TestNormalizeConfigConcurrency` `:9` which already covers nil/0/-1→GOMAXPROCS + 3→3)
- detectable failure: `normalizeConcurrency` floor break fails `:9` identically. Adds no new branch.
- non-test callers: `normalizeConfig` ← `executor.go:116`; `normalizeConcurrency` ← `config.go:14` only. Integration `TestIntegrationPublishJobLogBatchToKafka` (`executor_integration_test.go:67`) also exercises `normalizeConfig`.
- stronger proof: `:9` + `TestNormalizeConfigRejectsReconciliationLimitBelowConcurrency:59` + integration publish path.
- history: `d93683f1 (#107)`; `eab644e7 (#90)`, `ce20c0d6 (#132)` touched config.
- deletion unlocked: delete `:71-80` only; keep `:9`, `:59`, `TestSystemRetryBackoffIsBoundedExponential:82`.
- risk + validation: Low. `go test ./internal/repository/executor/ -run TestNormalizeConfig -count=1 -v`

## I. Thin service `TestRun` pass-throughs (mock replays)

- exact tests: `TestRun` — `internal/service/scheduler/scheduler_test.go:15` + twin `internal/service/executor/executor_test.go:15` (`mockRepo.EXPECT().Run(gomock.Any()).Return(...)` → `s.Run(ctx)` → assert same total/err; prod owners pure delegation + span)
- detectable failure: none on prod behavior — SQL/dispatch/claim changes bypass mock. Fails only on mock-expectation typo.
- non-test callers: `scheduler.Service.Run` ← `internal/app/scheduler/scheduler.go:49`; `executor.Service.Run` ← `internal/app/executor/executor.go:32`; `New(repo)` ← app wiring / `cmd/execution-worker/main.go:189`. Wiring intact on deletion.
- stronger proof: scheduler `scheduler_integration_test.go:58 TestIntegrationScheduleDueJobs` (+ `:113/:137/:166/:215` skip-guards); executor `executor_test.go` (classification/renewal/reconciliation fakes) + `executor_integration_test.go:65 TestIntegrationPublishJobLogBatchToKafka`.
- history: `bc173b9e`, `b5d779b8` scaffolding; `d93683f1 (#107)` last substantive.
- deletion unlocked: delete one or both twins (if keeping one as smoke, keep executor). Mocks + integrations stay.
- risk + validation: Low. `go test -short ./internal/service/scheduler/... ./internal/service/executor/... ./internal/repository/scheduler/... -count=1`

## J. Execution-worker retry literal shadowed by behavioral sibling

- exact test: `TestExecutionWorkerRetryConfig` — `cmd/execution-worker/main_test.go:15` (literals `MaxAttempts==3`, `100ms`, `[Unavailable,DeadlineExceeded]`); keeper `TestExecutionWorkerRetryConfigPerformsThreeAttempts` `:30` (real `retry.UnaryClientInterceptor` from `executionWorkerRetryConfig()`, 3 attempts, `>=100ms/>=200ms` delays)
- detectable failure: literal change fails `:30` too (`len(attempts)!=3`, delay thresholds hardcoded). `:15` catches nothing `:30` misses.
- non-test callers: `executionWorkerRetryConfig()` ← `main.go:134,145` (workflows + jobs conns).
- stronger proof: `:30` in same file is owner.
- history: `d93683f1 (#107)`; `02300e5f (#127)` cosmetic.
- deletion unlocked: delete `:15-28` only; keep `:30-68` + `main.go:210-217`.
- risk + validation: Low. `go test ./cmd/execution-worker/ -run TestExecutionWorkerRetryConfigPerformsThreeAttempts -count=1 -v`

## K. Assertion-free test — `heartbeat/TestNew`

- exact test: `TestNew` — `internal/pkg/kind/heartbeat/heartbeat_test.go:47` (single `success` row, `_ = heartbeat.New()`, `wantErr` never checked, no assert/require/t.Fatal)
- detectable failure: none — ignores return, passes on error/panic-return. Table scaffolding with dead `wantErr`.
- non-test callers: `heartbeat.New()` prod callers TBD (owner read done in lane 4; contrast: every other `func Test*` hits assert/require or custom `assertCommit/assertPartitionSignal/assertContains/assertRuntimeQueryContains`).
- stronger proof: none needed for this shape — repair (assert NoError + fields) or delete; no contract exists as written.
- history: file history not yet pulled — `git log --oneline -5 -- internal/pkg/kind/heartbeat/heartbeat_test.go` before edit.
- deletion unlocked: test-only — assert or delete single fn.
- risk + validation: Low. `go test ./internal/pkg/kind/heartbeat/ -run TestNew -count=1 -v`
- false-positive guard: `partition_runner_test.go:195,287,318`, `jobs_test.go:41,50`, `runtime_test.go:12` use custom asserts — NOT assertion-free, excluded.

## L. Expected computed by helper under test — imagepull `LockKey`

- exact tests: `internal/pkg/imagepull/imagepull_test.go:56,78` (`want := imagepull.LockKey(...)` then compare against `locks.keys[0]` produced by `Ensure()` → `LockKey(lockScope,...)` at `imagepull.go:57`)
- detectable failure: tautological — always passes; `LockScope→DockerHost` plumbing diff in `:78` unasserted except via same call.
- non-test callers: `LockKey` ← `Ensure` + tests.
- stronger proof: hardcode literal `want` (one-line fix), no new dep; surviving `TestEnsure*` table (8 tests `:16-189`) keeps branch cover.
- history: `90f01136 (#83)` herd protection; confirm via `git log --oneline -5 -- internal/pkg/imagepull/`.
- deletion unlocked: test-only — replace computed `want` with literals.
- risk + validation: Low. `go test ./internal/pkg/imagepull/ -count=1 -v`

## M. Expected computed by helper under test — joblogs event keys

- exact tests: `internal/repository/joblogs/process_logs_batch_test.go:112` (`expectedGeneratedKey := idempotency.LogEventKey("job-1","stdout",1)`) + `process_logs_analytics_test.go:12` (`LogEventKey("job-2","stderr",7)`); prod writes same call (`process_logs_batch.go:67,322`)
- detectable failure: `LogEventKey` refactor/bug passes silently — expected not literal `log:job-1:stdout:1`.
- non-test callers: `LogEventKey` ← prod batch writer + tests.
- stronger proof: replace with string literals; batch/analytics integrations remain.
- history: confirm via `git log --oneline -5 -- internal/repository/joblogs/`.
- deletion unlocked: test-only — two literal swaps.
- risk + validation: Low. `go test -short ./internal/repository/joblogs/... -count=1`
- note: candidate is repair (`F`), not delete — expected-value circularity.

## N. Exact SQL/string greps (fragile, not executable)

- exact tests: `internal/repository/jobs/jobs_test.go:41-47,335-349` (`claimJobQuery()` + `assertContains ORDER BY.../FOR UPDATE`); `internal/repository/runtime/runtime_test.go:12-17,31` (`lockRuntimeNodeQuery` + `FOR UPDATE`); `internal/repository/workflow/workflow_test.go:79-91` (`$1/$2` placeholder + `mutations_sync = 2` substring checks)
- detectable failure: substring presence only — whitespace/alias refactor fails test, semantic break with same substrings passes. ~30 `strings.Contains` hits repo-wide, densest here.
- non-test callers: `claimJobQuery` ← `lease.go:115,139`; `lockRuntimeNodeQuery` ← `runtime.go:143,204`; batch/meili/delete queries ← `process_logs_batch.go:77,81`, `delete_workflow.go:58` — all have prod callers, so queries stay.
- stronger proof: promote one per contract to integration `EXPLAIN`/exec (jobs claim, runtime lock, workflow mutations_sync); delete rest. Existing integrations: `jobs_integration_test.go:158,258`, `lease_integration_test.go:23`, `runtime_integration_test.go`, `workflow_integration_test.go`.
- history: confirm per-file `git log --oneline -5` before edit.
- deletion unlocked: test-only — remove substring asserts, keep integration exec cover.
- risk + validation: Medium (touches query contracts). `go test -short ./internal/repository/jobs/... ./internal/repository/runtime/... ./internal/repository/workflow/... -count=1` + relevant `TestIntegration*` under Docker.

## O. Mock implements asserted behavior — service jobs pass-throughs

- exact tests: `internal/service/jobs/jobs_test.go:56-69` + assert `:194` (`repo.EXPECT().ScheduleJob(...).Return("job_id",nil)` → `assert.Equal(jobID,"job_id")`); repeats `CancelJob:215-217`, `ClaimJob:310-356`, `GetJob:446-451` — densest `EXPECT/Return` file repo-wide
- detectable failure: canned `"job_id"` asserted back; service is pass-through — verifies mock wiring only.
- non-test callers: service methods ← `internal/app/jobs` + gRPC handlers; mocks stay.
- stronger proof: repository integrations (`jobs_integration_test.go:158,258`, `lease_integration_test.go:23`) + handler tests (`server/jobs_handlers_test.go`) + `app/jobs/jobs_test.go`. Overlap list: `ScheduleJob` 5 files, `GetJob` 7 files, `GetWorkflow` 12 files, `ClaimJob` 6 files — keep one contract test per layer boundary.
- history: confirm `git log --oneline -5 -- internal/service/jobs/`.
- deletion unlocked: test-only — collapse per-method tables to one error-path + rely on integration for success.
- risk + validation: Medium (large file, many callers). `go test -short ./internal/service/jobs/... ./internal/repository/jobs/... ./internal/app/jobs/... -count=1`

## P. Test re-implements prod hash — `getJobLogsCacheKeyForTest`

- exact test helper: `internal/service/jobs/jobs_test.go:1428-1435` mirrors prod `jobs.go:1040-1043` (both `sha256.Sum256(strings.Join(...+\"\\x00\"))` → `"job_logs:%s:%x"`); used 8× (`:773,839,915,992,1034,1136,1175,1250`) to set `cache.EXPECT().Get(cacheKey,...)`
- detectable failure: duplicate hash must drift in lockstep — prod key change passes only if test copy edited identically; asserts its own copy.
- non-test callers: prod `jobLogsCacheKey` ← cache read/write paths in `jobs.go`; test helper ← 8 test sites only.
- stronger proof: call prod helper (same package) or assert cache-miss behavior instead of precomputing key.
- history: confirm `git log --oneline -5 -- internal/service/jobs/jobs_test.go`.
- deletion unlocked: test-support — delete helper, call prod fn (net-negative test LOC).
- risk + validation: Low. `go test -short ./internal/service/jobs/ -run TestGetJobLogs -count=1 -v` (confirm exact `-run` filter from `go test -list`).

## Q. Dashboard — tautological prefix claim (`client.test.ts:82`)

- exact test: `"uses the configured base so edge path prefixes are kept"` — `dashboard/src/lib/api/client.test.ts:82` (`fetchApi` describe; stubs fetch, calls `fetchApi("/workflows")`, asserts `url === apiEndpoints.auth.csrf` where `client.ts:18` IS `fetch(apiEndpoints.auth.csrf)` and `NEXT_PUBLIC_API_URL` unset → both `"/auth/csrf"`, true by construction)
- detectable failure: misses hardcoding `fetch("/auth/csrf")` (drops base logic name claims). Real prefix pinned only by `endpoints.test.ts:29-34`.
- non-test callers: `fetchApi` widely used (`use-notifications`, `use-users`, `use-workflow-jobs`, `use-job-details`, `use-job-logs`, `use-workflow-details`, `use-workflows`, `use-auth`) — none depend on this `it`; contract lives in `endpoints.ts:5-46`.
- stronger proof: delete `it`; rely on `endpoints.test.ts:29`. Or rewrite with explicit base (`createApiEndpoints("https://example.com/api")`) + literal assert.
- history: `c5500197 (#145)`.
- deletion unlocked: whole `it` block; prefix cover survives.
- risk + validation: Low. `npx vitest run src/lib/api/client.test.ts` (30 passed in combined 5-file run)

## R. Dashboard — name claims fetch gating, exercises boolean (`user-preferences.test.ts:22`)

- exact test: `"does not fetch before the user is available"` — `dashboard/src/features/users/user-preferences.test.ts:22` (`expect(canReceiveNotifications(undefined)).toBe(false)`; source 1-liner `Boolean(user && pref!=="NONE")`, no fetch)
- detectable failure: misses `use-notifications.ts:45` → `enabled:true` (fetch always) — still passes. Only proves boolean.
- non-test callers: sole importer `use-notifications.ts:18,45` (`enabled` flag).
- stronger proof: delete `it` (`NONE→false` at line 18 already pins falsy branch) or replace with hook test asserting `useInfiniteQuery` gets `enabled:false` when `useUsers()` undefined.
- history: `bd7d1549 (#100)`.
- deletion unlocked: single `it` at line 22; keep `ALL/ALERTS/NONE` truth-table rows.
- risk + validation: Low. `npx vitest run src/features/users/user-preferences.test.ts`

## S. Dashboard — exact Tailwind-class grep (`job-status.test.ts:31`)

- exact test: `"exposes animation metadata only for running states"` — `dashboard/src/features/jobs/job-status.test.ts:31` (`RUNNING.iconClass === "animate-spin"`, `COMPLETED → undefined`)
- detectable failure: rename to `animate-pulse` fails test though UI still spins (false alarm); removing `meta.iconClass` from badge render (animation actually lost) still passes (checks data, not rendering).
- non-test callers: `getStatusMeta` ← `workflow-card.tsx:32`, `job-status-badge.tsx:25`, `workflow-details-page.tsx:125`; `iconClass` consumed via `cn(...)` at 4 sites.
- stronger proof: assert presence/absence (`toBeDefined/Undefined`) or delete; animation belongs to visual/snapshot check. `normalizeStatus`/`getStatusLabel` tests keep logic cover.
- history: `bd7d1549 (#100)`.
- deletion unlocked: single `it` at line 31.
- risk + validation: Low. `npx vitest run src/features/jobs/job-status.test.ts`

## T. Dashboard — 78-line TanStack harness for 1-line predicate (`workflow-query-state.test.ts:17`)

- exact test: `"stays locked while the opening fetch is paused offline with stale cache"` — `dashboard/src/features/workflows/workflow-query-state.test.ts:17` (exemplar; siblings `:37,:60` share pattern; source `workflow-query-state.ts:10-12`: `return fetchStatus==="idle" && !error`)
- detectable failure: 4/5 asserts verify `@tanstack/query-core` internals (`paused`/`isFetching`/`error`/stale passthrough) via real `QueryClient`/`QueryObserver` + `onlineManager.setOnline(false)` + `tick(100)`. Misses deleting `isWorkflowDetailsReady(...)` from sole importer `update-workflow-dialog.tsx:90` (all observer asserts still pass). TanStack upgrade renaming `"paused"` fails test with zero repo change.
- non-test callers: sole importer `update-workflow-dialog.tsx:37,90` (open-fetch latch).
- stronger proof: collapse file to 4-line pure truth table (`idle→true`, `paused/fetching/idle+err→false`) — no QueryClient/timers/onlineManager. Other two `it`s already pin both helper outcomes.
- history: `361c8de6 (#160)`.
- deletion unlocked: delete this `it` (or collapse file to truth table); removes flaky 100ms sleeps + global online-manager mutation.
- risk + validation: Low. `npx vitest run src/features/workflows/workflow-query-state.test.ts`

---

## Explicit non-candidates (retained, why valuable)

- Self-comparison: `assert.Equal(x,x)` / `expect(x).toBe(x)` → 0 hits (`rg --pcre2`, dashboard 0). Near-miss `idempotency_test.go:93,96` (upper vs canonical `ClaimCommandID`) is intentional canonicalization — needs owner read, not junk.
- Test-only seams / dead prod: `claimJobQuery/validateClaimReplayQuery/lockRuntimeNodeQuery/staleRuntimeHeartbeatAt/scheduleJobHashFields/dedupeLogsInBatch/meiliLogDocumentID/deleteWorkflowLogsClickHouseQuery` all have prod callers; exported `LockKey/LogEventKey/CanonicalJSON/ClaimCommandID` each 1 prod caller + N tests (normal). Only zero-prod-caller hit is `testkit.WithPostgres` (intentional infra). No high-confidence dead code.
- Dashboard overlaps deliberately not filed: `workflow-schemas` vs `interval-filter` (zod schema vs list-filter normalizer, independently hardcoded `1..10080` — fix is unifying constant, deleting either loses helper cover); `use-job-logs` heavy `vi.mock` (pins hook wiring `enabled`/SSE cleanup/URL params, no cheaper oracle); `render-states` exact strings (`"0 / 3"`, `"width:0%"`, `"Runs every 1 hour"`) pin user-visible empty/failure states, not internal ids.
- `service/workflow/workflow_test.go:31-63` (`mockRepo.EXPECT().Run→nil` / `status.Error Internal` + `errors.Is` without wrap) — trivial mock, but needs owner read to confirm `s.Run` is pure delegation before filing.

## Next steps (no edits yet)

1. Owner confirms one coherent batch (e.g. A–E test-only deletions, or F–J storage/workers, or Q–T dashboard).
2. Per-skill validation on that batch only: `go test -short` owner+siblings / `npx vitest run <path>`, targeted formatting, `git diff --check`, changed-gate classification, `git diff --numstat` (prod vs test), mandatory `$autoreview`.
3. Land one PR at a time via repo `scripts/pr` flow; refresh from `main` and rerun read-only discovery for next batch. Follow-ups: shared `newTLSConfig` extraction (D), `LockKey`/`LogEventKey` literalization (L/M), query-integration promotion (N), per-layer contract keepers (O), cache-key helper collapse (P).
