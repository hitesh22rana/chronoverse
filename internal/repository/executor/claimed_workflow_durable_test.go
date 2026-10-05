//nolint:testpackage // Durable tests share package-internal helpers and constructors.
package executor

import (
	"context"
	"database/sql"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
	jobsrepo "github.com/hitesh22rana/chronoverse/internal/repository/jobs"
	workflowsrepo "github.com/hitesh22rana/chronoverse/internal/repository/workflows"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

const (
	// durableCleanupBound caps every teardown statement so a stuck database cannot
	// hang the suite.
	durableCleanupBound = 15 * time.Second
	// durableHeartbeatPayload is a valid HEARTBEAT body; heartbeat workflows claim
	// without a runtime node, which keeps the fixtures free of container plumbing.
	durableHeartbeatPayload = `{"endpoint":"https://example.test/health","timeout":"5s"}`
)

// durableJobState is the durable jobs-row surface every claimed run must reach, or
// leave untouched when it no longer owns the job.
type durableJobState struct {
	Status             string
	LeaseToken         sql.NullString
	LeasedBy           sql.NullString
	TerminalReasonCode sql.NullString
	FailureKind        sql.NullString
	LastErrorCode      sql.NullString
	NextAttemptAt      sql.NullTime
	CompletedAt        sql.NullTime
	Attempts           int32
}

// durableJobsClient forwards the executor's jobs-service calls to the real
// jobs repository so every assertion observes committed PostgreSQL state instead
// of a recorded request.
type durableJobsClient struct {
	jobspb.JobsServiceClient

	repo *jobsrepo.Repository
}

func (c *durableJobsClient) ScheduleJob(
	ctx context.Context,
	req *jobspb.ScheduleJobRequest,
	_ ...grpc.CallOption,
) (*jobspb.ScheduleJobResponse, error) {
	id, err := c.repo.ScheduleJob(ctx, req.GetWorkflowId(), req.GetUserId(), req.GetScheduledAt(), req.GetTrigger(), req.GetIdempotencyKey(), req.GetWorkflowGeneration())
	if err != nil {
		return nil, err
	}

	return &jobspb.ScheduleJobResponse{Id: id}, nil
}

func (c *durableJobsClient) RenewJobLease(
	ctx context.Context,
	req *jobspb.RenewJobLeaseRequest,
	_ ...grpc.CallOption,
) (*jobspb.RenewJobLeaseResponse, error) {
	expiresAt, err := c.repo.RenewJobLease(ctx, req.GetId(), req.GetLeaseToken(), time.Duration(req.GetLeaseDurationSeconds())*time.Second)
	if err != nil {
		return nil, err
	}

	return &jobspb.RenewJobLeaseResponse{LeaseExpiresAt: expiresAt.Format(time.RFC3339Nano)}, nil
}

func (c *durableJobsClient) ReleaseJobForRetry(
	ctx context.Context,
	req *jobspb.ReleaseJobForRetryRequest,
	_ ...grpc.CallOption,
) (*jobspb.ReleaseJobForRetryResponse, error) {
	if err := c.repo.ReleaseJobForRetry(
		ctx,
		req.GetId(),
		req.GetLeaseToken(),
		req.GetNextAttemptAt(),
		req.GetErrorCode(),
		req.GetErrorMessage(),
		req.GetCommandId(),
	); err != nil {
		return nil, err
	}

	return &jobspb.ReleaseJobForRetryResponse{}, nil
}

func (c *durableJobsClient) CancelClaimedJob(
	ctx context.Context,
	req *jobspb.CancelClaimedJobRequest,
	_ ...grpc.CallOption,
) (*jobspb.CancelClaimedJobResponse, error) {
	if err := c.repo.CancelClaimedJob(ctx, req.GetId(), req.GetLeaseToken(), req.GetTerminalReasonCode(), req.GetCommandId()); err != nil {
		return nil, err
	}

	return &jobspb.CancelClaimedJobResponse{}, nil
}

func (c *durableJobsClient) FailJob(
	ctx context.Context,
	req *jobspb.FailJobRequest,
	_ ...grpc.CallOption,
) (*jobspb.FailJobResponse, error) {
	if err := c.repo.FailJob(
		ctx,
		req.GetId(),
		req.GetLeaseToken(),
		req.GetFailureKind(),
		req.GetErrorCode(),
		req.GetErrorMessage(),
		req.GetTerminalReasonCode(),
		req.GetCommandId(),
	); err != nil {
		return nil, err
	}

	return &jobspb.FailJobResponse{}, nil
}

func (c *durableJobsClient) CompleteJob(
	ctx context.Context,
	req *jobspb.CompleteJobRequest,
	_ ...grpc.CallOption,
) (*jobspb.CompleteJobResponse, error) {
	if err := c.repo.CompleteJob(ctx, req.GetId(), req.GetLeaseToken(), req.GetCommandId()); err != nil {
		return nil, err
	}

	return &jobspb.CompleteJobResponse{}, nil
}

// AttachJobContainer is unreachable: every durable fixture seeds a HEARTBEAT
// workflow and rejectingContainerSvc refuses any runtime, so no container is ever
// attached. Panicking keeps a future container fixture from passing through a
// seam these assertions cannot interpret.
func (*durableJobsClient) AttachJobContainer(
	context.Context,
	*jobspb.AttachJobContainerRequest,
	...grpc.CallOption,
) (*jobspb.AttachJobContainerResponse, error) {
	panic("durable fixtures seed HEARTBEAT workflows and must never attach a container")
}

// durableWorkflowsClient serves the executor's workflow read from the real
// workflows repository, so termination and generation come from committed state.
// onGet runs once the read has committed, which is the only seam between lease
// renewal and the executor's terminal decision: a test that needs the run to
// reach that decision on a lease it no longer holds steals it here.
type durableWorkflowsClient struct {
	workflowspb.WorkflowsServiceClient

	repo  *workflowsrepo.Repository
	onGet func(context.Context)
}

func (c *durableWorkflowsClient) GetWorkflowByID(
	ctx context.Context,
	req *workflowspb.GetWorkflowByIDRequest,
	_ ...grpc.CallOption,
) (*workflowspb.GetWorkflowByIDResponse, error) {
	res, err := c.repo.GetWorkflowByID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	if c.onGet != nil {
		c.onGet(ctx)
	}

	return res.ToProto(), nil
}

// recordingHeartbeatSvc reports heartbeat executions and can run an action in the
// middle of one, which is how a test steals a lease deterministically.
type recordingHeartbeatSvc struct {
	calls atomic.Int32
	onRun func(context.Context)
}

func (s *recordingHeartbeatSvc) Execute(ctx context.Context, _ time.Duration, _ string, _ int, _ map[string][]string) error {
	s.calls.Add(1)
	if s.onRun != nil {
		s.onRun(ctx)
	}

	return nil
}

// rejectingContainerSvc fails the test if a fixture asks for container execution.
// The durable fixtures are heartbeat workflows, so any container work is a bug in
// the executor's routing rather than a value this suite can assert on. Panicking
// rather than returning an error keeps a container-kind fixture from quietly
// reading as the release-for-retry outcome an unrunnable workflow legitimately
// produces.
type rejectingContainerSvc struct {
	factoryCalls atomic.Int32
}

func (s *rejectingContainerSvc) factory(string, string) (ContainerSvc, error) {
	s.factoryCalls.Add(1)

	panic("heartbeat workflow must not request a container runtime")
}

// durableFixture is one private user, workflow and claimed job, plus the durable
// identities the assertions need.
type durableFixture struct {
	UserID          string
	WorkflowID      string
	JobID           string
	LeaseToken      string
	Generation      int64
	Interval        int32
	LastScheduledAt time.Time
}

// durableFixtureTag returns a short unique identity fragment. Identity is never
// derived from t.Name(), because several subtests reuse one name.
func durableFixtureTag() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}

// seedDurableClaim seeds an isolated user and workflow, then schedules, queues and
// claims one AUTOMATIC job through the real jobs repository. Cleanup deletes the
// fixture user, which cascades to exactly this workflow and its jobs.
func seedDurableClaim(ctx context.Context, t *testing.T, pg *postgres.Postgres, repo *jobsrepo.Repository, buildStatus string, generation int64) *durableFixture {
	t.Helper()

	tag := durableFixtureTag()
	var userID string
	if err := pg.QueryRow(ctx, `INSERT INTO users (email, password) VALUES ($1, 'hash') RETURNING id`, "cv-durable-"+tag+"@chronoverse.test").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), durableCleanupBound)
		defer cancel()
		if _, err := pg.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
			t.Errorf("delete fixture user %q: %v", userID, err)
		}
	})

	const interval = 5
	var workflowID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO workflows (user_id, name, payload, kind, build_status, interval, generation, log_retention)
		VALUES ($1, $2, $3::jsonb, 'HEARTBEAT', $4, $5, $6, FALSE)
		RETURNING id
	`, userID, "cv-durable-"+tag, durableHeartbeatPayload, buildStatus, interval, generation).Scan(&workflowID); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}

	lastScheduledAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	jobID, err := repo.ScheduleJob(
		ctx,
		workflowID,
		userID,
		lastScheduledAt.Format(time.RFC3339Nano),
		jobsmodel.JobTriggerAutomatic.ToString(),
		"",
		generation,
	)
	if err != nil {
		t.Fatalf("ScheduleJob: %v", err)
	}
	if _, queueErr := pg.Exec(ctx, `UPDATE jobs SET status = 'QUEUED', dispatch_attempts = 1 WHERE id = $1`, jobID); queueErr != nil {
		t.Fatalf("queue job: %v", queueErr)
	}

	claimed, ok, reason, err := repo.ClaimJob(ctx, jobID, workflowID, "durable-worker", uuid.NewString(), uuid.NewString(), 30*time.Second, 1)
	if err != nil {
		t.Fatalf("ClaimJob: %v", err)
	}
	if !ok {
		t.Fatalf("ClaimJob did not claim the job: %s", reason)
	}

	return &durableFixture{
		UserID:          userID,
		WorkflowID:      workflowID,
		JobID:           jobID,
		LeaseToken:      claimed.LeaseToken,
		Generation:      generation,
		Interval:        interval,
		LastScheduledAt: lastScheduledAt,
	}
}

// claim returns the snapshot the worker received from ClaimJob.
func (f *durableFixture) claim() *jobspb.ClaimJobResponse {
	return &jobspb.ClaimJobResponse{
		Claimed:          true,
		Id:               f.JobID,
		WorkflowId:       f.WorkflowID,
		UserId:           f.UserID,
		Trigger:          jobsmodel.JobTriggerAutomatic.ToString(),
		Attempts:         1,
		LeaseToken:       f.LeaseToken,
		ScheduledAt:      f.LastScheduledAt.Format(time.RFC3339Nano),
		DispatchAttempts: 1,
	}
}

// newDurableExecutor wires the executor to the real jobs and workflows
// repositories plus the supplied execution seams, and registers one activated
// handoff permit for the claim so reconciliation state is observable.
func newDurableExecutor(
	fixture *durableFixture,
	jobs *jobsrepo.Repository,
	workflows *workflowsrepo.Repository,
	hsvc HeartBeatSvc,
	csvc *rejectingContainerSvc,
) *Repository {
	gate := newHandoffRegistry(1)
	request := &jobspb.ClaimJobRequest{Id: fixture.JobID, WorkflowId: fixture.WorkflowID}
	entry, owner, err := gate.getOrReserve("durable-claim-command", request)
	if err != nil || !owner {
		panic("durable fixture could not reserve a handoff permit")
	}
	if !gate.activate(entry, fixture.claim()) {
		panic("durable fixture could not activate a handoff permit")
	}

	return &Repository{
		tp:       otel.Tracer("executor-durable-test"),
		cfg:      durableRunConfig(),
		auth:     fakeAuth{},
		handoffs: gate,
		svc: &Services{
			Jobs:            &durableJobsClient{repo: jobs},
			Workflows:       &durableWorkflowsClient{repo: workflows},
			Hsvc:            hsvc,
			CsvcForEndpoint: csvc.factory,
		},
	}
}

// durableRunConfig keeps the lease comfortably longer than any fixture run, so
// renewal only ever proves authority.
func durableRunConfig() Config {
	return Config{
		WorkerID:            "durable-worker",
		Concurrency:         1,
		LeaseDuration:       30 * time.Second,
		LeaseRenewInterval:  5 * time.Second,
		SystemRetryLimit:    3,
		SystemRetryBackoff:  30 * time.Second,
		JobLogBatchSize:     10,
		JobLogBatchInterval: time.Hour,
	}
}

// newDurableRepositories builds the real jobs and workflows repositories over one
// PostgreSQL instance.
func newDurableRepositories(pg *postgres.Postgres) (jobs *jobsrepo.Repository, workflows *workflowsrepo.Repository) {
	jobs = jobsrepo.New(&jobsrepo.Config{
		FetchLimit:          20,
		LogsFetchLimit:      2,
		RuntimeHeartbeatTTL: time.Minute,
		RuntimeLostAfter:    5 * time.Minute,
	}, fakeAuth{}, pg, nil, nil, nil, &jobsrepo.Services{})

	return jobs, workflowsrepo.New(&workflowsrepo.Config{FetchLimit: 20}, pg)
}

// readDurableJobState reads the durable jobs row a claimed run must converge to.
func readDurableJobState(ctx context.Context, t *testing.T, pg *postgres.Postgres, jobID string) durableJobState {
	t.Helper()

	var state durableJobState
	if err := pg.QueryRow(ctx, `
		SELECT status, lease_token, leased_by, terminal_reason_code, failure_kind,
			last_error_code, next_attempt_at, completed_at, attempts
		FROM jobs
		WHERE id = $1
	`, jobID).Scan(
		&state.Status,
		&state.LeaseToken,
		&state.LeasedBy,
		&state.TerminalReasonCode,
		&state.FailureKind,
		&state.LastErrorCode,
		&state.NextAttemptAt,
		&state.CompletedAt,
		&state.Attempts,
	); err != nil {
		t.Fatalf("read durable job state: %v", err)
	}

	return state
}

// countWorkflowJobs returns every job row a workflow owns, so a test can prove a
// superseded or terminated workflow gained no new scheduled work.
func countWorkflowJobs(ctx context.Context, t *testing.T, pg *postgres.Postgres, workflowID string) int {
	t.Helper()

	var count int
	if err := pg.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE workflow_id = $1`, workflowID).Scan(&count); err != nil {
		t.Fatalf("count workflow jobs: %v", err)
	}

	return count
}

// stealJobLease simulates another worker taking over a running job: the durable
// row now holds a different lease token, so every command the previous holder
// issues must be rejected.
func stealJobLease(ctx context.Context, t *testing.T, pg *postgres.Postgres, jobID, thiefToken string) {
	t.Helper()

	if _, err := pg.Exec(ctx, `
		UPDATE jobs
		SET lease_token = $2, leased_by = 'thief-worker', lease_process_instance_id = $3
		WHERE id = $1
	`, jobID, thiefToken, uuid.NewString()); err != nil {
		t.Fatalf("steal job lease: %v", err)
	}
}

// TestIntegrationClaimedRunTerminatesWorkflowAndCancelsClaimedJob proves a user
// termination settles the in-flight job as CANCELED with the termination reason
// and leaves no follow-up schedule behind. Releasing such a job instead would
// resurrect work the user explicitly stopped.
func TestIntegrationClaimedRunTerminatesWorkflowAndCancelsClaimedJob(t *testing.T) {
	ctx := t.Context()
	pg := testkit.Postgres(t)
	jobs, workflows := newDurableRepositories(pg)

	fixture := seedDurableClaim(ctx, t, pg, jobs, workflowsmodel.WorkflowBuildStatusCompleted.ToString(), 3)
	heartbeat := &recordingHeartbeatSvc{}
	containers := &rejectingContainerSvc{}
	repo := newDurableExecutor(fixture, jobs, workflows, heartbeat, containers)

	if err := workflows.TerminateWorkflow(ctx, fixture.WorkflowID, fixture.UserID); err != nil {
		t.Fatalf("TerminateWorkflow: %v", err)
	}

	if err := repo.runClaimedWorkflow(ctx, fixture.claim(), fixture.LastScheduledAt, fixture.Generation); err != nil {
		t.Fatalf("runClaimedWorkflow() error = %v, want nil once the job was canceled", err)
	}

	state := readDurableJobState(ctx, t, pg, fixture.JobID)
	if state.Status != jobsmodel.JobStatusCanceled.ToString() {
		t.Fatalf("job status = %q, want %q", state.Status, jobsmodel.JobStatusCanceled.ToString())
	}
	if !state.TerminalReasonCode.Valid || state.TerminalReasonCode.String != terminalreason.WorkflowTerminated.String() {
		t.Fatalf("terminal_reason_code = %v, want %q", state.TerminalReasonCode, terminalreason.WorkflowTerminated.String())
	}
	if state.LeaseToken.Valid {
		t.Fatalf("lease_token = %q, want NULL after cancellation", state.LeaseToken.String)
	}
	if !state.CompletedAt.Valid {
		t.Fatal("completed_at is NULL, want the terminal instant")
	}

	if got := heartbeat.calls.Load(); got != 0 {
		t.Fatalf("heartbeat executions = %d, want 0 for a terminated workflow", got)
	}
	if got := containers.factoryCalls.Load(); got != 0 {
		t.Fatalf("container runtime lookups = %d, want 0 for a terminated workflow", got)
	}
	if got := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); got != 1 {
		t.Fatalf("workflow job rows = %d, want 1: a terminated workflow must gain no follow-up", got)
	}
	if got := repo.handoffs.size(); got != 0 {
		t.Fatalf("handoff permits = %d, want 0 once the cancellation was committed", got)
	}
}

// TestIntegrationClaimedRunReleasesUnreadyWorkflowForSystemRetry proves a workflow
// that is not runnable yet returns its job to the queue with the readiness cause
// recorded, instead of being canceled or failed. The user never asked for this job
// to stop, so the system must keep trying.
func TestIntegrationClaimedRunReleasesUnreadyWorkflowForSystemRetry(t *testing.T) {
	ctx := t.Context()
	pg := testkit.Postgres(t)
	jobs, workflows := newDurableRepositories(pg)

	fixture := seedDurableClaim(ctx, t, pg, jobs, workflowsmodel.WorkflowBuildStatusCompleted.ToString(), 2)
	heartbeat := &recordingHeartbeatSvc{}
	containers := &rejectingContainerSvc{}
	repo := newDurableExecutor(fixture, jobs, workflows, heartbeat, containers)

	// The job was dispatched against a runnable workflow; a new build then started,
	// which is exactly when a claimed job finds the workflow unfinished.
	if _, err := pg.Exec(ctx, `UPDATE workflows SET build_status = $2 WHERE id = $1`, fixture.WorkflowID, workflowsmodel.WorkflowBuildStatusStarted.ToString()); err != nil {
		t.Fatalf("restart workflow build: %v", err)
	}

	if err := repo.runClaimedWorkflow(ctx, fixture.claim(), fixture.LastScheduledAt, fixture.Generation); err != nil {
		t.Fatalf("runClaimedWorkflow() error = %v, want nil once the claim was released", err)
	}

	state := readDurableJobState(ctx, t, pg, fixture.JobID)
	if state.Status != jobsmodel.JobStatusPending.ToString() {
		t.Fatalf("job status = %q, want %q so the scheduler can dispatch it again", state.Status, jobsmodel.JobStatusPending.ToString())
	}
	if state.TerminalReasonCode.Valid {
		t.Fatalf("terminal_reason_code = %q, want none for a released claim", state.TerminalReasonCode.String)
	}
	if state.LeaseToken.Valid || state.LeasedBy.Valid {
		t.Fatalf("lease = (%q, %q), want both cleared after release", state.LeaseToken.String, state.LeasedBy.String)
	}
	if !state.NextAttemptAt.Valid {
		t.Fatal("next_attempt_at is NULL, want a retry instant")
	}
	if !state.NextAttemptAt.Time.After(time.Now().UTC()) {
		t.Fatalf("next_attempt_at = %s, want a future retry instant", state.NextAttemptAt.Time.UTC())
	}
	if got := state.LastErrorCode.String; got != codes.FailedPrecondition.String() {
		t.Fatalf("last_error_code = %q, want %q", got, codes.FailedPrecondition.String())
	}
	if state.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1: a release must not consume another attempt", state.Attempts)
	}

	if got := heartbeat.calls.Load(); got != 0 {
		t.Fatalf("heartbeat executions = %d, want 0 while the workflow is unbuilt", got)
	}
	if got := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); got != 1 {
		t.Fatalf("workflow job rows = %d, want 1: an unrunnable workflow must gain no follow-up", got)
	}
	if got := repo.handoffs.size(); got != 0 {
		t.Fatalf("handoff permits = %d, want 0 once the release was committed", got)
	}
}

// TestIntegrationClaimedRunCannotSettleJobAfterLeaseIsStolen proves a worker whose
// lease was taken over mid-run cannot write the durable row. Completion is the most
// dangerous case to get wrong: reporting success for another owner's job would
// report a workload the worker no longer owned as done.
func TestIntegrationClaimedRunCannotSettleJobAfterLeaseIsStolen(t *testing.T) {
	ctx := t.Context()
	pg := testkit.Postgres(t)
	jobs, workflows := newDurableRepositories(pg)

	fixture := seedDurableClaim(ctx, t, pg, jobs, workflowsmodel.WorkflowBuildStatusCompleted.ToString(), 1)

	const thiefToken = "thief-worker:00000000-0000-4000-8000-000000000001"
	heartbeat := &recordingHeartbeatSvc{
		onRun: func(context.Context) {
			stealJobLease(ctx, t, pg, fixture.JobID, thiefToken)
		},
	}
	repo := newDurableExecutor(fixture, jobs, workflows, heartbeat, &rejectingContainerSvc{})

	err := repo.runClaimedWorkflow(ctx, fixture.claim(), fixture.LastScheduledAt, fixture.Generation)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("runClaimedWorkflow() code = %s, want %s: %v", status.Code(err), codes.FailedPrecondition, err)
	}

	if got := heartbeat.calls.Load(); got != 1 {
		t.Fatalf("heartbeat executions = %d, want 1", got)
	}

	state := readDurableJobState(ctx, t, pg, fixture.JobID)
	if state.Status != jobsmodel.JobStatusRunning.ToString() {
		t.Fatalf("job status = %q, want %q: the thief owns the run", state.Status, jobsmodel.JobStatusRunning.ToString())
	}
	if !state.LeaseToken.Valid || state.LeaseToken.String != thiefToken {
		t.Fatalf("lease_token = %v, want the new owner's %q", state.LeaseToken, thiefToken)
	}
	if state.CompletedAt.Valid {
		t.Fatal("completed_at was written by a worker that no longer held the lease")
	}
	if state.TerminalReasonCode.Valid {
		t.Fatalf("terminal_reason_code = %q, want none from the previous holder", state.TerminalReasonCode.String)
	}

	if got := repo.handoffs.size(); got != 1 {
		t.Fatalf("handoff permits = %d, want 1: the rejected settlement still needs reconciliation", got)
	}
}

// TestIntegrationClaimedRunCannotCancelJobAfterLeaseIsStolen proves a stale holder
// cannot cancel a running job either. Cancellation is a user-visible terminal
// decision, so allowing it from a previous lease owner would let a zombie worker
// kill its replacement's run.
//
// The workflow must already be terminated before the run reads it, otherwise the
// executor never reaches the cancellation branch at all and the rejection is
// proven by the completion guard instead. The lease is stolen on the workflow
// read: that is the only point after renewal has proved authority and before the
// terminal write, so the run holds a token the thief has already replaced.
func TestIntegrationClaimedRunCannotCancelJobAfterLeaseIsStolen(t *testing.T) {
	ctx := t.Context()
	pg := testkit.Postgres(t)
	jobs, workflows := newDurableRepositories(pg)

	fixture := seedDurableClaim(ctx, t, pg, jobs, workflowsmodel.WorkflowBuildStatusCompleted.ToString(), 1)
	if err := workflows.TerminateWorkflow(ctx, fixture.WorkflowID, fixture.UserID); err != nil {
		t.Fatalf("TerminateWorkflow: %v", err)
	}

	const thiefToken = "thief-worker:00000000-0000-4000-8000-000000000002"
	heartbeat := &recordingHeartbeatSvc{}
	repo := newDurableExecutor(fixture, jobs, workflows, heartbeat, &rejectingContainerSvc{})
	client, ok := repo.svc.Workflows.(*durableWorkflowsClient)
	if !ok {
		t.Fatalf("durable fixture wired %T, want *durableWorkflowsClient", repo.svc.Workflows)
	}
	client.onGet = func(context.Context) {
		stealJobLease(ctx, t, pg, fixture.JobID, thiefToken)
	}

	err := repo.runClaimedWorkflow(ctx, fixture.claim(), fixture.LastScheduledAt, fixture.Generation)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("runClaimedWorkflow() code = %s, want %s: %v", status.Code(err), codes.FailedPrecondition, err)
	}

	// A terminated workflow must be canceled before anything is executed, so a
	// non-zero count would mean this run settled through another branch and the
	// assertions below are reading the completion guard, not the cancel guard.
	if got := heartbeat.calls.Load(); got != 0 {
		t.Fatalf("heartbeat executions = %d, want 0: the run must take the cancellation branch", got)
	}

	state := readDurableJobState(ctx, t, pg, fixture.JobID)
	if state.Status != jobsmodel.JobStatusRunning.ToString() {
		t.Fatalf("job status = %q, want %q: cancellation must be rejected for a stolen lease", state.Status, jobsmodel.JobStatusRunning.ToString())
	}
	if !state.LeaseToken.Valid || state.LeaseToken.String != thiefToken {
		t.Fatalf("lease_token = %v, want the new owner's %q", state.LeaseToken, thiefToken)
	}
	if state.TerminalReasonCode.Valid {
		t.Fatalf("terminal_reason_code = %q, want none: the previous lease holder cannot cancel", state.TerminalReasonCode.String)
	}
	if state.CompletedAt.Valid {
		t.Fatal("completed_at was written by a worker that no longer held the lease")
	}

	if got := repo.handoffs.size(); got != 1 {
		t.Fatalf("handoff permits = %d, want 1: the rejected cancellation still needs reconciliation", got)
	}
}

// TestIntegrationClaimedRunAutomaticFollowUpRespectsWorkflowGeneration proves the
// follow-up schedule is committed for the workflow owner at the generation the job
// was dispatched for, and that a generation the user has since superseded produces
// no new job row at all. Scheduling a stale generation would resurrect a schedule
// the user already replaced.
func TestIntegrationClaimedRunAutomaticFollowUpRespectsWorkflowGeneration(t *testing.T) {
	ctx := t.Context()
	pg := testkit.Postgres(t)
	jobs, workflows := newDurableRepositories(pg)

	tests := []struct {
		name              string
		claimedGeneration int64
		superseded        bool
		wantFollowUps     int
	}{
		{name: "current generation schedules the next occurrence", claimedGeneration: 4, superseded: false, wantFollowUps: 2},
		{name: "superseded generation schedules nothing", claimedGeneration: 4, superseded: true, wantFollowUps: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := seedDurableClaim(ctx, t, pg, jobs, workflowsmodel.WorkflowBuildStatusCompleted.ToString(), tt.claimedGeneration)
			heartbeat := &recordingHeartbeatSvc{}
			repo := newDurableExecutor(fixture, jobs, workflows, heartbeat, &rejectingContainerSvc{})

			if tt.superseded {
				if _, err := pg.Exec(ctx, `UPDATE workflows SET generation = generation + 1 WHERE id = $1`, fixture.WorkflowID); err != nil {
					t.Fatalf("supersede workflow generation: %v", err)
				}
			}

			if err := repo.runClaimedWorkflow(ctx, fixture.claim(), fixture.LastScheduledAt, tt.claimedGeneration); err != nil {
				t.Fatalf("runClaimedWorkflow() error = %v, want nil once the job settled", err)
			}

			state := readDurableJobState(ctx, t, pg, fixture.JobID)
			if state.Status != jobsmodel.JobStatusCompleted.ToString() {
				t.Fatalf("job status = %q, want %q: the dispatched run still has to finish", state.Status, jobsmodel.JobStatusCompleted.ToString())
			}
			if got := heartbeat.calls.Load(); got != 1 {
				t.Fatalf("heartbeat executions = %d, want 1", got)
			}
			if got := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); got != tt.wantFollowUps {
				t.Fatalf("workflow job rows = %d, want %d", got, tt.wantFollowUps)
			}
			if tt.wantFollowUps == 1 {
				if got := repo.handoffs.size(); got != 0 {
					t.Fatalf("handoff permits = %d, want 0 once completion was committed", got)
				}
				return
			}

			assertDurableFollowUp(ctx, t, pg, fixture)
		})
	}
}

// assertDurableFollowUp proves the committed follow-up belongs to the workflow
// owner, carries the claimed generation, and is scheduled exactly one interval
// after the occurrence that was dispatched.
func assertDurableFollowUp(ctx context.Context, t *testing.T, pg *postgres.Postgres, fixture *durableFixture) {
	t.Helper()

	var (
		userID       string
		trigger      string
		generation   int64
		scheduledAt  time.Time
		status       string
		observedJobs int
	)
	if err := pg.QueryRow(ctx, `
		SELECT user_id, trigger, workflow_generation, scheduled_at, status, count(*) OVER ()
		FROM jobs
		WHERE workflow_id = $1 AND trigger = 'AUTOMATIC'
		ORDER BY scheduled_at DESC
		LIMIT 1
	`, fixture.WorkflowID).Scan(&userID, &trigger, &generation, &scheduledAt, &status, &observedJobs); err != nil {
		t.Fatalf("read durable follow-up job: %v", err)
	}

	if userID != fixture.UserID {
		t.Fatalf("follow-up user_id = %q, want the workflow owner %q", userID, fixture.UserID)
	}
	if trigger != jobsmodel.JobTriggerAutomatic.ToString() {
		t.Fatalf("follow-up trigger = %q, want %q", trigger, jobsmodel.JobTriggerAutomatic.ToString())
	}
	if generation != fixture.Generation {
		t.Fatalf("follow-up workflow_generation = %d, want the claimed generation %d", generation, fixture.Generation)
	}
	if status != jobsmodel.JobStatusPending.ToString() {
		t.Fatalf("follow-up status = %q, want %q", status, jobsmodel.JobStatusPending.ToString())
	}
	if want := fixture.LastScheduledAt.Add(time.Duration(fixture.Interval) * time.Minute); !scheduledAt.UTC().Equal(want) {
		t.Fatalf("follow-up scheduled_at = %s, want %s", scheduledAt.UTC(), want)
	}
	if observedJobs != 2 {
		t.Fatalf("automatic job rows = %d, want 2 (the dispatched run plus its follow-up)", observedJobs)
	}
}
