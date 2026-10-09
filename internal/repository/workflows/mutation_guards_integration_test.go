//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package workflows

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

const (
	// blockedStatementPoll is how often the probe re-reads pg_stat_activity
	// while a command waits for a lock this test is holding.
	blockedStatementPoll = 2 * time.Millisecond
	// blockedStatementTimeout bounds how long a probe waits for a command to
	// reach the statement it is meant to interrupt. It guards the suite against
	// hanging; it is not an expectation any test should come close to.
	blockedStatementTimeout = 30 * time.Second
	// callerDeadlineDelay is the caller deadline used by the interruption cases
	// that want the deadline, rather than an explicit cancellation, to stop the
	// command. The clock starts before the lock is held and before the command
	// issues its first statement, so it has to cover that whole path on an
	// instrumented build rather than just the happy case: too short and the
	// deadline fires before the command reaches the statement, the command never
	// blocks, and the probe spins until blockedStatementTimeout before reporting
	// a stall that reads nothing like the deadline it was waiting for. The delay
	// also bounds each case, because the lock is released only after the command
	// returns, so the headroom is paid once by each case it guards.
	callerDeadlineDelay = 5 * time.Second
)

// workflowMutationState is every durable column a workflow mutation command can
// move. A command that is refused, replayed or interrupted has to leave the whole
// value alone, so comparing it in one shot keeps a "nothing changed" assertion
// from silently covering only the columns its author remembered.
type workflowMutationState struct {
	Name                   string
	Payload                string
	Interval               int32
	MaxConsecutiveFailures int32
	ConsecutiveFailures    int32
	BuildStatus            string
	BuildHash              sql.NullString
	Generation             int64
	terminatedAt           sql.NullTime
	ResolvedImageRef       sql.NullString
	ResolvedImageDigest    sql.NullString
}

// Terminated reports whether the workflow carries a durable termination instant.
func (s *workflowMutationState) Terminated() bool { return s.terminatedAt.Valid }

// readWorkflowMutationState reads the durable surface of one workflow row.
func readWorkflowMutationState(ctx context.Context, t *testing.T, pg *postgres.Postgres, workflowID string) *workflowMutationState {
	t.Helper()

	state := &workflowMutationState{}
	if err := pg.QueryRow(ctx, fmt.Sprintf(`
		SELECT name, payload::text, interval, max_consecutive_job_failures_allowed,
			consecutive_job_failures_count, build_status, build_hash, generation, terminated_at,
			resolved_image_ref, resolved_image_digest
		FROM %s
		WHERE id = $1
	`, postgres.TableWorkflows), workflowID).Scan(
		&state.Name,
		&state.Payload,
		&state.Interval,
		&state.MaxConsecutiveFailures,
		&state.ConsecutiveFailures,
		&state.BuildStatus,
		&state.BuildHash,
		&state.Generation,
		&state.terminatedAt,
		&state.ResolvedImageRef,
		&state.ResolvedImageDigest,
	); err != nil {
		t.Fatalf("read workflow mutation state for %q: %v", workflowID, err)
	}
	return state
}

// assertMutationState compares every durable column, so a partial mutation
// cannot pass as an unchanged workflow.
func assertMutationState(t *testing.T, name string, got, want *workflowMutationState) {
	t.Helper()

	if *got != *want {
		t.Errorf("%s workflow state = %+v, want %+v", name, got, want)
	}
}

// countCommandKeys counts durable ledger rows for one idempotency key in every
// user scope. A refused or rolled-back command must leave the key unconsumed, so
// the caller can still retry a request it is allowed to make.
func countCommandKeys(ctx context.Context, t *testing.T, pg *postgres.Postgres, idempotencyKey string) int {
	t.Helper()

	var count int
	query := fmt.Sprintf(`SELECT count(*) FROM %s WHERE idempotency_key = $1`, postgres.TableCommandIdempotencyKeys)
	if err := pg.QueryRow(ctx, query, idempotencyKey).Scan(&count); err != nil {
		t.Fatalf("count command ledger rows for key %q: %v", idempotencyKey, err)
	}
	return count
}

// repointCommandResource makes one completed ledger row resolve to another
// workflow. It is fixture SQL for a row whose recorded resource does not belong
// to the operation it is filed under, which is exactly what the replay guard
// exists to refuse; leaving that to production code would need a bug of that
// shape to exist first.
func repointCommandResource(
	ctx context.Context,
	t *testing.T,
	pg *postgres.Postgres,
	userID,
	workflowID,
	idempotencyKey,
	resourceID string,
) {
	t.Helper()

	tag, err := pg.Exec(ctx, fmt.Sprintf(`
		UPDATE %s
		SET resource_id = $4
		WHERE scope = $1 AND operation = $2 AND idempotency_key = $3
	`, postgres.TableCommandIdempotencyKeys),
		commandidempotency.UserScope(userID),
		commandidempotency.WorkflowUpdateOperation(workflowID),
		idempotencyKey,
		resourceID,
	)
	if err != nil {
		t.Fatalf("repoint command ledger resource for key %q: %v", idempotencyKey, err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("command ledger rows repointed for key %q = %d, want exactly 1", idempotencyKey, tag.RowsAffected())
	}
}

// interruptionLock is the fixture that stops a command at one of its own
// statements: `lockSQL` runs in a transaction of its own and stays held for the
// whole attempt, and `pattern` is the pg_stat_activity fragment that proves the
// command really reached the statement the lock guards.
type interruptionLock struct {
	label    string
	lockSQL  string
	lockArgs []any
	pattern  string
}

// lockWholeTable stops every statement that touches one table.
func lockWholeTable(table, pattern string) interruptionLock {
	return interruptionLock{
		label:   "the " + table + " table",
		lockSQL: fmt.Sprintf("LOCK TABLE %s IN ACCESS EXCLUSIVE MODE", table),
		pattern: pattern,
	}
}

// lockWorkflowRow stops writes to one workflow row while leaving reads of it
// free, which is what lets a command pass the guards that read the row and then
// wait on the statement that writes it.
func lockWorkflowRow(workflowID, pattern string) interruptionLock {
	return interruptionLock{
		label:    "the workflow row",
		lockSQL:  fmt.Sprintf("SELECT id::text FROM %s WHERE id = $1 FOR UPDATE", postgres.TableWorkflows),
		lockArgs: []any{workflowID},
		pattern:  pattern,
	}
}

// lockJobRow stops writes to one job row.
func lockJobRow(jobID, pattern string) interruptionLock {
	return interruptionLock{
		label:    "the queued job row",
		lockSQL:  fmt.Sprintf("SELECT id::text FROM %s WHERE id = $1 FOR UPDATE", postgres.TableJobs),
		lockArgs: []any{jobID},
		pattern:  pattern,
	}
}

// lockPublishIntent inserts the publish intent a command is about to write and
// leaves it uncommitted, so the command's own insert waits on that transaction. A
// concurrent duplicate command or a redrive already holding the same
// deterministic event key is the real-world shape of that contention. The payload
// is the event production would write, attributed to the fixture's own user, so
// the row stays inside the scope fixture cleanup deletes.
func lockPublishIntent(fixture *workflowFixture, eventKey, pattern string) interruptionLock {
	return interruptionLock{
		label: "the in-flight publish intent",
		lockSQL: fmt.Sprintf(`
			INSERT INTO %s (topic, kafka_key, event_key, payload)
			VALUES ($1, $2, $3, jsonb_build_object('ID', $4::text, 'UserID', $5::text))
		`, postgres.TableOutboxEvents),
		lockArgs: []any{
			kafka.TopicWorkflows, fixture.WorkflowID, eventKey, fixture.WorkflowID, fixture.UserID,
		},
		pattern: pattern,
	}
}

// lockWorkflowTableRead stops a deletion while it reads the workflow's
// termination state, the guard every deletion has to pass first.
func lockWorkflowTableRead(*workflowFixture) interruptionLock {
	return lockWholeTable(postgres.TableWorkflows, "%SELECT id::text, user_id::text, terminated_at%")
}

// lockJobsTableRead stops a deletion while it looks for a job still holding a
// runtime slot, after the workflow guard has already passed.
func lockJobsTableRead(*workflowFixture) interruptionLock {
	return lockWholeTable(postgres.TableJobs, "%SELECT id%FROM "+postgres.TableJobs+"%")
}

// lockWorkflowDelete stops a deletion at the delete itself, after both guards
// have passed and the command is committed to removing the row.
func lockWorkflowDelete(fixture *workflowFixture) interruptionLock {
	return lockWorkflowRow(fixture.WorkflowID, "%DELETE FROM "+postgres.TableWorkflows+"%")
}

// lockDeletePublishIntent stops a deletion at its last statement before the
// commit: recording the intent to announce the delete. The deterministic event
// key a deletion uses carries no generation, so the fixture holds exactly the
// row the command is about to write.
func lockDeletePublishIntent(fixture *workflowFixture) interruptionLock {
	return lockPublishIntent(
		fixture,
		idempotency.WorkflowEventKey(fixture.WorkflowID, workflowsmodel.ActionDelete.ToString(), 0),
		"%INSERT INTO "+postgres.TableOutboxEvents+"%",
	)
}

// seedDeleteFixture terminates a fixture workflow and gives it one queued job, the
// state every deletion under test starts from: the active-workflow guard and the
// running-job guard both pass, so the command reaches the statement being
// exercised.
func seedDeleteFixture(ctx context.Context, t *testing.T, pg *postgres.Postgres, repo *Repository, fixture *workflowFixture) {
	t.Helper()

	if err := repo.TerminateWorkflow(ctx, fixture.WorkflowID, fixture.UserID); err != nil {
		t.Fatalf("TerminateWorkflow: %v", err)
	}
	seedFixturePendingJob(ctx, t, pg, fixture, "AUTOMATIC", 0)
}

// assertWorkflowSurvivedDeletion proves an interrupted deletion applied none of
// itself: the row, its queued job and the publish intent are all still there.
func assertWorkflowSurvivedDeletion(
	ctx context.Context,
	t *testing.T,
	pg *postgres.Postgres,
	fixture *workflowFixture,
	before *workflowMutationState,
	eventsBefore int,
	name string,
) {
	t.Helper()

	// The row itself is a precondition for everything below: reading it is what
	// turns a missing row into a fatal "no rows" error, which would bury this
	// failure and skip the independent job and outbox checks.
	if count := countWorkflowRows(ctx, t, pg, fixture.WorkflowID); count != 1 {
		t.Fatalf("workflow rows after %s = %d, want 1", name, count)
	}
	assertMutationState(t, name, readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID), before)
	if count := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); count != 1 {
		t.Errorf("jobs after %s = %d, want 1", name, count)
	}
	if count := countWorkflowActionEvents(ctx, t, pg, fixture.WorkflowID, workflowsmodel.ActionDelete.ToString()); count != 0 {
		t.Errorf("DELETE outbox events after %s = %d, want 0", name, count)
	}
	if count := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID); count != eventsBefore {
		t.Errorf("outbox events after %s = %d, want unchanged %d", name, count, eventsBefore)
	}
}

// holdStatementLock takes lockSQL in a transaction of its own and keeps whatever
// it locks until the returned function runs, or until the test ends. Each test
// binary owns its own PostgreSQL container and this package's integration tests
// do not run in parallel, so the lock reaches no other test.
func holdStatementLock(
	ctx context.Context,
	t *testing.T,
	pg *postgres.Postgres,
	lock interruptionLock,
) (blocking pgx.Tx, release func()) {
	t.Helper()

	tx, err := pg.BeginTx(ctx)
	if err != nil {
		t.Fatalf("begin transaction holding %s: %v", lock.label, err)
	}
	blocking = tx
	if _, err = tx.Exec(ctx, lock.lockSQL, lock.lockArgs...); err != nil {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
			t.Errorf("rollback transaction holding %s: %v", lock.label, rollbackErr)
		}
		t.Fatalf("hold %s: %v", lock.label, err)
	}

	var once sync.Once
	release = func() {
		once.Do(func() {
			releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fixtureCleanupTimeout)
			defer cancel()
			if err := tx.Rollback(releaseCtx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
				t.Errorf("release %s: %v", lock.label, err)
			}
		})
	}
	t.Cleanup(release)
	return blocking, release
}

// waitForBlockedStatement waits until a backend is waiting on a lock while
// running a statement matching pattern. That is how a test learns the command
// under test really reached the statement the fixture blocks, instead of failing
// on an earlier one and pretending the intended path ran.
//
// It also gives up as soon as the command returns. A caller-deadline case can end
// the command before it ever reaches the blocked statement — on a loaded runner
// the deadline can beat the command's own preamble — and polling to the full
// blockedStatementTimeout in that case would report a stall that says nothing
// about the deadline that actually caused it.
func waitForBlockedStatement(
	ctx context.Context,
	t *testing.T,
	pg *postgres.Postgres,
	lock interruptionLock,
	commandDone <-chan error,
) {
	t.Helper()

	// The probe has to outlive the command it watches: a caller-deadline case
	// ends the command's own context on purpose.
	probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), blockedStatementTimeout)
	defer cancel()

	// The error is drained from the channel here only when the command finished
	// without ever blocking, which is a failure the caller needs reported; the
	// normal path leaves it for runWhileBlocked to read.
	commandErr := func() error {
		select {
		case err := <-commandDone:
			return err
		default:
			return errCommandStillRunning
		}
	}

	for {
		var waiting int
		if err := pg.QueryRow(probeCtx, `
			SELECT count(*)
			FROM pg_stat_activity
			WHERE datname = current_database()
				AND pid <> pg_backend_pid()
				AND state = 'active'
				AND wait_event_type = 'Lock'
				AND query LIKE $1
		`, lock.pattern).Scan(&waiting); err != nil {
			t.Fatalf("probe for a backend blocked in %q: %v", lock.pattern, err)
		}
		if waiting > 0 {
			return
		}

		// A command that has already returned can never block afterwards, so there
		// is nothing left to wait for.
		if err := commandErr(); !errors.Is(err, errCommandStillRunning) {
			t.Fatalf(
				"command returned (%v) without ever blocking on %s while running %q, so the intended interruption never happened",
				err, lock.label, lock.pattern,
			)
		}

		select {
		case <-probeCtx.Done():
			t.Fatalf("no backend waited on a lock while running %q within %s", lock.pattern, blockedStatementTimeout)
		case <-time.After(blockedStatementPoll):
		}
	}
}

// errCommandStillRunning marks the probe's non-blocking read of the command's
// result channel, so a still-pending command is distinguishable from a command
// that returned nil.
var errCommandStillRunning = errors.New("command still running")

// blockedStep is what a test does to a command while it is blocked: it receives
// the blocking transaction and the cancel function of the command's own context.
type blockedStep func(blocking pgx.Tx, cancelCommand context.CancelFunc)

// cancelCaller ends the command's context, which is how a caller that goes away
// reaches the repository.
func cancelCaller(_ pgx.Tx, cancelCommand context.CancelFunc) { cancelCommand() }

// letCallerDeadlineExpire leaves the command's deadline to fire on its own.
func letCallerDeadlineExpire(pgx.Tx, context.CancelFunc) {}

// runWhileBlocked runs command while a transaction of its own holds lock, waits
// until PostgreSQL reports the command blocked at the statement lock guards,
// applies step, then waits for the command to finish. The lock is released before
// it returns so the caller can assert durable state, and command's error is
// returned.
func runWhileBlocked(
	ctx context.Context,
	t *testing.T,
	pg *postgres.Postgres,
	lock interruptionLock,
	step blockedStep,
	command func(context.Context) error,
) error {
	t.Helper()

	blocking, release := holdStatementLock(ctx, t, pg, lock)
	defer release()

	commandCtx, cancelCommand := context.WithCancel(ctx)
	defer cancelCommand()

	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- command(commandCtx)
	}()
	// Fatal probe or step failures must release the lock and drain the command.
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
	step(blocking, cancelCommand)
	select {
	case err := <-done:
		return err
	case <-time.After(blockedStatementTimeout):
		t.Fatalf("command did not finish after its blocked step on %s", lock.label)
		return nil
	}
}

// TestIntegrationUpdateWorkflowReplayIsDurableAndIdempotent proves the durable
// command ledger makes a replayed update a complete no-op: the generation, the
// publish intent and the stale-work invalidation the first update produced all
// survive any number of replays, and the same key reused for a different request
// is refused instead of applied.
//
//nolint:gocyclo // One flow proves the whole replay contract: mutation, publish intent, invalidation and key reuse.
func TestIntegrationUpdateWorkflowReplayIsDurableAndIdempotent(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
	updateKey := "cv-update-replay-" + fixtureTag()
	updateName := "cv-replayed-" + fixtureTag()
	updatePayload := `{"image":"alpine:3.23.0"}`
	updateInterval := fixture.Interval * 2
	buildAction := workflowsmodel.ActionBuild.ToString()
	// Queued automatic work the update invalidates, so the replays also have to
	// prove they cancel nothing a second time.
	queuedJobID := seedFixturePendingJob(ctx, t, pg, fixture, "AUTOMATIC", 0)
	// Failures below the threshold are recorded first, so the counter the update
	// resets starts somewhere other than the zero CreateWorkflow left it at.
	for range 2 {
		reached, incrementErr := repo.IncrementWorkflowConsecutiveJobFailuresCount(
			ctx, fixture.WorkflowID, fixture.UserID, uuid.NewString(),
		)
		if incrementErr != nil || reached {
			t.Fatalf("failure below threshold = (reached %v, err %v), want (false, nil)", reached, incrementErr)
		}
	}
	// The reset assertion below is only meaningful while the counter the update
	// has to clear is non-zero, so pin that precondition rather than trust it.
	if seeded := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID); seeded.ConsecutiveFailures != 2 {
		t.Fatalf(
			"consecutive_job_failures_count = %d before the update, want 2 so the reset has something to clear",
			seeded.ConsecutiveFailures,
		)
	}

	update := func() error {
		return repo.UpdateWorkflow(
			ctx, fixture.WorkflowID, fixture.UserID, updateName, updatePayload,
			updateInterval, fixture.MaxConsecutiveJobFailures, updateKey,
		)
	}

	if err := update(); err != nil {
		t.Fatalf("UpdateWorkflow: %v", err)
	}
	afterFirst, err := repo.GetWorkflow(ctx, fixture.WorkflowID, fixture.UserID)
	if err != nil {
		t.Fatalf("GetWorkflow after update: %v", err)
	}
	if afterFirst.Generation != fixture.Generation+1 {
		t.Fatalf("generation = %d, want %d after a payload change queues a build", afterFirst.Generation, fixture.Generation+1)
	}
	if afterFirst.WorkflowBuildStatus != workflowsmodel.WorkflowBuildStatusQueued.ToString() {
		t.Fatalf("build_status = %q, want %q", afterFirst.WorkflowBuildStatus, workflowsmodel.WorkflowBuildStatusQueued)
	}
	canceledJob, ok := readFixtureJobState(ctx, t, pg, queuedJobID)
	if !ok {
		t.Fatal("automatic job disappeared after the update")
	}
	if canceledJob.Status != "CANCELED" {
		t.Fatalf("automatic job status = %q, want CANCELED", canceledJob.Status)
	}
	if canceledJob.TerminalReasonCode.String != terminalreason.WorkflowUpdated.String() {
		t.Fatalf(
			"automatic job terminal_reason_code = %q, want %q",
			canceledJob.TerminalReasonCode.String,
			terminalreason.WorkflowUpdated.String(),
		)
	}
	settled := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
	if settled.ConsecutiveFailures != 0 {
		t.Fatalf(
			"consecutive_job_failures_count = %d, want the update to reset the two recorded failures to 0",
			settled.ConsecutiveFailures,
		)
	}
	settledEvents := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)

	// Replaying the same command changes nothing at all: no second generation,
	// no second publish intent and no second invalidation.
	for attempt := range 2 {
		if replayErr := update(); replayErr != nil {
			t.Fatalf("UpdateWorkflow (replay %d): %v", attempt+1, replayErr)
		}
		assertMutationState(t, fmt.Sprintf("replay %d", attempt+1), readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID), settled)
	}
	// A replay still runs the ledger and legacy-identity writes and commits, so
	// the key has to stay one durable row rather than gain one per attempt.
	if count := countCommandKeys(ctx, t, pg, updateKey); count != 1 {
		t.Fatalf("ledger rows for the replayed key %q = %d, want the original 1", updateKey, count)
	}
	assertEventKeys(
		t,
		"build intents after the replays",
		workflowActionEventKeys(ctx, t, pg, fixture.WorkflowID, buildAction),
		idempotency.WorkflowEventKey(fixture.WorkflowID, buildAction, fixture.Generation),
		idempotency.WorkflowEventKey(fixture.WorkflowID, buildAction, settled.Generation),
	)
	if count := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID); count != settledEvents {
		t.Fatalf("outbox events after the replays = %d, want unchanged %d", count, settledEvents)
	}
	replayedJob, ok := readFixtureJobState(ctx, t, pg, queuedJobID)
	if !ok {
		t.Fatal("automatic job disappeared after the replays")
	}
	if replayedJob != canceledJob {
		t.Fatalf("automatic job after the replays = %+v, want unchanged %+v", replayedJob, canceledJob)
	}

	// The key is still bound to the request that used it, so a different request
	// under it is refused instead of silently becoming a second update.
	assertCode(
		t,
		"UpdateWorkflow (same key, different request)",
		repo.UpdateWorkflow(
			ctx, fixture.WorkflowID, fixture.UserID, updateName, fixturePayload,
			updateInterval, fixture.MaxConsecutiveJobFailures, updateKey,
		),
		codes.AlreadyExists,
	)
	assertMutationState(t, "refused key reuse", readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID), settled)
	if count := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID); count != settledEvents {
		t.Fatalf("outbox events after the refused key reuse = %d, want unchanged %d", count, settledEvents)
	}
	if count := countCommandKeys(ctx, t, pg, updateKey); count != 1 {
		t.Fatalf("ledger rows for the reused key %q = %d, want the original 1", updateKey, count)
	}
}

// TestIntegrationUpdateWorkflowReplayMustResolveToTheSameWorkflow proves a
// completed ledger row that resolves to a different workflow is not replayed as
// this workflow's update: the guard refuses it, and neither the workflow being
// updated nor the workflow the row names moves.
func TestIntegrationUpdateWorkflowReplayMustResolveToTheSameWorkflow(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	owner := seedWorkflowFixture(ctx, t, pg, repo, 3)
	sibling := seedWorkflowForUser(ctx, t, repo, owner.UserID, 3)
	updateKey := "cv-update-resource-" + fixtureTag()
	updateName := "cv-resource-" + fixtureTag()

	if err := repo.UpdateWorkflow(
		ctx, owner.WorkflowID, owner.UserID, updateName, fixturePayload,
		owner.Interval*2, owner.MaxConsecutiveJobFailures, updateKey,
	); err != nil {
		t.Fatalf("UpdateWorkflow: %v", err)
	}
	ownerSettled := readWorkflowMutationState(ctx, t, pg, owner.WorkflowID)
	siblingSettled := readWorkflowMutationState(ctx, t, pg, sibling.WorkflowID)
	ownerEvents := countWorkflowEvents(ctx, t, pg, owner.WorkflowID)

	repointCommandResource(ctx, t, pg, owner.UserID, owner.WorkflowID, updateKey, sibling.WorkflowID)

	assertCode(
		t,
		"UpdateWorkflow (replay naming another workflow)",
		repo.UpdateWorkflow(
			ctx, owner.WorkflowID, owner.UserID, updateName, fixturePayload,
			owner.Interval*2, owner.MaxConsecutiveJobFailures, updateKey,
		),
		codes.AlreadyExists,
	)
	assertMutationState(t, "refused cross-resource replay", readWorkflowMutationState(ctx, t, pg, owner.WorkflowID), ownerSettled)
	assertMutationState(t, "workflow the ledger row named", readWorkflowMutationState(ctx, t, pg, sibling.WorkflowID), siblingSettled)
	if count := countWorkflowEvents(ctx, t, pg, owner.WorkflowID); count != ownerEvents {
		t.Fatalf("outbox events after the refused replay = %d, want unchanged %d", count, ownerEvents)
	}
}

// TestIntegrationUpdateWorkflowRefusesForeignAndMalformedRequests proves every
// request the repository cannot own refuses as a whole command: a workflow the
// caller does not own, an unknown or malformed identity and a payload that is not
// JSON are all rejected before anything is written, and none of them consumes the
// idempotency key, so the same key stays free for a request that may proceed.
func TestIntegrationUpdateWorkflowRefusesForeignAndMalformedRequests(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	owner := seedWorkflowFixture(ctx, t, pg, repo, 3)
	stranger := seedWorkflowFixture(ctx, t, pg, repo, 3)
	before := readWorkflowMutationState(ctx, t, pg, owner.WorkflowID)
	eventsBefore := countWorkflowEvents(ctx, t, pg, owner.WorkflowID)

	cases := []struct {
		name       string
		workflowID string
		userID     string
		payload    string
		wantCode   codes.Code
	}{
		{"WorkflowOwnedByAnotherUser", owner.WorkflowID, stranger.UserID, fixturePayload, codes.NotFound},
		{"UnknownWorkflow", uuid.NewString(), owner.UserID, fixturePayload, codes.NotFound},
		{"MalformedWorkflowID", "not-a-uuid", owner.UserID, fixturePayload, codes.InvalidArgument},
		{"MalformedUserID", owner.WorkflowID, "not-a-uuid", fixturePayload, codes.InvalidArgument},
		{"PayloadThatIsNotJSON", owner.WorkflowID, owner.UserID, `{"image":`, codes.InvalidArgument},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := "cv-refused-" + fixtureTag()
			updateErr := repo.UpdateWorkflow(
				ctx, tc.workflowID, tc.userID, "cv-refused-"+fixtureTag(), tc.payload, 120, 3, key,
			)
			assertCode(t, tc.name, updateErr, tc.wantCode)
			if count := countCommandKeys(ctx, t, pg, key); count != 0 {
				t.Errorf("ledger rows for refused key %q = %d, want 0", key, count)
			}
		})
	}

	assertMutationState(t, "refused updates", readWorkflowMutationState(ctx, t, pg, owner.WorkflowID), before)
	if count := countWorkflowEvents(ctx, t, pg, owner.WorkflowID); count != eventsBefore {
		t.Fatalf("outbox events after the refused updates = %d, want unchanged %d", count, eventsBefore)
	}
}

// TestIntegrationUpdateWorkflowRefusesCallerThatWentAway proves a caller that is
// already gone is told so instead of being served, and that the attempt leaves no
// workflow mutation, publish intent or ledger row behind.
func TestIntegrationUpdateWorkflowRefusesCallerThatWentAway(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
	before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
	eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)

	goneCtx, cancelGone := context.WithCancel(ctx)
	cancelGone()

	key := "cv-update-gone-" + fixtureTag()
	assertCode(
		t,
		"UpdateWorkflow (caller already gone)",
		repo.UpdateWorkflow(
			goneCtx, fixture.WorkflowID, fixture.UserID, "cv-gone", `{"image":"alpine:3.23.0"}`,
			fixture.Interval*2, fixture.MaxConsecutiveJobFailures, key,
		),
		codes.Canceled,
	)
	assertMutationState(t, "canceled caller", readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID), before)
	if count := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID); count != eventsBefore {
		t.Errorf("outbox events after the canceled caller = %d, want unchanged %d", count, eventsBefore)
	}
	if count := countCommandKeys(ctx, t, pg, key); count != 0 {
		t.Errorf("ledger rows for the canceled caller's key %q = %d, want 0", key, count)
	}
}

// TestIntegrationUpdateWorkflowRollsBackWhenStaleWorkInvalidationIsInterrupted
// proves the stale-work invalidation belongs to the same transaction as the
// workflow mutation: when the queued job row is contended and the caller goes away
// while the update is invalidating it, the workflow keeps its old generation and
// the queued job stays queued, so no half-applied update survives.
func TestIntegrationUpdateWorkflowRollsBackWhenStaleWorkInvalidationIsInterrupted(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
	queuedJobID := seedFixturePendingJob(ctx, t, pg, fixture, "AUTOMATIC", 0)
	before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
	eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)
	updateKey := "cv-update-contended-" + fixtureTag()

	err := runWhileBlocked(
		ctx, t, pg,
		lockJobRow(queuedJobID, "%UPDATE "+postgres.TableJobs+"%SET status%"),
		cancelCaller,
		func(commandCtx context.Context) error {
			return repo.UpdateWorkflow(
				commandCtx, fixture.WorkflowID, fixture.UserID, "cv-contended", `{"image":"alpine:3.23.0"}`,
				fixture.Interval*2, fixture.MaxConsecutiveJobFailures, updateKey,
			)
		},
	)
	// The interrupted invalidation is reported as an internal failure rather than
	// as the cancellation it is; what this test pins is that nothing the command
	// had already done survives it.
	assertCode(t, "UpdateWorkflow (interrupted invalidation)", err, codes.Internal)
	assertMutationState(t, "interrupted invalidation", readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID), before)
	if count := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID); count != eventsBefore {
		t.Errorf("outbox events after the interrupted invalidation = %d, want unchanged %d", count, eventsBefore)
	}
	queued, ok := readFixtureJobState(ctx, t, pg, queuedJobID)
	if !ok {
		t.Fatal("queued job disappeared after the interrupted invalidation")
	}
	if queued.Status != "PENDING" || queued.TerminalReasonCode.Valid {
		t.Errorf("queued job after the interrupted invalidation = %+v, want it still PENDING with no terminal reason", queued)
	}
	if count := countCommandKeys(ctx, t, pg, updateKey); count != 0 {
		t.Errorf("ledger rows for the interrupted key %q = %d, want 0", updateKey, count)
	}
}

// TestIntegrationUpdateWorkflowDoesNotMutateWhenPublishIntentCannotBeRecorded
// proves the mutation and its publish intent commit together or not at all. A
// deterministic event key another in-flight command already holds blocks the
// intent insert, so an interrupted update leaves the workflow, its queued work and
// the outbox exactly as they were, on the build path and on the reschedule path
// alike.
func TestIntegrationUpdateWorkflowDoesNotMutateWhenPublishIntentCannotBeRecorded(t *testing.T) {
	cases := []struct {
		name      string
		completed bool
		action    workflowsmodel.Action
		interval  int32
	}{
		{name: "BuildPath", completed: false, action: workflowsmodel.ActionBuild, interval: 120},
		{name: "ReschedulePath", completed: true, action: workflowsmodel.ActionReschedule, interval: 120},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			pg := testkit.Postgres(t)
			repo := newTestRepository(t)

			fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
			if tc.completed {
				// A completed build is what puts the update on the reschedule
				// path, where the queue is rebuilt from the resolved image
				// instead of from a new build.
				completeFixtureBuild(ctx, t, repo, fixture)
			}
			queuedJobID := seedFixturePendingJob(ctx, t, pg, fixture, "AUTOMATIC", 0)
			before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
			eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)
			updateKey := "cv-update-intent-" + fixtureTag()
			conflictingIntent := idempotency.WorkflowEventKey(
				fixture.WorkflowID, tc.action.ToString(), fixture.Generation+1,
			)

			err := runWhileBlocked(
				ctx, t, pg,
				lockPublishIntent(fixture, conflictingIntent, "%INSERT INTO "+postgres.TableOutboxEvents+"%"),
				cancelCaller,
				func(commandCtx context.Context) error {
					return repo.UpdateWorkflow(
						commandCtx, fixture.WorkflowID, fixture.UserID, "cv-blocked-"+fixtureTag(), fixturePayload,
						tc.interval, fixture.MaxConsecutiveJobFailures, updateKey,
					)
				},
			)
			assertCode(t, tc.name+" (unrecordable publish intent)", err, codes.Internal)

			assertMutationState(t, tc.name, readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID), before)
			if count := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID); count != eventsBefore {
				t.Errorf("outbox events after %s = %d, want unchanged %d", tc.name, count, eventsBefore)
			}
			queued, ok := readFixtureJobState(ctx, t, pg, queuedJobID)
			if !ok {
				t.Fatal("queued job disappeared after the unrecordable publish intent")
			}
			if queued.Status != "PENDING" || queued.TerminalReasonCode.Valid {
				t.Errorf("queued job after %s = %+v, want it still PENDING with no terminal reason", tc.name, queued)
			}
			if count := countCommandKeys(ctx, t, pg, updateKey); count != 0 {
				t.Errorf("ledger rows for the interrupted key %q = %d, want 0", updateKey, count)
			}
		})
	}
}

// TestIntegrationDeleteWorkflowRefusesActiveWorkflow proves the guard that keeps a
// live workflow out of the delete path: an unterminated workflow is refused before
// its jobs are inspected, so a running job on an active workflow still reports the
// workflow itself, and nothing durable moves.
func TestIntegrationDeleteWorkflowRefusesActiveWorkflow(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
	nodeID := seedFixtureNode(ctx, t, pg, 1)
	runningJobID := seedFixtureRunningJob(ctx, t, pg, fixture, nodeID, "tcp://127.0.0.1:2375")
	before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
	eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)

	err := repo.DeleteWorkflow(ctx, fixture.WorkflowID, fixture.UserID)
	assertCode(t, "DeleteWorkflow (active workflow)", err, codes.FailedPrecondition)
	if message := status.Convert(err).Message(); message != "workflow is active, cannot delete" {
		t.Errorf("DeleteWorkflow (active workflow) message = %q, want the active-workflow refusal", message)
	}

	assertMutationState(t, "refused active deletion", readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID), before)
	if count := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); count != 1 {
		t.Errorf("jobs after the refused deletion = %d, want 1", count)
	}
	if runningJobs, _ := readRuntimeOccupancy(ctx, t, pg, nodeID); runningJobs != 1 {
		t.Errorf("runtime running_jobs after the refused deletion = %d, want the occupied slot kept", runningJobs)
	}
	if count := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID); count != eventsBefore {
		t.Errorf("outbox events after the refused deletion = %d, want unchanged %d", count, eventsBefore)
	}
	if state, ok := readFixtureJobState(ctx, t, pg, runningJobID); !ok || state.Status != "RUNNING" {
		t.Errorf("running job after the refused deletion = %+v (present %v), want it still RUNNING", state, ok)
	}
}

// TestIntegrationDeleteWorkflowRejectsMalformedIdentity proves a malformed
// workflow or user identity is refused as an invalid request instead of being
// reported as a missing workflow, and that the terminated workflow the caller
// named is untouched by either refusal.
func TestIntegrationDeleteWorkflowRejectsMalformedIdentity(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
	if err := repo.TerminateWorkflow(ctx, fixture.WorkflowID, fixture.UserID); err != nil {
		t.Fatalf("TerminateWorkflow: %v", err)
	}
	before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
	eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)

	cases := []struct {
		name       string
		workflowID string
		userID     string
	}{
		{"MalformedWorkflowID", "not-a-uuid", fixture.UserID},
		{"MalformedUserID", fixture.WorkflowID, "not-a-uuid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertCode(t, tc.name, repo.DeleteWorkflow(ctx, tc.workflowID, tc.userID), codes.InvalidArgument)
		})
	}

	assertMutationState(t, "refused malformed identities", readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID), before)
	if count := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID); count != eventsBefore {
		t.Fatalf("outbox events after the refused deletions = %d, want unchanged %d", count, eventsBefore)
	}
}

// TestIntegrationDeleteWorkflowInterruptionLeavesNothingBehind proves a caller
// that goes away, or whose deadline expires, while the deletion is running never
// leaves half of it behind: the workflow survives, its jobs survive and no publish
// intent is recorded, whichever of the deletion's statements the interruption lands
// on.
func TestIntegrationDeleteWorkflowInterruptionLeavesNothingBehind(t *testing.T) {
	// The four statements the deletion runs before it commits, each paired with
	// the two ways a caller's own context can stop it there. The last one records
	// the publish intent, so it is the case that proves the delete and its intent
	// are atomic: the row is already gone by then, and only the rollback can put
	// it back.
	cases := []struct {
		name        string
		useDeadline bool
		lock        func(*workflowFixture) interruptionLock
		wantCode    codes.Code
	}{
		{"WhileReadingTheWorkflow", false, lockWorkflowTableRead, codes.Canceled},
		{"WhileReadingTheWorkflowOnDeadline", true, lockWorkflowTableRead, codes.DeadlineExceeded},
		{"WhileReadingRunningJobs", false, lockJobsTableRead, codes.Canceled},
		{"WhileReadingRunningJobsOnDeadline", true, lockJobsTableRead, codes.DeadlineExceeded},
		{"WhileDeleting", false, lockWorkflowDelete, codes.Canceled},
		{"WhileDeletingOnDeadline", true, lockWorkflowDelete, codes.DeadlineExceeded},
		// The outbox insert wraps any error, a cancellation included, as
		// codes.Internal, so an interruption here is reported as an internal
		// failure rather than as the cancellation it is. The point of these cases
		// is the rollback either way: the workflow row is already deleted by the
		// time the intent is recorded, so only the rollback restores it.
		{"WhileRecordingThePublishIntent", false, lockDeletePublishIntent, codes.Internal},
		{"WhileRecordingThePublishIntentOnDeadline", true, lockDeletePublishIntent, codes.Internal},
	}

	t.Run("BeforeTheTransactionBegins", func(t *testing.T) {
		ctx := context.Background()
		pg := testkit.Postgres(t)
		repo := newTestRepository(t)

		fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
		seedDeleteFixture(ctx, t, pg, repo, fixture)
		before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
		eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)

		goneCtx, cancelGone := context.WithCancel(ctx)
		cancelGone()

		assertCode(
			t,
			"DeleteWorkflow (caller already gone)",
			repo.DeleteWorkflow(goneCtx, fixture.WorkflowID, fixture.UserID),
			codes.Canceled,
		)
		assertWorkflowSurvivedDeletion(ctx, t, pg, fixture, before, eventsBefore, "canceled caller")
	})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			pg := testkit.Postgres(t)
			repo := newTestRepository(t)

			fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
			// A terminated workflow holding only queued work passes both guards,
			// so the deletion really reaches the statement under test.
			seedDeleteFixture(ctx, t, pg, repo, fixture)
			before := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
			eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)

			callerCtx := ctx
			step := blockedStep(cancelCaller)
			if tc.useDeadline {
				var cancelDeadline context.CancelFunc
				callerCtx, cancelDeadline = context.WithTimeout(ctx, callerDeadlineDelay)
				defer cancelDeadline()
				step = letCallerDeadlineExpire
			}

			err := runWhileBlocked(
				callerCtx, t, pg, tc.lock(fixture), step,
				func(commandCtx context.Context) error {
					return repo.DeleteWorkflow(commandCtx, fixture.WorkflowID, fixture.UserID)
				},
			)
			assertCode(t, tc.name, err, tc.wantCode)
			assertWorkflowSurvivedDeletion(ctx, t, pg, fixture, before, eventsBefore, tc.name)
		})
	}
}

// TestIntegrationDeleteWorkflowRefusesWorkflowReactivatedUnderIt proves the
// deletion guard and the deletion itself agree about one workflow. A workflow
// reactivated by a concurrent update after the guard read it is not deleted and
// publishes nothing, because the delete re-checks its own condition once the row
// lock it waits for is granted.
func TestIntegrationDeleteWorkflowRefusesWorkflowReactivatedUnderIt(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
	seedDeleteFixture(ctx, t, pg, repo, fixture)
	terminated := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
	eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)

	// The transaction that holds the row lock reactivates the workflow and then
	// releases it, which is the interleaving the delete has to survive. Doing it
	// from the blocking transaction is what orders the two writes: the delete is
	// already waiting for that lock when the reactivation commits.
	//
	// A failed statement has to be fatal rather than reported and skipped. The
	// error aborts this transaction, which drops the row lock and lets the
	// deletion run straight through to a successful commit, so a reported-and-
	// returned step fails this case as "the delete was not refused" — blaming the
	// production guard for a fixture statement that never ran. Fatal stops at the
	// statement that actually failed, and unwinds through runWhileBlocked's
	// cleanup, which cancels the command and rolls the fixture transaction back.
	reactivate := func(blocking pgx.Tx, _ context.CancelFunc) {
		if _, err := blocking.Exec(ctx, fmt.Sprintf(`
			UPDATE %s SET terminated_at = NULL WHERE id = $1
		`, postgres.TableWorkflows), fixture.WorkflowID); err != nil {
			t.Fatalf("reactivate the workflow under the deletion: %v", err)
		}
		if err := blocking.Commit(ctx); err != nil {
			t.Fatalf("commit the concurrent reactivation: %v", err)
		}
	}

	err := runWhileBlocked(
		ctx, t, pg,
		lockWorkflowDelete(fixture),
		reactivate,
		func(commandCtx context.Context) error {
			return repo.DeleteWorkflow(commandCtx, fixture.WorkflowID, fixture.UserID)
		},
	)
	assertCode(t, "DeleteWorkflow (workflow reactivated under it)", err, codes.NotFound)

	reactivated := readWorkflowMutationState(ctx, t, pg, fixture.WorkflowID)
	if reactivated.Terminated() {
		t.Fatal("terminated_at is set, want the concurrent update's reactivation kept")
	}
	want := *terminated
	want.terminatedAt = sql.NullTime{}
	assertMutationState(t, "reactivated instead of deleted", reactivated, &want)
	if count := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID); count != eventsBefore {
		t.Errorf("outbox events after the refused deletion = %d, want unchanged %d", count, eventsBefore)
	}
}
