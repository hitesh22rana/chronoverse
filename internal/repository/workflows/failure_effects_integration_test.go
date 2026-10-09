//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package workflows

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

const (
	// fixtureCleanupTimeout bounds every cleanup statement so a wedged database
	// connection cannot hang the suite.
	fixtureCleanupTimeout = 15 * time.Second
	// fixtureImage is the resolved image reference a completed CONTAINER build
	// carries. Every fixture shares one payload and image so only the identities
	// under assertion can explain a differing outcome.
	fixtureImage = "alpine:3.22.2"
	// fixturePayload is the CONTAINER payload seeded into every fixture workflow.
	fixturePayload = `{"image":"alpine:3.22.2"}`
	// fixtureImageDigest is the resolved digest a completed CONTAINER build must carry.
	fixtureImageDigest = "sha256:3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5a4b3c2d1e0f9a8b7c6d5e4f3a2b"
	// fixtureScheduleOffset is how far ahead fixture jobs are scheduled. It keeps
	// them from becoming due, so nothing but the command under test can move
	// them and no test needs to wait.
	fixtureScheduleOffset = time.Hour
	// fixtureOccupiedSlots is the runtime occupancy a fixture node starts with:
	// one slot for the fixture's own running job plus two that no job row
	// references. The spare occupancy is what makes a repeated release
	// observable, because every release path clamps at zero and a fixture that
	// started at a single occupied slot would hide a second decrement behind that
	// clamp. The node is private to the fixture and removed during cleanup, so
	// the unattributed slots cannot affect any other test.
	fixtureOccupiedSlots = 3
	// terminalEffectFailed and terminalEffectCompleted are the durable
	// workflow_terminal_effects effect values. They are named here so every
	// assertion reads as the ledger contract it checks.
	terminalEffectFailed    = "FAILED"
	terminalEffectCompleted = "COMPLETED"
)

// workflowFixture is one workflow created through the public repository API,
// together with the durable identities its assertions need.
type workflowFixture struct {
	UserID                    string
	WorkflowID                string
	MaxConsecutiveJobFailures int32
	Interval                  int32
	BuildStatus               string
	Generation                int64
	ResolvedImageRef          string
	ResolvedImageDigest       string
}

// terminalEffectRow is one durable workflow_terminal_effects ledger row. Its
// primary key is the job id, which is what makes a terminal job effect
// exactly-once across Kafka redrives and service retries.
type terminalEffectRow struct {
	WorkflowID       string
	UserID           string
	Effect           string
	ThresholdReached sql.NullBool
	CreatedAt        time.Time
}

// workflowOutboxEvent is the durable publish intent one workflow command left behind.
type workflowOutboxEvent struct {
	Topic    string
	KafkaKey string
	EventKey string
	Payload  []byte
}

// workflowFailureState is the durable failure surface of one workflow row. Every
// counter and termination command must move all of it, or none of it.
type workflowFailureState struct {
	ConsecutiveJobFailuresCount int32
	MaxConsecutiveFailures      int32
	BuildStatus                 string
	Generation                  int64
	TerminatedAt                sql.NullTime
}

// fixtureJobState is the durable surface of one fixture job row that workflow
// commands are allowed to invalidate. It carries the whole claim identity, not
// only the token: the worker, the process instance, the expiry and the heartbeat
// are part of the same ownership, and an assertion about one of them is only
// meaningful if the fixture seeded it first.
type fixtureJobState struct {
	Trigger                string
	Status                 string
	CompletedAt            sql.NullTime
	LeaseToken             sql.NullString
	LeasedBy               sql.NullString
	LeaseProcessInstanceID sql.NullString
	TerminalReasonCode     sql.NullString
	RuntimeNodeID          sql.NullString
	LeaseExpiresAt         sql.NullTime
	LastHeartbeatAt        sql.NullTime
}

// fixtureTag returns a short unique identity fragment. Fixtures never derive
// identity from t.Name(): subtests reuse names and users.email is a
// VARCHAR(100), so every fixture owns a fresh random tag instead.
func fixtureTag() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}

// registerFixtureCleanup removes exactly the rows one fixture created. Workflows,
// jobs and terminal effects all cascade from the user, so one delete removes
// this fixture's domain rows and nothing any other test seeded. Outbox rows carry
// no foreign key and are scoped by the fixture's own user id, which no other
// fixture can hold, so this cannot delete another test's events.
func registerFixtureCleanup(ctx context.Context, t *testing.T, pg *postgres.Postgres, userID string) {
	t.Helper()

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fixtureCleanupTimeout)
		defer cancel()
		if _, err := pg.Exec(cleanupCtx, fmt.Sprintf(`DELETE FROM %s WHERE payload->>'UserID' = $1`, postgres.TableOutboxEvents), userID); err != nil {
			t.Errorf("delete fixture outbox events for user %q: %v", userID, err)
		}
		if _, err := pg.Exec(cleanupCtx, fmt.Sprintf(`DELETE FROM %s WHERE id = $1`, postgres.TableUsers), userID); err != nil {
			t.Errorf("delete fixture user %q: %v", userID, err)
		}
	})
}

// seedWorkflowFixture creates a fixture user and one workflow owned only by it.
// The workflow is created through CreateWorkflow so its generation, build event
// and command ledger rows are exactly what production wrote.
func seedWorkflowFixture(ctx context.Context, t *testing.T, pg *postgres.Postgres, repo *Repository, maxFailures int32) *workflowFixture {
	t.Helper()

	userID := testkit.SeedUser(ctx, t, pg, "cv-"+fixtureTag()+"@chronoverse.test")
	registerFixtureCleanup(ctx, t, pg, userID)

	return seedWorkflowForUser(ctx, t, repo, userID, maxFailures)
}

// seedWorkflowForUser creates one additional workflow under an existing fixture
// user, so a single owner can hold two independent workflows.
func seedWorkflowForUser(ctx context.Context, t *testing.T, repo *Repository, userID string, maxFailures int32) *workflowFixture {
	t.Helper()

	return createFixtureWorkflow(ctx, t, repo, userID, "CONTAINER", fixturePayload, maxFailures)
}

// createFixtureWorkflow creates one workflow of an explicit kind under an
// existing fixture user. Every seed helper goes through it, so a fixture workflow
// is built identically whatever kind a case needs and the build state each one
// starts in is produced by CreateWorkflow rather than by fixture SQL.
func createFixtureWorkflow(
	ctx context.Context,
	t *testing.T,
	repo *Repository,
	userID,
	kind,
	payload string,
	maxFailures int32,
) *workflowFixture {
	t.Helper()

	const interval = int32(60)
	tag := fixtureTag()
	created, err := repo.CreateWorkflow(ctx, userID, "cv-"+tag, payload, kind, interval, maxFailures, true, "cv-create-"+tag)
	if err != nil {
		t.Fatalf("CreateWorkflow (%s): %v", kind, err)
	}

	return &workflowFixture{
		UserID:                    userID,
		WorkflowID:                created.ID,
		MaxConsecutiveJobFailures: maxFailures,
		Interval:                  interval,
		BuildStatus:               created.WorkflowBuildStatus,
		Generation:                created.Generation,
	}
}

// completeFixtureBuild drives the fixture build pipeline to COMPLETED so update
// commands can be exercised on the reschedule-only path.
func completeFixtureBuild(ctx context.Context, t *testing.T, repo *Repository, fixture *workflowFixture) {
	t.Helper()

	completedStatus := workflowsmodel.WorkflowBuildStatusCompleted.ToString()
	if err := repo.UpdateWorkflowBuildStatus(ctx, fixture.WorkflowID, fixture.UserID, "STARTED", fixture.Generation, "", ""); err != nil {
		t.Fatalf("UpdateWorkflowBuildStatus(STARTED): %v", err)
	}
	if err := repo.UpdateWorkflowBuildStatus(
		ctx, fixture.WorkflowID, fixture.UserID, completedStatus, fixture.Generation, fixtureImage, fixtureImageDigest,
	); err != nil {
		t.Fatalf("UpdateWorkflowBuildStatus(%s): %v", completedStatus, err)
	}

	built, err := repo.GetWorkflow(ctx, fixture.WorkflowID, fixture.UserID)
	if err != nil {
		t.Fatalf("GetWorkflow after build: %v", err)
	}
	fixture.BuildStatus = built.WorkflowBuildStatus
	fixture.ResolvedImageRef = built.ResolvedImageRef.String
	fixture.ResolvedImageDigest = built.ResolvedImageDigest.String
}

// seedFixtureNode inserts a private READY runtime node whose only occupied slot
// belongs to this fixture, and registers cleanup that removes it together with
// any job row still referencing it.
func seedFixtureNode(ctx context.Context, t *testing.T, pg *postgres.Postgres, runningJobs int) string {
	t.Helper()

	nodeID := "cv-node-" + fixtureTag()
	if _, err := pg.Exec(ctx, `
		INSERT INTO runtime_nodes (id, node_name, docker_endpoint, status, last_heartbeat_at, max_concurrency, running_jobs)
		VALUES ($1, $1, 'tcp://127.0.0.1:2375', 'READY', now() AT TIME ZONE 'utc', 4, $2)
	`, nodeID, runningJobs); err != nil {
		t.Fatalf("seed fixture runtime node: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fixtureCleanupTimeout)
		defer cancel()
		// The fixture's jobs cascade from its user, but the runtime-node foreign
		// key would block that delete while a job row survives, so the node
		// cleanup releases its own jobs first. Both statements are exact: this
		// node id is unique to this fixture.
		if _, err := pg.Exec(cleanupCtx, fmt.Sprintf(`DELETE FROM %s WHERE runtime_node_id = $1`, postgres.TableJobs), nodeID); err != nil {
			t.Errorf("delete fixture jobs on node %q: %v", nodeID, err)
		}
		if _, err := pg.Exec(cleanupCtx, fmt.Sprintf(`DELETE FROM %s WHERE id = $1`, postgres.TableRuntimeNodes), nodeID); err != nil {
			t.Errorf("delete fixture runtime node %q: %v", nodeID, err)
		}
	})

	return nodeID
}

// seedFixturePendingJob inserts one PENDING job owned by the fixture. slot keeps
// the automatic schedule-slot index unique without any wall-clock wait.
func seedFixturePendingJob(ctx context.Context, t *testing.T, pg *postgres.Postgres, fixture *workflowFixture, trigger string, slot int) string {
	t.Helper()

	scheduledAt := time.Now().UTC().Add(fixtureScheduleOffset + time.Duration(slot)*time.Minute).Format(time.RFC3339Nano)
	var jobID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO jobs (workflow_id, user_id, status, trigger, scheduled_at)
		VALUES ($1, $2, 'PENDING', $3::job_trigger, $4::timestamp)
		RETURNING id
	`, fixture.WorkflowID, fixture.UserID, trigger, scheduledAt).Scan(&jobID); err != nil {
		t.Fatalf("seed %s pending fixture job: %v", trigger, err)
	}

	return jobID
}

// seedFixtureStaleLeasedQueuedJob inserts one AUTOMATIC job in the QUEUED state a
// scheduler dispatch leaves behind, still carrying the whole claim identity a
// worker holds: lease token, worker id, process instance id, expiry and
// heartbeat. That combination is deliberately stale metadata no production path
// creates, because the claim is granted only in the statement that flips the row
// to RUNNING and every path out of RUNNING clears all five columns; it is seeded
// as fixture SQL so the cancellation has real ownership columns to clean up. The
// row attributes no runtime node, so nothing here claims a runtime slot.
func seedFixtureStaleLeasedQueuedJob(ctx context.Context, t *testing.T, pg *postgres.Postgres, fixture *workflowFixture, slot int) string {
	t.Helper()

	scheduledAt := time.Now().UTC().Add(fixtureScheduleOffset + time.Duration(slot)*time.Minute).Format(time.RFC3339Nano)
	var jobID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO jobs (
			workflow_id, user_id, status, trigger, scheduled_at, queued_at, dispatch_attempts,
			lease_token, leased_by, lease_process_instance_id, lease_expires_at, last_heartbeat_at
		)
		VALUES (
			$1, $2, 'QUEUED', 'AUTOMATIC', $3::timestamp, now() AT TIME ZONE 'utc', 1,
			'stale-fixture-worker:' || $4, 'stale-fixture-worker', $5::uuid,
			now() AT TIME ZONE 'utc' + interval '10 minutes', now() AT TIME ZONE 'utc'
		)
		RETURNING id
	`, fixture.WorkflowID, fixture.UserID, scheduledAt, uuid.NewString(), uuid.NewString()).Scan(&jobID); err != nil {
		t.Fatalf("seed stale-leased queued fixture job: %v", err)
	}

	return jobID
}

// seedFixtureRunningJob inserts one RUNNING job that holds a runtime slot, the
// durable state a worker leaves behind while it executes a claimed job.
func seedFixtureRunningJob(ctx context.Context, t *testing.T, pg *postgres.Postgres, fixture *workflowFixture, nodeID, endpoint string) string {
	t.Helper()

	var jobID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO jobs (
			workflow_id, user_id, status, trigger, scheduled_at, started_at, attempts, dispatch_attempts,
			lease_token, leased_by, lease_process_instance_id, lease_expires_at, last_heartbeat_at,
			runtime_node_id, runtime_endpoint
		)
		VALUES (
			$1, $2, 'RUNNING', 'AUTOMATIC', now() AT TIME ZONE 'utc', now() AT TIME ZONE 'utc', 1, 1,
			'fixture-worker:' || $6, 'fixture-worker', $3::uuid, now() AT TIME ZONE 'utc' + interval '10 minutes',
			now() AT TIME ZONE 'utc', $4, $5
		)
		RETURNING id
	`, fixture.WorkflowID, fixture.UserID, uuid.NewString(), nodeID, endpoint, uuid.NewString()).Scan(&jobID); err != nil {
		t.Fatalf("seed running fixture job: %v", err)
	}

	return jobID
}

// cancelFixtureRunningJob is SQL fixture setup, not a verified jobs-service
// cancellation: it applies the durable transition the jobs repository owns when
// the workflow worker cancels a claimed job, including one runtime-slot release.
// This batch covers the workflow repository's own guards and effects; the
// jobs-service cancellation path and its exactly-once slot release are covered
// by the terminal-job suite from PR172. What this helper lets these tests prove
// is the complementary half: the workflows repository must not repeat that
// release, which the spare occupied slots make observable.
func cancelFixtureRunningJob(ctx context.Context, t *testing.T, pg *postgres.Postgres, jobID, nodeID string) {
	t.Helper()

	if _, err := pg.Exec(ctx, `
		UPDATE jobs
		SET status = 'CANCELED',
			completed_at = now() AT TIME ZONE 'utc',
			lease_token = NULL,
			leased_by = NULL,
			lease_process_instance_id = NULL,
			lease_expires_at = NULL,
			last_heartbeat_at = NULL,
			terminal_reason_code = $2
		WHERE id = $1
	`, jobID, terminalreason.WorkflowTerminated.String()); err != nil {
		t.Fatalf("cancel fixture running job %q: %v", jobID, err)
	}
	if _, err := pg.Exec(ctx, `
		UPDATE runtime_nodes
		SET running_jobs = GREATEST(0, running_jobs - 1)
		WHERE id = $1
	`, nodeID); err != nil {
		t.Fatalf("release runtime slot on node %q: %v", nodeID, err)
	}
}

func readWorkflowFailureState(ctx context.Context, t *testing.T, pg *postgres.Postgres, workflowID string) workflowFailureState {
	t.Helper()

	var state workflowFailureState
	if err := pg.QueryRow(ctx, fmt.Sprintf(`
		SELECT consecutive_job_failures_count, max_consecutive_job_failures_allowed, build_status, generation, terminated_at
		FROM %s
		WHERE id = $1
	`, postgres.TableWorkflows), workflowID).Scan(
		&state.ConsecutiveJobFailuresCount,
		&state.MaxConsecutiveFailures,
		&state.BuildStatus,
		&state.Generation,
		&state.TerminatedAt,
	); err != nil {
		t.Fatalf("read workflow failure state for %q: %v", workflowID, err)
	}
	return state
}

func countWorkflowRows(ctx context.Context, t *testing.T, pg *postgres.Postgres, workflowID string) int {
	t.Helper()

	var count int
	if err := pg.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s WHERE id = $1`, postgres.TableWorkflows), workflowID).Scan(&count); err != nil {
		t.Fatalf("count workflow rows for %q: %v", workflowID, err)
	}
	return count
}

func countWorkflowJobs(ctx context.Context, t *testing.T, pg *postgres.Postgres, workflowID string) int {
	t.Helper()

	var count int
	if err := pg.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s WHERE workflow_id = $1`, postgres.TableJobs), workflowID).Scan(&count); err != nil {
		t.Fatalf("count jobs for workflow %q: %v", workflowID, err)
	}
	return count
}

// readTerminalEffect returns the durable ledger row for one job, or false when
// the job has no recorded terminal effect.
func readTerminalEffect(ctx context.Context, t *testing.T, pg *postgres.Postgres, jobID string) (terminalEffectRow, bool) {
	t.Helper()

	var row terminalEffectRow
	err := pg.QueryRow(ctx, fmt.Sprintf(`
		SELECT workflow_id::text, user_id::text, effect, threshold_reached, created_at
		FROM %s
		WHERE job_id = $1
	`, postgres.TableWorkflowTerminalEffects), jobID).Scan(&row.WorkflowID, &row.UserID, &row.Effect, &row.ThresholdReached, &row.CreatedAt)
	if pg.IsNoRows(err) {
		return terminalEffectRow{}, false
	}
	if err != nil {
		t.Fatalf("read terminal effect for job %q: %v", jobID, err)
	}
	return row, true
}

func countTerminalEffects(ctx context.Context, t *testing.T, pg *postgres.Postgres, jobID string) int {
	t.Helper()

	var count int
	if err := pg.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s WHERE job_id = $1`, postgres.TableWorkflowTerminalEffects), jobID).Scan(&count); err != nil {
		t.Fatalf("count terminal effects for job %q: %v", jobID, err)
	}
	return count
}

// countWorkflowEvents counts every outbox row published under a workflow's own
// Kafka key, whatever action or event key it carries. A key allowlist would miss
// an effect published under an unexpected key.
func countWorkflowEvents(ctx context.Context, t *testing.T, pg *postgres.Postgres, workflowID string) int {
	t.Helper()

	var count int
	if err := pg.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s WHERE kafka_key = $1`, postgres.TableOutboxEvents), workflowID).Scan(&count); err != nil {
		t.Fatalf("count outbox events for workflow %q: %v", workflowID, err)
	}
	return count
}

func countWorkflowActionEvents(ctx context.Context, t *testing.T, pg *postgres.Postgres, workflowID, action string) int {
	t.Helper()

	var count int
	if err := pg.QueryRow(ctx, fmt.Sprintf(`
		SELECT count(*)
		FROM %s
		WHERE kafka_key = $1 AND payload->>'Action' = $2
	`, postgres.TableOutboxEvents), workflowID, action).Scan(&count); err != nil {
		t.Fatalf("count %s outbox events for workflow %q: %v", action, workflowID, err)
	}
	return count
}

func readWorkflowActionEvent(ctx context.Context, t *testing.T, pg *postgres.Postgres, workflowID, action string) workflowOutboxEvent {
	t.Helper()

	var event workflowOutboxEvent
	if err := pg.QueryRow(ctx, fmt.Sprintf(`
		SELECT topic, kafka_key, event_key, payload
		FROM %s
		WHERE kafka_key = $1 AND payload->>'Action' = $2
	`, postgres.TableOutboxEvents), workflowID, action).Scan(&event.Topic, &event.KafkaKey, &event.EventKey, &event.Payload); err != nil {
		t.Fatalf("read %s outbox event for workflow %q: %v", action, workflowID, err)
	}
	return event
}

// workflowActionEventKeys returns the sorted event keys of every outbox row
// published for one workflow action, so a test can assert the exact set of
// generations a command published instead of trusting row order.
func workflowActionEventKeys(ctx context.Context, t *testing.T, pg *postgres.Postgres, workflowID, action string) []string {
	t.Helper()

	rows, err := pg.Query(ctx, fmt.Sprintf(`
		SELECT event_key
		FROM %s
		WHERE kafka_key = $1 AND payload->>'Action' = $2
		ORDER BY event_key
	`, postgres.TableOutboxEvents), workflowID, action)
	if err != nil {
		t.Fatalf("query %s outbox event keys for workflow %q: %v", action, workflowID, err)
	}
	defer rows.Close()

	keys := make([]string, 0, 4)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatalf("scan %s outbox event key for workflow %q: %v", action, workflowID, err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s outbox event keys for workflow %q: %v", action, workflowID, err)
	}
	return keys
}

func readFixtureJobState(ctx context.Context, t *testing.T, pg *postgres.Postgres, jobID string) (fixtureJobState, bool) {
	t.Helper()

	var state fixtureJobState
	err := pg.QueryRow(ctx, fmt.Sprintf(`
		SELECT trigger::text, status, completed_at, lease_token, leased_by, lease_process_instance_id,
			terminal_reason_code, runtime_node_id, lease_expires_at, last_heartbeat_at
		FROM %s
		WHERE id = $1
	`, postgres.TableJobs), jobID).Scan(
		&state.Trigger,
		&state.Status,
		&state.CompletedAt,
		&state.LeaseToken,
		&state.LeasedBy,
		&state.LeaseProcessInstanceID,
		&state.TerminalReasonCode,
		&state.RuntimeNodeID,
		&state.LeaseExpiresAt,
		&state.LastHeartbeatAt,
	)
	if pg.IsNoRows(err) {
		return fixtureJobState{}, false
	}
	if err != nil {
		t.Fatalf("read fixture job state %q: %v", jobID, err)
	}
	return state, true
}

func readRuntimeOccupancy(ctx context.Context, t *testing.T, pg *postgres.Postgres, nodeID string) (runningJobs, maxConcurrency int) {
	t.Helper()

	if err := pg.QueryRow(ctx, `SELECT running_jobs, max_concurrency FROM runtime_nodes WHERE id = $1`, nodeID).
		Scan(&runningJobs, &maxConcurrency); err != nil {
		t.Fatalf("read runtime occupancy for node %q: %v", nodeID, err)
	}
	return runningJobs, maxConcurrency
}

// incrementFailureError invokes the failure command and returns only its error,
// so a rejected command can be asserted on its status alone.
func incrementFailureError(ctx context.Context, repo *Repository, workflowID, userID, jobID string) error {
	_, err := repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, workflowID, userID, jobID)
	return err
}

func decodeWorkflowEvent(t *testing.T, payload []byte) workflowsmodel.WorkflowEvent {
	t.Helper()

	var event workflowsmodel.WorkflowEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("decode workflow outbox payload: %v", err)
	}
	return event
}

func assertCode(t *testing.T, name string, err error, want codes.Code) {
	t.Helper()

	if got := status.Code(err); got != want {
		t.Fatalf("%s code = %v, want %v (err: %v)", name, got, want, err)
	}
}

func assertFailureState(t *testing.T, name string, got, want workflowFailureState) {
	t.Helper()

	if got.ConsecutiveJobFailuresCount != want.ConsecutiveJobFailuresCount {
		t.Errorf("%s consecutive_job_failures_count = %d, want %d", name, got.ConsecutiveJobFailuresCount, want.ConsecutiveJobFailuresCount)
	}
	if got.MaxConsecutiveFailures != want.MaxConsecutiveFailures {
		t.Errorf("%s max_consecutive_job_failures_allowed = %d, want %d", name, got.MaxConsecutiveFailures, want.MaxConsecutiveFailures)
	}
	if got.BuildStatus != want.BuildStatus {
		t.Errorf("%s build_status = %q, want %q", name, got.BuildStatus, want.BuildStatus)
	}
	if got.Generation != want.Generation {
		t.Errorf("%s generation = %d, want %d", name, got.Generation, want.Generation)
	}
	if got.TerminatedAt.Valid != want.TerminatedAt.Valid {
		t.Fatalf("%s terminated_at validity = %v, want %v (%v)", name, got.TerminatedAt.Valid, want.TerminatedAt.Valid, got.TerminatedAt)
	}
	if want.TerminatedAt.Valid && !got.TerminatedAt.Time.Equal(want.TerminatedAt.Time) {
		t.Errorf("%s terminated_at = %v, want unchanged %v", name, got.TerminatedAt.Time.UTC(), want.TerminatedAt.Time.UTC())
	}
}

func assertEventKeys(t *testing.T, name string, got []string, want ...string) {
	t.Helper()

	if sorted := slices.Sorted(slices.Values(want)); !slices.Equal(got, sorted) {
		t.Fatalf("%s event keys = %v, want %v", name, got, sorted)
	}
}

func assertNullString(t *testing.T, name string, column sql.NullString) {
	t.Helper()

	if column.Valid {
		t.Fatalf("%s = %q, want NULL", name, column.String)
	}
}

// assertSeededString proves the precondition a cleared-column assertion rests
// on. Without it, an assertion that a column ends up NULL also passes when the
// fixture never wrote one, which pins nothing.
func assertSeededString(t *testing.T, name string, column sql.NullString) {
	t.Helper()

	if !column.Valid {
		t.Fatalf("%s is NULL, want the seeded value the command must remove", name)
	}
}

func assertNullTime(t *testing.T, name string, column sql.NullTime) {
	t.Helper()

	if column.Valid {
		t.Fatalf("%s = %v, want NULL", name, column.Time.UTC())
	}
}

// assertSeededTime is assertSeededString for a timestamp column.
func assertSeededTime(t *testing.T, name string, column sql.NullTime) {
	t.Helper()

	if !column.Valid {
		t.Fatalf("%s is NULL, want the seeded value the command must remove", name)
	}
}

func assertNullBool(t *testing.T, name string, column sql.NullBool) {
	t.Helper()

	if column.Valid {
		t.Fatalf("%s = %v, want NULL", name, column.Bool)
	}
}

// TestIntegrationWorkflowFailureReplayDoesNotRecountOrRepeatEffects proves the
// durable terminal-effect ledger makes a replayed job failure a complete no-op.
// The counter, the termination instant and the single TERMINATE publish intent
// survive any number of redrives, and terminal commands arriving after
// termination are recorded without resurrecting the workflow.
//
//nolint:gocyclo // One flow proves the whole replay contract: counter, termination instant, ledger row and publish intent move together or not at all.
func TestIntegrationWorkflowFailureReplayDoesNotRecountOrRepeatEffects(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedWorkflowFixture(ctx, t, pg, repo, 2)
	replayJobID := uuid.NewString()
	thresholdJobID := uuid.NewString()
	lateFailureJobID := uuid.NewString()
	lateCompletedJobID := uuid.NewString()
	terminateAction := workflowsmodel.ActionTerminate.ToString()

	// The first failure counts once and does not reach the threshold.
	if reached, err := repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, replayJobID); err != nil || reached {
		t.Fatalf("first failure = (reached %v, err %v), want (false, nil)", reached, err)
	}
	afterFirst := readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID)
	if afterFirst.ConsecutiveJobFailuresCount != 1 {
		t.Fatalf("consecutive_job_failures_count = %d, want 1", afterFirst.ConsecutiveJobFailuresCount)
	}
	if afterFirst.TerminatedAt.Valid {
		t.Fatal("terminated_at is set after one failure, want the workflow still active")
	}
	if count := countWorkflowActionEvents(ctx, t, pg, fixture.WorkflowID, terminateAction); count != 0 {
		t.Fatalf("TERMINATE outbox events = %d, want 0 below the threshold", count)
	}
	replayEffect, ok := readTerminalEffect(ctx, t, pg, replayJobID)
	if !ok {
		t.Fatal("first failure has no recorded terminal effect")
	}
	if replayEffect.Effect != terminalEffectFailed || !replayEffect.ThresholdReached.Valid || replayEffect.ThresholdReached.Bool {
		t.Fatalf("first terminal effect = %+v, want FAILED with a recorded false threshold", replayEffect)
	}

	// Replaying the same job identity must not count it twice.
	if reached, err := repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, replayJobID); err != nil || reached {
		t.Fatalf("replayed failure = (reached %v, err %v), want (false, nil)", reached, err)
	}
	assertFailureState(t, "replayed failure", readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID), afterFirst)
	if count := countTerminalEffects(ctx, t, pg, replayJobID); count != 1 {
		t.Fatalf("terminal effects for the replayed job = %d, want exactly 1", count)
	}

	// The second failure reaches the threshold, terminates the workflow and
	// publishes exactly one TERMINATE intent for the current generation.
	if reached, err := repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, thresholdJobID); err != nil || !reached {
		t.Fatalf("threshold failure = (reached %v, err %v), want (true, nil)", reached, err)
	}
	afterThreshold := readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID)
	if afterThreshold.ConsecutiveJobFailuresCount != 2 {
		t.Fatalf("consecutive_job_failures_count = %d, want 2", afterThreshold.ConsecutiveJobFailuresCount)
	}
	if !afterThreshold.TerminatedAt.Valid {
		t.Fatal("terminated_at is NULL after reaching the threshold, want the workflow terminated")
	}
	assertEventKeys(
		t,
		"threshold termination",
		workflowActionEventKeys(ctx, t, pg, fixture.WorkflowID, terminateAction),
		idempotency.WorkflowEventKey(fixture.WorkflowID, terminateAction, fixture.Generation),
	)
	terminateEvent := readWorkflowActionEvent(ctx, t, pg, fixture.WorkflowID, terminateAction)
	if terminateEvent.Topic != kafka.TopicWorkflows || terminateEvent.KafkaKey != fixture.WorkflowID {
		t.Fatalf("TERMINATE topic/key = %q/%q, want %q/%q", terminateEvent.Topic, terminateEvent.KafkaKey, kafka.TopicWorkflows, fixture.WorkflowID)
	}
	decodedTerminate := decodeWorkflowEvent(t, terminateEvent.Payload)
	if decodedTerminate.Action != workflowsmodel.ActionTerminate || decodedTerminate.Generation != fixture.Generation {
		t.Fatalf("TERMINATE payload action/generation = %q/%d, want %q/%d", decodedTerminate.Action, decodedTerminate.Generation, workflowsmodel.ActionTerminate, fixture.Generation)
	}
	if decodedTerminate.ID != fixture.WorkflowID || decodedTerminate.UserID != fixture.UserID || decodedTerminate.EventKey != terminateEvent.EventKey {
		t.Fatalf(
			"TERMINATE payload identity = %q/%q/%q, want %q/%q/%q",
			decodedTerminate.ID, decodedTerminate.UserID, decodedTerminate.EventKey,
			fixture.WorkflowID, fixture.UserID, terminateEvent.EventKey,
		)
	}
	thresholdEffect, ok := readTerminalEffect(ctx, t, pg, thresholdJobID)
	if !ok {
		t.Fatal("threshold job has no recorded terminal effect")
	}
	if thresholdEffect.Effect != terminalEffectFailed || !thresholdEffect.ThresholdReached.Valid || !thresholdEffect.ThresholdReached.Bool {
		t.Fatalf("threshold terminal effect = %+v, want FAILED with threshold_reached true", thresholdEffect)
	}

	// Replaying the threshold-crossing job replays the recorded outcome without
	// re-terminating or republishing anything.
	if reached, err := repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, thresholdJobID); err != nil || !reached {
		t.Fatalf("replayed threshold failure = (reached %v, err %v), want the recorded (true, nil)", reached, err)
	}
	assertFailureState(t, "replayed threshold failure", readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID), afterThreshold)
	assertEventKeys(
		t,
		"replayed threshold termination",
		workflowActionEventKeys(ctx, t, pg, fixture.WorkflowID, terminateAction),
		idempotency.WorkflowEventKey(fixture.WorkflowID, terminateAction, fixture.Generation),
	)
	unchangedEffect, ok := readTerminalEffect(ctx, t, pg, thresholdJobID)
	if !ok {
		t.Fatal("replayed threshold job lost its terminal effect row")
	}
	if unchangedEffect != thresholdEffect {
		t.Fatalf("replayed terminal effect = %+v, want unchanged %+v", unchangedEffect, thresholdEffect)
	}

	// A failure that arrives after termination is recorded for redrive
	// diagnostics but must not count again or terminate a second time.
	if reached, err := repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, lateFailureJobID); err != nil || reached {
		t.Fatalf("late failure = (reached %v, err %v), want (false, nil)", reached, err)
	}
	assertFailureState(t, "late failure", readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID), afterThreshold)
	assertEventKeys(
		t,
		"late failure termination",
		workflowActionEventKeys(ctx, t, pg, fixture.WorkflowID, terminateAction),
		idempotency.WorkflowEventKey(fixture.WorkflowID, terminateAction, fixture.Generation),
	)
	lateEffect, ok := readTerminalEffect(ctx, t, pg, lateFailureJobID)
	if !ok {
		t.Fatal("late failure was not recorded for redrive diagnostics")
	}
	if lateEffect.Effect != terminalEffectFailed || !lateEffect.ThresholdReached.Valid || lateEffect.ThresholdReached.Bool {
		t.Fatalf("late terminal effect = %+v, want FAILED with a recorded false threshold", lateEffect)
	}

	// A completion that arrives after termination must not clear the counter or
	// resurrect the workflow.
	if err := repo.ResetWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, lateCompletedJobID); err != nil {
		t.Fatalf("ResetWorkflowConsecutiveJobFailuresCount after termination: %v", err)
	}
	assertFailureState(t, "late completion", readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID), afterThreshold)
	lateCompletedEffect, ok := readTerminalEffect(ctx, t, pg, lateCompletedJobID)
	if !ok {
		t.Fatal("late completion was not recorded")
	}
	if lateCompletedEffect.Effect != terminalEffectCompleted {
		t.Fatalf("late completion effect = %q, want COMPLETED", lateCompletedEffect.Effect)
	}
	assertNullBool(t, "COMPLETED threshold_reached", lateCompletedEffect.ThresholdReached)

	terminated, err := repo.GetWorkflow(ctx, fixture.WorkflowID, fixture.UserID)
	if err != nil {
		t.Fatalf("GetWorkflow after termination: %v", err)
	}
	if terminated.ConsecutiveJobFailuresCount != 2 || !terminated.TerminatedAt.Valid {
		t.Fatalf("terminated workflow counter/terminated_at = %d/%v, want 2/set", terminated.ConsecutiveJobFailuresCount, terminated.TerminatedAt.Valid)
	}
}

// TestIntegrationWorkflowTerminalEffectConflictsLeaveStateUntouched proves a
// job's terminal identity cannot be re-pointed at another workflow, owner or
// effect, and that every rejection is a whole-command rejection: the recorded
// effect, the workflow rows and the outbox all stay exactly as they were.
//
//nolint:gocyclo // Every rejection in one table-driven flow must be proved not to move any durable row.
func TestIntegrationWorkflowTerminalEffectConflictsLeaveStateUntouched(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	conflictedJobID := uuid.NewString()
	owner := seedWorkflowFixture(ctx, t, pg, repo, 3)
	sibling := seedWorkflowForUser(ctx, t, repo, owner.UserID, 3)
	stranger := seedWorkflowFixture(ctx, t, pg, repo, 3)
	siblingBaseline := workflowFailureState{
		MaxConsecutiveFailures: sibling.MaxConsecutiveJobFailures,
		BuildStatus:            sibling.BuildStatus,
		Generation:             sibling.Generation,
	}
	strangerBaseline := workflowFailureState{
		MaxConsecutiveFailures: stranger.MaxConsecutiveJobFailures,
		BuildStatus:            stranger.BuildStatus,
		Generation:             stranger.Generation,
	}

	if reached, err := repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, owner.WorkflowID, owner.UserID, conflictedJobID); err != nil || reached {
		t.Fatalf("recorded failure = (reached %v, err %v), want (false, nil)", reached, err)
	}
	ownerBefore := readWorkflowFailureState(ctx, t, pg, owner.WorkflowID)
	if ownerBefore.ConsecutiveJobFailuresCount != 1 {
		t.Fatalf("consecutive_job_failures_count = %d, want 1", ownerBefore.ConsecutiveJobFailuresCount)
	}
	ownerEventsBefore := countWorkflowEvents(ctx, t, pg, owner.WorkflowID)
	effectBefore, ok := readTerminalEffect(ctx, t, pg, conflictedJobID)
	if !ok {
		t.Fatal("recorded failure has no terminal effect row")
	}
	if effectBefore.WorkflowID != owner.WorkflowID || effectBefore.UserID != owner.UserID {
		t.Fatalf("recorded terminal effect identity = %q/%q, want %q/%q", effectBefore.WorkflowID, effectBefore.UserID, owner.WorkflowID, owner.UserID)
	}
	if effectBefore.Effect != terminalEffectFailed || !effectBefore.ThresholdReached.Valid || effectBefore.ThresholdReached.Bool {
		t.Fatalf("recorded terminal effect = %+v, want FAILED with a recorded false threshold", effectBefore)
	}

	// A completion cannot reuse a failure identity.
	assertCode(
		t,
		"ResetWorkflowConsecutiveJobFailuresCount(FAILED identity)",
		repo.ResetWorkflowConsecutiveJobFailuresCount(ctx, owner.WorkflowID, owner.UserID, conflictedJobID),
		codes.AlreadyExists,
	)
	assertFailureState(t, "completion conflict", readWorkflowFailureState(ctx, t, pg, owner.WorkflowID), ownerBefore)

	// A failure cannot be re-pointed at a sibling workflow of the same owner.
	assertCode(
		t,
		"IncrementWorkflowConsecutiveJobFailuresCount(sibling workflow)",
		incrementFailureError(ctx, repo, sibling.WorkflowID, owner.UserID, conflictedJobID),
		codes.AlreadyExists,
	)
	assertFailureState(t, "sibling conflict", readWorkflowFailureState(ctx, t, pg, sibling.WorkflowID), siblingBaseline)

	// A failure cannot be attributed to another owner.
	assertCode(
		t,
		"IncrementWorkflowConsecutiveJobFailuresCount(foreign owner)",
		incrementFailureError(ctx, repo, owner.WorkflowID, stranger.UserID, conflictedJobID),
		codes.AlreadyExists,
	)
	assertFailureState(t, "foreign owner conflict", readWorkflowFailureState(ctx, t, pg, owner.WorkflowID), ownerBefore)
	assertFailureState(t, "stranger workflow", readWorkflowFailureState(ctx, t, pg, stranger.WorkflowID), strangerBaseline)

	// A completion cannot be re-pointed at another owner's workflow either.
	assertCode(
		t,
		"ResetWorkflowConsecutiveJobFailuresCount(foreign workflow)",
		repo.ResetWorkflowConsecutiveJobFailuresCount(ctx, stranger.WorkflowID, stranger.UserID, conflictedJobID),
		codes.AlreadyExists,
	)
	assertFailureState(t, "stranger completion conflict", readWorkflowFailureState(ctx, t, pg, stranger.WorkflowID), strangerBaseline)

	// A workflow the caller does not own cannot record an effect at all: the
	// command is rejected and the whole transaction rolls back, so the identity
	// stays free for the job's real owner to record.
	unownedJobID := uuid.NewString()
	assertCode(
		t,
		"IncrementWorkflowConsecutiveJobFailuresCount(workflow owned by another user)",
		incrementFailureError(ctx, repo, sibling.WorkflowID, stranger.UserID, unownedJobID),
		codes.NotFound,
	)
	assertFailureState(t, "unowned workflow failure", readWorkflowFailureState(ctx, t, pg, sibling.WorkflowID), siblingBaseline)
	if count := countTerminalEffects(ctx, t, pg, unownedJobID); count != 0 {
		t.Fatalf("terminal effects for the unowned-workflow job = %d, want 0", count)
	}

	// Every rejected command is a whole-command rejection: one ledger row, one
	// unchanged workflow, and no publish intent anywhere.
	if count := countTerminalEffects(ctx, t, pg, conflictedJobID); count != 1 {
		t.Fatalf("terminal effects for the conflicted job = %d, want exactly 1", count)
	}
	effectAfter, ok := readTerminalEffect(ctx, t, pg, conflictedJobID)
	if !ok || effectAfter != effectBefore {
		t.Fatalf("terminal effect after conflicts = %+v, want unchanged %+v", effectAfter, effectBefore)
	}
	if count := countWorkflowEvents(ctx, t, pg, owner.WorkflowID); count != ownerEventsBefore {
		t.Fatalf("outbox events for the owning workflow = %d, want unchanged %d", count, ownerEventsBefore)
	}
	for name, fixture := range map[string]*workflowFixture{"sibling": sibling, "stranger": stranger} {
		if count := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID); count != 1 {
			t.Errorf("outbox events for the %s workflow = %d, want only its create event", name, count)
		}
		if count := countWorkflowActionEvents(ctx, t, pg, fixture.WorkflowID, workflowsmodel.ActionTerminate.ToString()); count != 0 {
			t.Errorf("TERMINATE outbox events for the %s workflow = %d, want 0", name, count)
		}
	}
}

// TestIntegrationTerminateWorkflowReplayPublishesOneTermination proves an
// explicit termination is idempotent in both directions: the first termination
// instant is kept and any number of replays republish nothing. Terminal job
// traffic that follows can neither un-terminate nor resurrect the workflow.
func TestIntegrationTerminateWorkflowReplayPublishesOneTermination(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
	terminateAction := workflowsmodel.ActionTerminate.ToString()
	terminateEventKey := idempotency.WorkflowEventKey(fixture.WorkflowID, terminateAction, fixture.Generation)

	if err := repo.TerminateWorkflow(ctx, fixture.WorkflowID, fixture.UserID); err != nil {
		t.Fatalf("TerminateWorkflow: %v", err)
	}
	terminated := readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID)
	if !terminated.TerminatedAt.Valid {
		t.Fatal("terminated_at is NULL after TerminateWorkflow, want a durable instant")
	}
	assertEventKeys(t, "termination", workflowActionEventKeys(ctx, t, pg, fixture.WorkflowID, terminateAction), terminateEventKey)

	// A retried termination keeps the first instant and publishes nothing new,
	// so the workflow worker's cleanup consumer sees the intent exactly once.
	if err := repo.TerminateWorkflow(ctx, fixture.WorkflowID, fixture.UserID); err != nil {
		t.Fatalf("TerminateWorkflow (replay): %v", err)
	}
	assertFailureState(t, "replayed termination", readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID), terminated)
	assertEventKeys(t, "replayed termination", workflowActionEventKeys(ctx, t, pg, fixture.WorkflowID, terminateAction), terminateEventKey)

	// Terminal job traffic after an explicit termination is recorded but neither
	// counts failures nor resurrects the workflow.
	failedJobID := uuid.NewString()
	if reached, err := repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, failedJobID); err != nil || reached {
		t.Fatalf("failure after termination = (reached %v, err %v), want (false, nil)", reached, err)
	}
	if err := repo.ResetWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, uuid.NewString()); err != nil {
		t.Fatalf("ResetWorkflowConsecutiveJobFailuresCount after termination: %v", err)
	}
	assertFailureState(t, "terminal job traffic", readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID), terminated)
	assertEventKeys(t, "termination after terminal job traffic", workflowActionEventKeys(ctx, t, pg, fixture.WorkflowID, terminateAction), terminateEventKey)
	if _, ok := readTerminalEffect(ctx, t, pg, failedJobID); !ok {
		t.Fatal("failure after termination was not recorded for redrive diagnostics")
	}
}

// TestIntegrationDeleteWorkflowRefusesWhileJobHoldsRuntimeSlot proves deletion
// refuses a terminated workflow whose job still occupies runtime capacity, so
// the cascade can never strand a slot. Once the job's own terminal path has
// released the slot exactly once, deletion succeeds and repeats neither the
// release nor the publish intent. The node keeps unattributed spare occupancy
// throughout, so a second release anywhere in this flow would be visible rather
// than hidden by the zero clamp.
//
//nolint:gocyclo // One flow proves the refused deletion, the single slot release, the cascade and the late-traffic rejection together.
func TestIntegrationDeleteWorkflowRefusesWhileJobHoldsRuntimeSlot(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
	nodeID := seedFixtureNode(ctx, t, pg, fixtureOccupiedSlots)
	runningJobID := seedFixtureRunningJob(ctx, t, pg, fixture, nodeID, "tcp://127.0.0.1:2375")
	deleteAction := workflowsmodel.ActionDelete.ToString()

	if runningJobs, maxConcurrency := readRuntimeOccupancy(ctx, t, pg, nodeID); runningJobs != fixtureOccupiedSlots || maxConcurrency != 4 {
		t.Fatalf("runtime occupancy = %d/%d, want %d/4", runningJobs, maxConcurrency, fixtureOccupiedSlots)
	}
	if count := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); count != 1 {
		t.Fatalf("fixture jobs = %d, want 1", count)
	}

	// Termination leaves the claimed job and its slot to the workflow worker,
	// which cancels the job through the jobs service exactly once.
	if err := repo.TerminateWorkflow(ctx, fixture.WorkflowID, fixture.UserID); err != nil {
		t.Fatalf("TerminateWorkflow: %v", err)
	}
	runningState, ok := readFixtureJobState(ctx, t, pg, runningJobID)
	if !ok {
		t.Fatal("claimed job disappeared after termination")
	}
	if runningState.Status != "RUNNING" {
		t.Fatalf("claimed job status = %q, want RUNNING so only the job command may end it", runningState.Status)
	}
	if runningState.RuntimeNodeID.String != nodeID {
		t.Fatalf("claimed job runtime_node_id = %q, want the occupied node %q", runningState.RuntimeNodeID.String, nodeID)
	}
	if runningJobs, _ := readRuntimeOccupancy(ctx, t, pg, nodeID); runningJobs != fixtureOccupiedSlots {
		t.Fatalf("runtime running_jobs = %d, want the termination not to release any slot", runningJobs)
	}

	// The running-job guard blocks deletion, leaving every durable row intact.
	assertCode(t, "DeleteWorkflow (running job)", repo.DeleteWorkflow(ctx, fixture.WorkflowID, fixture.UserID), codes.FailedPrecondition)
	if count := countWorkflowRows(ctx, t, pg, fixture.WorkflowID); count != 1 {
		t.Fatalf("workflow rows after refused deletion = %d, want 1", count)
	}
	if count := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); count != 1 {
		t.Fatalf("jobs after refused deletion = %d, want 1", count)
	}
	if runningJobs, _ := readRuntimeOccupancy(ctx, t, pg, nodeID); runningJobs != fixtureOccupiedSlots {
		t.Fatalf("runtime running_jobs after refused deletion = %d, want every occupied slot kept", runningJobs)
	}
	if count := countWorkflowActionEvents(ctx, t, pg, fixture.WorkflowID, deleteAction); count != 0 {
		t.Fatalf("DELETE outbox events after refused deletion = %d, want 0", count)
	}

	// The job's terminal path cancels it and releases exactly one slot. This is
	// SQL fixture setup standing in for the jobs service, which PR172 covers.
	cancelFixtureRunningJob(ctx, t, pg, runningJobID, nodeID)
	releasedOccupancy := fixtureOccupiedSlots - 1
	if runningJobs, _ := readRuntimeOccupancy(ctx, t, pg, nodeID); runningJobs != releasedOccupancy {
		t.Fatalf("runtime running_jobs after cancellation = %d, want exactly one slot released (%d)", runningJobs, releasedOccupancy)
	}

	// Deletion now succeeds and publishes exactly one intent.
	if err := repo.DeleteWorkflow(ctx, fixture.WorkflowID, fixture.UserID); err != nil {
		t.Fatalf("DeleteWorkflow: %v", err)
	}
	if count := countWorkflowRows(ctx, t, pg, fixture.WorkflowID); count != 0 {
		t.Fatalf("workflow rows after deletion = %d, want 0", count)
	}
	if count := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); count != 0 {
		t.Fatalf("jobs after deletion = %d, want the cascade to remove them", count)
	}
	if _, getErr := repo.GetWorkflowByID(ctx, fixture.WorkflowID); status.Code(getErr) != codes.NotFound {
		t.Fatalf("GetWorkflowByID after deletion code = %v, want %v (err: %v)", status.Code(getErr), codes.NotFound, getErr)
	}
	if _, getErr := repo.GetWorkflow(ctx, fixture.WorkflowID, fixture.UserID); status.Code(getErr) != codes.NotFound {
		t.Fatalf("GetWorkflow after deletion code = %v, want %v (err: %v)", status.Code(getErr), codes.NotFound, getErr)
	}

	// Capacity is neither consumed nor released a second time by the deletion,
	// which would show up as one of the spare slots disappearing.
	if runningJobs, maxConcurrency := readRuntimeOccupancy(ctx, t, pg, nodeID); runningJobs != releasedOccupancy || maxConcurrency != 4 {
		t.Fatalf(
			"runtime occupancy after deletion = %d/%d, want the already released %d/4 left untouched",
			runningJobs, maxConcurrency, releasedOccupancy,
		)
	}
	deleteEvent := readWorkflowActionEvent(ctx, t, pg, fixture.WorkflowID, deleteAction)
	assertEventKeys(
		t,
		"deletion",
		workflowActionEventKeys(ctx, t, pg, fixture.WorkflowID, deleteAction),
		idempotency.WorkflowEventKey(fixture.WorkflowID, deleteAction, 0),
	)
	if deleteEvent.Topic != kafka.TopicWorkflows || deleteEvent.KafkaKey != fixture.WorkflowID {
		t.Fatalf("DELETE topic/key = %q/%q, want %q/%q", deleteEvent.Topic, deleteEvent.KafkaKey, kafka.TopicWorkflows, fixture.WorkflowID)
	}
	decodedDelete := decodeWorkflowEvent(t, deleteEvent.Payload)
	if decodedDelete.Action != workflowsmodel.ActionDelete || decodedDelete.Generation != 0 {
		t.Fatalf("DELETE payload action/generation = %q/%d, want %q/0", decodedDelete.Action, decodedDelete.Generation, workflowsmodel.ActionDelete)
	}
	if decodedDelete.ID != fixture.WorkflowID || decodedDelete.UserID != fixture.UserID {
		t.Fatalf("DELETE payload identity = %q/%q, want %q/%q", decodedDelete.ID, decodedDelete.UserID, fixture.WorkflowID, fixture.UserID)
	}

	// A replayed deletion is rejected and publishes nothing.
	assertCode(t, "DeleteWorkflow (replay)", repo.DeleteWorkflow(ctx, fixture.WorkflowID, fixture.UserID), codes.NotFound)
	assertEventKeys(
		t,
		"replayed deletion",
		workflowActionEventKeys(ctx, t, pg, fixture.WorkflowID, deleteAction),
		idempotency.WorkflowEventKey(fixture.WorkflowID, deleteAction, 0),
	)

	// Late terminal-effect traffic cannot resurrect a deleted workflow or leave a
	// trace behind it.
	lateFailureJobID := uuid.NewString()
	lateCompletedJobID := uuid.NewString()
	eventsBefore := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID)
	// Both are refused, but not with the same status: the increment inserts into
	// the ledger first and trips the workflow_id foreign key (SQLSTATE 23503),
	// while the reset updates and matches no row. Pinning each keeps an
	// unrelated failure from passing as the refusal under test.
	assertCode(
		t,
		"IncrementWorkflowConsecutiveJobFailuresCount(deleted workflow)",
		incrementFailureError(ctx, repo, fixture.WorkflowID, fixture.UserID, lateFailureJobID),
		codes.Internal,
	)
	assertCode(
		t,
		"ResetWorkflowConsecutiveJobFailuresCount(deleted workflow)",
		repo.ResetWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, lateCompletedJobID),
		codes.NotFound,
	)
	for _, jobID := range []string{lateFailureJobID, lateCompletedJobID} {
		if count := countTerminalEffects(ctx, t, pg, jobID); count != 0 {
			t.Errorf("terminal effects for late job %q = %d, want 0", jobID, count)
		}
	}
	if count := countWorkflowRows(ctx, t, pg, fixture.WorkflowID); count != 0 {
		t.Errorf("workflow rows after late terminal traffic = %d, want 0", count)
	}
	if count := countWorkflowEvents(ctx, t, pg, fixture.WorkflowID); count != eventsBefore {
		t.Errorf("outbox events after late terminal traffic = %d, want unchanged %d", count, eventsBefore)
	}
	if runningJobs, _ := readRuntimeOccupancy(ctx, t, pg, nodeID); runningJobs != releasedOccupancy {
		t.Errorf("runtime running_jobs after late terminal traffic = %d, want unchanged %d", runningJobs, releasedOccupancy)
	}
}

// TestIntegrationWorkflowFailureReplayAfterReactivationKeepsNewLifecycle proves
// the terminal-effect ledger replays the recorded outcome instead of re-applying
// it: after an update reactivates a threshold-terminated workflow, redriving the
// old failure neither re-terminates it nor re-counts, while a genuinely new
// failure still terminates it at the new generation.
//
//nolint:gocyclo // One flow proves the reactivation, the inert replay and the new-generation termination together.
func TestIntegrationWorkflowFailureReplayAfterReactivationKeepsNewLifecycle(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedWorkflowFixture(ctx, t, pg, repo, 1)
	oldFailureJobID := uuid.NewString()
	buildAction := workflowsmodel.ActionBuild.ToString()
	rescheduledAction := workflowsmodel.ActionReschedule.ToString()
	terminateAction := workflowsmodel.ActionTerminate.ToString()

	if reached, err := repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, oldFailureJobID); err != nil || !reached {
		t.Fatalf("threshold failure = (reached %v, err %v), want (true, nil)", reached, err)
	}
	if state := readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID); !state.TerminatedAt.Valid {
		t.Fatal("terminated_at is NULL after the threshold failure, want the workflow terminated")
	}
	firstGeneration := fixture.Generation

	// An explicit update reactivates the workflow and starts a new generation.
	if err := repo.UpdateWorkflow(
		ctx, fixture.WorkflowID, fixture.UserID, "cv-reactivated", fixturePayload,
		fixture.Interval*2, fixture.MaxConsecutiveJobFailures, "cv-update-"+fixtureTag(),
	); err != nil {
		t.Fatalf("UpdateWorkflow: %v", err)
	}
	reactivated, err := repo.GetWorkflow(ctx, fixture.WorkflowID, fixture.UserID)
	if err != nil {
		t.Fatalf("GetWorkflow after update: %v", err)
	}
	if reactivated.TerminatedAt.Valid {
		t.Fatalf("terminated_at = %v, want NULL after an explicit update reactivates the workflow", reactivated.TerminatedAt.Time.UTC())
	}
	if reactivated.ConsecutiveJobFailuresCount != 0 {
		t.Fatalf("consecutive_job_failures_count = %d, want the update to reset it to 0", reactivated.ConsecutiveJobFailuresCount)
	}
	if reactivated.Generation != firstGeneration+1 {
		t.Fatalf("generation = %d, want %d after a reactivation build", reactivated.Generation, firstGeneration+1)
	}
	if reactivated.WorkflowBuildStatus != workflowsmodel.WorkflowBuildStatusQueued.ToString() {
		t.Fatalf("build_status = %q, want %q", reactivated.WorkflowBuildStatus, workflowsmodel.WorkflowBuildStatusQueued)
	}
	if count := countWorkflowActionEvents(ctx, t, pg, fixture.WorkflowID, rescheduledAction); count != 0 {
		t.Fatalf("RESCHEDULE outbox events = %d, want a reactivation build instead", count)
	}
	assertEventKeys(
		t,
		"build intents after reactivation",
		workflowActionEventKeys(ctx, t, pg, fixture.WorkflowID, buildAction),
		idempotency.WorkflowEventKey(fixture.WorkflowID, buildAction, firstGeneration),
		idempotency.WorkflowEventKey(fixture.WorkflowID, buildAction, reactivated.Generation),
	)
	afterReactivation := workflowFailureState{
		ConsecutiveJobFailuresCount: reactivated.ConsecutiveJobFailuresCount,
		MaxConsecutiveFailures:      reactivated.MaxConsecutiveJobFailuresAllowed,
		BuildStatus:                 reactivated.WorkflowBuildStatus,
		Generation:                  reactivated.Generation,
	}

	// Redriving the pre-termination failure replays its recorded threshold but
	// must not terminate or count against the reactivated workflow.
	if reached, err := repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, oldFailureJobID); err != nil || !reached {
		t.Fatalf("replayed failure after reactivation = (reached %v, err %v), want the recorded (true, nil)", reached, err)
	}
	assertFailureState(t, "replayed failure after reactivation", readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID), afterReactivation)
	assertEventKeys(
		t,
		"termination after the replay",
		workflowActionEventKeys(ctx, t, pg, fixture.WorkflowID, terminateAction),
		idempotency.WorkflowEventKey(fixture.WorkflowID, terminateAction, firstGeneration),
	)

	// A genuinely new failure is still counted and still terminates, at the new
	// generation.
	newFailureJobID := uuid.NewString()
	if reached, err := repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, newFailureJobID); err != nil || !reached {
		t.Fatalf("new failure after reactivation = (reached %v, err %v), want (true, nil)", reached, err)
	}
	afterNewFailure := readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID)
	if afterNewFailure.ConsecutiveJobFailuresCount != 1 {
		t.Fatalf("consecutive_job_failures_count = %d, want 1", afterNewFailure.ConsecutiveJobFailuresCount)
	}
	if !afterNewFailure.TerminatedAt.Valid {
		t.Fatal("terminated_at is NULL after the new threshold failure, want the workflow terminated again")
	}
	assertEventKeys(
		t,
		"terminations across both generations",
		workflowActionEventKeys(ctx, t, pg, fixture.WorkflowID, terminateAction),
		idempotency.WorkflowEventKey(fixture.WorkflowID, terminateAction, firstGeneration),
		idempotency.WorkflowEventKey(fixture.WorkflowID, terminateAction, reactivated.Generation),
	)
	newEffect, ok := readTerminalEffect(ctx, t, pg, newFailureJobID)
	if !ok {
		t.Fatal("new failure after reactivation has no terminal effect row")
	}
	if !newEffect.ThresholdReached.Valid || !newEffect.ThresholdReached.Bool {
		t.Fatalf("new failure terminal effect = %+v, want threshold_reached true", newEffect)
	}
}

// TestIntegrationUpdateWorkflowResetsFailuresAndCancelsOnlyAutomaticJobs proves
// the failure-counter reset and the stale-job cancellation commit together on the
// reschedule-only path: user-scheduled manual work survives, automatic queued work
// is invalidated exactly once with every stale lease and ownership column it still
// carried cleared, and a failure recorded before the update cannot re-count
// against the counter the update reset.
//
//nolint:gocyclo // One flow proves the counter reset, the selective job cancellation and the inert replay together.
func TestIntegrationUpdateWorkflowResetsFailuresAndCancelsOnlyAutomaticJobs(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	fixture := seedWorkflowFixture(ctx, t, pg, repo, 3)
	completeFixtureBuild(ctx, t, repo, fixture)

	preUpdateFailureJobID := uuid.NewString()
	secondFailureJobID := uuid.NewString()
	for _, jobID := range []string{preUpdateFailureJobID, secondFailureJobID} {
		if reached, err := repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, jobID); err != nil || reached {
			t.Fatalf("failure below threshold = (reached %v, err %v), want (false, nil)", reached, err)
		}
	}
	beforeUpdate := readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID)
	if beforeUpdate.ConsecutiveJobFailuresCount != 2 {
		t.Fatalf("consecutive_job_failures_count = %d, want 2 before the update", beforeUpdate.ConsecutiveJobFailuresCount)
	}

	// The automatic job is queued work that still carries a stale claim, so the
	// cancellation below must clean up ownership metadata rather than only flip
	// a status. Manual work is seeded untouched for the contrast asserted later.
	automaticJobID := seedFixtureStaleLeasedQueuedJob(ctx, t, pg, fixture, 0)
	manualJobID := seedFixturePendingJob(ctx, t, pg, fixture, "MANUAL", 1)

	// Read the seeded ownership before the command runs. These assertions are
	// what make the post-update clearing assertions live rather than vacuous:
	// without a non-null starting value, "still NULL afterwards" proves nothing.
	queuedBefore, ok := readFixtureJobState(ctx, t, pg, automaticJobID)
	if !ok {
		t.Fatal("automatic queued job is missing before the reschedule")
	}
	if queuedBefore.Trigger != "AUTOMATIC" || queuedBefore.Status != "QUEUED" {
		t.Fatalf(
			"automatic job trigger/status before the reschedule = %q/%q, want AUTOMATIC/QUEUED",
			queuedBefore.Trigger,
			queuedBefore.Status,
		)
	}
	assertSeededString(t, "automatic job lease_token before the reschedule", queuedBefore.LeaseToken)
	assertSeededString(t, "automatic job leased_by before the reschedule", queuedBefore.LeasedBy)
	assertSeededString(t, "automatic job lease_process_instance_id before the reschedule", queuedBefore.LeaseProcessInstanceID)
	assertSeededTime(t, "automatic job lease_expires_at before the reschedule", queuedBefore.LeaseExpiresAt)
	assertSeededTime(t, "automatic job last_heartbeat_at before the reschedule", queuedBefore.LastHeartbeatAt)

	// A reschedule-only update: unchanged payload, new interval, completed build.
	if err := repo.UpdateWorkflow(
		ctx, fixture.WorkflowID, fixture.UserID, "cv-rescheduled", fixturePayload,
		fixture.Interval*2, fixture.MaxConsecutiveJobFailures, "cv-update-"+fixtureTag(),
	); err != nil {
		t.Fatalf("UpdateWorkflow: %v", err)
	}

	afterUpdate, err := repo.GetWorkflow(ctx, fixture.WorkflowID, fixture.UserID)
	if err != nil {
		t.Fatalf("GetWorkflow after reschedule: %v", err)
	}
	if afterUpdate.ConsecutiveJobFailuresCount != 0 {
		t.Fatalf("consecutive_job_failures_count = %d, want the reschedule to reset it to 0", afterUpdate.ConsecutiveJobFailuresCount)
	}
	if afterUpdate.Generation != beforeUpdate.Generation+1 {
		t.Fatalf("generation = %d, want %d", afterUpdate.Generation, beforeUpdate.Generation+1)
	}
	if afterUpdate.WorkflowBuildStatus != workflowsmodel.WorkflowBuildStatusCompleted.ToString() {
		t.Fatalf(
			"build_status = %q, want %q (a reschedule must not queue a rebuild)",
			afterUpdate.WorkflowBuildStatus, workflowsmodel.WorkflowBuildStatusCompleted,
		)
	}
	if afterUpdate.Interval != fixture.Interval*2 {
		t.Fatalf("interval = %d, want %d", afterUpdate.Interval, fixture.Interval*2)
	}
	if afterUpdate.ResolvedImageRef.String != fixtureImage || afterUpdate.ResolvedImageDigest.String != fixtureImageDigest {
		t.Fatalf("resolved image identity = %q/%q, want the completed build preserved", afterUpdate.ResolvedImageRef.String, afterUpdate.ResolvedImageDigest.String)
	}
	if afterUpdate.TerminatedAt.Valid {
		t.Fatalf("terminated_at = %v, want a reschedule of an active workflow to leave it active", afterUpdate.TerminatedAt.Time.UTC())
	}

	// The automatic queued job is invalidated with the workflow-update reason,
	// and the stale claim it carried is cleared rather than left behind on a row
	// no worker owns any more. This is queued-job metadata cleanup, not a
	// running-claim cancellation: the row holds no runtime node and no slot.
	canceled, ok := readFixtureJobState(ctx, t, pg, automaticJobID)
	if !ok {
		t.Fatal("automatic job disappeared after the reschedule")
	}
	if canceled.Status != "CANCELED" {
		t.Fatalf("automatic job status = %q, want CANCELED", canceled.Status)
	}
	if !canceled.CompletedAt.Valid {
		t.Fatal("automatic job completed_at is NULL, want a durable cancellation instant")
	}
	if canceled.TerminalReasonCode.String != terminalreason.WorkflowUpdated.String() {
		t.Fatalf("automatic job terminal_reason_code = %q, want %q", canceled.TerminalReasonCode.String, terminalreason.WorkflowUpdated.String())
	}
	assertNullString(t, "automatic job lease_token", canceled.LeaseToken)
	assertNullString(t, "automatic job leased_by", canceled.LeasedBy)
	assertNullString(t, "automatic job lease_process_instance_id", canceled.LeaseProcessInstanceID)
	assertNullTime(t, "automatic job lease_expires_at", canceled.LeaseExpiresAt)
	assertNullTime(t, "automatic job last_heartbeat_at", canceled.LastHeartbeatAt)

	// Manual work is user-requested and must survive a reschedule untouched.
	preserved, ok := readFixtureJobState(ctx, t, pg, manualJobID)
	if !ok {
		t.Fatal("manual job disappeared after the reschedule")
	}
	if preserved.Trigger != "MANUAL" || preserved.Status != "PENDING" {
		t.Fatalf("manual job trigger/status = %q/%q, want MANUAL/PENDING", preserved.Trigger, preserved.Status)
	}
	assertNullString(t, "manual job terminal_reason_code", preserved.TerminalReasonCode)
	assertNullTime(t, "manual job completed_at", preserved.CompletedAt)

	assertEventKeys(
		t,
		"reschedule",
		workflowActionEventKeys(ctx, t, pg, fixture.WorkflowID, workflowsmodel.ActionReschedule.ToString()),
		idempotency.WorkflowEventKey(fixture.WorkflowID, workflowsmodel.ActionReschedule.ToString(), afterUpdate.Generation),
	)
	assertEventKeys(
		t,
		"build intents",
		workflowActionEventKeys(ctx, t, pg, fixture.WorkflowID, workflowsmodel.ActionBuild.ToString()),
		idempotency.WorkflowEventKey(fixture.WorkflowID, workflowsmodel.ActionBuild.ToString(), beforeUpdate.Generation),
	)

	// A failure recorded before the update keeps its ledger row and cannot undo
	// the counter the update reset.
	replayed, replayErr := repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, preUpdateFailureJobID)
	if replayErr != nil || replayed {
		t.Fatalf("replayed pre-update failure = (reached %v, err %v), want (false, nil)", replayed, replayErr)
	}
	afterReplay, err := repo.GetWorkflow(ctx, fixture.WorkflowID, fixture.UserID)
	if err != nil {
		t.Fatalf("GetWorkflow after replay: %v", err)
	}
	if afterReplay.ConsecutiveJobFailuresCount != 0 || afterReplay.TerminatedAt.Valid {
		t.Fatalf(
			"counter/terminated_at after replay = %d/%v, want 0/NULL",
			afterReplay.ConsecutiveJobFailuresCount, afterReplay.TerminatedAt.Valid,
		)
	}
	if count := countTerminalEffects(ctx, t, pg, preUpdateFailureJobID); count != 1 {
		t.Fatalf("terminal effects for the pre-update job = %d, want exactly 1", count)
	}
}

// TestIntegrationConcurrentWorkflowFailureEffectsAreExactlyOnce drives the
// terminal-effect ledger concurrently: duplicated deliveries of one job id count
// once, distinct jobs all count with no lost update, and only one transaction
// crosses the threshold and publishes a termination.
//
//nolint:gocyclo // Both concurrency subtests assert the same exactly-once contract from different interleavings.
func TestIntegrationConcurrentWorkflowFailureEffectsAreExactlyOnce(t *testing.T) {
	const workers = 4

	t.Run("duplicated delivery of one job counts once", func(t *testing.T) {
		ctx := context.Background()
		pg := testkit.Postgres(t)
		repo := newTestRepository(t)

		fixture := seedWorkflowFixture(ctx, t, pg, repo, 5)
		duplicatedJobID := uuid.NewString()

		reached := runConcurrently(workers, func(_ int) bool {
			got, err := repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, duplicatedJobID)
			if err != nil {
				t.Errorf("IncrementWorkflowConsecutiveJobFailuresCount: %v", err)
				return false
			}
			return got
		})

		for worker, got := range reached {
			if got {
				t.Errorf("worker %d threshold = true, want false with max %d failures", worker, fixture.MaxConsecutiveJobFailures)
			}
		}
		state := readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID)
		if state.ConsecutiveJobFailuresCount != 1 {
			t.Fatalf("consecutive_job_failures_count = %d, want 1 after %d duplicate deliveries", state.ConsecutiveJobFailuresCount, workers)
		}
		if state.TerminatedAt.Valid {
			t.Fatal("terminated_at is set, want the workflow still active")
		}
		if count := countTerminalEffects(ctx, t, pg, duplicatedJobID); count != 1 {
			t.Fatalf("terminal effects for the duplicated job = %d, want exactly 1", count)
		}
		if count := countWorkflowActionEvents(ctx, t, pg, fixture.WorkflowID, workflowsmodel.ActionTerminate.ToString()); count != 0 {
			t.Fatalf("TERMINATE outbox events = %d, want 0", count)
		}
	})

	t.Run("distinct jobs all count and only one crosses the threshold", func(t *testing.T) {
		ctx := context.Background()
		pg := testkit.Postgres(t)
		repo := newTestRepository(t)

		fixture := seedWorkflowFixture(ctx, t, pg, repo, workers)
		jobIDs := make([]string, workers)
		for worker := range jobIDs {
			jobIDs[worker] = uuid.NewString()
		}

		reached := runConcurrently(workers, func(worker int) bool {
			got, err := repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, jobIDs[worker])
			if err != nil {
				t.Errorf("IncrementWorkflowConsecutiveJobFailuresCount: %v", err)
				return false
			}
			return got
		})

		crossings := 0
		for _, got := range reached {
			if got {
				crossings++
			}
		}
		if crossings != 1 {
			t.Fatalf("threshold crossings = %d, want exactly 1 across %d concurrent failures", crossings, workers)
		}
		state := readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID)
		if state.ConsecutiveJobFailuresCount != workers {
			t.Fatalf("consecutive_job_failures_count = %d, want %d with no lost update", state.ConsecutiveJobFailuresCount, workers)
		}
		if !state.TerminatedAt.Valid {
			t.Fatal("terminated_at is NULL after the threshold, want the workflow terminated")
		}
		assertEventKeys(
			t,
			"concurrent threshold termination",
			workflowActionEventKeys(ctx, t, pg, fixture.WorkflowID, workflowsmodel.ActionTerminate.ToString()),
			idempotency.WorkflowEventKey(fixture.WorkflowID, workflowsmodel.ActionTerminate.ToString(), fixture.Generation),
		)
		for _, jobID := range jobIDs {
			effect, ok := readTerminalEffect(ctx, t, pg, jobID)
			if !ok {
				t.Fatalf("concurrent job %q has no terminal effect row", jobID)
			}
			if effect.Effect != terminalEffectFailed || !effect.ThresholdReached.Valid {
				t.Fatalf("concurrent job %q terminal effect = %+v, want FAILED with a recorded threshold", jobID, effect)
			}
		}
	})
}

// runConcurrently starts n workers that all block on one release signal, so they
// issue their command together without any timing assumption, and returns each
// worker's outcome in call order.
func runConcurrently(n int, work func(worker int) bool) []bool {
	results := make([]bool, n)
	start := make(chan struct{})

	var ready, done sync.WaitGroup
	ready.Add(n)
	done.Add(n)
	for worker := range n {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			results[worker] = work(worker)
		}()
	}

	ready.Wait()
	close(start)
	done.Wait()

	return results
}
