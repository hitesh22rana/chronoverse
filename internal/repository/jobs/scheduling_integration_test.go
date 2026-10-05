//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

const (
	// manualScheduleReplayWindow is the published replay window of a client
	// command: a client may retry its key for exactly this long.
	manualScheduleReplayWindow = 24 * time.Hour
	// automaticScheduleReplayWindow is the published replay window of a
	// deterministic event command, matching the outbox redrive window.
	automaticScheduleReplayWindow = 14 * 24 * time.Hour
	// scheduleTestUserDomain keeps seeded addresses inside the users.email
	// column, which is shorter than the generated fixture identities.
	scheduleTestUserDomain = "@chronoverse.test"
)

// scheduleCommandFixture is one isolated workflow together with the ledger
// operation its schedule commands are reserved under. Both scopes are derived
// from the identities here by the ledger helpers the repository itself uses.
type scheduleCommandFixture struct {
	UserID     string
	WorkflowID string
	// ManualOperation is the ledger operation of a manual schedule command on
	// the fixture workflow.
	ManualOperation string
	// AutomaticOperation is the ledger operation of an automatic schedule command
	// on the fixture workflow.
	AutomaticOperation string
}

// manualScope is the ledger scope a user reserves manual schedule commands in.
func manualScope(userID string) string { return commandidempotency.UserScope(userID) }

// automaticScope is the ledger scope a workflow reserves automatic schedule
// commands in.
func automaticScope(workflowID string) string { return commandidempotency.WorkflowScope(workflowID) }

// scheduleCommand is one schedule command's arguments as a caller supplies them.
type scheduleCommand struct {
	workflowID     string
	userID         string
	scheduledAt    string
	trigger        string
	idempotencyKey string
}

// scheduledJobRow is the durable jobs-row surface a schedule command must
// converge to, or leave absent when the command is rejected.
type scheduledJobRow struct {
	WorkflowID         string
	UserID             string
	Status             string
	Trigger            string
	ScheduledAt        time.Time
	IdempotencyKey     sql.NullString
	WorkflowGeneration sql.NullInt64
}

// seedScheduleFixture seeds an isolated user and a built workflow, returning the
// ledger operations its schedule commands are reserved under.
func seedScheduleFixture(ctx context.Context, t *testing.T, pg *postgres.Postgres) scheduleCommandFixture {
	t.Helper()

	userID, workflowID := seedIsolatedWorkflow(ctx, t, pg)
	return scheduleCommandFixture{
		UserID:             userID,
		WorkflowID:         workflowID,
		ManualOperation:    commandidempotency.ManualScheduleOperation(workflowID),
		AutomaticOperation: commandidempotency.OperationJobScheduleAutomatic,
	}
}

// seedScheduleUser inserts a second user for a fixture, used to prove the
// ownership guards of a schedule command.
func seedScheduleUser(ctx context.Context, t *testing.T, pg *postgres.Postgres) string {
	t.Helper()

	return testkit.SeedUser(ctx, t, pg, "cv-"+fixtureTag()+scheduleTestUserDomain)
}

// setWorkflowGeneration moves a fixture workflow to another generation, the way
// a workflow update that requires a rebuild does.
func setWorkflowGeneration(ctx context.Context, t *testing.T, pg *postgres.Postgres, workflowID string, generation int64) {
	t.Helper()

	tag, err := pg.Exec(ctx, `UPDATE workflows SET generation = $2 WHERE id = $1`, workflowID, generation)
	if err != nil {
		t.Fatalf("set workflow %q generation to %d: %v", workflowID, generation, err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("set workflow %q generation affected %d rows, want 1", workflowID, tag.RowsAffected())
	}
}

// seedLegacyAutomaticJob inserts a jobs row the way a binary from before the
// shared command ledger wrote one, so the upgrade-compatibility path is
// exercised against a real row. A nil generation reproduces a row whose
// workflow generation the writing binary never recorded.
func seedLegacyAutomaticJob(
	ctx context.Context,
	t *testing.T,
	pg *postgres.Postgres,
	fixture scheduleCommandFixture,
	idempotencyKey string,
	scheduledAt time.Time,
	generation *int64,
) string {
	t.Helper()

	var recorded any
	if generation != nil {
		recorded = *generation
	}
	var jobID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO jobs (workflow_id, user_id, scheduled_at, trigger, idempotency_key, workflow_generation)
		VALUES ($1, $2, $3, 'AUTOMATIC', $4, $5)
		RETURNING id
	`, fixture.WorkflowID, fixture.UserID, scheduledAt, idempotencyKey, recorded).Scan(&jobID); err != nil {
		t.Fatalf("seed legacy automatic job: %v", err)
	}
	return jobID
}

func readScheduledJob(ctx context.Context, t *testing.T, pg *postgres.Postgres, jobID string) *scheduledJobRow {
	t.Helper()

	var row scheduledJobRow
	err := pg.QueryRow(ctx, `
		SELECT workflow_id, user_id, status, trigger, scheduled_at, idempotency_key, workflow_generation
		FROM jobs
		WHERE id = $1
	`, jobID).Scan(
		&row.WorkflowID, &row.UserID, &row.Status, &row.Trigger,
		&row.ScheduledAt, &row.IdempotencyKey, &row.WorkflowGeneration,
	)
	if err != nil {
		t.Fatalf("read scheduled job %q: %v", jobID, err)
	}
	return &row
}

func readScheduleCommand(ctx context.Context, t *testing.T, pg *postgres.Postgres, scope, operation, key string) (*jobCommandRow, bool) {
	t.Helper()

	var row jobCommandRow
	err := pg.QueryRow(ctx, `
		SELECT status, request_hash, resource_id, response, completed_at, expires_at
		FROM command_idempotency_keys
		WHERE scope = $1 AND operation = $2 AND idempotency_key = $3
	`, scope, operation, key,
	).Scan(&row.Status, &row.RequestHash, &row.ResourceID, &row.Response, &row.CompletedAt, &row.ExpiresAt)
	if pg.IsNoRows(err) {
		return nil, false
	}
	if err != nil {
		t.Fatalf("read schedule command ledger row: %v", err)
	}
	return &row, true
}

func countWorkflowJobs(ctx context.Context, t *testing.T, pg *postgres.Postgres, workflowID string) int {
	t.Helper()

	var count int
	if err := pg.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE workflow_id = $1`, workflowID).Scan(&count); err != nil {
		t.Fatalf("count workflow jobs: %v", err)
	}
	return count
}

func countScheduleCommands(ctx context.Context, t *testing.T, pg *postgres.Postgres, scope, operation string) int {
	t.Helper()

	var count int
	if err := pg.QueryRow(ctx, `
		SELECT count(*)
		FROM command_idempotency_keys
		WHERE scope = $1 AND operation = $2
	`, scope, operation).Scan(&count); err != nil {
		t.Fatalf("count schedule command ledger rows: %v", err)
	}
	return count
}

// assertScheduleReplayWindow asserts the completed command's published replay
// window: expires_at must be the completion instant plus exactly that window, so
// the row's own timestamps decide when its identity may be spent again.
func assertScheduleReplayWindow(t *testing.T, command *jobCommandRow, want time.Duration) {
	t.Helper()

	if !command.CompletedAt.Valid || !command.ExpiresAt.Valid {
		t.Fatalf("command completed_at/expires_at validity = %v/%v, want both set", command.CompletedAt.Valid, command.ExpiresAt.Valid)
	}
	if got := command.ExpiresAt.Time.Sub(command.CompletedAt.Time); got != want {
		t.Fatalf("command replay window = %s, want %s", got, want)
	}
}

// assertScheduledCommandResponse asserts the completed command's replay payload
// names the job the command created, which is what a replaying caller reads.
func assertScheduledCommandResponse(t *testing.T, command *jobCommandRow, jobID string) {
	t.Helper()

	var response map[string]string
	if err := json.Unmarshal(command.Response, &response); err != nil {
		t.Fatalf("decode command response %q: %v", string(command.Response), err)
	}
	if response["id"] != jobID {
		t.Fatalf("command response id = %q, want %q", response["id"], jobID)
	}
}

// assertCompletedScheduleCommand asserts a schedule command's ledger row is the
// completed reservation every replay of its identity resolves to.
func assertCompletedScheduleCommand(t *testing.T, command *jobCommandRow, jobID string, wantWindow time.Duration) {
	t.Helper()

	if command.Status != "COMPLETED" {
		t.Fatalf("command status = %q, want %q", command.Status, "COMPLETED")
	}
	assertNullString(t, "command resource_id", command.ResourceID, jobID)
	assertScheduledCommandResponse(t, command, jobID)
	assertScheduleReplayWindow(t, command, wantWindow)
}

// assertScheduledJobOwnership asserts the durable jobs row is the pending
// occurrence the command reserved for the fixture's workflow and user, at the
// instant the caller asked for and under the identity the command normalized.
func assertScheduledJobOwnership(
	t *testing.T,
	job *scheduledJobRow,
	fixture scheduleCommandFixture,
	wantTrigger string,
	wantScheduledAt time.Time,
	wantKey string,
) {
	t.Helper()

	if job.WorkflowID != fixture.WorkflowID || job.UserID != fixture.UserID {
		t.Fatalf("job ownership = %q/%q, want %q/%q", job.WorkflowID, job.UserID, fixture.WorkflowID, fixture.UserID)
	}
	if job.Status != jobsmodel.JobStatusPending.ToString() {
		t.Fatalf("job status = %q, want %q", job.Status, jobsmodel.JobStatusPending.ToString())
	}
	if job.Trigger != wantTrigger {
		t.Fatalf("job trigger = %q, want %q", job.Trigger, wantTrigger)
	}
	if !job.ScheduledAt.Equal(wantScheduledAt) {
		t.Fatalf("job scheduled_at = %s, want the requested occurrence %s", job.ScheduledAt.UTC(), wantScheduledAt.UTC())
	}
	assertNullString(t, "job idempotency_key", job.IdempotencyKey, wantKey)
}

// assertNoScheduleCommandEffect asserts a refused or rejected command left no
// job row and no command ledger row behind. The ledger assertion is the
// atomicity check: a reservation that survived the rollback would make every
// retry of the same identity fail as still processing.
func assertNoScheduleCommandEffect(ctx context.Context, t *testing.T, pg *postgres.Postgres, fixture scheduleCommandFixture, scope, operation string) {
	t.Helper()

	if count := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); count != 0 {
		t.Fatalf("workflow %q has %d jobs, want 0 after a refused schedule command", fixture.WorkflowID, count)
	}
	if count := countScheduleCommands(ctx, t, pg, scope, operation); count != 0 {
		t.Fatalf("ledger holds %d %q rows, want 0 after a refused schedule command", count, operation)
	}
}

// occurrenceInstant returns a schedule instant PostgreSQL stores exactly and
// that is far enough ahead for the command to be dispatched afterwards.
func occurrenceInstant(offset time.Duration) time.Time {
	return time.Now().UTC().Truncate(time.Microsecond).Add(offset)
}

// automaticOccurrenceKey is the deterministic identity a schedule command
// derives when the execution worker reports the next occurrence of a job
// without carrying an event key of its own.
func automaticOccurrenceKey(workflowID string, scheduledAt time.Time) string {
	return idempotency.JobDispatchEventKey(workflowID + ":" + scheduledAt.Format(time.RFC3339Nano))
}

// buildEventScheduleKey is the identity a workflow build or reschedule event
// spends on the first automatic occurrence of a workflow generation.
func buildEventScheduleKey(workflowID string, generation int64) string {
	return idempotency.AutomaticScheduleEventKey(
		idempotency.WorkflowEventKey(workflowID, "BUILD", generation),
	)
}

func TestIntegrationScheduleJobManualPersistsOneJobAndReplaysFromLedger(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedScheduleFixture(ctx, t, pg)
	commandKey := "manual-" + fixtureTag()
	// Client keys arrive over HTTP headers, so the command must normalize the
	// padding before the key becomes a durable identity.
	paddedKey := "  " + commandKey + " "
	scheduledAt := occurrenceInstant(time.Hour)

	jobID, err := repo.ScheduleJob(
		ctx, fixture.WorkflowID, fixture.UserID, scheduledAt.Format(time.RFC3339Nano),
		jobsmodel.JobTriggerManual.ToString(), paddedKey, 1,
	)
	if err != nil {
		t.Fatalf("ScheduleJob: %v", err)
	}

	job := readScheduledJob(ctx, t, pg, jobID)
	assertScheduledJobOwnership(
		t, job, fixture, jobsmodel.JobTriggerManual.ToString(), scheduledAt, commandKey,
	)
	if job.WorkflowGeneration.Valid {
		t.Fatalf("manual job workflow_generation = %d, want NULL", job.WorkflowGeneration.Int64)
	}
	if count := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); count != 1 {
		t.Fatalf("workflow has %d jobs, want exactly the one the command created", count)
	}

	command, ok := readScheduleCommand(ctx, t, pg, manualScope(fixture.UserID), fixture.ManualOperation, commandKey)
	if !ok {
		t.Fatalf("manual schedule command %q has no ledger row, want a completed reservation", commandKey)
	}
	assertCompletedScheduleCommand(t, command, jobID, manualScheduleReplayWindow)
	if count := countScheduleCommands(ctx, t, pg, manualScope(fixture.UserID), fixture.ManualOperation); count != 1 {
		t.Fatalf("ledger holds %d manual schedule rows, want exactly one durable command", count)
	}

	// Scheduling reserves an occurrence; publishing it is the scheduler's job, so
	// an unexpected dispatch event here would leak work into Kafka.
	if count := countJobScopedOutboxEvents(ctx, t, pg, jobID, fixture.WorkflowID); count != 0 {
		t.Fatalf("outbox holds %d events naming job %q or workflow %q, want 0 from a schedule command", count, jobID, fixture.WorkflowID)
	}

	// A manual retry is the same command even when it names another instant, and
	// the replay must not disturb the job the first attempt created.
	retryScheduledAt := occurrenceInstant(2 * time.Hour)
	replayed, err := repo.ScheduleJob(
		ctx, fixture.WorkflowID, fixture.UserID, retryScheduledAt.Format(time.RFC3339Nano),
		jobsmodel.JobTriggerManual.ToString(), paddedKey, 1,
	)
	if err != nil {
		t.Fatalf("ScheduleJob (replay): %v", err)
	}
	if replayed != jobID {
		t.Fatalf("replayed job id = %q, want the original %q", replayed, jobID)
	}
	if count := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); count != 1 {
		t.Fatalf("replay left %d jobs, want the single job the first command created", count)
	}
	if after := readScheduledJob(ctx, t, pg, jobID); !after.ScheduledAt.Equal(scheduledAt) {
		t.Fatalf("replay moved scheduled_at to %s, want the original %s", after.ScheduledAt.UTC(), scheduledAt.UTC())
	}
	afterCommand, ok := readScheduleCommand(ctx, t, pg, manualScope(fixture.UserID), fixture.ManualOperation, commandKey)
	if !ok {
		t.Fatalf("replay removed the ledger row of command %q", commandKey)
	}
	assertSameLedgerRow(t, afterCommand, command)
}

// TestIntegrationScheduleJobManualAcceptsEveryStorableInstantSpelling pins the
// schedule instants a command must accept: every RFC3339 spelling of a future
// instant the jobs table can store exactly, which is what internal callers
// report after formatting a time in UTC.
func TestIntegrationScheduleJobManualAcceptsEveryStorableInstantSpelling(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedScheduleFixture(ctx, t, pg)
	instant := occurrenceInstant(time.Hour)
	spellings := []struct {
		name  string
		value time.Time
	}{
		{name: "utc without a fraction", value: instant.Truncate(time.Second)},
		{name: "utc with microseconds", value: instant},
		{name: "utc with milliseconds", value: instant.Truncate(time.Millisecond)},
	}
	for _, spelling := range spellings {
		t.Run(spelling.name, func(t *testing.T) {
			jobID, err := repo.ScheduleJob(
				ctx, fixture.WorkflowID, fixture.UserID, spelling.value.Format(time.RFC3339Nano),
				jobsmodel.JobTriggerManual.ToString(), "manual-spelling-"+fixtureTag(), 1,
			)
			if err != nil {
				t.Fatalf("ScheduleJob: %v", err)
			}

			stored := readScheduledJob(ctx, t, pg, jobID)
			if !stored.ScheduledAt.Equal(spelling.value) {
				t.Fatalf("job scheduled_at = %s, want the requested instant %s", stored.ScheduledAt.UTC(), spelling.value.UTC())
			}
		})
	}
}

func TestIntegrationScheduleJobManualKeyIsScopedToOneWorkflow(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedScheduleFixture(ctx, t, pg)
	secondWorkflowID := testkit.SeedWorkflow(ctx, t, pg, fixture.UserID, "cv-second-"+fixtureTag())
	commandKey := "manual-shared-" + fixtureTag()
	scheduledAt := occurrenceInstant(time.Hour)

	firstJobID, err := repo.ScheduleJob(
		ctx, fixture.WorkflowID, fixture.UserID, scheduledAt.Format(time.RFC3339Nano),
		jobsmodel.JobTriggerManual.ToString(), commandKey, 1,
	)
	if err != nil {
		t.Fatalf("ScheduleJob (first workflow): %v", err)
	}
	secondJobID, err := repo.ScheduleJob(
		ctx, secondWorkflowID, fixture.UserID, scheduledAt.Format(time.RFC3339Nano),
		jobsmodel.JobTriggerManual.ToString(), commandKey, 1,
	)
	if err != nil {
		t.Fatalf("ScheduleJob (second workflow): %v", err)
	}

	// One client key is one command per workflow, so scheduling a second workflow
	// must create a second job instead of replaying the first one.
	if secondJobID == firstJobID {
		t.Fatalf("second workflow reused job %q, want its own scheduled job", firstJobID)
	}
	if count := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); count != 1 {
		t.Fatalf("first workflow has %d jobs, want 1", count)
	}
	if count := countWorkflowJobs(ctx, t, pg, secondWorkflowID); count != 1 {
		t.Fatalf("second workflow has %d jobs, want 1", count)
	}
	secondCommand, ok := readScheduleCommand(
		ctx, t, pg, manualScope(fixture.UserID),
		commandidempotency.ManualScheduleOperation(secondWorkflowID), commandKey,
	)
	if !ok {
		t.Fatalf("second workflow has no ledger row for key %q", commandKey)
	}
	assertNullString(t, "command resource_id", secondCommand.ResourceID, secondJobID)
}

func TestIntegrationScheduleJobManualRejectionLeavesItsKeyReusable(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedScheduleFixture(ctx, t, pg)
	attackerID := seedScheduleUser(ctx, t, pg)
	commandKey := "manual-rejected-" + fixtureTag()
	scheduledAt := occurrenceInstant(time.Hour)

	// The command runs in one transaction: its reservation, the job insert and
	// the completion either all land or none of them do.
	if _, err := repo.ScheduleJob(
		ctx, fixture.WorkflowID, attackerID, scheduledAt.Format(time.RFC3339Nano),
		jobsmodel.JobTriggerManual.ToString(), commandKey, 1,
	); status.Code(err) != codes.NotFound {
		t.Fatalf("cross-user ScheduleJob code = %v, want %v (err: %v)", status.Code(err), codes.NotFound, err)
	}
	assertNoScheduleCommandEffect(
		ctx, t, pg, fixture, manualScope(attackerID), fixture.ManualOperation,
	)

	// A reservation that survived the rollback would make the owner's own retry
	// fail as still processing, so the owner's identical command must succeed.
	jobID, err := repo.ScheduleJob(
		ctx, fixture.WorkflowID, fixture.UserID, scheduledAt.Format(time.RFC3339Nano),
		jobsmodel.JobTriggerManual.ToString(), commandKey, 1,
	)
	if err != nil {
		t.Fatalf("ScheduleJob (owner, reused key): %v", err)
	}
	command, ok := readScheduleCommand(ctx, t, pg, manualScope(fixture.UserID), fixture.ManualOperation, commandKey)
	if !ok {
		t.Fatalf("owner's command %q has no ledger row after the rejected attempt", commandKey)
	}
	if command.Status != "COMPLETED" {
		t.Fatalf("command status = %q, want %q", command.Status, "COMPLETED")
	}
	assertNullString(t, "command resource_id", command.ResourceID, jobID)
	if count := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); count != 1 {
		t.Fatalf("workflow has %d jobs, want only the owner's job", count)
	}
	if count := countScheduleCommands(ctx, t, pg, manualScope(fixture.UserID), fixture.ManualOperation); count != 1 {
		t.Fatalf("ledger holds %d manual schedule rows, want only the owner's command", count)
	}
}

func TestIntegrationScheduleJobRejectsInvalidInputWithoutDurableEffect(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	tests := []struct {
		name string
		// mutate replaces a valid command's arguments with the spelling this case
		// must refuse.
		mutate func(command scheduleCommand) scheduleCommand
	}{
		{
			name:   "workflow id is not a uuid",
			mutate: func(c scheduleCommand) scheduleCommand { c.workflowID = "not-a-uuid"; return c },
		},
		{
			name:   "user id is not a uuid",
			mutate: func(c scheduleCommand) scheduleCommand { c.userID = "not-a-uuid"; return c },
		},
		{
			name:   "scheduled at is not an RFC3339 instant",
			mutate: func(c scheduleCommand) scheduleCommand { c.scheduledAt = "next tuesday"; return c },
		},
		{
			name:   "scheduled at is an impossible calendar date",
			mutate: func(c scheduleCommand) scheduleCommand { c.scheduledAt = "2035-13-01T00:00:00Z"; return c },
		},
		{
			name:   "trigger is not a job trigger",
			mutate: func(c scheduleCommand) scheduleCommand { c.trigger = "CRON"; return c },
		},
		{
			name:   "trigger spelling is not normalized",
			mutate: func(c scheduleCommand) scheduleCommand { c.trigger = "manual"; return c },
		},
		{
			name:   "idempotency key is blank",
			mutate: func(c scheduleCommand) scheduleCommand { c.idempotencyKey = "   "; return c },
		},
		{
			name:   "idempotency key carries a control character",
			mutate: func(c scheduleCommand) scheduleCommand { c.idempotencyKey = "manual\nkey"; return c },
		},
		{
			name:   "idempotency key is longer than 255 bytes",
			mutate: func(c scheduleCommand) scheduleCommand { c.idempotencyKey = strings.Repeat("k", 256); return c },
		},
		{
			name:   "idempotency key is not valid utf-8",
			mutate: func(c scheduleCommand) scheduleCommand { c.idempotencyKey = "manual-\xff-key"; return c },
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := seedScheduleFixture(ctx, t, pg)
			command := testCase.mutate(scheduleCommand{
				workflowID:     fixture.WorkflowID,
				userID:         fixture.UserID,
				scheduledAt:    occurrenceInstant(time.Hour).Format(time.RFC3339Nano),
				trigger:        jobsmodel.JobTriggerManual.ToString(),
				idempotencyKey: "manual-invalid-" + fixtureTag(),
			})

			jobID, err := repo.ScheduleJob(
				ctx, command.workflowID, command.userID, command.scheduledAt,
				command.trigger, command.idempotencyKey, 1,
			)
			if code := status.Code(err); code != codes.InvalidArgument {
				t.Fatalf("ScheduleJob code = %v, want %v (err: %v)", code, codes.InvalidArgument, err)
			}
			if jobID != "" {
				t.Fatalf("rejected ScheduleJob returned job %q, want an empty id", jobID)
			}
			assertNoScheduleCommandEffect(ctx, t, pg, fixture, manualScope(fixture.UserID), fixture.ManualOperation)
		})
	}
}

func TestIntegrationScheduleJobCanceledCallerLeavesNoDurableEffect(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedScheduleFixture(ctx, t, pg)
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()

	// A caller that goes away mid-command must be told the command did not run,
	// and must leave neither a job nor a reservation for the retry to trip over.
	_, err := repo.ScheduleJob(
		canceledCtx, fixture.WorkflowID, fixture.UserID, occurrenceInstant(time.Hour).Format(time.RFC3339Nano),
		jobsmodel.JobTriggerManual.ToString(), "manual-canceled-"+fixtureTag(), 1,
	)
	if code := status.Code(err); code != codes.Internal {
		t.Fatalf("canceled ScheduleJob code = %v, want %v (err: %v)", code, codes.Internal, err)
	}
	assertNoScheduleCommandEffect(ctx, t, pg, fixture, manualScope(fixture.UserID), fixture.ManualOperation)
}

func TestIntegrationScheduleJobAutomaticOccurrenceUsesDeterministicIdentity(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedScheduleFixture(ctx, t, pg)
	firstOccurrence := occurrenceInstant(time.Hour)

	// The execution worker reports the next occurrence of a workflow without an
	// event key, so the command must derive the identity redeliveries repeat.
	jobID, err := repo.ScheduleJob(
		ctx, fixture.WorkflowID, fixture.UserID, firstOccurrence.Format(time.RFC3339Nano),
		jobsmodel.JobTriggerAutomatic.ToString(), "", 1,
	)
	if err != nil {
		t.Fatalf("ScheduleJob: %v", err)
	}

	job := readScheduledJob(ctx, t, pg, jobID)
	assertScheduledJobOwnership(
		t, job, fixture, jobsmodel.JobTriggerAutomatic.ToString(), firstOccurrence,
		automaticOccurrenceKey(fixture.WorkflowID, firstOccurrence),
	)
	if !job.WorkflowGeneration.Valid || job.WorkflowGeneration.Int64 != 1 {
		t.Fatalf("automatic job workflow_generation = %v, want 1", job.WorkflowGeneration)
	}

	command, ok := readScheduleCommand(
		ctx, t, pg, automaticScope(fixture.WorkflowID), fixture.AutomaticOperation,
		automaticOccurrenceKey(fixture.WorkflowID, firstOccurrence),
	)
	if !ok {
		t.Fatal("derived occurrence identity has no ledger row, want a completed reservation")
	}
	// Event commands live as long as the events that can redeliver them.
	assertCompletedScheduleCommand(t, command, jobID, automaticScheduleReplayWindow)

	// A redelivered occurrence is the same command: Kafka and the outbox replay
	// the request the worker reported, so the derived identity must resolve back
	// to the job the first delivery created.
	redelivered := firstOccurrence.Format(time.RFC3339Nano)
	replayed, err := repo.ScheduleJob(
		ctx, fixture.WorkflowID, fixture.UserID, redelivered,
		jobsmodel.JobTriggerAutomatic.ToString(), "", 1,
	)
	if err != nil {
		t.Fatalf("ScheduleJob (redelivered occurrence): %v", err)
	}
	if replayed != jobID {
		t.Fatalf("redelivered occurrence id = %q, want the original %q", replayed, jobID)
	}
	if count := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); count != 1 {
		t.Fatalf("redelivery left %d jobs, want one job per occurrence", count)
	}

	// The next occurrence of the same workflow is a different command, and it is
	// what keeps a built workflow running.
	nextOccurrence := firstOccurrence.Add(5 * time.Minute)
	nextJobID, err := repo.ScheduleJob(
		ctx, fixture.WorkflowID, fixture.UserID, nextOccurrence.Format(time.RFC3339Nano),
		jobsmodel.JobTriggerAutomatic.ToString(), "", 1,
	)
	if err != nil {
		t.Fatalf("ScheduleJob (next occurrence): %v", err)
	}
	if nextJobID == jobID {
		t.Fatalf("next occurrence reused job %q, want a new scheduled job", jobID)
	}
	if count := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); count != 2 {
		t.Fatalf("workflow has %d jobs, want one per scheduled occurrence", count)
	}
	if count := countScheduleCommands(ctx, t, pg, automaticScope(fixture.WorkflowID), fixture.AutomaticOperation); count != 2 {
		t.Fatalf("ledger holds %d automatic schedule rows, want one per occurrence", count)
	}
}

func TestIntegrationScheduleJobAutomaticCommandEnforcesWorkflowGeneration(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	tests := []struct {
		name string
		// prepare returns a fixture workflow whose generation no automatic command
		// may schedule for.
		prepare func(ctx context.Context, t *testing.T, pg *postgres.Postgres) scheduleCommandFixture
	}{
		{
			// The workflow was rebuilt, so the event this command replays belongs
			// to a generation that no longer exists.
			name: "workflow moved to another generation",
			prepare: func(ctx context.Context, t *testing.T, pg *postgres.Postgres) scheduleCommandFixture {
				t.Helper()

				fixture := seedScheduleFixture(ctx, t, pg)
				setWorkflowGeneration(ctx, t, pg, fixture.WorkflowID, 2)
				return fixture
			},
		},
		{
			name: "workflow is terminated",
			prepare: func(ctx context.Context, t *testing.T, pg *postgres.Postgres) scheduleCommandFixture {
				t.Helper()

				fixture := seedScheduleFixture(ctx, t, pg)
				tag, err := pg.Exec(ctx, `UPDATE workflows SET terminated_at = now() AT TIME ZONE 'utc' WHERE id = $1`, fixture.WorkflowID)
				if err != nil || tag.RowsAffected() != 1 {
					t.Fatalf("terminate fixture workflow: %v", err)
				}
				return fixture
			},
		},
		{
			name: "workflow has no completed build",
			prepare: func(ctx context.Context, t *testing.T, pg *postgres.Postgres) scheduleCommandFixture {
				t.Helper()

				fixture := seedScheduleFixture(ctx, t, pg)
				tag, err := pg.Exec(ctx, `UPDATE workflows SET build_status = 'FAILED' WHERE id = $1`, fixture.WorkflowID)
				if err != nil || tag.RowsAffected() != 1 {
					t.Fatalf("unbuild fixture workflow: %v", err)
				}
				return fixture
			},
		},
		{
			name: "workflow belongs to another user",
			prepare: func(ctx context.Context, t *testing.T, pg *postgres.Postgres) scheduleCommandFixture {
				t.Helper()

				fixture := seedScheduleFixture(ctx, t, pg)
				tag, err := pg.Exec(ctx, `UPDATE workflows SET user_id = $2 WHERE id = $1`, fixture.WorkflowID, seedScheduleUser(ctx, t, pg))
				if err != nil || tag.RowsAffected() != 1 {
					t.Fatalf("transfer fixture workflow: %v", err)
				}
				return fixture
			},
		},
		{
			name: "workflow does not exist",
			prepare: func(ctx context.Context, t *testing.T, pg *postgres.Postgres) scheduleCommandFixture {
				t.Helper()

				fixture := seedScheduleFixture(ctx, t, pg)
				if _, err := pg.Exec(ctx, `DELETE FROM workflows WHERE id = $1`, fixture.WorkflowID); err != nil {
					t.Fatalf("delete fixture workflow: %v", err)
				}
				return fixture
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := testCase.prepare(ctx, t, pg)

			jobID, err := repo.ScheduleJob(
				ctx, fixture.WorkflowID, fixture.UserID, occurrenceInstant(time.Hour).Format(time.RFC3339Nano),
				jobsmodel.JobTriggerAutomatic.ToString(), buildEventScheduleKey(fixture.WorkflowID, 1), 1,
			)
			if code := status.Code(err); code != codes.FailedPrecondition {
				t.Fatalf("ScheduleJob code = %v, want %v (err: %v)", code, codes.FailedPrecondition, err)
			}
			if jobID != "" {
				t.Fatalf("refused ScheduleJob returned job %q, want an empty id", jobID)
			}
			assertNoScheduleCommandEffect(ctx, t, pg, fixture, automaticScope(fixture.WorkflowID), fixture.AutomaticOperation)
		})
	}
}

func TestIntegrationScheduleJobAutomaticIdentityConflictPreservesOriginalJob(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedScheduleFixture(ctx, t, pg)
	commandKey := buildEventScheduleKey(fixture.WorkflowID, 1)
	scheduledAt := occurrenceInstant(time.Hour)

	jobID, err := repo.ScheduleJob(
		ctx, fixture.WorkflowID, fixture.UserID, scheduledAt.Format(time.RFC3339Nano),
		jobsmodel.JobTriggerAutomatic.ToString(), commandKey, 1,
	)
	if err != nil {
		t.Fatalf("ScheduleJob: %v", err)
	}
	command, ok := readScheduleCommand(ctx, t, pg, automaticScope(fixture.WorkflowID), fixture.AutomaticOperation, commandKey)
	if !ok {
		t.Fatalf("automatic schedule command %q has no ledger row", commandKey)
	}

	// The workflow is rebuilt and a stale caller offers the identity it already
	// spent, now under a different generation: a conflicting command, not a replay.
	setWorkflowGeneration(ctx, t, pg, fixture.WorkflowID, 2)
	conflicting, err := repo.ScheduleJob(
		ctx, fixture.WorkflowID, fixture.UserID, scheduledAt.Format(time.RFC3339Nano),
		jobsmodel.JobTriggerAutomatic.ToString(), commandKey, 2,
	)
	if code := status.Code(err); code != codes.AlreadyExists {
		t.Fatalf("conflicting ScheduleJob code = %v, want %v (err: %v)", code, codes.AlreadyExists, err)
	}
	if conflicting != "" {
		t.Fatalf("conflicting ScheduleJob returned job %q, want an empty id", conflicting)
	}

	// The first command's job and ledger row are the durable state a redelivery
	// still resolves to, so the conflict must leave both untouched.
	after := readScheduledJob(ctx, t, pg, jobID)
	if !after.WorkflowGeneration.Valid || after.WorkflowGeneration.Int64 != 1 {
		t.Fatalf("job workflow_generation = %v, want the original 1", after.WorkflowGeneration)
	}
	if !after.ScheduledAt.Equal(scheduledAt) {
		t.Fatalf("job scheduled_at = %s, want the original %s", after.ScheduledAt.UTC(), scheduledAt.UTC())
	}
	if count := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); count != 1 {
		t.Fatalf("workflow has %d jobs, want the single job of the first command", count)
	}
	afterCommand, ok := readScheduleCommand(ctx, t, pg, automaticScope(fixture.WorkflowID), fixture.AutomaticOperation, commandKey)
	if !ok {
		t.Fatalf("conflict removed the ledger row of command %q", commandKey)
	}
	assertSameLedgerRow(t, afterCommand, command)
	assertNullString(t, "command resource_id", afterCommand.ResourceID, jobID)
	if count := countScheduleCommands(ctx, t, pg, automaticScope(fixture.WorkflowID), fixture.AutomaticOperation); count != 1 {
		t.Fatalf("ledger holds %d automatic schedule rows, want only the first command", count)
	}
}

func TestIntegrationScheduleJobAutomaticWithoutGenerationRecordsLegacyIdentity(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedScheduleFixture(ctx, t, pg)
	scheduledAt := occurrenceInstant(time.Hour)
	occurrenceKey := automaticOccurrenceKey(fixture.WorkflowID, scheduledAt)

	// A caller that predates workflow generations reports none, and the command
	// must still record the occurrence against the generation it was given.
	jobID, err := repo.ScheduleJob(
		ctx, fixture.WorkflowID, fixture.UserID, scheduledAt.Format(time.RFC3339Nano),
		jobsmodel.JobTriggerAutomatic.ToString(), "", 0,
	)
	if err != nil {
		t.Fatalf("ScheduleJob: %v", err)
	}

	job := readScheduledJob(ctx, t, pg, jobID)
	if !job.WorkflowGeneration.Valid || job.WorkflowGeneration.Int64 != 0 {
		t.Fatalf("legacy automatic job workflow_generation = %v, want 0", job.WorkflowGeneration)
	}
	assertNullString(t, "job idempotency_key", job.IdempotencyKey, occurrenceKey)

	command, ok := readScheduleCommand(ctx, t, pg, automaticScope(fixture.WorkflowID), fixture.AutomaticOperation, occurrenceKey)
	if !ok {
		t.Fatal("legacy occurrence identity has no ledger row, want a completed reservation")
	}
	assertNullString(t, "command resource_id", command.ResourceID, jobID)

	// The ledger binds the exact input it accepted, so the same occurrence re-offered
	// as a generation-aware command is a conflict rather than a replay.
	if _, err = repo.ScheduleJob(
		ctx, fixture.WorkflowID, fixture.UserID, scheduledAt.Format(time.RFC3339Nano),
		jobsmodel.JobTriggerAutomatic.ToString(), "", 1,
	); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("conflicting legacy occurrence code = %v, want %v (err: %v)", status.Code(err), codes.AlreadyExists, err)
	}
	if count := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); count != 1 {
		t.Fatalf("workflow has %d jobs, want the single legacy occurrence", count)
	}
	afterCommand, ok := readScheduleCommand(ctx, t, pg, automaticScope(fixture.WorkflowID), fixture.AutomaticOperation, occurrenceKey)
	if !ok {
		t.Fatalf("conflict removed the ledger row of the legacy occurrence %q", occurrenceKey)
	}
	assertSameLedgerRow(t, afterCommand, command)
}

func TestIntegrationScheduleJobAdoptsLegacyAutomaticRowWithoutDuplicatingJob(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedScheduleFixture(ctx, t, pg)
	scheduledAt := occurrenceInstant(time.Hour)
	commandKey := "legacy-event-" + fixtureTag()
	generation := int64(1)
	legacyJobID := seedLegacyAutomaticJob(ctx, t, pg, fixture, commandKey, scheduledAt, &generation)

	// The ledger row an upgraded binary would rebuild is absent here, exactly as
	// it is while an occurrence committed before the upgrade is redelivered.
	jobID, err := repo.ScheduleJob(
		ctx, fixture.WorkflowID, fixture.UserID, scheduledAt.Format(time.RFC3339Nano),
		jobsmodel.JobTriggerAutomatic.ToString(), commandKey, 1,
	)
	if err != nil {
		t.Fatalf("ScheduleJob: %v", err)
	}
	if jobID != legacyJobID {
		t.Fatalf("adopted job id = %q, want the committed legacy job %q", jobID, legacyJobID)
	}

	// The occurrence must stay exactly one job, and the adopted identity must be
	// replayable for the whole event window.
	if count := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); count != 1 {
		t.Fatalf("workflow has %d jobs, want only the committed legacy job", count)
	}
	command, ok := readScheduleCommand(ctx, t, pg, automaticScope(fixture.WorkflowID), fixture.AutomaticOperation, commandKey)
	if !ok {
		t.Fatalf("adopted identity %q has no ledger row, want a completed reservation", commandKey)
	}
	assertCompletedScheduleCommand(t, command, legacyJobID, automaticScheduleReplayWindow)
	if count := countScheduleCommands(ctx, t, pg, automaticScope(fixture.WorkflowID), fixture.AutomaticOperation); count != 1 {
		t.Fatalf("ledger holds %d automatic schedule rows, want only the adopted identity", count)
	}
}

func TestIntegrationScheduleJobRefusesUnverifiableLegacyAutomaticRows(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	tests := []struct {
		name string
		// prepare seeds a pre-upgrade automatic job and returns the fixture plus
		// the identity and generation the redelivered command must be refused for.
		prepare func(ctx context.Context, t *testing.T, pg *postgres.Postgres) (scheduleCommandFixture, string, int64)
	}{
		{
			// The writing binary never recorded a generation, so the command cannot
			// prove the committed row answers this occurrence.
			name: "legacy job has no recorded generation",
			prepare: func(ctx context.Context, t *testing.T, pg *postgres.Postgres) (scheduleCommandFixture, string, int64) {
				t.Helper()

				fixture := seedScheduleFixture(ctx, t, pg)
				key := "legacy-unknown-generation-" + fixtureTag()
				seedLegacyAutomaticJob(ctx, t, pg, fixture, key, occurrenceInstant(time.Hour), nil)
				return fixture, key, 1
			},
		},
		{
			// The identity was spent by another generation of this workflow, so
			// treating it as a replay would report the wrong occurrence.
			name: "legacy job records another generation",
			prepare: func(ctx context.Context, t *testing.T, pg *postgres.Postgres) (scheduleCommandFixture, string, int64) {
				t.Helper()

				fixture := seedScheduleFixture(ctx, t, pg)
				setWorkflowGeneration(ctx, t, pg, fixture.WorkflowID, 3)
				key := "legacy-stale-generation-" + fixtureTag()
				staleGeneration := int64(2)
				seedLegacyAutomaticJob(ctx, t, pg, fixture, key, occurrenceInstant(time.Hour), &staleGeneration)
				return fixture, key, 3
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fixture, commandKey, offeredGeneration := testCase.prepare(ctx, t, pg)

			jobID, err := repo.ScheduleJob(
				ctx, fixture.WorkflowID, fixture.UserID, occurrenceInstant(2*time.Hour).Format(time.RFC3339Nano),
				jobsmodel.JobTriggerAutomatic.ToString(), commandKey, offeredGeneration,
			)
			if code := status.Code(err); code != codes.AlreadyExists {
				t.Fatalf("ScheduleJob code = %v, want %v (err: %v)", code, codes.AlreadyExists, err)
			}
			if jobID != "" {
				t.Fatalf("refused ScheduleJob returned job %q, want an empty id", jobID)
			}

			// The refused command must add nothing: the committed legacy job stays
			// the only job, and the identity stays unspent so the occurrence it does
			// belong to is still decidable by a later delivery.
			if count := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); count != 1 {
				t.Fatalf("workflow has %d jobs, want only the committed legacy job", count)
			}
			if count := countScheduleCommands(ctx, t, pg, automaticScope(fixture.WorkflowID), fixture.AutomaticOperation); count != 0 {
				t.Fatalf("ledger holds %d automatic schedule rows, want 0 for a refused identity", count)
			}
		})
	}
}
