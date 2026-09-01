//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package jobs

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"
	"time"

	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
	"github.com/hitesh22rana/chronoverse/internal/pkg/joblogevents"
	meilisearchpkg "github.com/hitesh22rana/chronoverse/internal/pkg/meilisearch"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
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

func (f *fakeWorkflowsService) CreateWorkflow(context.Context, *workflowspb.CreateWorkflowRequest, ...grpc.CallOption) (*workflowspb.CreateWorkflowResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeWorkflowsService) UpdateWorkflow(context.Context, *workflowspb.UpdateWorkflowRequest, ...grpc.CallOption) (*workflowspb.UpdateWorkflowResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeWorkflowsService) UpdateWorkflowBuildStatus(context.Context, *workflowspb.UpdateWorkflowBuildStatusRequest, ...grpc.CallOption) (*workflowspb.UpdateWorkflowBuildStatusResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeWorkflowsService) GetWorkflow(context.Context, *workflowspb.GetWorkflowRequest, ...grpc.CallOption) (*workflowspb.GetWorkflowResponse, error) {
	return &workflowspb.GetWorkflowResponse{LogRetention: true, Name: "integration-workflow", Kind: "CONTAINER"}, nil
}

func (f *fakeWorkflowsService) GetWorkflowByID(context.Context, *workflowspb.GetWorkflowByIDRequest, ...grpc.CallOption) (*workflowspb.GetWorkflowByIDResponse, error) {
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

func (f *fakeWorkflowsService) TerminateWorkflow(context.Context, *workflowspb.TerminateWorkflowRequest, ...grpc.CallOption) (*workflowspb.TerminateWorkflowResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeWorkflowsService) DeleteWorkflow(context.Context, *workflowspb.DeleteWorkflowRequest, ...grpc.CallOption) (*workflowspb.DeleteWorkflowResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeWorkflowsService) ListWorkflows(context.Context, *workflowspb.ListWorkflowsRequest, ...grpc.CallOption) (*workflowspb.ListWorkflowsResponse, error) {
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
		INSERT INTO runtime_nodes (id, node_name, docker_endpoint, status, last_heartbeat_at, max_concurrency)
		VALUES ($1, $2, 'tcp://127.0.0.1:2375', 'READY', now() AT TIME ZONE 'utc', 4)
		ON CONFLICT (id) DO UPDATE
		SET status = 'READY',
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
	for i := uint32(1); i <= 3; i++ {
		if insertErr := ch.Exec(ctx, `
			INSERT INTO job_logs (event_id, user_id, workflow_id, job_id, timestamp, message, sequence_num, stream)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, fmt.Sprintf("log:%s:%s:%d", jobID, "stdout", i), userID, workflowID, jobID, now, fmt.Sprintf("log line %d", i), i, "stdout"); insertErr != nil {
			t.Fatalf("insert job log %d: %v", i, insertErr)
		}
	}

	// LogsFetchLimit is 2, so the first page returns the two newest logs plus a cursor.
	logs, _, err := repo.GetJobLogs(ctx, jobID, workflowID, userID, "", jobsmodel.JobLogsSortOrderDesc, &jobsmodel.GetJobLogsFilters{Stream: 1})
	if err != nil {
		t.Fatalf("GetJobLogs: %v", err)
	}
	if len(logs.JobLogs) != 2 {
		t.Fatalf("GetJobLogs returned %d logs, want 2", len(logs.JobLogs))
	}
	// Newest first by default.
	if logs.JobLogs[0].SequenceNum != 3 {
		t.Fatalf("first log sequence_num = %d, want 3 (descending order)", logs.JobLogs[0].SequenceNum)
	}
	if logs.JobLogs[0].Message != "log line 3" {
		t.Fatalf("first log message = %q, want %q", logs.JobLogs[0].Message, "log line 3")
	}
	if logs.Cursor == "" {
		t.Fatal("expected a cursor when there are more logs")
	}

	// Cursor pagination returns the remaining log.
	paged, _, err := repo.GetJobLogs(ctx, jobID, workflowID, userID, logs.Cursor, jobsmodel.JobLogsSortOrderDesc, &jobsmodel.GetJobLogsFilters{Stream: 1})
	if err != nil {
		t.Fatalf("GetJobLogs (paged): %v", err)
	}
	if len(paged.JobLogs) != 1 {
		t.Fatalf("GetJobLogs (paged) returned %d logs, want 1", len(paged.JobLogs))
	}
	if paged.JobLogs[0].SequenceNum != 1 {
		t.Fatalf("paged log sequence_num = %d, want 1", paged.JobLogs[0].SequenceNum)
	}
}

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
	docs := []map[string]any{
		{
			"id": "doc-" + t.Name() + "-1", jobLogsEventIDField: "log-1", docJobIDField: jobID, docWorkflowIDField: workflowID,
			docUserIDField: userID, jobLogsMessageField: "authentication succeeded", jobLogsTimestampField: time.Now().UTC().Format(time.RFC3339Nano), jobLogsSequenceNumField: 1, jobLogsStreamField: "stdout",
		},
		{
			"id": "doc-" + t.Name() + "-2", jobLogsEventIDField: "log-2", docJobIDField: jobID, docWorkflowIDField: workflowID,
			docUserIDField: userID, jobLogsMessageField: "database connection refused",
			jobLogsTimestampField: time.Now().UTC().Format(time.RFC3339Nano), jobLogsSequenceNumField: 2, jobLogsStreamField: "stderr",
		},
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
	search, _, searchErr := repo.SearchJobLogs(ctx, jobID, workflowID, userID, "", &jobsmodel.SearchJobLogsFilters{
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
}

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

	sub, err := repo.StreamJobLogs(ctx, jobID, workflowID, userID)
	if err != nil {
		t.Fatalf("StreamJobLogs: %v", err)
	}
	defer sub.Close()

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
	if _, err := joblogevents.PublishLive(ctx, testkit.Redis(t), event); err != nil {
		t.Fatalf("PublishLive: %v", err)
	}

	msg, receiveErr := sub.ReceiveMessage(ctx)
	if receiveErr != nil {
		t.Fatalf("ReceiveMessage: %v", receiveErr)
	}
	if got := msg.Channel; got != "job_logs:"+jobID {
		t.Fatalf("channel = %q, want %q", got, "job_logs:"+jobID)
	}
	if msg.Payload == "" {
		t.Fatal("expected a non-empty payload")
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
