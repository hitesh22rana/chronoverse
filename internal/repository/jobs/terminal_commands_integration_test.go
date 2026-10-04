//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	analyticsmodel "github.com/hitesh22rana/chronoverse/internal/model/analytics"
	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

const (
	failureKindColumn = "failure_kind"
	// terminalFailureErrorCode is the gRPC status the executor reports for a
	// non-zero workload exit.
	terminalFailureErrorCode = "Aborted"
	// terminalFailureMessage is the raw container stderr persisted as failure reason.
	terminalFailureMessage = "container exited with code 1"
	// staleLeaseTokenSuffix replaces the random half of a lease token so the
	// command carries a well-formed token no worker ever held.
	staleLeaseTokenSuffix = "00000000-0000-4000-8000-00000000dead"
	// multiByteRuneBytes is the encoded width of the rune used by the
	// truncation boundary fixture.
	multiByteRuneBytes = 3
	// expectedTruncatedRunes is the literal expectation for the truncation
	// boundary: maxJobErrorMessageLength bytes of 3-byte runes hold
	// (4096/3) complete runes plus one byte of a partial rune, which
	// truncateJobError trims back to the last whole rune.
	expectedTruncatedRunes = maxJobErrorMessageLength / multiByteRuneBytes
	// cleanupTimeout bounds every t.Cleanup statement so a stuck container
	// cannot hang the suite.
	cleanupTimeout = 15 * time.Second
)

// terminalJobState is the durable jobs-row surface every terminal command must
// converge to, or leave byte-identical when it rejects the caller.
type terminalJobState struct {
	Status                 string
	StartedAt              sql.NullTime
	CompletedAt            sql.NullTime
	LeaseToken             sql.NullString
	LeasedBy               sql.NullString
	LeaseProcessInstanceID sql.NullString
	LeaseExpiresAt         sql.NullTime
	LastHeartbeatAt        sql.NullTime
	TerminalReasonCode     sql.NullString
	FailureKind            sql.NullString
	LastErrorCode          sql.NullString
	LastErrorMessage       sql.NullString
	RuntimeNodeID          sql.NullString
	Attempts               int32
	DispatchAttempts       int32
}

// jobOutboxEvent is the durable outbox row a terminal command publishes.
type jobOutboxEvent struct {
	Topic    string
	KafkaKey string
	EventKey string
	Payload  []byte
}

// jobCommandRow is the durable command-ledger record of one job command.
type jobCommandRow struct {
	Status      string
	RequestHash string
	ResourceID  sql.NullString
	Response    []byte
	CompletedAt sql.NullTime
	ExpiresAt   sql.NullTime
}

// claimedFixture is one job brought all the way to a held lease, together with
// the durable identities its assertions need.
type claimedFixture struct {
	JobID      string
	WorkflowID string
	UserID     string
	LeaseToken string
}

// terminalCommandOperation binds a terminal command to its ledger operation so
// one table can drive every test that must hold for both commands.
type terminalCommandOperation struct {
	Name       string
	Operation  string
	WantStatus string
	Invoke     func(ctx context.Context, repo *Repository, jobID, leaseToken, commandID string) error
}

// terminalCommandOperations is the single source of truth for the two
// lease-holding terminal commands. Tests that must prove the same reservation
// discipline for both iterate this table so coverage cannot silently diverge.
var terminalCommandOperations = []terminalCommandOperation{
	{Name: "fail job", Operation: commandidempotency.OperationJobFail, WantStatus: "FAILED", Invoke: failJobTerminal},
	{Name: "cancel claimed job", Operation: commandidempotency.OperationJobCancelClaimed, WantStatus: "CANCELED", Invoke: cancelClaimedTerminal},
}

// failJobTerminal invokes FailJob with the arguments the executor uses for a
// non-zero workload exit.
func failJobTerminal(ctx context.Context, repo *Repository, jobID, leaseToken, commandID string) error {
	return repo.FailJob(
		ctx, jobID, leaseToken,
		jobsmodel.FailureKindUser.ToString(), terminalFailureErrorCode, terminalFailureMessage,
		terminalreason.NonZeroExit.String(), commandID,
	)
}

// cancelClaimedTerminal invokes CancelClaimedJob with the reason the executor
// reports when workflow termination cancels a claimed job.
func cancelClaimedTerminal(ctx context.Context, repo *Repository, jobID, leaseToken, commandID string) error {
	return repo.CancelClaimedJob(ctx, jobID, leaseToken, terminalreason.WorkflowTerminated.String(), commandID)
}

// fixtureTag returns a short unique identity fragment. Fixtures never derive
// identity from t.Name(): a test that seeds several fixtures reuses one name,
// and the long names of subtests overflow users.email VARCHAR(100).
func fixtureTag() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}

// seedIsolatedWorkflow inserts a fresh user and workflow owned by nobody else,
// and registers cleanup that deletes exactly those rows (jobs included, through
// the user cascade) even when the test aborts.
func seedIsolatedWorkflow(ctx context.Context, t *testing.T, pg *postgres.Postgres) (userID, workflowID string) {
	t.Helper()

	tag := fixtureTag()
	userID = testkit.SeedUser(ctx, t, pg, "cv-"+tag+"@chronoverse.test")
	workflowID = testkit.SeedWorkflow(ctx, t, pg, userID, "cv-"+tag)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		// workflows and jobs cascade from users, so one delete removes exactly
		// this fixture's rows and nothing any other test seeded.
		if _, err := pg.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
			t.Errorf("delete fixture user %q: %v", userID, err)
		}
	})
	return userID, workflowID
}

// seedClaimedJob schedules, queues and claims one job on a private workflow and
// a private runtime node. The node identity is unique per fixture so no two
// fixtures reset each other's running_jobs through seedReadyRuntimeNode, and the
// node is restored to its seeded baseline when the test ends.
func seedClaimedJob(ctx context.Context, t *testing.T, pg *postgres.Postgres, repo *Repository) claimedFixture {
	t.Helper()

	userID, workflowID := seedIsolatedWorkflow(ctx, t, pg)
	nodeID := seedReadyRuntimeNode(ctx, t, pg, "lease-"+fixtureTag())
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		if _, err := pg.Exec(cleanupCtx, `
			UPDATE runtime_nodes
			SET status = 'READY', running_jobs = 0, last_heartbeat_at = now() AT TIME ZONE 'utc'
			WHERE id = $1
		`, nodeID); err != nil {
			t.Errorf("restore fixture runtime node %q: %v", nodeID, err)
		}
	})

	scheduledAt := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	jobID, err := repo.ScheduleJob(ctx, workflowID, userID, scheduledAt, "MANUAL", "idem-lease-"+fixtureTag(), 1)
	if err != nil {
		t.Fatalf("ScheduleJob: %v", err)
	}
	queueJob(ctx, t, pg, jobID)
	claimed, ok, reason, err := repo.ClaimJob(ctx, jobID, workflowID, "lease-test-worker", uuid.NewString(), "claim-"+fixtureTag(), 30*time.Second, 1)
	if err != nil {
		t.Fatalf("ClaimJob: %v", err)
	}
	if !ok {
		t.Fatalf("ClaimJob did not claim the job: %s", reason)
	}

	return claimedFixture{
		JobID:      jobID,
		WorkflowID: workflowID,
		UserID:     userID,
		LeaseToken: claimed.LeaseToken,
	}
}

func readTerminalJobState(ctx context.Context, t *testing.T, pg *postgres.Postgres, jobID string) *terminalJobState {
	t.Helper()

	var state terminalJobState
	if err := pg.QueryRow(ctx, `
		SELECT status, started_at, completed_at, lease_token, leased_by, lease_process_instance_id,
			lease_expires_at, last_heartbeat_at, terminal_reason_code, failure_kind,
			last_error_code, last_error_message, runtime_node_id, attempts, dispatch_attempts
		FROM jobs
		WHERE id = $1
	`, jobID).Scan(
		&state.Status, &state.StartedAt, &state.CompletedAt, &state.LeaseToken, &state.LeasedBy,
		&state.LeaseProcessInstanceID, &state.LeaseExpiresAt, &state.LastHeartbeatAt,
		&state.TerminalReasonCode, &state.FailureKind, &state.LastErrorCode, &state.LastErrorMessage,
		&state.RuntimeNodeID, &state.Attempts, &state.DispatchAttempts,
	); err != nil {
		t.Fatalf("read terminal job state: %v", err)
	}
	return &state
}

func readRuntimeRunningJobs(ctx context.Context, t *testing.T, pg *postgres.Postgres, nodeID string) int {
	t.Helper()

	var running int
	if err := pg.QueryRow(ctx, `SELECT running_jobs FROM runtime_nodes WHERE id = $1`, nodeID).Scan(&running); err != nil {
		t.Fatalf("read runtime running_jobs: %v", err)
	}
	return running
}

// mustClaimedRuntimeNode returns the node that actually owns the claimed job's
// slot. Claim may select any healthy node, so the node the fixture seeded is not
// a valid substitute for the one the job row records.
func mustClaimedRuntimeNode(ctx context.Context, t *testing.T, pg *postgres.Postgres, jobID string) string {
	t.Helper()

	nodeID := readTerminalJobState(ctx, t, pg, jobID).RuntimeNodeID
	if !nodeID.Valid || nodeID.String == "" {
		t.Fatalf("claimed job %q has no runtime node, want a durable runtime slot owner", jobID)
	}
	return nodeID.String
}

// occupySpareSlots adds spare occupied slots so a duplicate decrement cannot be
// hidden by the GREATEST(0, running_jobs - 1) clamp, and returns the attributed
// node plus the resulting occupancy. Cleanup restores only the slots added here.
func occupySpareSlots(ctx context.Context, t *testing.T, pg *postgres.Postgres, jobID string) (nodeID string, occupied int) {
	t.Helper()

	const spare = 2
	nodeID = mustClaimedRuntimeNode(ctx, t, pg, jobID)
	before := readRuntimeRunningJobs(ctx, t, pg, nodeID)
	if _, err := pg.Exec(ctx, `UPDATE runtime_nodes SET running_jobs = running_jobs + $2 WHERE id = $1`, nodeID, spare); err != nil {
		t.Fatalf("occupy spare runtime slots on %q: %v", nodeID, err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		if _, err := pg.Exec(cleanupCtx, `UPDATE runtime_nodes SET running_jobs = GREATEST(0, running_jobs - $2) WHERE id = $1`, nodeID, spare); err != nil {
			t.Errorf("restore spare runtime slots on %q: %v", nodeID, err)
		}
	})
	return nodeID, before + spare
}

// countTerminalOutboxEvents counts the outbox events whose deterministic keys a
// terminal job command owns. Used where those keys are the contract under test.
func countTerminalOutboxEvents(ctx context.Context, t *testing.T, pg *postgres.Postgres, jobID string) int {
	t.Helper()

	var count int
	if err := pg.QueryRow(ctx, `
		SELECT count(*)
		FROM outbox_events
		WHERE event_key IN ($1, $2)
	`, idempotency.JobCompletedAnalyticsEventKey(jobID),
		idempotency.JobWorkflowEventKey(jobID, workflowsmodel.ActionJobFailed.ToString()),
	).Scan(&count); err != nil {
		t.Fatalf("count terminal outbox events: %v", err)
	}
	return count
}

// countJobScopedOutboxEvents counts every outbox event naming this job or its
// workflow in its payload, whatever event key or payload shape it uses. A
// cancellation that emitted an unexpected JOB_CANCELED notification would still
// be counted here, which a key allowlist cannot prove.
func countJobScopedOutboxEvents(ctx context.Context, t *testing.T, pg *postgres.Postgres, jobID, workflowID string) int {
	t.Helper()

	var count int
	if err := pg.QueryRow(ctx, `
		SELECT count(*)
		FROM outbox_events
		WHERE payload->>'JobID' = $1 OR payload->>'WorkflowID' = $2
	`, jobID, workflowID).Scan(&count); err != nil {
		t.Fatalf("count job scoped outbox events: %v", err)
	}
	return count
}

func readJobOutboxEvent(ctx context.Context, t *testing.T, pg *postgres.Postgres, eventKey string) jobOutboxEvent {
	t.Helper()

	var event jobOutboxEvent
	err := pg.QueryRow(ctx, `
		SELECT topic, kafka_key, event_key, payload
		FROM outbox_events
		WHERE event_key = $1
	`, eventKey).Scan(&event.Topic, &event.KafkaKey, &event.EventKey, &event.Payload)
	if err != nil {
		t.Fatalf("read outbox event %q: %v", eventKey, err)
	}
	return event
}

func readJobCommand(ctx context.Context, t *testing.T, pg *postgres.Postgres, jobID, operation, commandID string) (*jobCommandRow, bool) {
	t.Helper()

	var row jobCommandRow
	err := pg.QueryRow(ctx, `
		SELECT status, request_hash, resource_id, response, completed_at, expires_at
		FROM command_idempotency_keys
		WHERE scope = $1 AND operation = $2 AND idempotency_key = $3
	`, commandidempotency.JobScope(jobID), operation, commandID,
	).Scan(&row.Status, &row.RequestHash, &row.ResourceID, &row.Response, &row.CompletedAt, &row.ExpiresAt)
	if pg.IsNoRows(err) {
		return nil, false
	}
	if err != nil {
		t.Fatalf("read job command ledger row: %v", err)
	}
	return &row, true
}

func countCommandRowsByKey(ctx context.Context, t *testing.T, pg *postgres.Postgres, operation, commandID string) int {
	t.Helper()

	var count int
	if err := pg.QueryRow(ctx, `
		SELECT count(*)
		FROM command_idempotency_keys
		WHERE operation = $1 AND idempotency_key = $2
	`, operation, commandID).Scan(&count); err != nil {
		t.Fatalf("count command ledger rows: %v", err)
	}
	return count
}

func assertNullString(t *testing.T, name string, column sql.NullString, want string) {
	t.Helper()

	if !column.Valid {
		t.Fatalf("%s is NULL, want %q", name, want)
	}
	if column.String != want {
		t.Fatalf("%s = %q, want %q", name, column.String, want)
	}
}

func assertNullText(t *testing.T, name string, column sql.NullString) {
	t.Helper()

	if column.Valid {
		t.Fatalf("%s = %q, want NULL", name, column.String)
	}
}

// assertTerminalLeaseReleased asserts every lease ownership column is cleared.
func assertTerminalLeaseReleased(t *testing.T, state *terminalJobState) {
	t.Helper()

	for name, column := range map[string]sql.NullString{
		"lease_token":               state.LeaseToken,
		"leased_by":                 state.LeasedBy,
		"lease_process_instance_id": state.LeaseProcessInstanceID,
	} {
		assertNullText(t, name, column)
	}
	for name, column := range map[string]sql.NullTime{
		"lease_expires_at":  state.LeaseExpiresAt,
		"last_heartbeat_at": state.LastHeartbeatAt,
	} {
		if column.Valid {
			t.Errorf("%s = %v, want NULL after a terminal command", name, column.Time.UTC())
		}
	}
}

// assertUnchangedLeaseOwnership asserts a rejected command left every durable
// lease column exactly as the pre-command snapshot recorded it.
func assertUnchangedLeaseOwnership(t *testing.T, after, before *terminalJobState) {
	t.Helper()

	assertNullString(t, "lease_token", after.LeaseToken, before.LeaseToken.String)
	assertNullString(t, "leased_by", after.LeasedBy, before.LeasedBy.String)
	assertNullString(t, "lease_process_instance_id", after.LeaseProcessInstanceID, before.LeaseProcessInstanceID.String)
	if !after.LeaseExpiresAt.Valid || !after.LeaseExpiresAt.Time.Equal(before.LeaseExpiresAt.Time) {
		t.Fatalf("lease_expires_at = %v, want unchanged %v", after.LeaseExpiresAt.Time.UTC(), before.LeaseExpiresAt.Time.UTC())
	}
	if !after.LastHeartbeatAt.Valid || !after.LastHeartbeatAt.Time.Equal(before.LastHeartbeatAt.Time) {
		t.Fatalf("last_heartbeat_at = %v, want unchanged %v", after.LastHeartbeatAt.Time.UTC(), before.LastHeartbeatAt.Time.UTC())
	}
}

func assertUnchangedCompletion(t *testing.T, after, before *terminalJobState) {
	t.Helper()

	if !after.CompletedAt.Valid || !before.CompletedAt.Valid {
		t.Fatalf("completed_at validity = %v/%v, want both set", after.CompletedAt.Valid, before.CompletedAt.Valid)
	}
	if !after.CompletedAt.Time.Equal(before.CompletedAt.Time) {
		t.Fatalf("completed_at = %v, want unchanged %v", after.CompletedAt.Time.UTC(), before.CompletedAt.Time.UTC())
	}
}

func assertRetryCountersUnchanged(t *testing.T, state, want *terminalJobState) {
	t.Helper()

	if state.Attempts != want.Attempts {
		t.Errorf("attempts = %d, want %d (terminal commands must not inflate retry counters)", state.Attempts, want.Attempts)
	}
	if state.DispatchAttempts != want.DispatchAttempts {
		t.Errorf("dispatch_attempts = %d, want %d", state.DispatchAttempts, want.DispatchAttempts)
	}
}

// assertNoOutboxEffect asserts the fixture produced no outbox event of any shape,
// not merely none of the two known terminal keys.
func assertNoOutboxEffect(ctx context.Context, t *testing.T, pg *postgres.Postgres, fixture claimedFixture) {
	t.Helper()

	if count := countJobScopedOutboxEvents(ctx, t, pg, fixture.JobID, fixture.WorkflowID); count != 0 {
		t.Fatalf("outbox events naming job %q or workflow %q = %d, want 0 while the command is rejected", fixture.JobID, fixture.WorkflowID, count)
	}
}

func assertSameLedgerRow(t *testing.T, after, before *jobCommandRow) {
	t.Helper()

	if after.RequestHash != before.RequestHash {
		t.Fatalf("command request hash = %q, want unchanged %q", after.RequestHash, before.RequestHash)
	}
	if !after.CompletedAt.Valid || !before.CompletedAt.Valid || !after.CompletedAt.Time.Equal(before.CompletedAt.Time) {
		t.Fatalf("command completed_at = %v, want unchanged %v", after.CompletedAt.Time, before.CompletedAt.Time)
	}
}

func decodeAnalyticsEvent(t *testing.T, payload []byte) (analyticsmodel.AnalyticEvent, analyticsmodel.EventTypeJobsData) {
	t.Helper()

	var event analyticsmodel.AnalyticEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("decode analytics outbox payload: %v", err)
	}
	var data analyticsmodel.EventTypeJobsData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		t.Fatalf("decode analytics outbox data: %v", err)
	}
	return event, data
}

func decodeWorkflowEvent(t *testing.T, payload []byte) workflowsmodel.WorkflowEvent {
	t.Helper()

	var event workflowsmodel.WorkflowEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("decode workflow outbox payload: %v", err)
	}
	return event
}

// staleLeaseToken keeps the worker prefix and swaps only the random half, so the
// forged token is well formed, owned by the same worker, and distinct.
func staleLeaseToken(leaseToken string) string {
	worker, _, found := strings.Cut(leaseToken, ":")
	if !found {
		return staleLeaseTokenSuffix
	}
	return worker + ":" + staleLeaseTokenSuffix
}

//nolint:gocyclo // One durable failure transition verifies job, lease, runtime, ledger and both outbox effects together.
func TestIntegrationFailJobPersistsTerminalFailureStateAndEffects(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedClaimedJob(ctx, t, pg, repo)
	nodeID, occupied := occupySpareSlots(ctx, t, pg, fixture.JobID)
	commandID := "fail-" + fixtureTag()

	if err := failJobTerminal(ctx, repo, fixture.JobID, fixture.LeaseToken, commandID); err != nil {
		t.Fatalf("FailJob: %v", err)
	}

	state := readTerminalJobState(ctx, t, pg, fixture.JobID)
	if state.Status != "FAILED" {
		t.Fatalf("job status = %q, want %q", state.Status, "FAILED")
	}
	if !state.CompletedAt.Valid {
		t.Fatal("completed_at is NULL, want a durable completion instant")
	}
	if state.CompletedAt.Time.Before(state.StartedAt.Time) {
		t.Fatalf("completed_at %v precedes started_at %v", state.CompletedAt.Time.UTC(), state.StartedAt.Time.UTC())
	}
	assertTerminalLeaseReleased(t, state)
	assertNullString(t, "terminal_reason_code", state.TerminalReasonCode, terminalreason.NonZeroExit.String())
	assertNullString(t, failureKindColumn, state.FailureKind, jobsmodel.FailureKindUser.ToString())
	assertNullString(t, "last_error_code", state.LastErrorCode, terminalFailureErrorCode)
	assertNullString(t, "last_error_message", state.LastErrorMessage, terminalFailureMessage)
	assertRetryCountersUnchanged(t, state, &terminalJobState{Attempts: 1, DispatchAttempts: 1})

	// Exactly one occupied slot is released; the spare slots survive so a
	// duplicate decrement would be visible.
	if got := readRuntimeRunningJobs(ctx, t, pg, nodeID); got != occupied-1 {
		t.Fatalf("runtime running_jobs = %d, want %d after releasing exactly one slot", got, occupied-1)
	}

	analyticsEvent := readJobOutboxEvent(ctx, t, pg, idempotency.JobCompletedAnalyticsEventKey(fixture.JobID))
	if analyticsEvent.Topic != "analytics" || analyticsEvent.KafkaKey != fixture.WorkflowID {
		t.Fatalf("analytics outbox topic/key = %q/%q, want %q/%q", analyticsEvent.Topic, analyticsEvent.KafkaKey, "analytics", fixture.WorkflowID)
	}
	decodedAnalytics, duration := decodeAnalyticsEvent(t, analyticsEvent.Payload)
	if decodedAnalytics.EventKey != idempotency.JobCompletedAnalyticsEventKey(fixture.JobID) {
		t.Fatalf("analytics event key = %q, want the job-scoped deterministic key", decodedAnalytics.EventKey)
	}
	if decodedAnalytics.EventType != analyticsmodel.EventTypeJobs {
		t.Fatalf("analytics event type = %q, want %q", decodedAnalytics.EventType, analyticsmodel.EventTypeJobs)
	}
	if decodedAnalytics.UserID != fixture.UserID || decodedAnalytics.WorkflowID != fixture.WorkflowID {
		t.Fatalf("analytics user/workflow = %q/%q, want %q/%q", decodedAnalytics.UserID, decodedAnalytics.WorkflowID, fixture.UserID, fixture.WorkflowID)
	}
	if want := uint64(state.CompletedAt.Time.Sub(state.StartedAt.Time).Seconds()); duration.JobExecutionDuration != want {
		t.Fatalf("analytics job_execution_duration = %d, want %d derived from the durable row", duration.JobExecutionDuration, want)
	}

	workflowEvent := readJobOutboxEvent(ctx, t, pg, idempotency.JobWorkflowEventKey(fixture.JobID, workflowsmodel.ActionJobFailed.ToString()))
	if workflowEvent.Topic != "workflows" || workflowEvent.KafkaKey != fixture.WorkflowID {
		t.Fatalf("workflow outbox topic/key = %q/%q, want %q/%q", workflowEvent.Topic, workflowEvent.KafkaKey, "workflows", fixture.WorkflowID)
	}
	decodedWorkflow := decodeWorkflowEvent(t, workflowEvent.Payload)
	if decodedWorkflow.Action != workflowsmodel.ActionJobFailed {
		t.Fatalf("workflow event action = %q, want %q", decodedWorkflow.Action, workflowsmodel.ActionJobFailed)
	}
	if decodedWorkflow.EventKey != idempotency.JobWorkflowEventKey(fixture.JobID, workflowsmodel.ActionJobFailed.ToString()) {
		t.Fatalf("workflow event key = %q, want the job/action deterministic key", decodedWorkflow.EventKey)
	}
	if decodedWorkflow.ID != fixture.WorkflowID || decodedWorkflow.UserID != fixture.UserID || decodedWorkflow.JobID != fixture.JobID {
		t.Fatalf("workflow event ownership = %q/%q/%q, want %q/%q/%q", decodedWorkflow.ID, decodedWorkflow.UserID, decodedWorkflow.JobID, fixture.WorkflowID, fixture.UserID, fixture.JobID)
	}
	assertNullString(t, "workflow event failure_kind", sql.NullString{String: decodedWorkflow.FailureKind, Valid: true}, jobsmodel.FailureKindUser.ToString())
	assertNullString(t, "workflow event error_code", sql.NullString{String: decodedWorkflow.ErrorCode, Valid: true}, terminalFailureErrorCode)
	assertNullString(t, "workflow event error_message", sql.NullString{String: decodedWorkflow.ErrorMessage, Valid: true}, terminalFailureMessage)

	if got := countTerminalOutboxEvents(ctx, t, pg, fixture.JobID); got != 2 {
		t.Fatalf("deterministic terminal outbox events = %d, want exactly 2", got)
	}
	if got := countJobScopedOutboxEvents(ctx, t, pg, fixture.JobID, fixture.WorkflowID); got != 2 {
		t.Fatalf("job scoped outbox events = %d, want exactly 2 (no extra terminal notification)", got)
	}

	command, ok := readJobCommand(ctx, t, pg, fixture.JobID, commandidempotency.OperationJobFail, commandID)
	if !ok {
		t.Fatalf("command ledger has no row for the completed fail command %q", commandID)
	}
	if command.Status != "COMPLETED" {
		t.Fatalf("command status = %q, want %q", command.Status, "COMPLETED")
	}
	assertNullString(t, "command resource_id", command.ResourceID, fixture.JobID)
	if !command.ExpiresAt.Valid || !command.CompletedAt.Valid {
		t.Fatalf("command completed_at/expires_at = %v/%v, want both set", command.CompletedAt.Valid, command.ExpiresAt.Valid)
	}
}

func TestIntegrationFailJobStoresBoundedValidUTF8FailureMessage(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	tests := []struct {
		name    string
		message string
		want    string
	}{
		{
			name:    "multi-byte rune straddling the byte boundary",
			message: strings.Repeat("日", maxJobErrorMessageLength) + "trailing detail",
			// The literal expectation, not the production helper: a partial rune
			// at the 4096th byte must be trimmed back to the last whole rune.
			want: strings.Repeat("日", expectedTruncatedRunes),
		},
		{
			name:    "raw container stderr with invalid utf-8 bytes",
			message: strings.Repeat("a", maxJobErrorMessageLength-1) + "\xc3",
			want:    strings.Repeat("a", maxJobErrorMessageLength-1),
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := seedClaimedJob(ctx, t, pg, repo)
			commandID := "fail-truncate-" + fixtureTag()

			if err := repo.FailJob(
				ctx, fixture.JobID, fixture.LeaseToken,
				jobsmodel.FailureKindUser.ToString(), terminalFailureErrorCode, testCase.message,
				terminalreason.ExecutionFailed.String(), commandID,
			); err != nil {
				t.Fatalf("FailJob: %v", err)
			}

			stored := readTerminalJobState(ctx, t, pg, fixture.JobID)
			if stored.Status != "FAILED" {
				t.Fatalf("job status = %q, want %q", stored.Status, "FAILED")
			}
			if !utf8.ValidString(stored.LastErrorMessage.String) {
				t.Fatalf("stored failure message %q is not valid UTF-8", stored.LastErrorMessage.String)
			}
			if got := len(stored.LastErrorMessage.String); got != len(testCase.want) {
				t.Fatalf("stored failure message length = %d, want %d", got, len(testCase.want))
			}
			assertNullString(t, "last_error_message", stored.LastErrorMessage, testCase.want)
			workflowEvent := decodeWorkflowEvent(t, readJobOutboxEvent(ctx, t, pg, idempotency.JobWorkflowEventKey(fixture.JobID, workflowsmodel.ActionJobFailed.ToString())).Payload)
			if workflowEvent.ErrorMessage != testCase.want {
				t.Fatalf("workflow event error message = %q, want the literal %q", workflowEvent.ErrorMessage, testCase.want)
			}

			// The truncated message, not the raw one, is what the command ledger
			// hashes, so replaying the same oversized payload is still the same
			// command rather than a conflicting one.
			if err := repo.FailJob(
				ctx, fixture.JobID, fixture.LeaseToken,
				jobsmodel.FailureKindUser.ToString(), terminalFailureErrorCode, testCase.message,
				terminalreason.ExecutionFailed.String(), commandID,
			); err != nil {
				t.Fatalf("FailJob (replay of the same oversized message): %v", err)
			}
			if got := countJobScopedOutboxEvents(ctx, t, pg, fixture.JobID, fixture.WorkflowID); got != 2 {
				t.Fatalf("job scoped outbox events = %d after replay, want exactly 2", got)
			}
		})
	}
}

func TestIntegrationTerminalCommandsRejectStaleLeaseTokenWithoutAnyEffect(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	for _, operation := range terminalCommandOperations {
		t.Run(operation.Name, func(t *testing.T) {
			fixture := seedClaimedJob(ctx, t, pg, repo)
			nodeID, occupied := occupySpareSlots(ctx, t, pg, fixture.JobID)
			before := readTerminalJobState(ctx, t, pg, fixture.JobID)
			commandID := "stale-" + fixtureTag()

			err := operation.Invoke(ctx, repo, fixture.JobID, staleLeaseToken(fixture.LeaseToken), commandID)
			if status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("%s(stale token) code = %v, want %v (err: %v)", operation.Name, status.Code(err), codes.FailedPrecondition, err)
			}

			after := readTerminalJobState(ctx, t, pg, fixture.JobID)
			if after.Status != "RUNNING" {
				t.Fatalf("job status = %q, want %q after a rejected terminal command", after.Status, "RUNNING")
			}
			if after.CompletedAt.Valid {
				t.Fatalf("completed_at = %v, want NULL after a rejected terminal command", after.CompletedAt.Time.UTC())
			}
			assertUnchangedLeaseOwnership(t, after, before)
			for name, column := range map[string]sql.NullString{
				failureKindColumn:      after.FailureKind,
				"last_error_code":      after.LastErrorCode,
				"last_error_message":   after.LastErrorMessage,
				"terminal_reason_code": after.TerminalReasonCode,
			} {
				assertNullText(t, name, column)
			}
			assertRetryCountersUnchanged(t, after, before)
			if got := readRuntimeRunningJobs(ctx, t, pg, nodeID); got != occupied {
				t.Fatalf("runtime running_jobs = %d, want unchanged %d after a rejected terminal command", got, occupied)
			}
			assertNoOutboxEffect(ctx, t, pg, fixture)
			if _, ok := readJobCommand(ctx, t, pg, fixture.JobID, operation.Operation, commandID); ok {
				t.Fatalf("command ledger kept a reservation for the rejected %s command %q", operation.Operation, commandID)
			}

			// The still-held lease must remain usable, proving the rejection
			// mutated neither the job nor its lease.
			if err := operation.Invoke(ctx, repo, fixture.JobID, fixture.LeaseToken, "authoritative-"+fixtureTag()); err != nil {
				t.Fatalf("%s with the authoritative lease after a stale rejection: %v", operation.Name, err)
			}
			if got := readTerminalJobState(ctx, t, pg, fixture.JobID).Status; got != operation.WantStatus {
				t.Fatalf("job status = %q, want %q once the authoritative lease is used", got, operation.WantStatus)
			}
		})
	}
}

func TestIntegrationTerminalCommandsReplaysCommandWithoutRepeatingEffects(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	for _, operation := range terminalCommandOperations {
		t.Run(operation.Name, func(t *testing.T) {
			fixture := seedClaimedJob(ctx, t, pg, repo)
			nodeID, occupied := occupySpareSlots(ctx, t, pg, fixture.JobID)
			commandID := "replay-" + fixtureTag()

			if err := operation.Invoke(ctx, repo, fixture.JobID, fixture.LeaseToken, commandID); err != nil {
				t.Fatalf("%s: %v", operation.Name, err)
			}
			accepted := readTerminalJobState(ctx, t, pg, fixture.JobID)
			commandAfterFirst, ok := readJobCommand(ctx, t, pg, fixture.JobID, operation.Operation, commandID)
			if !ok {
				t.Fatalf("command ledger has no row for %q", commandID)
			}
			firstSlotCount := readRuntimeRunningJobs(ctx, t, pg, nodeID)
			if firstSlotCount != occupied-1 {
				t.Fatalf("runtime running_jobs = %d, want %d after the first terminal command", firstSlotCount, occupied-1)
			}
			wantOutbox := terminalOutboxEventCount(operation.Operation)
			assertOutboxEffectCount(ctx, t, pg, fixture, wantOutbox)

			if err := operation.Invoke(ctx, repo, fixture.JobID, fixture.LeaseToken, commandID); err != nil {
				t.Fatalf("%s (idempotent replay): %v", operation.Name, err)
			}

			replayed := readTerminalJobState(ctx, t, pg, fixture.JobID)
			if replayed.Status != accepted.Status {
				t.Fatalf("replayed job status = %q, want unchanged %q", replayed.Status, accepted.Status)
			}
			assertUnchangedCompletion(t, replayed, accepted)
			assertRetryCountersUnchanged(t, replayed, accepted)
			// The spare slots make a repeated decrement observable.
			if got := readRuntimeRunningJobs(ctx, t, pg, nodeID); got != firstSlotCount {
				t.Fatalf("runtime running_jobs = %d after replay, want unchanged %d", got, firstSlotCount)
			}
			assertOutboxEffectCount(ctx, t, pg, fixture, wantOutbox)

			commandAfterReplay, ok := readJobCommand(ctx, t, pg, fixture.JobID, operation.Operation, commandID)
			if !ok {
				t.Fatalf("command ledger lost the row for %q", commandID)
			}
			assertSameLedgerRow(t, commandAfterReplay, commandAfterFirst)
		})
	}
}

func TestIntegrationTerminalCommandsRejectsConflictingCommandPayload(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	for _, operation := range terminalCommandOperations {
		t.Run(operation.Name, func(t *testing.T) {
			fixture := seedClaimedJob(ctx, t, pg, repo)
			nodeID, occupied := occupySpareSlots(ctx, t, pg, fixture.JobID)
			commandID := "conflict-" + fixtureTag()

			if err := operation.Invoke(ctx, repo, fixture.JobID, fixture.LeaseToken, commandID); err != nil {
				t.Fatalf("%s: %v", operation.Name, err)
			}
			accepted := readTerminalJobState(ctx, t, pg, fixture.JobID)
			wantOutbox := terminalOutboxEventCount(operation.Operation)
			assertOutboxEffectCount(ctx, t, pg, fixture, wantOutbox)
			commandAfterFirst, ok := readJobCommand(ctx, t, pg, fixture.JobID, operation.Operation, commandID)
			if !ok {
				t.Fatalf("command ledger has no row for %q", commandID)
			}
			occupiedAfterFirst := readRuntimeRunningJobs(ctx, t, pg, nodeID)
			if occupiedAfterFirst != occupied-1 {
				t.Fatalf("runtime running_jobs = %d, want %d after the first terminal command", occupiedAfterFirst, occupied-1)
			}

			// The same command identity must never be re-bound to a different
			// payload. Every varied field is part of the reserved request hash.
			for _, conflict := range conflictingTerminalPayloads(operation.Operation) {
				t.Run(conflict.name, func(t *testing.T) {
					err := conflict.invoke(ctx, repo, fixture, commandID)
					if status.Code(err) != codes.AlreadyExists {
						t.Fatalf("%s(conflicting %s) code = %v, want %v (err: %v)", operation.Name, conflict.name, status.Code(err), codes.AlreadyExists, err)
					}

					after := readTerminalJobState(ctx, t, pg, fixture.JobID)
					assertUnchangedCompletion(t, after, accepted)
					if after.Status != accepted.Status || after.FailureKind != accepted.FailureKind ||
						after.LastErrorCode != accepted.LastErrorCode ||
						after.LastErrorMessage != accepted.LastErrorMessage ||
						after.TerminalReasonCode != accepted.TerminalReasonCode {
						t.Fatalf("conflicting %s changed the terminal row: before %+v after %+v", conflict.name, accepted, after)
					}
					if got := readRuntimeRunningJobs(ctx, t, pg, nodeID); got != occupiedAfterFirst {
						t.Fatalf("runtime running_jobs = %d, want unchanged %d", got, occupiedAfterFirst)
					}
					assertOutboxEffectCount(ctx, t, pg, fixture, wantOutbox)
					commandAfterConflict, ok := readJobCommand(ctx, t, pg, fixture.JobID, operation.Operation, commandID)
					if !ok {
						t.Fatalf("command ledger lost the row for %q", commandID)
					}
					assertSameLedgerRow(t, commandAfterConflict, commandAfterFirst)
				})
			}
		})
	}
}

// invokeTerminalCommand adapts a job-id command to the fixture form the table
// driven scenarios use.
func invokeTerminalCommand(
	command func(ctx context.Context, repo *Repository, jobID, leaseToken, commandID string) error,
) func(ctx context.Context, repo *Repository, fixture claimedFixture, commandID string) error {
	return func(ctx context.Context, repo *Repository, fixture claimedFixture, commandID string) error {
		return command(ctx, repo, fixture.JobID, fixture.LeaseToken, commandID)
	}
}

// conflictingTerminalPayload is one already-accepted terminal command replayed
// with exactly one hashed field changed.
type conflictingTerminalPayload struct {
	name   string
	invoke func(ctx context.Context, repo *Repository, fixture claimedFixture, commandID string) error
}

// conflictingTerminalPayloads enumerates the payload fields each terminal
// command reserves. Cancellation hashes terminal_reason_code beyond the lease
// token and job identity; failure also hashes the failure metadata.
func conflictingTerminalPayloads(operation string) []conflictingTerminalPayload {
	cancel := func(reason string) func(context.Context, *Repository, claimedFixture, string) error {
		return func(ctx context.Context, repo *Repository, fixture claimedFixture, commandID string) error {
			return repo.CancelClaimedJob(ctx, fixture.JobID, fixture.LeaseToken, reason, commandID)
		}
	}
	fail := func(kind, errorCode, message, reason string) func(context.Context, *Repository, claimedFixture, string) error {
		return func(ctx context.Context, repo *Repository, fixture claimedFixture, commandID string) error {
			return repo.FailJob(ctx, fixture.JobID, fixture.LeaseToken, kind, errorCode, message, reason, commandID)
		}
	}
	if operation == commandidempotency.OperationJobCancelClaimed {
		return []conflictingTerminalPayload{
			{name: "different terminal reason code", invoke: cancel(terminalreason.WorkflowUpdated.String())},
			{name: "empty terminal reason code", invoke: cancel("")},
		}
	}

	user := jobsmodel.FailureKindUser.ToString()
	return []conflictingTerminalPayload{
		{name: "different error message", invoke: fail(user, terminalFailureErrorCode, "container exited with code 2", terminalreason.NonZeroExit.String())},
		{name: "different error code", invoke: fail(user, "DeadlineExceeded", terminalFailureMessage, terminalreason.NonZeroExit.String())},
		{name: "different failure kind", invoke: fail(jobsmodel.FailureKindSystem.ToString(), terminalFailureErrorCode, terminalFailureMessage, terminalreason.NonZeroExit.String())},
		{name: "different terminal reason code", invoke: fail(user, terminalFailureErrorCode, terminalFailureMessage, terminalreason.ExecutionFailed.String())},
	}
}

// terminalOutboxEventCount is the durable outbox footprint each terminal command
// owns: failure publishes an analytics plus a workflow event, claimed-job
// cancellation deliberately publishes neither.
func terminalOutboxEventCount(operation string) int {
	if operation == commandidempotency.OperationJobFail {
		return 2
	}
	return 0
}

func TestIntegrationTerminalCommandsRollbackReservationWhenOwnershipFails(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	for _, operation := range terminalCommandOperations {
		t.Run(operation.Name, func(t *testing.T) {
			fixture := seedClaimedJob(ctx, t, pg, repo)
			commandID := "rollback-" + fixtureTag()

			err := operation.Invoke(ctx, repo, fixture.JobID, staleLeaseToken(fixture.LeaseToken), commandID)
			if status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("%s(stale token) code = %v, want %v (err: %v)", operation.Name, status.Code(err), codes.FailedPrecondition, err)
			}
			if _, ok := readJobCommand(ctx, t, pg, fixture.JobID, operation.Operation, commandID); ok {
				t.Fatalf("command ledger kept a %s reservation after the owning transaction rolled back", operation.Operation)
			}
			if got := readTerminalJobState(ctx, t, pg, fixture.JobID).Status; got != "RUNNING" {
				t.Fatalf("job status = %q, want %q", got, "RUNNING")
			}

			// Reusing the very same command identity afterwards must be accepted.
			// A leaked PROCESSING reservation would surface here as codes.Aborted
			// instead, which is what makes this the strongest available proof.
			if err := operation.Invoke(ctx, repo, fixture.JobID, fixture.LeaseToken, commandID); err != nil {
				t.Fatalf("%s after a rolled-back reservation: %v", operation.Name, err)
			}
			command, ok := readJobCommand(ctx, t, pg, fixture.JobID, operation.Operation, commandID)
			if !ok {
				t.Fatalf("command ledger has no row for the accepted %s command %q", operation.Name, commandID)
			}
			if command.Status != "COMPLETED" {
				t.Fatalf("command status = %q, want %q", command.Status, "COMPLETED")
			}
			assertNullString(t, "command resource_id", command.ResourceID, fixture.JobID)
		})
	}
}

func TestIntegrationTerminalCommandsCannotMutateTerminalJobUnderFreshCommand(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	// Both terminal commands are tried against a job the other one finished:
	// terminal state is final regardless of which command arrives next.
	scenarios := []struct {
		name            string
		first           func(ctx context.Context, repo *Repository, fixture claimedFixture, commandID string) error
		firstOperation  string
		second          func(ctx context.Context, repo *Repository, fixture claimedFixture, commandID string) error
		secondOperation string
		wantStatus      string
		wantReason      string
	}{
		{
			name:            "failure is not cancellable",
			first:           invokeTerminalCommand(failJobTerminal),
			firstOperation:  commandidempotency.OperationJobFail,
			second:          invokeTerminalCommand(cancelClaimedTerminal),
			secondOperation: commandidempotency.OperationJobCancelClaimed,
			wantStatus:      "FAILED",
			wantReason:      terminalreason.NonZeroExit.String(),
		},
		{
			name:            "cancellation is not failable",
			first:           invokeTerminalCommand(cancelClaimedTerminal),
			firstOperation:  commandidempotency.OperationJobCancelClaimed,
			second:          invokeTerminalCommand(failJobTerminal),
			secondOperation: commandidempotency.OperationJobFail,
			wantStatus:      "CANCELED",
			wantReason:      terminalreason.WorkflowTerminated.String(),
		},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			fixture := seedClaimedJob(ctx, t, pg, repo)
			nodeID, occupied := occupySpareSlots(ctx, t, pg, fixture.JobID)
			if err := scenario.first(ctx, repo, fixture, "first-"+fixtureTag()); err != nil {
				t.Fatalf("first terminal command: %v", err)
			}
			settled := readTerminalJobState(ctx, t, pg, fixture.JobID)
			wantOutbox := terminalOutboxEventCount(scenario.firstOperation)
			assertOutboxEffectCount(ctx, t, pg, fixture, wantOutbox)

			// A different fresh command must not rewrite the terminal row.
			rejected := "rejected-" + fixtureTag()
			err := scenario.second(ctx, repo, fixture, rejected)
			if status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("second terminal command code = %v, want %v (err: %v)", status.Code(err), codes.FailedPrecondition, err)
			}
			if _, ok := readJobCommand(ctx, t, pg, fixture.JobID, scenario.secondOperation, rejected); ok {
				t.Fatalf("command ledger kept a reservation for the rejected %s command %q", scenario.secondOperation, rejected)
			}

			after := readTerminalJobState(ctx, t, pg, fixture.JobID)
			if after.Status != scenario.wantStatus {
				t.Fatalf("job status = %q, want %q after a rejected terminal command", after.Status, scenario.wantStatus)
			}
			assertUnchangedCompletion(t, after, settled)
			assertNullString(t, "terminal_reason_code", after.TerminalReasonCode, scenario.wantReason)
			assertTerminalLeaseReleased(t, after)
			if after.FailureKind != settled.FailureKind || after.LastErrorCode != settled.LastErrorCode ||
				after.LastErrorMessage != settled.LastErrorMessage {
				t.Fatalf("rejected terminal command rewrote the failure metadata: before %+v after %+v", settled, after)
			}
			if got := readRuntimeRunningJobs(ctx, t, pg, nodeID); got != occupied-1 {
				t.Fatalf("runtime running_jobs = %d, want %d after exactly one terminal transition", got, occupied-1)
			}
			assertOutboxEffectCount(ctx, t, pg, fixture, wantOutbox)
		})
	}
}

func TestIntegrationCancelClaimedJobPersistsTerminalCancellation(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedClaimedJob(ctx, t, pg, repo)
	nodeID, occupied := occupySpareSlots(ctx, t, pg, fixture.JobID)
	commandID := "cancel-" + fixtureTag()

	if err := cancelClaimedTerminal(ctx, repo, fixture.JobID, fixture.LeaseToken, commandID); err != nil {
		t.Fatalf("CancelClaimedJob: %v", err)
	}

	state := readTerminalJobState(ctx, t, pg, fixture.JobID)
	if state.Status != "CANCELED" {
		t.Fatalf("job status = %q, want %q", state.Status, "CANCELED")
	}
	if !state.CompletedAt.Valid {
		t.Fatal("completed_at is NULL, want a durable completion instant")
	}
	if state.CompletedAt.Time.Before(state.StartedAt.Time) {
		t.Fatalf("completed_at %v precedes started_at %v", state.CompletedAt.Time.UTC(), state.StartedAt.Time.UTC())
	}
	assertTerminalLeaseReleased(t, state)
	assertNullString(t, "terminal_reason_code", state.TerminalReasonCode, terminalreason.WorkflowTerminated.String())
	for name, column := range map[string]sql.NullString{
		failureKindColumn:    state.FailureKind,
		"last_error_code":    state.LastErrorCode,
		"last_error_message": state.LastErrorMessage,
	} {
		assertNullText(t, name, column)
	}
	assertRetryCountersUnchanged(t, state, &terminalJobState{Attempts: 1, DispatchAttempts: 1})

	if got := readRuntimeRunningJobs(ctx, t, pg, nodeID); got != occupied-1 {
		t.Fatalf("runtime running_jobs = %d, want %d after releasing exactly one slot", got, occupied-1)
	}

	// Claimed-job cancellation deliberately publishes no workflow or analytics
	// event. Assert the job-scoped zero explicitly instead of assuming it, and
	// use the payload-scoped counter so an unexpected event of any shape fails.
	assertNoOutboxEffect(ctx, t, pg, fixture)

	command, ok := readJobCommand(ctx, t, pg, fixture.JobID, commandidempotency.OperationJobCancelClaimed, commandID)
	if !ok {
		t.Fatalf("command ledger has no row for the completed cancel command %q", commandID)
	}
	if command.Status != "COMPLETED" {
		t.Fatalf("command status = %q, want %q", command.Status, "COMPLETED")
	}
	assertNullString(t, "command resource_id", command.ResourceID, fixture.JobID)
}

func TestIntegrationCancelClaimedJobRejectsEmptyTerminalReasonConflict(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedClaimedJob(ctx, t, pg, repo)
	commandID := "cancel-conflict-" + fixtureTag()

	if err := cancelClaimedTerminal(ctx, repo, fixture.JobID, fixture.LeaseToken, commandID); err != nil {
		t.Fatalf("CancelClaimedJob: %v", err)
	}
	canceled := readTerminalJobState(ctx, t, pg, fixture.JobID)

	// An empty reason code hashes differently from the accepted one, so the same
	// command identity must be refused instead of silently re-binding.
	err := repo.CancelClaimedJob(ctx, fixture.JobID, fixture.LeaseToken, "", commandID)
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("CancelClaimedJob(conflicting empty reason) code = %v, want %v (err: %v)", status.Code(err), codes.AlreadyExists, err)
	}
	after := readTerminalJobState(ctx, t, pg, fixture.JobID)
	assertUnchangedCompletion(t, after, canceled)
	assertNullString(t, "terminal_reason_code", after.TerminalReasonCode, terminalreason.WorkflowTerminated.String())
}

func TestIntegrationTerminalCommandsRejectMalformedJobIdentity(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	for _, operation := range terminalCommandOperations {
		t.Run(operation.Name, func(t *testing.T) {
			for _, jobID := range []string{"not-a-uuid", ""} {
				commandID := "malformed-" + fixtureTag()
				err := operation.Invoke(ctx, repo, jobID, "worker:token", commandID)
				if status.Code(err) != codes.InvalidArgument {
					t.Fatalf("%s(%q) code = %v, want %v (err: %v)", operation.Name, jobID, status.Code(err), codes.InvalidArgument, err)
				}
				if count := countCommandRowsByKey(ctx, t, pg, operation.Operation, commandID); count != 0 {
					t.Fatalf("command ledger holds %d %s rows for the malformed job id %q", count, operation.Operation, jobID)
				}
			}
		})
	}
}

// assertOutboxEffectCount asserts both the deterministic terminal keys and the
// job-scoped payload total, so an extra event of an unexpected shape fails too.
func assertOutboxEffectCount(ctx context.Context, t *testing.T, pg *postgres.Postgres, fixture claimedFixture, want int) {
	t.Helper()

	if got := countTerminalOutboxEvents(ctx, t, pg, fixture.JobID); got != want {
		t.Fatalf("deterministic terminal outbox events for job %q = %d, want %d", fixture.JobID, got, want)
	}
	if got := countJobScopedOutboxEvents(ctx, t, pg, fixture.JobID, fixture.WorkflowID); got != want {
		t.Fatalf("job scoped outbox events for job %q = %d, want %d", fixture.JobID, got, want)
	}
}
