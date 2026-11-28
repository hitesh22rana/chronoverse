//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package jobs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	chdriver "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
	"github.com/hitesh22rana/chronoverse/internal/pkg/joblogevents"
	meilisearchpkg "github.com/hitesh22rana/chronoverse/internal/pkg/meilisearch"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
	workflowsrepo "github.com/hitesh22rana/chronoverse/internal/repository/workflows"
	jobssvc "github.com/hitesh22rana/chronoverse/internal/service/jobs"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

// meilisearch document field names for the seeded job log search documents.
const (
	docJobIDField      = "job_id"
	docWorkflowIDField = "workflow_id"
	docUserIDField     = "user_id"
)

func TestMain(m *testing.M) {
	testkit.Run(m, testkit.WithPostgres(), testkit.WithClickHouse(), testkit.WithRedis(), testkit.WithMeilisearch())
}

// fakeWorkflowsService is a minimal workflowspb.WorkflowsServiceClient stub that
// always reports log retention enabled.
type fakeWorkflowsService struct{}

func (f *fakeWorkflowsService) CreateWorkflow(
	context.Context,
	*workflowspb.CreateWorkflowRequest,
	...grpc.CallOption,
) (
	*workflowspb.CreateWorkflowResponse,
	error,
) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeWorkflowsService) UpdateWorkflow(
	context.Context,
	*workflowspb.UpdateWorkflowRequest,
	...grpc.CallOption,
) (
	*workflowspb.UpdateWorkflowResponse,
	error,
) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeWorkflowsService) UpdateWorkflowBuildStatus(
	context.Context,
	*workflowspb.UpdateWorkflowBuildStatusRequest,
	...grpc.CallOption,
) (
	*workflowspb.UpdateWorkflowBuildStatusResponse,
	error,
) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeWorkflowsService) GetWorkflow(
	context.Context,
	*workflowspb.GetWorkflowRequest,
	...grpc.CallOption,
) (
	*workflowspb.GetWorkflowResponse,
	error,
) {
	return &workflowspb.GetWorkflowResponse{LogRetention: true, Name: "integration-workflow", Kind: "CONTAINER"}, nil
}

func (f *fakeWorkflowsService) GetWorkflowByID(
	context.Context,
	*workflowspb.GetWorkflowByIDRequest,
	...grpc.CallOption,
) (
	*workflowspb.GetWorkflowByIDResponse,
	error,
) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeWorkflowsService) IncrementWorkflowConsecutiveJobFailuresCount(
	context.Context, *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest, ...grpc.CallOption,
) (*workflowspb.IncrementWorkflowConsecutiveJobFailuresCountResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeWorkflowsService) ResetWorkflowConsecutiveJobFailuresCount(
	context.Context, *workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest, ...grpc.CallOption,
) (*workflowspb.ResetWorkflowConsecutiveJobFailuresCountResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeWorkflowsService) TerminateWorkflow(
	context.Context,
	*workflowspb.TerminateWorkflowRequest,
	...grpc.CallOption,
) (
	*workflowspb.TerminateWorkflowResponse,
	error,
) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeWorkflowsService) DeleteWorkflow(
	context.Context,
	*workflowspb.DeleteWorkflowRequest,
	...grpc.CallOption,
) (
	*workflowspb.DeleteWorkflowResponse,
	error,
) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeWorkflowsService) ListWorkflows(
	context.Context,
	*workflowspb.ListWorkflowsRequest,
	...grpc.CallOption,
) (
	*workflowspb.ListWorkflowsResponse,
	error,
) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

// newTestRepository builds a jobs repository against the shared containers.
func newTestRepository(t *testing.T) *Repository {
	t.Helper()

	ctrl := gomock.NewController(t)
	_auth := authmock.NewMockIAuth(ctrl)
	_auth.EXPECT().
		IssueToken(gomock.Any(), gomock.Any(), gomock.Any()).
		Return("test-token", nil).
		AnyTimes()

	return New(
		&Config{
			FetchLimit:          20,
			LogsFetchLimit:      2,
			RuntimeHeartbeatTTL: time.Minute,
			RuntimeLostAfter:    5 * time.Minute,
		},
		_auth,
		testkit.Postgres(t),
		testkit.Redis(t),
		testkit.ClickHouse(t),
		testkit.Meilisearch(t),
		&Services{Workflows: &fakeWorkflowsService{}},
	)
}

// seedUserWorkflow inserts a user and a completed workflow, returning both ids.
func seedUserWorkflow(ctx context.Context, t *testing.T, pg *postgres.Postgres) (userID, workflowID string) {
	t.Helper()

	userID = testkit.SeedUser(ctx, t, pg, fmt.Sprintf("jobs-%s@chronoverse.test", t.Name()))
	workflowID = testkit.SeedWorkflow(ctx, t, pg, userID, "jobs-"+t.Name())
	return userID, workflowID
}

// queueJob moves a PENDING job to QUEUED the way the scheduler does before a
// worker can claim it.
func queueJob(ctx context.Context, t *testing.T, pg *postgres.Postgres, jobID string) {
	t.Helper()
	if _, err := pg.Exec(ctx, `UPDATE jobs SET status = 'QUEUED', dispatch_attempts = 1 WHERE id = $1`, jobID); err != nil {
		t.Fatalf("queue job: %v", err)
	}
}

// seedReadyRuntimeNode registers a healthy READY runtime node so CONTAINER
// workflow jobs can be claimed, returning its opaque node id.
func seedReadyRuntimeNode(ctx context.Context, t *testing.T, pg *postgres.Postgres, name string) string {
	t.Helper()

	nodeID := "integration-node-" + name
	if _, err := pg.Exec(ctx, `
		INSERT INTO
			runtime_nodes (
				id,
				node_name,
				docker_endpoint,
				status,
				last_heartbeat_at,
				max_concurrency
			)
		VALUES
			(
				$1,
				$2,
				'tcp://127.0.0.1:2375',
				'READY',
				now() AT TIME ZONE 'utc',
				4
			)
		ON CONFLICT (id) DO UPDATE
		SET
			status = 'READY',
			last_heartbeat_at = now() AT TIME ZONE 'utc',
			running_jobs = 0
	`, nodeID, nodeID); err != nil {
		t.Fatalf("seed runtime node: %v", err)
	}
	return nodeID
}

//nolint:gocyclo // The lifecycle test exercises the full job state machine in one flow.
func TestIntegrationScheduleAndQueryJob(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	userID, workflowID := seedUserWorkflow(ctx, t, pg)

	scheduledAt := time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)
	jobID, err := repo.ScheduleJob(
		ctx, workflowID, userID, scheduledAt, "MANUAL", "idem-"+t.Name(), 1,
	)
	if err != nil {
		t.Fatalf("ScheduleJob: %v", err)
	}
	if jobID == "" {
		t.Fatal("expected a job id")
	}

	// Replaying the idempotency key returns the same job.
	replayed, err := repo.ScheduleJob(
		ctx, workflowID, userID, scheduledAt, "MANUAL", "idem-"+t.Name(), 1,
	)
	if err != nil {
		t.Fatalf("ScheduleJob (idempotent): %v", err)
	}
	if replayed != jobID {
		t.Fatalf("idempotent replay id = %q, want %q", replayed, jobID)
	}

	// The job is PENDING in the database.
	var status string
	if statusErr := pg.QueryRow(ctx, `SELECT status FROM jobs WHERE id = $1`, jobID).Scan(&status); statusErr != nil {
		t.Fatalf("fetch job status: %v", statusErr)
	}
	if status != "PENDING" {
		t.Fatalf("job status = %q, want %q", status, "PENDING")
	}

	// GetJob returns the scheduled job.
	job, err := repo.GetJob(ctx, jobID, workflowID, userID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.ID != jobID || job.WorkflowID != workflowID {
		t.Fatalf("GetJob = %+v, want id %q workflow %q", job, jobID, workflowID)
	}

	// GetJobByID returns the same job without user scoping.
	byID, err := repo.GetJobByID(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJobByID: %v", err)
	}
	if byID.ID != jobID {
		t.Fatalf("GetJobByID id = %q, want %q", byID.ID, jobID)
	}

	// The full lease lifecycle transitions the job to RUNNING then COMPLETED.
	queueJob(ctx, t, pg, jobID)
	nodeID := seedReadyRuntimeNode(ctx, t, pg, t.Name())
	claimed, ok, claimReason, claimErr := repo.ClaimJob(
		ctx, jobID, workflowID, "integration-worker",
		"00000000-0000-0000-0000-0000000000c1", "claim-"+t.Name(), time.Minute, 1,
	)
	if claimErr != nil {
		t.Fatalf("ClaimJob: %v", claimErr)
	}
	if !ok {
		t.Fatalf("ClaimJob did not claim the job: %s", claimReason)
	}
	if claimed.LeaseToken == "" {
		t.Fatal("expected a lease token")
	}
	if claimed.RuntimeNodeID.String != nodeID {
		t.Fatalf("runtime_node_id = %q, want %q", claimed.RuntimeNodeID.String, nodeID)
	}

	// Attaching the container persists it against the held lease.
	seedContainerProvenance(ctx, t, pg, repo, jobID, claimed.LeaseToken, "container-1")
	if attachErr := repo.AttachJobContainer(
		ctx, jobID, claimed.LeaseToken, "container-1", nodeID, "attach-"+t.Name(),
	); attachErr != nil {
		t.Fatalf("AttachJobContainer: %v", attachErr)
	}

	// Completing the job releases the lease and the runtime slot.
	if completeErr := repo.CompleteJob(ctx, jobID, claimed.LeaseToken, "complete-"+t.Name()); completeErr != nil {
		t.Fatalf("CompleteJob: %v", completeErr)
	}
	final, err := repo.GetJob(ctx, jobID, workflowID, userID)
	if err != nil {
		t.Fatalf("GetJob after completion: %v", err)
	}
	if final.JobStatus != "COMPLETED" {
		t.Fatalf("job status = %q, want %q", final.JobStatus, "COMPLETED")
	}
	var runningJobs int
	if err := pg.QueryRow(ctx, `SELECT running_jobs FROM runtime_nodes WHERE id = $1`, nodeID).Scan(&runningJobs); err != nil {
		t.Fatalf("fetch runtime slot: %v", err)
	}
	if runningJobs != 0 {
		t.Fatalf("running_jobs = %d, want 0 after completion", runningJobs)
	}

	// Replaying the completion command is a no-op (idempotent terminal effect).
	if replayCompleteErr := repo.CompleteJob(ctx, jobID, claimed.LeaseToken, "complete-"+t.Name()); replayCompleteErr != nil {
		t.Fatalf("CompleteJob (idempotent replay): %v", replayCompleteErr)
	}
}

func TestIntegrationScheduleJobManualOwnershipGuard(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	ownerID, workflowID := seedUserWorkflow(ctx, t, pg)
	attackerID := testkit.SeedUser(ctx, t, pg, t.Name()+"-attacker@chronoverse.test")

	scheduledAt := time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)

	// Unbuilt and terminated workflows exist and are owned by the caller,
	// but they must not be schedulable.
	unbuiltWorkflowID := testkit.SeedWorkflow(ctx, t, pg, ownerID, t.Name()+"-unbuilt")
	if _, err := pg.Exec(ctx, `UPDATE workflows SET build_status = 'QUEUED' WHERE id = $1`, unbuiltWorkflowID); err != nil {
		t.Fatalf("unbuild workflow: %v", err)
	}
	terminatedWorkflowID := testkit.SeedWorkflow(ctx, t, pg, ownerID, t.Name()+"-terminated")
	if _, err := pg.Exec(ctx, `UPDATE workflows SET terminated_at = (now() AT TIME ZONE 'utc') WHERE id = $1`, terminatedWorkflowID); err != nil {
		t.Fatalf("terminate workflow: %v", err)
	}

	cases := []struct {
		name       string
		workflowID string
		userID     string
	}{
		{"cross user", workflowID, attackerID},
		{"nonexistent workflow", "00000000-0000-0000-0000-000000000000", ownerID},
		{"unbuilt workflow", unbuiltWorkflowID, ownerID},
		{"terminated workflow", terminatedWorkflowID, ownerID},
	}
	for _, tc := range cases {
		_, err := repo.ScheduleJob(ctx, tc.workflowID, tc.userID, scheduledAt, "MANUAL", fmt.Sprintf("idem-guard-%s-%s", t.Name(), tc.name), 1)
		if status.Code(err) != codes.NotFound {
			t.Fatalf("ScheduleJob(%s) code = %v, want %v (err: %v)", tc.name, status.Code(err), codes.NotFound, err)
		}
	}

	// Rejected attempts must not leave job rows behind.
	for _, wfID := range []string{workflowID, unbuiltWorkflowID, terminatedWorkflowID} {
		var jobCount int
		if err := pg.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE workflow_id = $1`, wfID).Scan(&jobCount); err != nil {
			t.Fatalf("count jobs: %v", err)
		}
		if jobCount != 0 {
			t.Fatalf("workflow %q has %d jobs, want 0 after rejected schedules", wfID, jobCount)
		}
	}

	// The owner can still schedule their built workflow after the rejections.
	jobID, err := repo.ScheduleJob(ctx, workflowID, ownerID, scheduledAt, "MANUAL", fmt.Sprintf("idem-guard-owner-%s", t.Name()), 1)
	if err != nil {
		t.Fatalf("ScheduleJob (owner): %v", err)
	}
	if jobID == "" {
		t.Fatal("expected a job id")
	}
}

func TestIntegrationCancelJobReplaysSnapshot(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	userID, workflowID := seedUserWorkflow(ctx, t, pg)
	scheduledAt := time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)
	jobID, err := repo.ScheduleJob(ctx, workflowID, userID, scheduledAt, "MANUAL", "idem-cancel-"+t.Name(), 1)
	if err != nil {
		t.Fatalf("ScheduleJob: %v", err)
	}

	// Canceling a PENDING job persists the pre-cancellation cleanup snapshot.
	commandID := "cancel-" + t.Name()
	snapshot, cancelErr := repo.CancelJob(ctx, jobID, commandID, "OPERATOR_REQUEST")
	if cancelErr != nil {
		t.Fatalf("CancelJob: %v", cancelErr)
	}
	if snapshot.ID != jobID {
		t.Fatalf("snapshot id = %q, want %q", snapshot.ID, jobID)
	}
	if snapshot.PreviousStatus != "PENDING" {
		t.Fatalf("snapshot previous_status = %q, want %q", snapshot.PreviousStatus, "PENDING")
	}

	// The job is CANCELED in the database.
	job, err := repo.GetJob(ctx, jobID, workflowID, userID)
	if err != nil {
		t.Fatalf("GetJob after cancel: %v", err)
	}
	if job.JobStatus != "CANCELED" {
		t.Fatalf("job status = %q, want %q", job.JobStatus, "CANCELED")
	}

	// Replaying the same cancellation command returns the stored snapshot.
	replayedSnapshot, replayErr := repo.CancelJob(ctx, jobID, commandID, "OPERATOR_REQUEST")
	if replayErr != nil {
		t.Fatalf("CancelJob (idempotent replay): %v", replayErr)
	}
	if replayedSnapshot == nil || replayedSnapshot.ID != snapshot.ID || replayedSnapshot.PreviousStatus != snapshot.PreviousStatus {
		t.Fatalf("replayed snapshot = %+v, want %+v", replayedSnapshot, snapshot)
	}

	// Canceling an unknown job is not found.
	if _, unknownErr := repo.CancelJob(ctx, "00000000-0000-0000-0000-000000000000", "cancel-unknown-"+t.Name(), "OPERATOR_REQUEST"); status.Code(unknownErr) != codes.NotFound {
		t.Fatalf("CancelJob(unknown) code = %v, want %v (err: %v)", status.Code(unknownErr), codes.NotFound, unknownErr)
	}
}

func TestIntegrationGetJobLogsFromClickHouse(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	ch := testkit.ClickHouse(t)

	userID, workflowID := seedUserWorkflow(ctx, t, pg)
	jobID, err := repo.ScheduleJob(ctx, workflowID, userID, time.Now().UTC().Format(time.RFC3339Nano), "MANUAL", "idem-logs-"+t.Name(), 1)
	if err != nil {
		t.Fatalf("ScheduleJob: %v", err)
	}

	// Seed logs directly in ClickHouse.
	now := time.Now().UTC()
	const logCount = 5
	for i := uint32(1); i <= logCount; i++ {
		if insertErr := ch.Exec(ctx, `
			INSERT INTO
				job_logs (
					event_id,
					user_id,
					workflow_id,
					job_id,
					timestamp,
					message,
					sequence_num,
					stream
				)
			VALUES
				(?, ?, ?, ?, ?, ?, ?, ?)
		`, fmt.Sprintf("log:%s:%s:%d", jobID, "stdout", i), userID, workflowID, jobID, now, fmt.Sprintf("log line %d", i), i, "stdout"); insertErr != nil {
			t.Fatalf("insert job log %d: %v", i, insertErr)
		}
	}

	// LogsFetchLimit is 2, so the first page returns the two newest logs plus a cursor.
	logs, err := repo.GetJobLogs(ctx, jobID, workflowID, userID, "", jobsmodel.JobLogsSortOrderDesc, &jobsmodel.GetJobLogsFilters{Stream: 1})
	if err != nil {
		t.Fatalf("GetJobLogs: %v", err)
	}
	if len(logs.JobLogs) != 2 {
		t.Fatalf("GetJobLogs returned %d logs, want 2", len(logs.JobLogs))
	}
	// Newest first by default.
	if logs.JobLogs[0].SequenceNum != 5 {
		t.Fatalf("first log sequence_num = %d, want 5 (descending order)", logs.JobLogs[0].SequenceNum)
	}
	if logs.JobLogs[0].Message != "log line 5" {
		t.Fatalf("first log message = %q, want %q", logs.JobLogs[0].Message, "log line 5")
	}
	if logs.Cursor == "" {
		t.Fatal("expected a cursor when there are more logs")
	}

	// The middle page has a continuation cursor and was eligible for Redis caching.
	paged, err := repo.GetJobLogs(ctx, jobID, workflowID, userID, logs.Cursor, jobsmodel.JobLogsSortOrderDesc, &jobsmodel.GetJobLogsFilters{Stream: 1})
	if err != nil {
		t.Fatalf("GetJobLogs (paged): %v", err)
	}
	require.Len(t, paged.JobLogs, 2)
	require.NotEmpty(t, paged.Cursor)
	if paged.JobLogs[0].SequenceNum != 3 {
		t.Fatalf("paged log sequence_num = %d, want 3", paged.JobLogs[0].SequenceNum)
	}

	verifyDeletedWorkflowDeniesRetainedLogRead(ctx, t, repo, userID, workflowID, func(svc *jobssvc.Service) (*jobsmodel.GetJobLogsResponse, error) {
		return svc.GetJobLogs(ctx, &jobspb.GetJobLogsRequest{
			Id: jobID, WorkflowId: workflowID, UserId: userID, Cursor: logs.Cursor,
			Filters: &jobspb.GetJobLogsFilters{Stream: jobspb.LogStream_LOG_STREAM_STDOUT},
		})
	})
	rows, err := ch.Query(ctx, "SELECT count() FROM job_logs WHERE job_id = ?", jobID)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next(), "missing retained ClickHouse row count: %v", rows.Err())
	var remaining uint64
	require.NoError(t, rows.Scan(&remaining))
	require.Equal(t, uint64(logCount), remaining, "retained ClickHouse rows after workflow deletion")
}

func TestIntegrationClickHouseBoundsLogPageTransfer(t *testing.T) {
	ctx := t.Context()
	repo := newTestRepository(t)
	repo.cfg.LogsFetchLimit = 100
	ch := testkit.ClickHouse(t)
	userID, workflowID := seedUserWorkflow(ctx, t, testkit.Postgres(t))
	jobID, err := repo.ScheduleJob(ctx, workflowID, userID, time.Now().UTC().Format(time.RFC3339Nano), "MANUAL", "idem-logs-"+t.Name(), 1)
	require.NoError(t, err)

	// Each message has the maximum normal ingestion width.
	require.NoError(t, ch.Exec(ctx, `
		INSERT INTO job_logs (event_id, user_id, workflow_id, job_id, timestamp, message, sequence_num, stream)
		SELECT
			concat(?, '-', toString(number)),
			toUUID(?), toUUID(?), toUUID(?), now64(3),
			repeat('x', 256 * 1024), toUInt32(number + 1), 'stdout'
		FROM numbers(101)
	`, jobID, userID, workflowID, jobID))

	queryID := "lookahead-" + jobID
	queryCtx := chdriver.Context(ctx, chdriver.WithQueryID(queryID), chdriver.WithSettings(chdriver.Settings{"log_queries": 1}))
	filters := &jobsmodel.GetJobLogsFilters{Stream: 1}
	first, err := repo.GetJobLogs(queryCtx, jobID, workflowID, userID, "", jobsmodel.JobLogsSortOrderAsc, filters)
	require.NoError(t, err)
	require.Len(t, first.JobLogs, 11)
	require.NotEmpty(t, first.Cursor)
	second, err := repo.GetJobLogs(ctx, jobID, workflowID, userID, first.Cursor, jobsmodel.JobLogsSortOrderAsc, filters)
	require.NoError(t, err)
	require.Len(t, second.JobLogs, 11)
	require.Equal(t, uint32(12), second.JobLogs[0].SequenceNum)

	require.NoError(t, ch.Exec(ctx, "SYSTEM FLUSH LOGS query_log"))
	rows, err := ch.Query(ctx, `
		SELECT result_rows, result_bytes
		FROM system.query_log
		WHERE query_id = ? AND type = 'QueryFinish'
	`, queryID)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next(), "missing completed query metrics: %v", rows.Err())
	var resultRows, resultBytes uint64
	require.NoError(t, rows.Scan(&resultRows, &resultBytes))
	t.Logf("ClickHouse result: %d rows, %d uncompressed result bytes", resultRows, resultBytes)
	require.LessOrEqual(t, resultRows, uint64(13), "one crossing row and enough lookahead to preserve the cursor")
	require.Less(t, resultBytes, uint64(4*1024*1024), "ClickHouse must omit the discarded payload")
}

//nolint:gocyclo // Verifies filtering and cursor continuity against the same indexed logs.
func TestIntegrationSearchJobLogsViaMeilisearch(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	ms := testkit.Meilisearch(t)

	userID, workflowID := seedUserWorkflow(ctx, t, pg)
	jobID, err := repo.ScheduleJob(ctx, workflowID, userID, time.Now().UTC().Format(time.RFC3339Nano), "MANUAL", "idem-search-"+t.Name(), 1)
	if err != nil {
		t.Fatalf("ScheduleJob: %v", err)
	}

	// Index documents directly with the same fields the joblogs processor
	// writes (including user_id, which SearchJobLogs filters on); the
	// meilisearch index is already configured by the testkit.
	fixtures := []struct {
		eventID         string
		sequence        uint32
		stream, message string
	}{
		{"log-1", 1, "stdout", "authentication succeeded"},
		{"log-2", 2, "stderr", "pagination fatal eror"},
		{"log-3", 2, "stdout", "pagination fatal error"},
		{"log-4", 2, "stdout", "pagination fatal error"},
		{"log-5", 2, "stdout", "pagination fatal error"},
		{"log-6", 3, "stdout", "pagination fatal error"},
		{"log-7", 4, "stdout", "pagination fatal startup"},
		{"log-8", 5, "stdout", "up small"},
		{"log-9", 6, "stdout", strings.Repeat("up ", 40_000)},
		{"log-a", 7, "stdout", "up last"},
	}
	docs := make([]map[string]any, 0, len(fixtures))
	for _, log := range fixtures {
		docs = append(docs, map[string]any{
			"id": "doc-" + t.Name() + "-" + log.eventID, jobLogsEventIDField: log.eventID,
			docJobIDField: jobID, docWorkflowIDField: workflowID, docUserIDField: userID,
			jobLogsMessageField: log.message, jobLogsTimestampField: time.Now().UTC().Format(time.RFC3339Nano),
			jobLogsSequenceNumField: log.sequence, jobLogsStreamField: log.stream,
		})
	}
	task, err := ms.Index(meilisearchpkg.IndexJobLogs).AddDocuments(docs, nil)
	if err != nil {
		t.Fatalf("AddDocuments: %v", err)
	}
	testkit.Eventually(t, 20*time.Second, 500*time.Millisecond, func() bool {
		taskInfo, err := ms.GetTask(task.TaskUID)
		return err == nil && taskInfo.Status == "succeeded"
	})

	// Searching for a term only returns the matching log.
	search, searchErr := repo.SearchJobLogs(ctx, jobID, workflowID, userID, "", &jobsmodel.SearchJobLogsFilters{
		Stream:  1,
		Message: "authentication",
	}, jobsmodel.SearchJobLogsOptions{SortOrder: jobsmodel.JobLogsSortOrderDesc, DisableHighlight: true})
	if searchErr != nil {
		t.Fatalf("SearchJobLogs: %v", searchErr)
	}
	if len(search.JobLogs) != 1 {
		t.Fatalf("SearchJobLogs returned %d logs, want 1", len(search.JobLogs))
	}
	if search.JobLogs[0].Message != "authentication succeeded" {
		t.Fatalf("searched message = %q, want %q", search.JobLogs[0].Message, "authentication succeeded")
	}

	var retainedCursor string
	for _, tc := range []struct {
		name, query string
		order       jobsmodel.JobLogsSortOrder
		want        []string
	}{
		{"ascending", "", jobsmodel.JobLogsSortOrderAsc, []string{"log-1", "log-2", "log-3", "log-4", "log-5", "log-6", "log-7", "log-8", "log-9", "log-a"}},
		{"descending", "", jobsmodel.JobLogsSortOrderDesc, []string{"log-a", "log-9", "log-8", "log-7", "log-6", "log-2", "log-3", "log-4", "log-5", "log-1"}},
		{"mixed relevance ascending", "fatal error", jobsmodel.JobLogsSortOrderAsc, []string{"log-2", "log-3", "log-4", "log-5", "log-6"}},
		{"mixed relevance descending", "fatal error", jobsmodel.JobLogsSortOrderDesc, []string{"log-6", "log-2", "log-3", "log-4", "log-5"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			cursor := ""
			for page := 0; ; page++ {
				if page >= len(docs) {
					t.Fatal("search pagination did not terminate")
				}
				result, err := repo.SearchJobLogs(
					ctx, jobID, workflowID, userID, cursor,
					&jobsmodel.SearchJobLogsFilters{Message: tc.query},
					jobsmodel.SearchJobLogsOptions{SortOrder: tc.order, DisableHighlight: true},
				)
				if err != nil {
					t.Fatalf("SearchJobLogs(page %d): %v", page, err)
				}
				if len(result.JobLogs) == 0 || len(result.JobLogs) > repo.cfg.LogsFetchLimit {
					t.Fatalf("page %d returned %d logs", page, len(result.JobLogs))
				}
				for _, log := range result.JobLogs {
					got = append(got, log.EventID)
				}
				if result.Cursor == "" {
					break
				}
				if page == 0 && tc.name == "descending" {
					retainedCursor = result.Cursor
				}
				cursor = result.Cursor
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("paged log IDs = %v, want %v without omissions or duplicates", got, tc.want)
			}
		})
	}
	t.Run("oversized highlights preserve the original line and continuation", func(t *testing.T) {
		limit := repo.cfg.LogsFetchLimit
		repo.cfg.LogsFetchLimit = 1
		defer func() { repo.cfg.LogsFetchLimit = limit }()
		filters := &jobsmodel.SearchJobLogsFilters{Message: "up"}
		options := jobsmodel.SearchJobLogsOptions{SortOrder: jobsmodel.JobLogsSortOrderAsc}
		first, err := repo.SearchJobLogs(ctx, jobID, workflowID, userID, "", filters, options)
		require.NoError(t, err)
		require.Len(t, first.JobLogs, 1)
		require.NotEmpty(t, first.Cursor)
		start, _ := jobLogsHighlightTags(first.HighlightToken)
		require.Contains(t, first.JobLogs[0].Message, start+"up")

		second, err := repo.SearchJobLogs(ctx, jobID, workflowID, userID, first.Cursor, filters, options)
		require.NoError(t, err)
		require.Len(t, second.JobLogs, 1)
		require.Equal(t, "log-9", second.JobLogs[0].EventID)
		require.Equal(t, strings.Repeat("up ", 40_000), second.JobLogs[0].Message)
		require.NotEmpty(t, second.Cursor)

		last, err := repo.SearchJobLogs(ctx, jobID, workflowID, userID, second.Cursor, filters, options)
		require.NoError(t, err)
		require.Len(t, last.JobLogs, 1)
		require.Equal(t, "log-a", last.JobLogs[0].EventID)
		require.Empty(t, last.Cursor)
	})

	if retainedCursor == "" {
		t.Fatal("fixture requires a continuation cursor")
	}
	verifyDeletedWorkflowDeniesRetainedLogRead(ctx, t, repo, userID, workflowID, func(svc *jobssvc.Service) (*jobsmodel.GetJobLogsResponse, error) {
		return svc.SearchJobLogs(ctx, &jobspb.SearchJobLogsRequest{
			Id: jobID, WorkflowId: workflowID, UserId: userID, Cursor: retainedCursor,
			Filters:          &jobspb.SearchJobLogsFilters{Stream: jobspb.LogStream_LOG_STREAM_ALL, Message: "pagination"},
			DisableHighlight: true,
		})
	})
	var document map[string]any
	if err := ms.Index(meilisearchpkg.IndexJobLogs).GetDocument("doc-"+t.Name()+"-log-3", nil, &document); err != nil || document["job_id"] != jobID {
		t.Fatalf("retained Meilisearch document unavailable after workflow deletion: %v", err)
	}
}

func TestIntegrationSearchJobLogsPastMeilisearchHitCeiling(t *testing.T) {
	ctx := t.Context()
	repo := newTestRepository(t)
	repo.cfg.LogsFetchLimit = 1000
	userID, workflowID := seedUserWorkflow(ctx, t, testkit.Postgres(t))
	jobID, err := repo.ScheduleJob(ctx, workflowID, userID, time.Now().UTC().Format(time.RFC3339Nano), "MANUAL", "idem-search-"+t.Name(), 1)
	require.NoError(t, err)

	const matchingLogs = 1002
	docs := make([]map[string]any, 0, matchingLogs)
	for seq := 1; seq <= matchingLogs; seq++ {
		eventID := fmt.Sprintf("%s-%04d", jobID, seq)
		docs = append(docs, map[string]any{
			"id": eventID, jobLogsEventIDField: eventID,
			docJobIDField: jobID, docWorkflowIDField: workflowID, docUserIDField: userID,
			jobLogsMessageField: "ceiling fixture", jobLogsSequenceNumField: seq, jobLogsStreamField: "stdout",
		})
	}
	ms := testkit.Meilisearch(t)
	task, err := ms.Index(meilisearchpkg.IndexJobLogs).AddDocuments(docs, nil)
	require.NoError(t, err)
	testkit.Eventually(t, 20*time.Second, 500*time.Millisecond, func() bool {
		taskInfo, taskErr := ms.GetTask(task.TaskUID)
		return taskErr == nil && taskInfo.Status == "succeeded"
	})

	filters := &jobsmodel.SearchJobLogsFilters{Message: "ceiling fixture"}
	options := jobsmodel.SearchJobLogsOptions{SortOrder: jobsmodel.JobLogsSortOrderAsc, DisableHighlight: true}
	first, err := repo.SearchJobLogs(ctx, jobID, workflowID, userID, "", filters, options)
	require.NoError(t, err)
	require.NotEmpty(t, first.Cursor, "search must reserve a lookahead row beyond the first page")
	require.Len(t, first.JobLogs, 999)
	last, err := repo.SearchJobLogs(ctx, jobID, workflowID, userID, first.Cursor, filters, options)
	require.NoError(t, err)
	require.Empty(t, last.Cursor)
	all := slices.Concat(first.JobLogs, last.JobLogs)
	require.Len(t, all, matchingLogs)
	for i, log := range all {
		require.Equal(t, uint32(i+1), log.SequenceNum)
	}
}

// storedWorkflowLogsClient keeps the workflow policy lookup backed by PostgreSQL.
type storedWorkflowLogsClient struct {
	fakeWorkflowsService
	repo *workflowsrepo.Repository
}

func (c *storedWorkflowLogsClient) GetWorkflow(
	ctx context.Context,
	req *workflowspb.GetWorkflowRequest,
	_ ...grpc.CallOption,
) (*workflowspb.GetWorkflowResponse, error) {
	workflow, err := c.repo.GetWorkflow(ctx, req.GetId(), req.GetUserId())
	if err != nil {
		return nil, err
	}
	return workflow.ToProto(), nil
}

func verifyDeletedWorkflowDeniesRetainedLogRead(
	ctx context.Context,
	t *testing.T,
	first *Repository,
	userID, workflowID string,
	read func(*jobssvc.Service) (*jobsmodel.GetJobLogsResponse, error),
) {
	t.Helper()
	workflow := workflowsrepo.New(&workflowsrepo.Config{}, first.pg)
	first.svc.Workflows = &storedWorkflowLogsClient{repo: workflow}
	second := New(first.cfg, first.auth, first.pg, first.rdb, first.ch, first.ms, &Services{
		Workflows: &storedWorkflowLogsClient{repo: workflowsrepo.New(&workflowsrepo.Config{}, first.pg)},
	})
	page, err := read(jobssvc.New(validator.New(), first))
	if err != nil || page == nil || len(page.JobLogs) == 0 {
		t.Fatalf("expected a visible retained page before deletion, got %v, %v", page, err)
	}
	require.NotEmpty(t, page.Cursor, "pre-deletion page must have qualified for the former Redis cache")
	if _, seedErr := first.pg.Exec(ctx, "UPDATE workflows SET terminated_at = clock_timestamp() WHERE id = $1", workflowID); seedErr != nil {
		t.Fatalf("seed terminated workflow: %v", seedErr)
	}
	// The delete writes the outbox event. No worker consumes it in this fixture.
	if deleteErr := workflow.DeleteWorkflow(ctx, workflowID, userID); deleteErr != nil {
		t.Fatalf("commit workflow deletion: %v", deleteErr)
	}
	page, err = read(jobssvc.New(validator.New(), second))
	if page != nil || status.Code(err) != codes.NotFound {
		t.Fatalf("fresh read on another service instance returned %v, %v, want NotFound", page, err)
	}
}

//nolint:gocyclo // Keeps subscription ordering and payload checks in one lifecycle.
func TestIntegrationStreamJobLogsOverRedis(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	userID, workflowID := seedUserWorkflow(ctx, t, pg)
	jobID, err := repo.ScheduleJob(ctx, workflowID, userID, time.Now().UTC().Format(time.RFC3339Nano), "MANUAL", "idem-stream-"+t.Name(), 1)
	if err != nil {
		t.Fatalf("ScheduleJob: %v", err)
	}

	// Streaming live logs requires the job to be RUNNING; move it through the
	// real claim lifecycle the way a worker would.
	queueJob(ctx, t, pg, jobID)
	seedReadyRuntimeNode(ctx, t, pg, t.Name())
	if _, ok, _, claimErr := repo.ClaimJob(
		ctx, jobID, workflowID, "integration-worker",
		"00000000-0000-0000-0000-0000000000c2", "claim-stream-"+t.Name(), time.Minute, 1,
	); claimErr != nil || !ok {
		t.Fatalf("ClaimJob: ok=%v err=%v", ok, claimErr)
	}

	sub, err := repo.StreamJobLogs(ctx, strings.ToUpper(jobID), workflowID, userID)
	if err != nil {
		t.Fatalf("StreamJobLogs: %v", err)
	}
	// Retain cleanup on early failure without closing the subscription twice.
	closed := false
	defer func() {
		if !closed {
			_ = sub.Close()
		}
	}()

	// Publish a live log event the way the joblogs processor does.
	event := &jobsmodel.JobLogEvent{
		EventKey:    "log:" + jobID + ":stdout:1",
		JobID:       jobID,
		WorkflowID:  workflowID,
		UserID:      userID,
		Message:     "hello from live stream",
		TimeStamp:   time.Now().UTC(),
		SequenceNum: 1,
		Stream:      "stdout",
		Retention:   true,
	}
	if published, publishErr := joblogevents.PublishLive(ctx, testkit.Redis(t), event); publishErr != nil || !published {
		t.Fatalf("PublishLive: published=%v error=%v", published, publishErr)
	}

	msg, receiveErr := sub.ReceiveMessage(ctx)
	if receiveErr != nil {
		t.Fatalf("ReceiveMessage: %v", receiveErr)
	}
	if got := msg.Channel; got != "job_logs:"+jobID {
		t.Fatalf("channel = %q, want %q", got, "job_logs:"+jobID)
	}
	var received jobsmodel.JobLogEvent
	if decodeErr := json.Unmarshal([]byte(msg.Payload), &received); decodeErr != nil {
		t.Fatalf("decode live event: %v", decodeErr)
	}
	if received.EventKey != event.EventKey || received.JobID != jobID || received.WorkflowID != workflowID ||
		received.UserID != userID || received.Message != event.Message || received.SequenceNum != 1 || received.Stream != "stdout" {
		t.Fatalf("live event = %+v, want %+v", received, event)
	}
	event.EventKey = "log:" + jobID + ":stderr:2"
	event.SequenceNum = 2
	event.Stream = "stderr"
	event.Message = "next live event"
	if published, publishErr := joblogevents.PublishLive(ctx, testkit.Redis(t), event); publishErr != nil || !published {
		t.Fatalf("publish next event: published=%v error=%v", published, publishErr)
	}
	msg, err = sub.ReceiveMessage(ctx)
	if err != nil {
		t.Fatalf("receive next event: %v", err)
	}
	if decodeErr := json.Unmarshal([]byte(msg.Payload), &received); decodeErr != nil {
		t.Fatalf("decode next event: %v", decodeErr)
	}
	if received.EventKey != event.EventKey || received.SequenceNum != 2 || received.Stream != "stderr" || received.Message != event.Message {
		t.Fatalf("next live event = %+v, want %+v", received, event)
	}
	closeErr := sub.Close()
	closed = true
	if closeErr != nil {
		t.Fatalf("close subscription: %v", closeErr)
	}
	if _, receiveErr = sub.ReceiveMessage(ctx); receiveErr == nil {
		t.Fatal("closed subscription still accepts messages")
	}
}

func TestIntegrationListJobsWithCursor(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	userID, workflowID := seedUserWorkflow(ctx, t, pg)

	// Seed more jobs than a single page holds so pagination has work to do.
	const totalJobs = 25
	jobIDs := make([]string, 0, totalJobs)
	for i := range totalJobs {
		jobID, scheduleErr := repo.ScheduleJob(
			ctx, workflowID, userID, time.Now().UTC().Format(time.RFC3339Nano), "MANUAL", fmt.Sprintf("idem-list-%s-%d", t.Name(), i), 1,
		)
		if scheduleErr != nil {
			t.Fatalf("ScheduleJob %d: %v", i, scheduleErr)
		}
		jobIDs = append(jobIDs, jobID)
	}

	want := make(map[string]struct{}, totalJobs)
	for _, id := range jobIDs {
		want[id] = struct{}{}
	}

	first, err := repo.ListJobs(ctx, workflowID, userID, "", &jobsmodel.ListJobsFilters{})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(first.Jobs) != repo.cfg.FetchLimit {
		t.Fatalf("ListJobs returned %d jobs, want %d", len(first.Jobs), repo.cfg.FetchLimit)
	}
	if first.Cursor == "" {
		t.Fatal("ListJobs returned an empty cursor although more jobs exist")
	}

	seen := walkListJobsPages(ctx, t, repo, workflowID, userID, first)

	if len(seen) != totalJobs {
		t.Fatalf("pagination visited %d distinct jobs, want %d", len(seen), totalJobs)
	}
	for id := range want {
		if _, ok := seen[id]; !ok {
			t.Fatalf("pagination missed job %q", id)
		}
	}

	// Filtering by status hides non-matching jobs.
	completed, err := repo.ListJobs(ctx, workflowID, userID, "", &jobsmodel.ListJobsFilters{Status: "COMPLETED"})
	if err != nil {
		t.Fatalf("ListJobs(COMPLETED): %v", err)
	}
	if len(completed.Jobs) != 0 {
		t.Fatalf("ListJobs(COMPLETED) returned %d jobs, want 0", len(completed.Jobs))
	}
}

// walkListJobsPages follows ListJobs cursors from the first page until the
// cursor is exhausted, returning every distinct job id observed. It fails the
// test on invalid cursors, transport errors, empty pages or duplicate ids.
func walkListJobsPages(
	ctx context.Context,
	t *testing.T,
	repo *Repository,
	workflowID, userID string,
	first *jobsmodel.ListJobsResponse,
) map[string]struct{} {
	t.Helper()

	seen := make(map[string]struct{}, len(first.Jobs))
	for _, job := range first.Jobs {
		seen[job.ID] = struct{}{}
	}

	// The returned cursor is base64-encoded; the gRPC layer decodes it before
	// reaching the repository.
	for page, cursor := 2, first.Cursor; cursor != ""; page++ {
		raw, decodeErr := base64.StdEncoding.DecodeString(cursor)
		if decodeErr != nil {
			t.Fatalf("ListJobs(page %d) returned an invalid cursor: %v", page, decodeErr)
		}
		next, listErr := repo.ListJobs(ctx, workflowID, userID, string(raw), &jobsmodel.ListJobsFilters{})
		if listErr != nil {
			t.Fatalf("ListJobs(page %d): %v", page, listErr)
		}
		if len(next.Jobs) == 0 {
			t.Fatalf("ListJobs(page %d) returned no jobs for a non-empty cursor", page)
		}
		for _, job := range next.Jobs {
			if _, ok := seen[job.ID]; ok {
				t.Fatalf("ListJobs(page %d) returned duplicate job %q", page, job.ID)
			}
			seen[job.ID] = struct{}{}
		}
		cursor = next.Cursor
	}

	return seen
}
