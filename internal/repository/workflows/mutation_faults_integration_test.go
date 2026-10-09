//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package workflows

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

// failStatementFunction is the trigger body every fault trigger in this file
// calls. It raises a plain SQL error, so the command under test sees the same
// kind of failure a broken trigger, an extension or a server-side cancellation
// produces, and its own error classification decides what that means.
const failStatementFunction = `
	CREATE OR REPLACE FUNCTION cv_test_fail_statement() RETURNS trigger
	LANGUAGE plpgsql AS $body$
	BEGIN
		RAISE EXCEPTION 'injected statement failure on %', TG_TABLE_NAME;
	END;
	$body$
`

// lockWorkflowWrite stops writes to every workflow row while leaving reads of
// them free.
//
// SHARE ROW EXCLUSIVE is what makes the split possible: it conflicts with the
// ROW EXCLUSIVE an UPDATE takes but not with the ROW SHARE a SELECT ... FOR
// UPDATE takes. A command therefore passes the guards that read the row and then
// waits on the statement that writes it, which a row lock or an ACCESS EXCLUSIVE
// table lock cannot do because they would stop the read first.
func lockWorkflowWrite(pattern string) interruptionLock {
	return interruptionLock{
		label:   "writes to the " + postgres.TableWorkflows + " table",
		lockSQL: "LOCK TABLE " + postgres.TableWorkflows + " IN SHARE ROW EXCLUSIVE MODE",
		pattern: pattern,
	}
}

// lockWorkflowStateRead stops a command at the row lock its own state read takes,
// so every mutation that depends on that state never runs.
func lockWorkflowStateRead(fixture *workflowFixture) interruptionLock {
	return lockWorkflowRow(fixture.WorkflowID, "%SELECT kind, build_hash, generation%")
}

// lockTerminateStateRead stops a termination at the generation read it takes its
// row lock on. The pattern names that read's own projection, not a general
// workflows read, so a probe cannot mistake another command's read for it.
func lockTerminateStateRead(*workflowFixture) interruptionLock {
	return lockWholeTable(postgres.TableWorkflows, "%SELECT generation%FROM "+postgres.TableWorkflows+"%")
}

// lockLegacyIdentities stops a command at the rollback-compatible identity it
// records. The ledger reservation that precedes it writes a different table, so
// this lock stops the identity write without disturbing anything before it.
func lockLegacyIdentities(*workflowFixture) interruptionLock {
	return lockWholeTable(
		postgres.TableCommandIdempotencyLegacyIdentities,
		"%"+postgres.TableCommandIdempotencyLegacyIdentities+"%",
	)
}

// cancelBlockedStatement cancels, inside PostgreSQL, the statement the command is
// itself running. This is the only way to reach the internal-failure arm of an
// error chain whose other arms are caller cancellation, deadline expiry, a
// missing row and a malformed identifier: no client-side context changes, so the
// error the command sees is a server-side query cancellation and nothing else.
func cancelBlockedStatement(ctx context.Context, t *testing.T, pg *postgres.Postgres, lock interruptionLock) {
	t.Helper()

	probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), blockedStatementTimeout)
	defer cancel()

	var pid int
	if err := pg.QueryRow(probeCtx, `
		SELECT pid
		FROM pg_stat_activity
		WHERE datname = current_database()
			AND pid <> pg_backend_pid()
			AND state = 'active'
			AND wait_event_type = 'Lock'
			AND query LIKE $1
		LIMIT 1
	`, lock.pattern).Scan(&pid); err != nil {
		t.Fatalf("find the backend blocked in %q: %v", lock.pattern, err)
	}

	var canceled bool
	if err := pg.QueryRow(probeCtx, `SELECT pg_cancel_backend($1)`, pid).Scan(&canceled); err != nil {
		t.Fatalf("cancel the backend blocked in %q: %v", lock.pattern, err)
	}
	if !canceled {
		t.Fatalf("pg_cancel_backend(%d) reported that no statement was canceled", pid)
	}
}

// faultTrigger installs a trigger that raises on one event of one table and
// removes it again when the test ends.
//
// An immediate trigger fires while the command is running the affected statement,
// so the failure lands on the command's own write. A deferred trigger queues the
// check instead and PostgreSQL runs it at COMMIT, which is the one failure a
// command whose every statement succeeded still has to roll back. The two are
// distinct fixtures because they exercise distinct statements of the same command.
func faultTrigger(ctx context.Context, t *testing.T, pg *postgres.Postgres, event string, deferred bool) {
	t.Helper()

	if _, err := pg.Exec(ctx, failStatementFunction); err != nil {
		t.Fatalf("create the fault trigger function: %v", err)
	}
	// PostgreSQL accepts DEFERRABLE only on a constraint trigger, so a deferred
	// fault has to be declared as one.
	triggerKind, timing := "TRIGGER", ""
	if deferred {
		triggerKind, timing = "CONSTRAINT TRIGGER", "DEFERRABLE INITIALLY DEFERRED"
	}
	name := "cv_test_" + fixtureTag()
	create := fmt.Sprintf(`
		CREATE %s %s
		AFTER %s ON %s
		%s
		FOR EACH ROW EXECUTE FUNCTION cv_test_fail_statement()
	`, triggerKind, name, event, postgres.TableWorkflows, timing)
	if _, err := pg.Exec(ctx, create); err != nil {
		t.Fatalf("install the failing %s trigger on %s: %v", event, postgres.TableWorkflows, err)
	}
	// PostgreSQL silently defaults a trigger whose DEFERRABLE clause is missing to
	// IMMEDIATE, which would turn a deferred fault into a statement-level one and
	// leave the commit path untested while the test still passed. Read the
	// installed trigger back and fail loudly instead.
	var deferrable, initDeferred bool
	if err := pg.QueryRow(ctx, `
		SELECT tgdeferrable, tginitdeferred FROM pg_trigger WHERE tgname = $1
	`, name).Scan(&deferrable, &initDeferred); err != nil {
		t.Fatalf("read back the failing %s trigger on %s: %v", event, postgres.TableWorkflows, err)
	}
	if deferrable != deferred || initDeferred != deferred {
		t.Fatalf(
			"the failing %s trigger on %s is deferrable=%v initdeferred=%v, want both %v",
			event, postgres.TableWorkflows, deferrable, initDeferred, deferred,
		)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), fixtureCleanupTimeout)
		defer cleanupCancel()
		if _, err := pg.Exec(
			cleanupCtx,
			fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON %s`, name, postgres.TableWorkflows),
		); err != nil {
			t.Errorf("drop the failing %s trigger on %s: %v", event, postgres.TableWorkflows, err)
		}
	})
}

// failLedgerCompletionWith installs a trigger that refuses every UPDATE of the
// command ledger. A fresh reservation inserts its row and writes its rollback
// identity, both of which still succeed, so the first ledger write this refuses is
// the completion that would have made the mutation replayable.
func failLedgerCompletionWith(ctx context.Context, t *testing.T, pg *postgres.Postgres) {
	t.Helper()

	if _, err := pg.Exec(ctx, failStatementFunction); err != nil {
		t.Fatalf("create the fault trigger function: %v", err)
	}
	name := "cv_test_" + fixtureTag()
	create := fmt.Sprintf(`
		CREATE TRIGGER %s
		BEFORE UPDATE ON %s
		FOR EACH ROW EXECUTE FUNCTION cv_test_fail_statement()
	`, name, postgres.TableCommandIdempotencyKeys)
	if _, err := pg.Exec(ctx, create); err != nil {
		t.Fatalf("install the failing UPDATE trigger on %s: %v", postgres.TableCommandIdempotencyKeys, err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), fixtureCleanupTimeout)
		defer cleanupCancel()
		if _, err := pg.Exec(
			cleanupCtx,
			fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON %s`, name, postgres.TableCommandIdempotencyKeys),
		); err != nil {
			t.Errorf("drop the failing UPDATE trigger on %s: %v", postgres.TableCommandIdempotencyKeys, err)
		}
	})
}

// callerStopPlan turns one case's declared way of stopping a blocked command into
// the caller context and the step applied while it waits. The three shapes are
// distinct outcomes rather than variations of one: an explicit cancellation and an
// expired deadline are reported as the caller's own failure with different codes,
// while a cancellation issued inside PostgreSQL is a database failure.
func callerStopPlan(
	ctx context.Context,
	t *testing.T,
	pg *postgres.Postgres,
	stop string,
	lock interruptionLock,
) (context.Context, blockedStep) {
	t.Helper()

	switch stop {
	case "cancel":
		return ctx, blockedStep(cancelCaller)
	case "deadline":
		callerCtx, cancelDeadline := context.WithTimeout(ctx, callerDeadlineDelay)
		t.Cleanup(cancelDeadline)
		return callerCtx, blockedStep(letCallerDeadlineExpire)
	default:
		return ctx, func(pgx.Tx, context.CancelFunc) { cancelBlockedStatement(ctx, t, pg, lock) }
	}
}

// assertNotRefusedAsMissing proves a failure is not being reported as one of the
// refusals the same command can legitimately return. Those messages carry the
// caller's own doing, so a database failure reported as one of them would tell the
// caller to change a request that is fine.
func assertNotRefusedAsMissing(t *testing.T, name string, err error, refusals ...string) {
	t.Helper()

	message := status.Convert(err).Message()
	for _, refusal := range refusals {
		if message == refusal {
			t.Errorf("%s reported %q, which is a refusal rather than the failure that stopped it", name, message)
		}
	}
}

// assertMutationUndone proves a command that reported a failure applied none of
// itself: the workflow keeps every durable column, its queued work stays queued,
// nothing was published and the ledger key is free for a retry that may proceed.
func assertMutationUndone(
	ctx context.Context,
	t *testing.T,
	pg *postgres.Postgres,
	fixture *workflowFixture,
	before *workflowMutationState,
	eventsBefore int,
	queuedJobID string,
	key string,
	name string,
) {
	t.Helper()

	assertMutationState(t, name, readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID), before)
	if count := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID); count != eventsBefore {
		t.Errorf("outbox events after %s = %d, want unchanged %d", name, count, eventsBefore)
	}
	queued, ok := readFixtureJobState(ctx, t, pg, queuedJobID)
	if !ok {
		t.Fatalf("queued job disappeared after %s", name)
	}
	if queued.Status != "PENDING" || queued.TerminalReasonCode.Valid {
		t.Errorf("queued job after %s = %+v, want it still PENDING with no terminal reason", name, queued)
	}
	if count := countCommandKeys(ctx, t, pg, key); count != 0 {
		t.Errorf("ledger rows for %s = %d, want 0", name, count)
	}
}

// TestIntegrationUpdateWorkflowStateReadReportsEachOutcome proves the state read
// every update depends on tells a caller that went away or ran out of time apart
// from a database failure, and refuses to continue on all three. A row lock the
// read itself waits on is what makes the interruption land on this statement
// rather than on an earlier one.
func TestIntegrationUpdateWorkflowStateReadReportsEachOutcome(t *testing.T) {
	cases := []struct {
		name string
		// callerStop is how the caller's own context stops the command: an
		// explicit cancellation, or a deadline that expires on its own. An empty
		// value leaves the caller alone and lets PostgreSQL cancel the statement,
		// which is what reaches the internal-failure arm.
		callerStop string
		wantCode   codes.Code
	}{
		{name: "CanceledCaller", callerStop: "cancel", wantCode: codes.Canceled},
		{name: "ExpiredDeadline", callerStop: "deadline", wantCode: codes.DeadlineExceeded},
		{name: "CanceledInsidePostgres", wantCode: codes.Internal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			pg := testkit.Postgres(t)
			repo := newTestRepository(t)

			fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
			queuedJobID := seedFixturePendingJob(ctx, t, pg, fixture, "AUTOMATIC", 0)
			before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
			eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)
			updateKey := "cv-state-" + fixtureTag()

			lock := lockWorkflowStateRead(fixture)
			callerCtx, step := callerStopPlan(ctx, t, pg, tc.callerStop, lock)

			err := runWhileBlocked(
				callerCtx, t, pg, lock, step,
				func(commandCtx context.Context) error {
					return repo.UpdateWorkflow(
						commandCtx, fixture.WorkflowID, fixture.UserID, "cv-interrupted-"+fixtureTag(), fixturePayload,
						fixture.Interval*2, fixture.MaxConsecutiveJobFailures, updateKey,
					)
				},
			)
			assertCode(t, tc.name, err, tc.wantCode)
			assertNotRefusedAsMissing(t, tc.name, err, "workflow not found")
			assertMutationUndone(ctx, t, pg, fixture, before, eventsBefore, queuedJobID, updateKey, tc.name)
		})
	}
}

// TestIntegrationUpdateWorkflowWriteRefusedRollsBackTheUpdate proves the workflow
// mutation is reported against the write that failed, and that failing write leaves
// the workflow, its queued work, the outbox and the ledger key exactly as they
// were.
func TestIntegrationUpdateWorkflowWriteRefusedRollsBackTheUpdate(t *testing.T) {
	cases := []struct {
		name string
		// callerStop is how the caller's own context stops the command: an
		// explicit cancellation, or a deadline that expires on its own. An empty
		// value leaves the caller alone and lets PostgreSQL cancel the statement,
		// which is what reaches the internal-failure arm.
		callerStop string
		wantCode   codes.Code
	}{
		{name: "CanceledCaller", callerStop: "cancel", wantCode: codes.Canceled},
		{name: "ExpiredDeadline", callerStop: "deadline", wantCode: codes.DeadlineExceeded},
		{name: "RefusedByPostgres", wantCode: codes.Internal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			pg := testkit.Postgres(t)
			repo := newTestRepository(t)

			fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
			queuedJobID := seedFixturePendingJob(ctx, t, pg, fixture, "AUTOMATIC", 0)
			before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
			eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)
			updateKey := "cv-write-" + fixtureTag()

			// SHARE ROW EXCLUSIVE lets the state read through and stops the
			// UPDATE, which is the statement under test.
			lock := lockWorkflowWrite("%UPDATE " + postgres.TableWorkflows + "%SET name%")
			callerCtx, step := callerStopPlan(ctx, t, pg, tc.callerStop, lock)

			err := runWhileBlocked(
				callerCtx, t, pg, lock, step,
				func(commandCtx context.Context) error {
					return repo.UpdateWorkflow(
						commandCtx, fixture.WorkflowID, fixture.UserID, "cv-blocked-"+fixtureTag(), fixturePayload,
						fixture.Interval*2, fixture.MaxConsecutiveJobFailures, updateKey,
					)
				},
			)
			assertCode(t, tc.name, err, tc.wantCode)
			assertNotRefusedAsMissing(t, tc.name, err, "workflow not found")
			assertMutationUndone(ctx, t, pg, fixture, before, eventsBefore, queuedJobID, updateKey, tc.name)
		})
	}
}

// TestIntegrationUpdateWorkflowLedgerWriteRefusedRollsBackTheUpdate proves the
// rollback-compatible identity and the completion that make an update replayable
// belong to the same transaction as the mutation. When either cannot be recorded,
// none of what the update had already written survives it, and the key stays free.
func TestIntegrationUpdateWorkflowLedgerWriteRefusedRollsBackTheUpdate(t *testing.T) {
	t.Run("IdentityWriteRefused", func(t *testing.T) {
		ctx := context.Background()
		pg := testkit.Postgres(t)
		repo := newTestRepository(t)

		fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
		queuedJobID := seedFixturePendingJob(ctx, t, pg, fixture, "AUTOMATIC", 0)
		before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
		eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)
		updateKey := "cv-identity-" + fixtureTag()
		lock := lockLegacyIdentities(fixture)

		_, release := holdStatementLock(ctx, t, pg, lock)
		defer release()

		commandCtx, cancelCommand := context.WithCancel(ctx)
		defer cancelCommand()
		done := make(chan error, 1)
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			done <- repo.UpdateWorkflow(
				commandCtx, fixture.WorkflowID, fixture.UserID, "cv-blocked-"+fixtureTag(), fixturePayload,
				fixture.Interval*2, fixture.MaxConsecutiveJobFailures, updateKey,
			)
		}()
		// A fatal probe or step failure must still release the lock and join the
		// command goroutine, or the lock outlives the test.
		defer func() {
			cancelCommand()
			release()
			select {
			case <-finished:
			case <-time.After(blockedStatementTimeout):
				t.Errorf("command did not stop after releasing %s", lock.label)
			}
		}()

		waitForBlockedStatement(ctx, t, pg, lock, done)
		cancelCaller(nil, cancelCommand)

		assertCode(t, "UpdateWorkflow (identity write refused)", <-done, codes.Internal)
		assertMutationUndone(ctx, t, pg, fixture, before, eventsBefore, queuedJobID, updateKey, "identity write refused")
	})

	t.Run("CompletionRefused", func(t *testing.T) {
		ctx := context.Background()
		pg := testkit.Postgres(t)
		repo := newTestRepository(t)

		fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
		queuedJobID := seedFixturePendingJob(ctx, t, pg, fixture, "AUTOMATIC", 0)
		before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
		eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)
		updateKey := "cv-complete-" + fixtureTag()

		failLedgerCompletionWith(ctx, t, pg)

		assertCode(
			t, "UpdateWorkflow (completion refused)",
			repo.UpdateWorkflow(
				ctx, fixture.WorkflowID, fixture.UserID, "cv-uncompletable", fixturePayload,
				fixture.Interval*2, fixture.MaxConsecutiveJobFailures, updateKey,
			),
			codes.Internal,
		)
		assertMutationUndone(ctx, t, pg, fixture, before, eventsBefore, queuedJobID, updateKey, "completion refused")
	})
}

// TestIntegrationUpdateWorkflowCommitRefusedRollsBackTheUpdate proves a deferred
// commit failure takes the whole update with it. Every statement has already
// succeeded by then, so the generation bump, the publish intent and the
// stale-work invalidation are all written and only the rollback can undo them.
func TestIntegrationUpdateWorkflowCommitRefusedRollsBackTheUpdate(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
	queuedJobID := seedFixturePendingJob(ctx, t, pg, fixture, "AUTOMATIC", 0)
	before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
	eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)
	updateKey := "cv-commit-" + fixtureTag()

	faultTrigger(ctx, t, pg, "UPDATE", true)

	assertCode(
		t, "UpdateWorkflow (commit refused)",
		repo.UpdateWorkflow(
			ctx, fixture.WorkflowID, fixture.UserID, "cv-uncommittable", fixturePayload,
			fixture.Interval*2, fixture.MaxConsecutiveJobFailures, updateKey,
		),
		codes.Internal,
	)
	assertMutationUndone(ctx, t, pg, fixture, before, eventsBefore, queuedJobID, updateKey, "commit refused")
}

// TestIntegrationDeleteWorkflowQueryFailureIsNotAMissingWorkflow proves the three
// guards and the delete itself distinguish a database failure from the refusals
// the same command legitimately returns. Canceling each statement inside
// PostgreSQL is what reaches the internal-failure arm: a caller-side cancellation
// can only ever be reported as the cancellation it is, and the identity checks
// upstream of it never see a malformed value.
func TestIntegrationDeleteWorkflowQueryFailureIsNotAMissingWorkflow(t *testing.T) {
	const missingWorkflow = "workflow not found or not owned by user"

	cases := []struct {
		name string
		lock func(*workflowFixture) interruptionLock
	}{
		{name: "WhileReadingTheWorkflow", lock: lockWorkflowTableRead},
		{name: "WhileReadingRunningJobs", lock: lockJobsTableRead},
		{name: "WhileDeleting", lock: lockWorkflowDelete},
	}

	for _, tc := range cases {
		for _, serverSide := range []bool{true, false} {
			name := tc.name + "ByPostgres"
			wantCode := codes.Internal
			if !serverSide {
				name = tc.name
				wantCode = codes.Canceled
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				pg := testkit.Postgres(t)
				repo := newTestRepository(t)

				fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
				seedDeleteFixture(ctx, t, pg, repo, fixture)
				before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
				eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)

				lock := tc.lock(fixture)
				step := blockedStep(cancelCaller)
				if serverSide {
					step = func(pgx.Tx, context.CancelFunc) { cancelBlockedStatement(ctx, t, pg, lock) }
				}

				err := runWhileBlocked(
					ctx, t, pg, lock, step,
					func(commandCtx context.Context) error {
						return repo.DeleteWorkflow(commandCtx, fixture.WorkflowID, fixture.UserID)
					},
				)
				assertCode(t, name, err, wantCode)
				assertNotRefusedAsMissing(t, name, err, missingWorkflow, "invalid workflow or user ID")
				assertWorkflowSurvivedDeletion(ctx, t, pg, fixture, before, eventsBefore, name)
			})
		}
	}
}

// TestIntegrationDeleteWorkflowCommitRefusedRestoresTheWorkflow proves the delete
// and its publish intent commit together: by the time the commit refuses them the
// row is already gone from this transaction, and only the rollback restores it.
func TestIntegrationDeleteWorkflowCommitRefusedRestoresTheWorkflow(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
	seedDeleteFixture(ctx, t, pg, repo, fixture)
	before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
	eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)

	faultTrigger(ctx, t, pg, "DELETE", true)

	assertCode(
		t, "DeleteWorkflow (commit refused)",
		repo.DeleteWorkflow(ctx, fixture.WorkflowID, fixture.UserID),
		codes.Internal,
	)
	assertWorkflowSurvivedDeletion(ctx, t, pg, fixture, before, eventsBefore, "commit refused")
}

// TestIntegrationTerminateWorkflowReportsEachOutcome proves the termination path
// classifies its failures the way the other mutations do, and applies none of
// itself when it cannot classify one. Termination takes no idempotency key, so
// what each case has to leave behind is only the workflow and its publish intent.
func TestIntegrationTerminateWorkflowReportsEachOutcome(t *testing.T) {
	const missingWorkflow = "workflow not found or not owned by user"

	// A caller-side stop can land on either the state read or the termination
	// write, and both have to be reported as the cancellation they are. A
	// server-side cancellation on the same two statements is the only way to
	// reach their internal-failure arms, which no caller-side stop can reach.
	interruptions := []struct {
		name string
		lock func(*workflowFixture) interruptionLock
	}{
		{name: "WhileReadingTheWorkflow", lock: lockTerminateStateRead},
		{name: "WhileTerminating", lock: func(*workflowFixture) interruptionLock {
			return lockWorkflowWrite("%UPDATE " + postgres.TableWorkflows + "%SET terminated_at%")
		}},
	}
	for _, tc := range interruptions {
		for _, serverSide := range []bool{true, false} {
			name := tc.name + "ByPostgres"
			wantCode := codes.Internal
			if !serverSide {
				name = tc.name
				wantCode = codes.Canceled
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				pg := testkit.Postgres(t)
				repo := newTestRepository(t)

				fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
				before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
				eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)

				lock := tc.lock(fixture)
				step := blockedStep(cancelCaller)
				if serverSide {
					step = func(pgx.Tx, context.CancelFunc) { cancelBlockedStatement(ctx, t, pg, lock) }
				}

				err := runWhileBlocked(
					ctx, t, pg, lock, step,
					func(commandCtx context.Context) error {
						return repo.TerminateWorkflow(commandCtx, fixture.WorkflowID, fixture.UserID)
					},
				)
				assertCode(t, name, err, wantCode)
				assertNotRefusedAsMissing(t, name, err, missingWorkflow, "invalid workflow ID")
				assertTerminationUndone(ctx, t, pg, fixture, before, eventsBefore, name)
			})
		}
	}

	// The deadline cases pair with the cancellation ones above: a caller whose
	// clock runs out on the state read and one whose clock runs out on the
	// termination write are each reported as the deadline they are, which is a
	// distinct code from the cancellation and a retry signal rather than a
	// permanent failure.
	for _, tc := range []struct {
		name string
		lock func(*workflowFixture) interruptionLock
	}{
		{name: "DeadlineExpiredWhileReadingTheWorkflow", lock: lockTerminateStateRead},
		{name: "DeadlineExpiredWhileTerminating", lock: func(*workflowFixture) interruptionLock {
			return lockWorkflowWrite("%UPDATE " + postgres.TableWorkflows + "%SET terminated_at%")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			pg := testkit.Postgres(t)
			repo := newTestRepository(t)

			fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
			before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
			eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)

			callerCtx, cancelDeadline := context.WithTimeout(ctx, callerDeadlineDelay)
			defer cancelDeadline()

			err := runWhileBlocked(
				callerCtx, t, pg, tc.lock(fixture), letCallerDeadlineExpire,
				func(commandCtx context.Context) error {
					return repo.TerminateWorkflow(commandCtx, fixture.WorkflowID, fixture.UserID)
				},
			)
			assertCode(t, tc.name, err, codes.DeadlineExceeded)
			assertNotRefusedAsMissing(t, tc.name, err, missingWorkflow, "invalid workflow ID")
			assertTerminationUndone(ctx, t, pg, fixture, before, eventsBefore, tc.name)
		})
	}

	// A caller that is already gone fails before any statement runs. Termination
	// has no identity canonicalization to reject first, so this is where its
	// canceled outcome is reported from.
	t.Run("CallerAlreadyGone", func(t *testing.T) {
		ctx := context.Background()
		pg := testkit.Postgres(t)
		repo := newTestRepository(t)

		fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
		before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
		eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)

		goneCtx, cancelGone := context.WithCancel(ctx)
		cancelGone()

		assertCode(t, "TerminateWorkflow (caller already gone)", repo.TerminateWorkflow(goneCtx, fixture.WorkflowID, fixture.UserID), codes.Canceled)
		assertTerminationUndone(ctx, t, pg, fixture, before, eventsBefore, "caller already gone")
	})

	// Termination hands its identity to PostgreSQL as given, so a malformed one is
	// a refused request rather than a missing workflow, and an unknown one is
	// missing rather than malformed. Neither touches the workflow it names.
	t.Run("UnknownWorkflow", func(t *testing.T) {
		ctx := context.Background()
		pg := testkit.Postgres(t)
		repo := newTestRepository(t)

		fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
		before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
		eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)

		err := repo.TerminateWorkflow(ctx, uuid.NewString(), fixture.UserID)
		assertCode(t, "TerminateWorkflow (unknown workflow)", err, codes.NotFound)
		assertNotRefusedAsMissing(t, "TerminateWorkflow (unknown workflow)", err, "invalid workflow ID")
		assertTerminationUndone(ctx, t, pg, fixture, before, eventsBefore, "unknown workflow")
	})

	for _, tc := range []struct {
		name       string
		workflowID func(*workflowFixture) string
		userID     func(*workflowFixture) string
	}{
		{name: "MalformedWorkflowID", workflowID: func(*workflowFixture) string { return "not-a-uuid" }, userID: func(f *workflowFixture) string { return f.UserID }},
		{name: "MalformedUserID", workflowID: func(f *workflowFixture) string { return f.WorkflowID }, userID: func(*workflowFixture) string { return "not-a-uuid" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			pg := testkit.Postgres(t)
			repo := newTestRepository(t)

			fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
			before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
			eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)

			err := repo.TerminateWorkflow(ctx, tc.workflowID(fixture), tc.userID(fixture))
			assertCode(t, "TerminateWorkflow ("+tc.name+")", err, codes.InvalidArgument)
			assertTerminationUndone(ctx, t, pg, fixture, before, eventsBefore, tc.name)
		})
	}

	// The publish intent is the last statement before the commit, so a refusal
	// there is the case where the workflow row is already updated and only the
	// rollback can undo it.
	t.Run("PublishIntentRefused", func(t *testing.T) {
		ctx := context.Background()
		pg := testkit.Postgres(t)
		repo := newTestRepository(t)

		fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
		before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
		eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)

		lock := lockPublishIntent(
			fixture,
			idempotency.WorkflowEventKey(
				fixture.WorkflowID, workflowsmodel.ActionTerminate.ToString(), fixture.Generation,
			),
			"%INSERT INTO "+postgres.TableOutboxEvents+"%",
		)
		err := runWhileBlocked(
			ctx, t, pg, lock, cancelCaller,
			func(commandCtx context.Context) error {
				return repo.TerminateWorkflow(commandCtx, fixture.WorkflowID, fixture.UserID)
			},
		)
		assertCode(t, "TerminateWorkflow (unrecordable publish intent)", err, codes.Internal)
		assertTerminationUndone(ctx, t, pg, fixture, before, eventsBefore, "unrecordable publish intent")
	})

	t.Run("CommitRefused", func(t *testing.T) {
		ctx := context.Background()
		pg := testkit.Postgres(t)
		repo := newTestRepository(t)

		fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
		before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
		eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)

		faultTrigger(ctx, t, pg, "UPDATE", true)

		assertCode(
			t, "TerminateWorkflow (commit refused)",
			repo.TerminateWorkflow(ctx, fixture.WorkflowID, fixture.UserID),
			codes.Internal,
		)
		assertTerminationUndone(ctx, t, pg, fixture, before, eventsBefore, "commit refused")
	})
}

// assertTerminationUndone proves a failed termination neither terminated the
// workflow nor announced one.
func assertTerminationUndone(
	ctx context.Context,
	t *testing.T,
	pg *postgres.Postgres,
	fixture *workflowFixture,
	before *workflowMutationState,
	eventsBefore int,
	name string,
) {
	t.Helper()

	assertMutationState(t, name, readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID), before)
	if count := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID); count != eventsBefore {
		t.Errorf("outbox events after %s = %d, want unchanged %d", name, count, eventsBefore)
	}
	if count := countWorkflowActionEvents(
		ctx, t, pg, fixture.WorkflowID, workflowsmodel.ActionTerminate.ToString(),
	); count != 0 {
		t.Errorf("TERMINATE intents after %s = %d, want 0", name, count)
	}
}

// TestIntegrationBuildStatusReplayRefusesADifferentResult proves a build result the
// executor redelivers is applied once. Replaying the recorded identity commits
// nothing new, and the same build status carrying a different image is refused
// rather than overwriting what the first delivery recorded, which is the only thing
// that keeps a redelivered result from changing which image a later run executes.
func TestIntegrationBuildStatusReplayRefusesADifferentResult(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
	completeFixtureBuild(ctx, t, repo, fixture)
	settled := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
	eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)

	completed := workflowsmodel.WorkflowBuildStatusCompleted.ToString()
	replay := func(ref, digest string) error {
		return repo.UpdateWorkflowBuildStatus(
			ctx, fixture.WorkflowID, fixture.UserID, completed, fixture.Generation, ref, digest,
		)
	}

	if err := replay(fixture.ResolvedImageRef, fixture.ResolvedImageDigest); err != nil {
		t.Fatalf("UpdateWorkflowBuildStatus (replay): %v", err)
	}
	assertMutationState(t, "build status replay", readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID), settled)
	if count := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID); count != eventsBefore {
		t.Errorf("outbox events after the build status replay = %d, want unchanged %d", count, eventsBefore)
	}

	const otherDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	assertCode(
		t, "UpdateWorkflowBuildStatus (replay naming another digest)",
		replay(fixture.ResolvedImageRef, otherDigest),
		codes.FailedPrecondition,
	)
	assertMutationState(t, "refused build status replay", readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID), settled)
	if count := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID); count != eventsBefore {
		t.Errorf("outbox events after the refused replay = %d, want unchanged %d", count, eventsBefore)
	}
}
