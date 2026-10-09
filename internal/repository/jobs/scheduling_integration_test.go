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
	// manualScheduleReplayWindow is the published window a client may retry its key in.
	manualScheduleReplayWindow = 24 * time.Hour
	// automaticScheduleReplayWindow matches the redrive window that can still redeliver an event command.
	automaticScheduleReplayWindow = 14 * 24 * time.Hour
	// Keeps seeded addresses inside the shorter users.email column.
	scheduleTestUserDomain = "@chronoverse.test"
)

// scheduleCommandFixture is one isolated workflow plus the ledger operations its schedule commands use.
type scheduleCommandFixture struct {
	UserID             string
	WorkflowID         string
	ManualOperation    string
	AutomaticOperation string
}

// manualScope is the ledger scope a user reserves manual schedule commands in.
func manualScope(userID string) string { return commandidempotency.UserScope(userID) }

// automaticScope is the ledger scope a workflow reserves automatic schedule commands in.
func automaticScope(workflowID string) string { return commandidempotency.WorkflowScope(workflowID) }

// scheduleCommand is one command's arguments as a caller supplies them.
type scheduleCommand struct {
	workflowID     string
	userID         string
	scheduledAt    string
	trigger        string
	idempotencyKey string
}

// scheduledJobRow is the durable jobs-row surface a command must converge to, or leave absent.
type scheduledJobRow struct {
	WorkflowID         string
	UserID             string
	Status             string
	Trigger            string
	ScheduledAt        time.Time
	IdempotencyKey     sql.NullString
	WorkflowGeneration sql.NullInt64
}

// legacyAutomaticJob is what a pre-ledger binary recorded on an automatic job. A nil
// field is one it never wrote, which stays unverifiable rather than guessed at.
type legacyAutomaticJob struct {
	idempotencyKey *string
	generation     *int64
}

// legacyRedelivery is one committed pre-ledger occurrence plus the automatic command
// refused for it. An empty commandKey is the worker's derived occurrence identity.
type legacyRedelivery struct {
	fixture     scheduleCommandFixture
	commandKey  string
	scheduledAt time.Time
	generation  int64
}

// seedScheduleFixture seeds an isolated user and workflow plus its ledger operations.
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

// seedScheduleUser inserts a second user, to prove the ownership guards.
func seedScheduleUser(ctx context.Context, t *testing.T, pg *postgres.Postgres) string {
	t.Helper()

	return testkit.SeedUser(ctx, t, pg, "cv-"+fixtureTag()+scheduleTestUserDomain)
}

// setWorkflowGeneration moves a workflow to another generation, as a rebuild does.
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

// seedLegacyAutomaticJob commits the jobs row a pre-ledger binary wrote.
func seedLegacyAutomaticJob(
	ctx context.Context,
	t *testing.T,
	pg *postgres.Postgres,
	fixture scheduleCommandFixture,
	scheduledAt time.Time,
	legacy legacyAutomaticJob,
) string {
	t.Helper()

	var key any
	if legacy.idempotencyKey != nil {
		key = *legacy.idempotencyKey
	}
	var recorded any
	if legacy.generation != nil {
		recorded = *legacy.generation
	}
	var jobID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO jobs (workflow_id, user_id, scheduled_at, trigger, idempotency_key, workflow_generation)
		VALUES ($1, $2, $3, 'AUTOMATIC', $4, $5)
		RETURNING id
	`, fixture.WorkflowID, fixture.UserID, scheduledAt, key, recorded).Scan(&jobID); err != nil {
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

// assertScheduleReplayWindow asserts expires_at is completed_at plus exactly the published
// window, so the row's own timestamps decide when its key may be spent again.
func assertScheduleReplayWindow(t *testing.T, command *jobCommandRow, want time.Duration) {
	t.Helper()

	if !command.CompletedAt.Valid || !command.ExpiresAt.Valid {
		t.Fatalf("command completed_at/expires_at validity = %v/%v, want both set", command.CompletedAt.Valid, command.ExpiresAt.Valid)
	}
	if got := command.ExpiresAt.Time.Sub(command.CompletedAt.Time); got != want {
		t.Fatalf("command replay window = %s, want %s", got, want)
	}
}

// assertScheduledCommandResponse asserts the completed command replays the job id it created.
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

// assertCompletedScheduleCommand asserts the completed reservation that every replay of
// that identity resolves to the named job.
func assertCompletedScheduleCommand(t *testing.T, command *jobCommandRow, jobID string, wantWindow time.Duration) {
	t.Helper()

	if command.Status != "COMPLETED" {
		t.Fatalf("command status = %q, want %q", command.Status, "COMPLETED")
	}
	assertNullString(t, "command resource_id", command.ResourceID, jobID)
	assertScheduledCommandResponse(t, command, jobID)
	assertScheduleReplayWindow(t, command, wantWindow)
}

// assertScheduledJobOwnership asserts the durable row is the pending occurrence reserved
// for the fixture, at the requested instant and identity.
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

// assertNoScheduleCommandEffect asserts a refused command left neither a job nor a
// reservation: the ledger assertion is the atomicity check, since a reservation that
// survived the rollback would fail every retry as still processing.
func assertNoScheduleCommandEffect(ctx context.Context, t *testing.T, pg *postgres.Postgres, fixture scheduleCommandFixture, scope, operation string) {
	t.Helper()

	if count := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); count != 0 {
		t.Fatalf("workflow %q has %d jobs, want 0 after a refused schedule command", fixture.WorkflowID, count)
	}
	if count := countScheduleCommands(ctx, t, pg, scope, operation); count != 0 {
		t.Fatalf("ledger holds %d %q rows, want 0 after a refused schedule command", count, operation)
	}
}

// expireScheduleCommand ages a reservation past its window, as the ledger sits between expiry and cleanup.
func expireScheduleCommand(ctx context.Context, t *testing.T, pg *postgres.Postgres, scope, operation, key string) {
	t.Helper()

	tag, err := pg.Exec(ctx, `
		UPDATE command_idempotency_keys
		SET completed_at = completed_at - interval '2 days', expires_at = expires_at - interval '2 days'
		WHERE scope = $1 AND operation = $2 AND idempotency_key = $3
	`, scope, operation, key)
	if err != nil {
		t.Fatalf("expire schedule command %q: %v", key, err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("expire schedule command %q affected %d rows, want 1", key, tag.RowsAffected())
	}
}

// occurrenceInstant is a schedule instant PostgreSQL stores exactly, far enough ahead to dispatch.
func occurrenceInstant(offset time.Duration) time.Time {
	return time.Now().UTC().Truncate(time.Microsecond).Add(offset)
}

// automaticOccurrenceKey is the identity a command derives when the worker reports an
// occurrence without an event key: the workflow and the instant, both in UTC, so it does
// not depend on the spelling the caller used to report the occurrence.
func automaticOccurrenceKey(workflowID string, scheduledAt time.Time) string {
	return idempotency.JobDispatchEventKey(workflowID + ":" + scheduledAt.UTC().Format(time.RFC3339Nano))
}

// buildEventScheduleKey is the identity a build or reschedule event spends on a generation's first occurrence.
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
	// Client keys arrive in HTTP headers, so padding is normalized before the key is durable.
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

	command, ok := readCommandByScope(ctx, t, pg, manualScope(fixture.UserID), fixture.ManualOperation, commandKey)
	if !ok {
		t.Fatalf("manual schedule command %q has no ledger row, want a completed reservation", commandKey)
	}
	assertCompletedScheduleCommand(t, command, jobID, manualScheduleReplayWindow)
	if count := countScheduleCommands(ctx, t, pg, manualScope(fixture.UserID), fixture.ManualOperation); count != 1 {
		t.Fatalf("ledger holds %d manual schedule rows, want exactly one durable command", count)
	}

	// Scheduling reserves only; the scheduler publishes, so a schedule command writes no outbox event.
	if count := countJobScopedOutboxEvents(ctx, t, pg, jobID, fixture.WorkflowID); count != 0 {
		t.Fatalf("outbox holds %d events naming job %q or workflow %q, want 0 from a schedule command", count, jobID, fixture.WorkflowID)
	}

	// A retry is the same command even at another instant, and must not disturb the first job.
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
	afterCommand, ok := readCommandByScope(ctx, t, pg, manualScope(fixture.UserID), fixture.ManualOperation, commandKey)
	if !ok {
		t.Fatalf("replay removed the ledger row of command %q", commandKey)
	}
	assertSameLedgerRow(t, afterCommand, command)
}

// TestIntegrationScheduleJobManualStoresEveryUTCInstantSpellingExactly pins the UTC
// spellings of a future instant that the jobs table stores exactly, which is what every
// in-tree caller formats. The offset spellings denoting those same instants land on the
// same stored instant, as TestIntegrationScheduleJobManualStoresAnOffsetSpellingAsItsInstant
// shows.
func TestIntegrationScheduleJobManualStoresEveryUTCInstantSpellingExactly(t *testing.T) {
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

// TestIntegrationScheduleJobManualStoresAnOffsetSpellingAsItsInstant pins the UTC
// contract of jobs.scheduled_at, which is TIMESTAMP WITHOUT TIME ZONE. An offset spelling
// denotes an instant, so the row holds that instant rather than the spelling's own wall
// clock: the column keeps no offset of its own, so the instant has to be normalized before
// storage. A deployment whose TZ is not UTC is what produces such a spelling, and what
// would otherwise store and dispatch the occurrence the offset's distance away.
func TestIntegrationScheduleJobManualStoresAnOffsetSpellingAsItsInstant(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedScheduleFixture(ctx, t, pg)
	instant := occurrenceInstant(time.Hour)
	spelling := instant.In(time.FixedZone("plus2", 2*3600))

	jobID, err := repo.ScheduleJob(
		ctx, fixture.WorkflowID, fixture.UserID, spelling.Format(time.RFC3339Nano),
		jobsmodel.JobTriggerManual.ToString(), "manual-offset-"+fixtureTag(), 1,
	)
	if err != nil {
		t.Fatalf("ScheduleJob: %v", err)
	}

	stored := readScheduledJob(ctx, t, pg, jobID)
	if !stored.ScheduledAt.Equal(instant) {
		t.Fatalf("job scheduled_at = %s, want the instant the spelling denotes %s", stored.ScheduledAt.UTC(), instant.UTC())
	}
	if got := stored.ScheduledAt.Sub(instant); got != 0 {
		t.Fatalf("job scheduled_at sits %s from the requested instant, want 0", got)
	}
}

// TestIntegrationScheduleJobAutomaticDerivesOneIdentityPerInstantSpelling pins the
// consequence of that UTC contract for an occurrence reported without an event key. The
// derived identity is built from the instant, not from the caller's RFC3339 text, so one
// instant spelled with an offset and again in UTC is one command replaying rather than two
// occurrences reserving two jobs. Occurrences derived before this normalization by a
// deployment outside UTC keep the keys they were stored under; nothing rewrites them.
func TestIntegrationScheduleJobAutomaticDerivesOneIdentityPerInstantSpelling(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedScheduleFixture(ctx, t, pg)
	instant := occurrenceInstant(time.Hour)
	spelling := instant.In(time.FixedZone("plus2", 2*3600))

	utcJobID, err := repo.ScheduleJob(
		ctx, fixture.WorkflowID, fixture.UserID, instant.Format(time.RFC3339Nano),
		jobsmodel.JobTriggerAutomatic.ToString(), "", 1,
	)
	if err != nil {
		t.Fatalf("ScheduleJob (utc spelling): %v", err)
	}
	offsetJobID, err := repo.ScheduleJob(
		ctx, fixture.WorkflowID, fixture.UserID, spelling.Format(time.RFC3339Nano),
		jobsmodel.JobTriggerAutomatic.ToString(), "", 1,
	)
	if err != nil {
		t.Fatalf("ScheduleJob (offset spelling): %v", err)
	}

	if offsetJobID != utcJobID {
		t.Fatalf("offset spelling id = %q, want the job the UTC spelling reserved %q", offsetJobID, utcJobID)
	}
	if count := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); count != 1 {
		t.Fatalf("workflow has %d jobs, want one per occurrence rather than one per spelling", count)
	}

	// The ledger holds the single identity both spellings derive, resolving to that job.
	occurrenceKey := automaticOccurrenceKey(fixture.WorkflowID, instant)
	command, ok := readCommandByScope(
		ctx, t, pg, automaticScope(fixture.WorkflowID), fixture.AutomaticOperation, occurrenceKey,
	)
	if !ok {
		t.Fatalf("derived identity %q has no ledger row, want a completed reservation", occurrenceKey)
	}
	assertCompletedScheduleCommand(t, command, utcJobID, automaticScheduleReplayWindow)
	if count := countScheduleCommands(
		ctx, t, pg, automaticScope(fixture.WorkflowID), fixture.AutomaticOperation,
	); count != 1 {
		t.Fatalf("ledger holds %d automatic schedule rows, want one per occurrence", count)
	}

	// The occurrence the scheduler reads back is the requested instant, not the offset's.
	stored := readScheduledJob(ctx, t, pg, utcJobID)
	if !stored.ScheduledAt.Equal(instant) {
		t.Fatalf("job scheduled_at = %s, want the requested occurrence %s", stored.ScheduledAt.UTC(), instant.UTC())
	}
	assertNullString(t, "job idempotency_key", stored.IdempotencyKey, occurrenceKey)
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

	// One client key is one command per workflow, so the second workflow gets its own job.
	if secondJobID == firstJobID {
		t.Fatalf("second workflow reused job %q, want its own scheduled job", firstJobID)
	}
	if count := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); count != 1 {
		t.Fatalf("first workflow has %d jobs, want 1", count)
	}
	if count := countWorkflowJobs(ctx, t, pg, secondWorkflowID); count != 1 {
		t.Fatalf("second workflow has %d jobs, want 1", count)
	}
	secondCommand, ok := readCommandByScope(
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

	// Reservation, job insert and completion share one transaction: all land or none do.
	if _, err := repo.ScheduleJob(
		ctx, fixture.WorkflowID, attackerID, scheduledAt.Format(time.RFC3339Nano),
		jobsmodel.JobTriggerManual.ToString(), commandKey, 1,
	); status.Code(err) != codes.NotFound {
		t.Fatalf("cross-user ScheduleJob code = %v, want %v (err: %v)", status.Code(err), codes.NotFound, err)
	}
	assertNoScheduleCommandEffect(
		ctx, t, pg, fixture, manualScope(attackerID), fixture.ManualOperation,
	)

	// A reservation that survived the rollback would fail the owner's retry as still processing.
	jobID, err := repo.ScheduleJob(
		ctx, fixture.WorkflowID, fixture.UserID, scheduledAt.Format(time.RFC3339Nano),
		jobsmodel.JobTriggerManual.ToString(), commandKey, 1,
	)
	if err != nil {
		t.Fatalf("ScheduleJob (owner, reused key): %v", err)
	}
	command, ok := readCommandByScope(ctx, t, pg, manualScope(fixture.UserID), fixture.ManualOperation, commandKey)
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

// TestIntegrationScheduleJobManualKeyIsReusableAfterItsReplayWindow pins the other half of
// the published window: once it passes the same client key reserves a new occurrence
// instead of replaying onto the wrong job.
func TestIntegrationScheduleJobManualKeyIsReusableAfterItsReplayWindow(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedScheduleFixture(ctx, t, pg)
	commandKey := "manual-expired-" + fixtureTag()
	firstScheduledAt := occurrenceInstant(time.Hour)

	firstJobID, err := repo.ScheduleJob(
		ctx, fixture.WorkflowID, fixture.UserID, firstScheduledAt.Format(time.RFC3339Nano),
		jobsmodel.JobTriggerManual.ToString(), commandKey, 1,
	)
	if err != nil {
		t.Fatalf("ScheduleJob: %v", err)
	}
	expireScheduleCommand(ctx, t, pg, manualScope(fixture.UserID), fixture.ManualOperation, commandKey)

	secondScheduledAt := occurrenceInstant(2 * time.Hour)
	secondJobID, err := repo.ScheduleJob(
		ctx, fixture.WorkflowID, fixture.UserID, secondScheduledAt.Format(time.RFC3339Nano),
		jobsmodel.JobTriggerManual.ToString(), commandKey, 1,
	)
	if err != nil {
		t.Fatalf("ScheduleJob (expired key): %v", err)
	}
	if secondJobID == firstJobID {
		t.Fatalf("expired key replayed job %q, want the occurrence it now reserves", firstJobID)
	}
	if count := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); count != 2 {
		t.Fatalf("workflow has %d jobs, want one per occurrence the key reserved", count)
	}

	// The reservation is spent by the new occurrence, and its window runs from that completion.
	command, ok := readCommandByScope(ctx, t, pg, manualScope(fixture.UserID), fixture.ManualOperation, commandKey)
	if !ok {
		t.Fatalf("command %q has no ledger row after the expired key was reused", commandKey)
	}
	assertCompletedScheduleCommand(t, command, secondJobID, manualScheduleReplayWindow)
	if job := readScheduledJob(ctx, t, pg, secondJobID); !job.ScheduledAt.Equal(secondScheduledAt) {
		t.Fatalf("second job scheduled_at = %s, want the occurrence it reserved %s", job.ScheduledAt.UTC(), secondScheduledAt.UTC())
	}
}

func TestIntegrationScheduleJobRejectsInvalidInputWithoutDurableEffect(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	tests := []struct {
		name string
		// mutate swaps in the spelling this case must refuse.
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

// TestIntegrationScheduleJobAbandonedCallerLeavesNoDurableEffect covers a caller that goes
// away before the command starts. The transaction is opened before the insert, so the
// failure arrives from BeginTx, and it is reported as the cancellation or deadline it is.
// The point is accuracy: a client that has gone away is not told the server had an internal
// fault. Nothing durable exists either way, so a redelivery would have been harmless; the
// only behavioral difference is that a caller is no longer asked to retry a command which
// provably reserved nothing.
func TestIntegrationScheduleJobAbandonedCallerLeavesNoDurableEffect(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	tests := []struct {
		name string
		// abandon returns a context whose caller is already gone when the command starts.
		abandon func(context.Context) (context.Context, context.CancelFunc)
		want    codes.Code
	}{
		{
			name: "canceled caller",
			abandon: func(ctx context.Context) (context.Context, context.CancelFunc) {
				abandoned, cancel := context.WithCancel(ctx)
				cancel()
				return abandoned, cancel
			},
			want: codes.Canceled,
		},
		{
			name: "expired deadline",
			abandon: func(ctx context.Context) (context.Context, context.CancelFunc) {
				return context.WithDeadline(ctx, time.Now().Add(-time.Second))
			},
			want: codes.DeadlineExceeded,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := seedScheduleFixture(ctx, t, pg)
			abandoned, cancel := testCase.abandon(ctx)
			t.Cleanup(cancel)

			jobID, err := repo.ScheduleJob(
				abandoned, fixture.WorkflowID, fixture.UserID,
				occurrenceInstant(time.Hour).Format(time.RFC3339Nano),
				jobsmodel.JobTriggerManual.ToString(), "manual-abandoned-"+fixtureTag(), 1,
			)
			if code := status.Code(err); code != testCase.want {
				t.Fatalf("ScheduleJob code = %v, want %v (err: %v)", code, testCase.want, err)
			}
			if jobID != "" {
				t.Fatalf("ScheduleJob returned job %q, want an empty id", jobID)
			}
			assertNoScheduleCommandEffect(ctx, t, pg, fixture, manualScope(fixture.UserID), fixture.ManualOperation)
		})
	}
}

func TestIntegrationScheduleJobAutomaticOccurrenceUsesDeterministicIdentity(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedScheduleFixture(ctx, t, pg)
	firstOccurrence := occurrenceInstant(time.Hour)

	// The worker reports the next occurrence without an event key, so the command derives an identity redeliveries repeat.
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

	command, ok := readCommandByScope(
		ctx, t, pg, automaticScope(fixture.WorkflowID), fixture.AutomaticOperation,
		automaticOccurrenceKey(fixture.WorkflowID, firstOccurrence),
	)
	if !ok {
		t.Fatal("derived occurrence identity has no ledger row, want a completed reservation")
	}
	// Event commands live as long as the events that can redeliver them.
	assertCompletedScheduleCommand(t, command, jobID, automaticScheduleReplayWindow)

	// Kafka and the outbox replay the reported request, so a redelivered occurrence resolves to the first job.
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

	// The next occurrence is a different command, and is what keeps a built workflow running.
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
		// prepare returns a fixture workflow no automatic command may schedule for.
		prepare func(ctx context.Context, t *testing.T, pg *postgres.Postgres) scheduleCommandFixture
	}{
		{
			// The workflow was rebuilt, so this event belongs to a generation that no longer exists.
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
	command, ok := readCommandByScope(ctx, t, pg, automaticScope(fixture.WorkflowID), fixture.AutomaticOperation, commandKey)
	if !ok {
		t.Fatalf("automatic schedule command %q has no ledger row", commandKey)
	}

	// A stale caller offers the identity it already spent under another generation: a conflict, not a replay.
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

	// The first command's job and ledger row are what a redelivery resolves to, so both stay untouched.
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
	afterCommand, ok := readCommandByScope(ctx, t, pg, automaticScope(fixture.WorkflowID), fixture.AutomaticOperation, commandKey)
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

	// A caller that predates workflow generations reports none, and the occurrence is still recorded against it.
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

	command, ok := readCommandByScope(ctx, t, pg, automaticScope(fixture.WorkflowID), fixture.AutomaticOperation, occurrenceKey)
	if !ok {
		t.Fatal("legacy occurrence identity has no ledger row, want a completed reservation")
	}
	assertNullString(t, "command resource_id", command.ResourceID, jobID)

	// The ledger binds the input it accepted, so re-offering the occurrence generation-aware is a conflict.
	if _, err = repo.ScheduleJob(
		ctx, fixture.WorkflowID, fixture.UserID, scheduledAt.Format(time.RFC3339Nano),
		jobsmodel.JobTriggerAutomatic.ToString(), "", 1,
	); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("conflicting legacy occurrence code = %v, want %v (err: %v)", status.Code(err), codes.AlreadyExists, err)
	}
	if count := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); count != 1 {
		t.Fatalf("workflow has %d jobs, want the single legacy occurrence", count)
	}
	afterCommand, ok := readCommandByScope(ctx, t, pg, automaticScope(fixture.WorkflowID), fixture.AutomaticOperation, occurrenceKey)
	if !ok {
		t.Fatalf("conflict removed the ledger row of the legacy occurrence %q", occurrenceKey)
	}
	assertSameLedgerRow(t, afterCommand, command)
}

// TestIntegrationScheduleJobAutomaticWithoutGenerationSkipsTheWorkflowGuard records the
// other side of the generation-0 legacy path, which the legacy-identity test above cannot
// show because its workflow is healthy. automaticScheduleGuardSQL emits no workflow clause
// below generation 1, so an automatic command carrying no generation reaches the insert
// with no ownership, termination or build-status clause at all, and every workflow state
// the generation-1 table refuses is admitted here. Only a workflow that does not exist is
// still refused, by the foreign key rather than the guard, and that arrives as Internal
// because mapScheduleInsertError maps a missing-workflow NoRows to FailedPrecondition only
// for generation > 0. These are recorded as found so that closing the generation-0 path is
// a deliberate change.
func TestIntegrationScheduleJobAutomaticWithoutGenerationSkipsTheWorkflowGuard(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	tests := []struct {
		name string
		// prepare leaves the fixture workflow in the state this case reports.
		prepare func(ctx context.Context, t *testing.T, pg *postgres.Postgres) scheduleCommandFixture
		// admitted is whether the generation-0 command nonetheless creates the job.
		admitted bool
	}{
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
			admitted: true,
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
			admitted: true,
		},
		{
			// The one state still refused, but by the foreign key rather than by the guard.
			name: "workflow does not exist",
			prepare: func(ctx context.Context, t *testing.T, pg *postgres.Postgres) scheduleCommandFixture {
				t.Helper()

				fixture := seedScheduleFixture(ctx, t, pg)
				if _, err := pg.Exec(ctx, `DELETE FROM workflows WHERE id = $1`, fixture.WorkflowID); err != nil {
					t.Fatalf("delete fixture workflow: %v", err)
				}
				return fixture
			},
			admitted: false,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := testCase.prepare(ctx, t, pg)
			scheduledAt := occurrenceInstant(time.Hour)

			// With the guard emitted, every state in the table is a precondition failure.
			refusedJobID, refusedErr := repo.ScheduleJob(
				ctx, fixture.WorkflowID, fixture.UserID, scheduledAt.Format(time.RFC3339Nano),
				jobsmodel.JobTriggerAutomatic.ToString(), "", 1,
			)
			if code := status.Code(refusedErr); code != codes.FailedPrecondition {
				t.Fatalf("guarded generation-1 ScheduleJob code = %v, want %v (err: %v)", code, codes.FailedPrecondition, refusedErr)
			}
			if refusedJobID != "" {
				t.Fatalf("refused generation-1 ScheduleJob returned job %q, want an empty id", refusedJobID)
			}
			assertNoScheduleCommandEffect(ctx, t, pg, fixture, automaticScope(fixture.WorkflowID), fixture.AutomaticOperation)

			// Repeating it without the generation drops the guard, so the same state is
			// prepared again on a second isolated workflow.
			unguarded := testCase.prepare(ctx, t, pg)
			jobID, err := repo.ScheduleJob(
				ctx, unguarded.WorkflowID, unguarded.UserID, scheduledAt.Format(time.RFC3339Nano),
				jobsmodel.JobTriggerAutomatic.ToString(), "", 0,
			)
			if testCase.admitted {
				if err != nil {
					t.Fatalf("unguarded generation-0 ScheduleJob: %v", err)
				}
				// The row names the caller's user even when that is not the workflow's owner.
				job := readScheduledJob(ctx, t, pg, jobID)
				assertScheduledJobOwnership(
					t, job, unguarded, jobsmodel.JobTriggerAutomatic.ToString(), scheduledAt,
					automaticOccurrenceKey(unguarded.WorkflowID, scheduledAt),
				)
				if !job.WorkflowGeneration.Valid || job.WorkflowGeneration.Int64 != 0 {
					t.Fatalf("unguarded job workflow_generation = %v, want 0", job.WorkflowGeneration)
				}
				return
			}
			if code := status.Code(err); code != codes.Internal {
				t.Fatalf("unguarded generation-0 ScheduleJob code = %v, want %v (err: %v)", code, codes.Internal, err)
			}
			if jobID != "" {
				t.Fatalf("unguarded generation-0 ScheduleJob returned job %q, want an empty id", jobID)
			}
			assertNoScheduleCommandEffect(ctx, t, pg, unguarded, automaticScope(unguarded.WorkflowID), unguarded.AutomaticOperation)
		})
	}
}

// TestIntegrationScheduleJobAdoptsLegacyAutomaticRowWithoutDuplicatingJob covers a
// pre-ledger occurrence committed for an identity this command redelivers: the committed
// job is adopted rather than duplicated. The padded case is what pins the lookup, because
// only a key whose stored text differs from the normalized caller key reaches it — a key
// that matches exactly is already resolved by the insert's own ON CONFLICT. Without the
// lookup the padded case fails as codes.Internal on idx_jobs_automatic_schedule_slot,
// which a client would retry forever.
func TestIntegrationScheduleJobAdoptsLegacyAutomaticRowWithoutDuplicatingJob(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	tests := []struct {
		name string
		// pad stores the legacy row's key with the padding a pre-normalization binary kept.
		pad bool
	}{
		{name: "legacy key already normalized"},
		{name: "legacy key stored padded", pad: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := seedScheduleFixture(ctx, t, pg)
			scheduledAt := occurrenceInstant(time.Hour)
			commandKey := "legacy-event-" + fixtureTag()
			generation := int64(1)
			storedKey := commandKey
			if testCase.pad {
				storedKey = "  " + commandKey + " "
			}
			legacyJobID := seedLegacyAutomaticJob(
				ctx, t, pg, fixture, scheduledAt,
				legacyAutomaticJob{idempotencyKey: &storedKey, generation: &generation},
			)

			// No ledger row exists for an occurrence committed before the upgrade, as while it is redelivered.
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

			// The occurrence stays exactly one job, replayable for the whole event window.
			if count := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); count != 1 {
				t.Fatalf("workflow has %d jobs, want only the committed legacy job", count)
			}
			command, ok := readCommandByScope(ctx, t, pg, automaticScope(fixture.WorkflowID), fixture.AutomaticOperation, commandKey)
			if !ok {
				t.Fatalf("adopted identity %q has no ledger row, want a completed reservation", commandKey)
			}
			assertCompletedScheduleCommand(t, command, legacyJobID, automaticScheduleReplayWindow)
			if count := countScheduleCommands(ctx, t, pg, automaticScope(fixture.WorkflowID), fixture.AutomaticOperation); count != 1 {
				t.Fatalf("ledger holds %d automatic schedule rows, want only the adopted identity", count)
			}
		})
	}
}

// TestIntegrationScheduleJobRefusesUnverifiableLegacyAutomaticRows covers the rule in the
// other direction: a pre-ledger occurrence is refused, never duplicated, unless its
// recorded generation proves it answers this command.
func TestIntegrationScheduleJobRefusesUnverifiableLegacyAutomaticRows(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	tests := []struct {
		name string
		// prepare commits a pre-ledger automatic job and the redelivery refused for it.
		prepare func(ctx context.Context, t *testing.T, pg *postgres.Postgres) legacyRedelivery
	}{
		{
			// The writing binary never recorded a generation, so nothing proves the row answers this occurrence.
			name: "legacy job has no recorded generation",
			prepare: func(ctx context.Context, t *testing.T, pg *postgres.Postgres) legacyRedelivery {
				t.Helper()

				fixture := seedScheduleFixture(ctx, t, pg)
				key := "legacy-unknown-generation-" + fixtureTag()
				seedLegacyAutomaticJob(ctx, t, pg, fixture, occurrenceInstant(time.Hour), legacyAutomaticJob{idempotencyKey: &key})
				return legacyRedelivery{
					fixture:     fixture,
					commandKey:  key,
					scheduledAt: occurrenceInstant(2 * time.Hour),
					generation:  1,
				}
			},
		},
		{
			// Another generation of this workflow spent the identity, so adopting it reports the wrong occurrence.
			name: "legacy job records another generation",
			prepare: func(ctx context.Context, t *testing.T, pg *postgres.Postgres) legacyRedelivery {
				t.Helper()

				fixture := seedScheduleFixture(ctx, t, pg)
				setWorkflowGeneration(ctx, t, pg, fixture.WorkflowID, 3)
				key := "legacy-stale-generation-" + fixtureTag()
				staleGeneration := int64(2)
				seedLegacyAutomaticJob(
					ctx, t, pg, fixture, occurrenceInstant(time.Hour),
					legacyAutomaticJob{idempotencyKey: &key, generation: &staleGeneration},
				)
				return legacyRedelivery{
					fixture:     fixture,
					commandKey:  key,
					scheduledAt: occurrenceInstant(2 * time.Hour),
					generation:  3,
				}
			},
		},
		{
			// A pre-idempotency binary wrote neither field, so the committed slot is the
			// occurrence's only trace and cannot be attributed to this command.
			name: "legacy job recorded no identity",
			prepare: func(ctx context.Context, t *testing.T, pg *postgres.Postgres) legacyRedelivery {
				t.Helper()

				fixture := seedScheduleFixture(ctx, t, pg)
				scheduledAt := occurrenceInstant(time.Hour)
				seedLegacyAutomaticJob(ctx, t, pg, fixture, scheduledAt, legacyAutomaticJob{})
				return legacyRedelivery{
					fixture:     fixture,
					scheduledAt: scheduledAt,
					generation:  1,
				}
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			redelivery := testCase.prepare(ctx, t, pg)

			jobID, err := repo.ScheduleJob(
				ctx, redelivery.fixture.WorkflowID, redelivery.fixture.UserID,
				redelivery.scheduledAt.Format(time.RFC3339Nano),
				jobsmodel.JobTriggerAutomatic.ToString(), redelivery.commandKey, redelivery.generation,
			)
			if code := status.Code(err); code != codes.AlreadyExists {
				t.Fatalf("ScheduleJob code = %v, want %v (err: %v)", code, codes.AlreadyExists, err)
			}
			if jobID != "" {
				t.Fatalf("refused ScheduleJob returned job %q, want an empty id", jobID)
			}

			// The refusal adds nothing: the legacy job stays the only job and the identity
			// unspent, so a later delivery can still decide the occurrence.
			if count := countWorkflowJobs(ctx, t, pg, redelivery.fixture.WorkflowID); count != 1 {
				t.Fatalf("workflow has %d jobs, want only the committed legacy job", count)
			}
			if count := countScheduleCommands(
				ctx, t, pg, automaticScope(redelivery.fixture.WorkflowID), redelivery.fixture.AutomaticOperation,
			); count != 0 {
				t.Fatalf("ledger holds %d automatic schedule rows, want 0 for a refused identity", count)
			}
		})
	}
}
