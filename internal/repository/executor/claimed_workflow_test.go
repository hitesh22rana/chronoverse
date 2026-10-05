//nolint:testpackage // Behavioral tests drive the unexported claimed-run state machine.
package executor

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	containerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

const (
	// claimedJobID, claimedWorkflowID and claimedLeaseToken are the durable
	// identities every command for one claim must carry. Ownership assertions
	// compare against them, so a command built from any other job or lease fails.
	claimedJobID      = "job-1"
	claimedWorkflowID = "workflow-1"
	claimedLeaseToken = "lease-1"
	// claimedRunBound bounds every wait for the executor's own goroutines, so a
	// broken cancellation path fails the test instead of hanging the suite.
	claimedRunBound = 10 * time.Second
	// claimedHeartbeatPayload is a valid HEARTBEAT workflow body; it routes
	// execution to the heartbeat seam instead of a container runtime.
	claimedHeartbeatPayload = `{"endpoint":"https://example.test/health","timeout":"5s"}`
	// claimedContainerPayload is a valid CONTAINER workflow body.
	claimedContainerPayload = `{"image":"alpine:3.22","cmd":["echo","ok"],"timeout":"5s"}`
)

// jobCommand names one jobs-service command the executor can issue.
type jobCommand string

const (
	commandRenewLease   jobCommand = "RenewJobLease"
	commandScheduleJob  jobCommand = "ScheduleJob"
	commandReleaseRetry jobCommand = "ReleaseJobForRetry"
	commandCancelClaim  jobCommand = "CancelClaimedJob"
	commandFailJob      jobCommand = "FailJob"
	commandCompleteJob  jobCommand = "CompleteJob"
	commandAttach       jobCommand = "AttachJobContainer"
)

// recordedCommand is one jobs-service command reduced to the fields that carry
// ownership, scheduling and settlement decisions. Assertions read these fields
// instead of the executor's internal call sequence, so an equivalent
// reimplementation still passes.
type recordedCommand struct {
	Op                   jobCommand
	JobID                string
	WorkflowID           string
	UserID               string
	Trigger              string
	LeaseToken           string
	CommandID            string
	ScheduledAt          string
	NextAttemptAt        string
	WorkflowGeneration   int64
	LeaseDurationSeconds int32
	ErrorCode            string
	ErrorMessage         string
	FailureKind          string
	TerminalReasonCode   string
}

// settleOp reports whether an operation settles a claim. Renewal, scheduling and
// container attachment are progress, not settlement.
func settleOp(op jobCommand) bool {
	switch op {
	case commandCompleteJob, commandReleaseRetry, commandCancelClaim, commandFailJob:
		return true
	case commandRenewLease, commandScheduleJob, commandAttach:
		return false
	default:
		panic(fmt.Sprintf("%q is unclassified by settleOp", op))
	}
}

// leaseScoped reports whether a command acts on the claimed job's lease.
// ScheduleJob creates a new job for the workflow, so it is deliberately not scoped
// to the claim and carries neither a job id nor a lease token.
func leaseScoped(op jobCommand) bool {
	switch op {
	case commandRenewLease, commandAttach, commandReleaseRetry, commandCancelClaim, commandFailJob, commandCompleteJob:
		return true
	case commandScheduleJob:
		return false
	default:
		panic(fmt.Sprintf("%q is unclassified by leaseScoped", op))
	}
}

// commandScoped reports whether a command must be replay safe through a command
// id. RenewJobLease extends a lease in place and ScheduleJob deduplicates on the
// scheduling instant, so neither carries one.
func commandScoped(op jobCommand) bool {
	switch op {
	case commandAttach, commandReleaseRetry, commandCancelClaim, commandFailJob, commandCompleteJob:
		return true
	case commandRenewLease, commandScheduleJob:
		return false
	default:
		panic(fmt.Sprintf("%q is unclassified by commandScoped", op))
	}
}

// jobsCallLog records the jobs-service commands one claimed run issued. It is
// mutex guarded because the executor's lease-renewal loop writes to it from its
// own goroutine while the test reads it. Commands are held by pointer so the
// executor's own goroutines never hand a test a torn copy.
type jobsCallLog struct {
	mu       sync.Mutex
	commands []*recordedCommand
}

func (l *jobsCallLog) add(command *recordedCommand) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.commands = append(l.commands, command)
}

func (l *jobsCallLog) snapshot() []*recordedCommand {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]*recordedCommand(nil), l.commands...)
}

// settleOps returns the commands that settled a claim, in issue order.
func settleOps(commands []*recordedCommand) []jobCommand {
	ops := make([]jobCommand, 0, len(commands))
	for _, command := range commands {
		if settleOp(command.Op) {
			ops = append(ops, command.Op)
		}
	}

	return ops
}

func countOp(commands []*recordedCommand, op jobCommand) int {
	count := 0
	for _, command := range commands {
		if command.Op == op {
			count++
		}
	}

	return count
}

func firstOp(commands []*recordedCommand, op jobCommand) (*recordedCommand, bool) {
	for _, command := range commands {
		if command.Op == op {
			return command, true
		}
	}

	return nil, false
}

// scriptedJobsClient is a jobs-service seam that records every command and
// leaves each outcome to the test. Nil hooks succeed, so a test only has to
// describe the failure it exercises. Every hook receives the caller's context so
// a test can observe the executor's cancellation directly.
type scriptedJobsClient struct {
	jobspb.JobsServiceClient

	log *jobsCallLog

	renewLease         func(context.Context, *jobspb.RenewJobLeaseRequest) error
	scheduleJob        func(context.Context, *jobspb.ScheduleJobRequest) error
	releaseJobForRetry func(context.Context, *jobspb.ReleaseJobForRetryRequest) error
	cancelClaimedJob   func(context.Context, *jobspb.CancelClaimedJobRequest) error
	failJob            func(context.Context, *jobspb.FailJobRequest) error
	completeJob        func(context.Context, *jobspb.CompleteJobRequest) error
	attachJobContainer func(context.Context, *jobspb.AttachJobContainerRequest) error
}

func (c *scriptedJobsClient) RenewJobLease(ctx context.Context, req *jobspb.RenewJobLeaseRequest, _ ...grpc.CallOption) (*jobspb.RenewJobLeaseResponse, error) {
	c.log.add(&recordedCommand{
		Op:                   commandRenewLease,
		JobID:                req.GetId(),
		LeaseToken:           req.GetLeaseToken(),
		LeaseDurationSeconds: req.GetLeaseDurationSeconds(),
	})
	if c.renewLease != nil {
		if err := c.renewLease(ctx, req); err != nil {
			return nil, err
		}
	}

	return &jobspb.RenewJobLeaseResponse{}, nil
}

func (c *scriptedJobsClient) ScheduleJob(ctx context.Context, req *jobspb.ScheduleJobRequest, _ ...grpc.CallOption) (*jobspb.ScheduleJobResponse, error) {
	c.log.add(&recordedCommand{
		Op:                 commandScheduleJob,
		WorkflowID:         req.GetWorkflowId(),
		UserID:             req.GetUserId(),
		Trigger:            req.GetTrigger(),
		ScheduledAt:        req.GetScheduledAt(),
		WorkflowGeneration: req.GetWorkflowGeneration(),
	})
	if c.scheduleJob != nil {
		if err := c.scheduleJob(ctx, req); err != nil {
			return nil, err
		}
	}

	return &jobspb.ScheduleJobResponse{Id: "scheduled-follow-up"}, nil
}

func (c *scriptedJobsClient) ReleaseJobForRetry(ctx context.Context, req *jobspb.ReleaseJobForRetryRequest, _ ...grpc.CallOption) (*jobspb.ReleaseJobForRetryResponse, error) {
	c.log.add(&recordedCommand{
		Op:            commandReleaseRetry,
		JobID:         req.GetId(),
		LeaseToken:    req.GetLeaseToken(),
		CommandID:     req.GetCommandId(),
		NextAttemptAt: req.GetNextAttemptAt(),
		ErrorCode:     req.GetErrorCode(),
		ErrorMessage:  req.GetErrorMessage(),
	})
	if c.releaseJobForRetry != nil {
		if err := c.releaseJobForRetry(ctx, req); err != nil {
			return nil, err
		}
	}

	return &jobspb.ReleaseJobForRetryResponse{}, nil
}

func (c *scriptedJobsClient) CancelClaimedJob(ctx context.Context, req *jobspb.CancelClaimedJobRequest, _ ...grpc.CallOption) (*jobspb.CancelClaimedJobResponse, error) {
	c.log.add(&recordedCommand{
		Op:                 commandCancelClaim,
		JobID:              req.GetId(),
		LeaseToken:         req.GetLeaseToken(),
		CommandID:          req.GetCommandId(),
		TerminalReasonCode: req.GetTerminalReasonCode(),
	})
	if c.cancelClaimedJob != nil {
		if err := c.cancelClaimedJob(ctx, req); err != nil {
			return nil, err
		}
	}

	return &jobspb.CancelClaimedJobResponse{}, nil
}

func (c *scriptedJobsClient) FailJob(ctx context.Context, req *jobspb.FailJobRequest, _ ...grpc.CallOption) (*jobspb.FailJobResponse, error) {
	c.log.add(&recordedCommand{
		Op:                 commandFailJob,
		JobID:              req.GetId(),
		LeaseToken:         req.GetLeaseToken(),
		CommandID:          req.GetCommandId(),
		ErrorCode:          req.GetErrorCode(),
		ErrorMessage:       req.GetErrorMessage(),
		FailureKind:        req.GetFailureKind(),
		TerminalReasonCode: req.GetTerminalReasonCode(),
	})
	if c.failJob != nil {
		if err := c.failJob(ctx, req); err != nil {
			return nil, err
		}
	}

	return &jobspb.FailJobResponse{}, nil
}

func (c *scriptedJobsClient) CompleteJob(ctx context.Context, req *jobspb.CompleteJobRequest, _ ...grpc.CallOption) (*jobspb.CompleteJobResponse, error) {
	c.log.add(&recordedCommand{
		Op:         commandCompleteJob,
		JobID:      req.GetId(),
		LeaseToken: req.GetLeaseToken(),
		CommandID:  req.GetCommandId(),
	})
	if c.completeJob != nil {
		if err := c.completeJob(ctx, req); err != nil {
			return nil, err
		}
	}

	return &jobspb.CompleteJobResponse{}, nil
}

func (c *scriptedJobsClient) AttachJobContainer(ctx context.Context, req *jobspb.AttachJobContainerRequest, _ ...grpc.CallOption) (*jobspb.AttachJobContainerResponse, error) {
	c.log.add(&recordedCommand{
		Op:         commandAttach,
		JobID:      req.GetId(),
		LeaseToken: req.GetLeaseToken(),
		CommandID:  req.GetCommandId(),
	})
	if c.attachJobContainer != nil {
		if err := c.attachJobContainer(ctx, req); err != nil {
			return nil, err
		}
	}

	return &jobspb.AttachJobContainerResponse{}, nil
}

// workflowLookupLog counts the workflow reads one claimed run performed, so a
// test can prove the executor never reached the workflow or the execution seam
// instead of merely failing later.
type workflowLookupLog struct {
	mu      sync.Mutex
	lookups []string
}

func (l *workflowLookupLog) record(workflowID string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.lookups = append(l.lookups, workflowID)
}

func (l *workflowLookupLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.lookups)
}

// scriptedWorkflowsClient serves one workflow snapshot and records every read.
type scriptedWorkflowsClient struct {
	workflowspb.WorkflowsServiceClient

	log     *workflowLookupLog
	flow    *workflowspb.GetWorkflowByIDResponse
	failure error
}

func (c *scriptedWorkflowsClient) GetWorkflowByID(_ context.Context, req *workflowspb.GetWorkflowByIDRequest, _ ...grpc.CallOption) (*workflowspb.GetWorkflowByIDResponse, error) {
	c.log.record(req.GetId())
	if c.failure != nil {
		return nil, c.failure
	}

	return c.flow, nil
}

// failingTokenAuth refuses to mint the internal service token, which is what a
// misconfigured or expired signing key looks like to the worker.
type failingTokenAuth struct{}

func (failingTokenAuth) IssueToken(context.Context, string, ...string) (string, error) {
	return "", status.Error(codes.Unavailable, "auth signing key is unavailable")
}

func (failingTokenAuth) ValidateToken(context.Context, string) (context.Context, *jwt.Token, error) {
	return context.Background(), &jwt.Token{}, nil
}

// blockingHeartbeatSvc holds a heartbeat execution open until its context ends,
// exactly like a real endpoint call. It reports the cancellation it observed so a
// test can prove lease loss stops in-flight work.
type blockingHeartbeatSvc struct {
	started   chan struct{}
	stopped   chan error
	startOnce sync.Once
}

// newBlockingHeartbeatSvc returns the heartbeat seam plus the channels a test
// uses to observe that execution really started and really ended.
func newBlockingHeartbeatSvc() *blockingHeartbeatSvc {
	return &blockingHeartbeatSvc{
		started: make(chan struct{}),
		stopped: make(chan error, 1),
	}
}

func (s *blockingHeartbeatSvc) Execute(ctx context.Context, _ time.Duration, _ string, _ int, _ map[string][]string) error {
	s.startOnce.Do(func() { close(s.started) })
	<-ctx.Done()
	err := status.Errorf(codes.Canceled, "heartbeat execution canceled: %v", ctx.Err())
	s.stopped <- err

	return err
}

// observedContainerSvc runs a workload to an immediate clean exit and records
// every build, execution and removal so a test can prove a container was never
// started for a claim that must not execute.
type observedContainerSvc struct {
	mu       sync.Mutex
	executes []string
	removes  []string
}

func (s *observedContainerSvc) Build(context.Context, string) error {
	return nil
}

func (*observedContainerSvc) ImageExists(context.Context, string) (bool, error) {
	return true, nil
}

func (*observedContainerSvc) DockerHost() string {
	return "tcp://docker-proxy:2375"
}

func (s *observedContainerSvc) Execute(
	_ context.Context,
	_ time.Duration,
	image string,
	_, _ []string,
) (containerID string, logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	s.mu.Lock()
	s.executes = append(s.executes, image)
	s.mu.Unlock()

	// The workload finishes immediately, so both channels are already drained when
	// the executor starts reading them.
	logCh := make(chan *jobsmodel.JobLog)
	errCh := make(chan error)
	close(logCh)
	close(errCh)

	return "container-1", logCh, errCh, nil
}

func (*observedContainerSvc) Logs(
	context.Context,
	string,
) (logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	return nil, nil, nil
}

func (*observedContainerSvc) Inspect(context.Context, string) (*containerpkg.State, error) {
	return &containerpkg.State{}, nil
}

func (s *observedContainerSvc) Remove(_ context.Context, containerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.removes = append(s.removes, containerID)
	return nil
}

func (*observedContainerSvc) Terminate(context.Context, string) error {
	return nil
}

func (s *observedContainerSvc) executionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.executes)
}

func (s *observedContainerSvc) removals() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.removes...)
}

// failingContainerSvc runs a workload that exits non-zero and records every
// execution and removal, including the removals that fail. executeErr replaces
// the non-zero exit with a bare gRPC code, which is what separates a user mistake
// from a retryable runtime fault.
type failingContainerSvc struct {
	removeErr  error
	executeErr error

	mu       sync.Mutex
	executes int
	removes  []string
}

func (*failingContainerSvc) Build(context.Context, string) error {
	return nil
}

func (*failingContainerSvc) ImageExists(context.Context, string) (bool, error) {
	return true, nil
}

func (*failingContainerSvc) DockerHost() string {
	return "tcp://docker-proxy:2375"
}

func (s *failingContainerSvc) Execute(
	context.Context,
	time.Duration,
	string,
	[]string,
	[]string,
) (containerID string, logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	s.mu.Lock()
	s.executes++
	s.mu.Unlock()

	logCh := make(chan *jobsmodel.JobLog)
	errCh := make(chan error, 1)
	close(logCh)
	failure := s.executeErr
	if failure == nil {
		failure = terminalreason.Wrap(
			terminalreason.NonZeroExit,
			status.Error(codes.Aborted, "container exited with non-zero code: 1"),
		)
	}
	errCh <- failure
	close(errCh)

	return "container-1", logCh, errCh, nil
}

func (*failingContainerSvc) Logs(
	context.Context,
	string,
) (logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	return nil, nil, nil
}

func (*failingContainerSvc) Inspect(context.Context, string) (*containerpkg.State, error) {
	return &containerpkg.State{}, nil
}

func (s *failingContainerSvc) Remove(_ context.Context, containerID string) error {
	s.mu.Lock()
	s.removes = append(s.removes, containerID)
	s.mu.Unlock()

	return s.removeErr
}

func (*failingContainerSvc) Terminate(context.Context, string) error {
	return nil
}

func (s *failingContainerSvc) executionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.executes
}

func (s *failingContainerSvc) removals() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.removes...)
}

// containerObserver is the surface a test asserts on after a run: how many
// workloads were started, and which containers the executor removed.
type containerObserver interface {
	executionCount() int
	removals() []string
}

// claimedRunFixture bundles the seams one claimed-run test drives.
type claimedRunFixture struct {
	repo      *Repository
	log       *jobsCallLog
	lookups   *workflowLookupLog
	container containerObserver
}

// newClaimedRunFixture builds a Repository whose only live collaborators are the
// recording jobs client, the scripted workflow read and the supplied execution
// seams. The handoff registry starts with one activated permit for this claim, so
// tests can observe exactly when the worker stops tracking a settled job.
func newClaimedRunFixture(
	jobs *scriptedJobsClient,
	workflows *scriptedWorkflowsClient,
	heartbeat *blockingHeartbeatSvc,
	container ContainerSvc,
) *claimedRunFixture {
	return newClaimedRunFixtureWithAuth(jobs, workflows, heartbeat, container, fakeAuth{})
}

// newClaimedRunFixtureWithAuth is the fixture constructor for the cases that must
// drive a non-default authorization seam.
func newClaimedRunFixtureWithAuth(
	jobs *scriptedJobsClient,
	workflows *scriptedWorkflowsClient,
	heartbeat *blockingHeartbeatSvc,
	container ContainerSvc,
	auth auth.IAuth,
) *claimedRunFixture {
	log := &jobsCallLog{}
	lookups := &workflowLookupLog{}
	jobs.log = log
	workflows.log = lookups

	gate := newHandoffRegistry(1)
	entry, owner, err := gate.getOrReserve("claim-command", &jobspb.ClaimJobRequest{Id: claimedJobID, WorkflowId: claimedWorkflowID})
	if err != nil || !owner {
		panic("claimed run fixture could not reserve a handoff permit")
	}
	if !gate.activate(entry, &jobspb.ClaimJobResponse{Claimed: true, Id: claimedJobID, LeaseToken: claimedLeaseToken}) {
		panic("claimed run fixture could not activate a handoff permit")
	}

	var observed containerObserver
	if container == nil {
		recorder := &observedContainerSvc{}
		container = recorder
		observed = recorder
	} else {
		recorder, ok := container.(containerObserver)
		if !ok {
			panic("claimed run fixture container must report executions and removals")
		}
		observed = recorder
	}

	var hsvc HeartBeatSvc
	if heartbeat != nil {
		hsvc = heartbeat
	}

	repo := &Repository{
		tp:       otel.Tracer("executor-claimed-run-test"),
		cfg:      claimedRunConfig(),
		auth:     auth,
		handoffs: gate,
		svc: &Services{
			Jobs:      jobs,
			Workflows: workflows,
			Hsvc:      hsvc,
			CsvcForEndpoint: func(nodeID, endpoint string) (ContainerSvc, error) {
				if endpoint == "" {
					return nil, status.Errorf(codes.Unavailable, "no runtime endpoint for node %q", nodeID)
				}
				return container, nil
			},
		},
	}

	return &claimedRunFixture{repo: repo, log: log, lookups: lookups, container: observed}
}

// claimedRunConfig keeps lease renewals fast enough to observe inside a test
// while leaving enough retry budget that a single execution failure is released
// instead of failed.
func claimedRunConfig() Config {
	return Config{
		WorkerID:            "worker-1",
		Concurrency:         1,
		LeaseDuration:       time.Minute,
		LeaseRenewInterval:  5 * time.Millisecond,
		SystemRetryLimit:    3,
		SystemRetryBackoff:  time.Minute,
		JobLogBatchSize:     10,
		JobLogBatchInterval: time.Hour,
	}
}

// claimedJob is the claim snapshot a worker received from ClaimJob.
func claimedJob(attempts int32) *jobspb.ClaimJobResponse {
	return &jobspb.ClaimJobResponse{
		Claimed:          true,
		Id:               claimedJobID,
		WorkflowId:       claimedWorkflowID,
		UserId:           "user-1",
		Trigger:          jobsmodel.JobTriggerAutomatic.ToString(),
		Attempts:         attempts,
		LeaseToken:       claimedLeaseToken,
		RuntimeNodeId:    "runtime-1",
		RuntimeEndpoint:  "tcp://docker-proxy:2375",
		LeaseExpiresAt:   time.Now().Add(time.Minute).Format(time.RFC3339Nano),
		ScheduledAt:      time.Now().Format(time.RFC3339Nano),
		DispatchAttempts: 1,
	}
}

// heartbeatWorkflow is a ready HEARTBEAT workflow snapshot.
func heartbeatWorkflow() *workflowspb.GetWorkflowByIDResponse {
	return &workflowspb.GetWorkflowByIDResponse{
		Id:           claimedWorkflowID,
		UserId:       "user-1",
		Kind:         workflowsmodel.KindHeartbeat.ToString(),
		BuildStatus:  workflowsmodel.WorkflowBuildStatusCompleted.ToString(),
		Interval:     5,
		Payload:      claimedHeartbeatPayload,
		LogRetention: false,
	}
}

// containerWorkflow is a ready CONTAINER workflow snapshot.
func containerWorkflow() *workflowspb.GetWorkflowByIDResponse {
	return &workflowspb.GetWorkflowByIDResponse{
		Id:                  claimedWorkflowID,
		UserId:              "user-1",
		Kind:                workflowsmodel.KindContainer.ToString(),
		BuildStatus:         workflowsmodel.WorkflowBuildStatusCompleted.ToString(),
		Interval:            5,
		Payload:             claimedContainerPayload,
		LogRetention:        false,
		ResolvedImageDigest: "alpine@sha256:abc",
	}
}

// assertSingleSettlement fails unless the run issued exactly the given claim
// settlement, which is the durable decision a claimed run must make exactly once.
func assertSingleSettlement(t *testing.T, log *jobsCallLog, want jobCommand) {
	t.Helper()

	ops := settleOps(log.snapshot())
	if len(ops) != 1 || ops[0] != want {
		t.Fatalf("claim settlements = %v, want exactly [%s]", ops, want)
	}
}

// assertNoOwnershipStealing fails when any command the run issued carried a job
// id or lease token other than the ones the claim granted it, or when a
// state-changing command was not made replay safe.
func assertNoOwnershipStealing(t *testing.T, log *jobsCallLog) {
	t.Helper()

	for _, command := range log.snapshot() {
		if commandScoped(command.Op) && command.CommandID == "" {
			t.Fatalf("%s carried no command id, want a replay-safe command", command.Op)
		}
		if !leaseScoped(command.Op) {
			continue
		}
		if command.LeaseToken != claimedLeaseToken {
			t.Fatalf("%s used lease token %q, want the claimed token %q", command.Op, command.LeaseToken, claimedLeaseToken)
		}
		if command.JobID != claimedJobID {
			t.Fatalf("%s used job id %q, want the claimed job %q", command.Op, command.JobID, claimedJobID)
		}
	}
}

// TestRunClaimedWorkflowStopsBeforeWorkflowReadWhenLeaseRenewalFails proves the
// executor proves lease authority before it reads the workflow or touches any
// execution seam. The mandatory first renewal is what distinguishes a job this
// worker still owns from one another worker already took over, so a rejected
// renewal must stop the run before any workload or follow-up schedule exists.
func TestRunClaimedWorkflowStopsBeforeWorkflowReadWhenLeaseRenewalFails(t *testing.T) {
	t.Parallel()

	renewed := make(chan struct{}, 1)
	jobs := &scriptedJobsClient{
		renewLease: func(context.Context, *jobspb.RenewJobLeaseRequest) error {
			select {
			case renewed <- struct{}{}:
			default:
			}
			return status.Error(codes.FailedPrecondition, "lease is no longer held")
		},
	}
	fixture := newClaimedRunFixture(jobs, &scriptedWorkflowsClient{flow: containerWorkflow()}, nil, nil)

	err := fixture.repo.runClaimedWorkflow(t.Context(), claimedJob(1), time.Now(), 1)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("runClaimedWorkflow() code = %s, want %s: %v", status.Code(err), codes.FailedPrecondition, err)
	}

	select {
	case <-renewed:
	case <-time.After(claimedRunBound):
		t.Fatal("timed out waiting for the mandatory lease renewal")
	}

	commands := fixture.log.snapshot()
	if got := fixture.lookups.count(); got != 0 {
		t.Fatalf("workflow reads = %d, want 0 after a failed lease renewal", got)
	}
	if got := settleOps(commands); len(got) != 0 {
		t.Fatalf("claim settlements = %v, want none: the lost lease belongs to another worker", got)
	}
	if got := countOp(commands, commandScheduleJob); got != 0 {
		t.Fatalf("follow-up schedules = %d, want 0 after a failed lease renewal", got)
	}
	if got := fixture.container.executionCount(); got != 0 {
		t.Fatalf("container executions = %d, want 0 after a failed lease renewal", got)
	}
	assertNoOwnershipStealing(t, fixture.log)

	if got := fixture.repo.handoffs.size(); got != 1 {
		t.Fatalf("handoff permits = %d, want 1: reconciliation must still observe the ambiguous lease", got)
	}
}

// TestRunClaimedWorkflowCancelsExecutionWhenLeaseRenewalFailsMidRun proves a
// lease lost mid-run stops in-flight work instead of letting it finish and settle
// the claim. The heartbeat seam blocks until the executor's own context ends, so
// the assertion is about real cancellation rather than call ordering.
func TestRunClaimedWorkflowCancelsExecutionWhenLeaseRenewalFailsMidRun(t *testing.T) {
	t.Parallel()

	heartbeat := newBlockingHeartbeatSvc()
	jobs := &scriptedJobsClient{renewLease: loseLeaseAfterFirstRenewal(heartbeat)}
	fixture := newClaimedRunFixture(jobs, &scriptedWorkflowsClient{flow: heartbeatWorkflow()}, heartbeat, nil)

	claim := claimedJob(1)
	claim.Trigger = jobsmodel.JobTriggerManual.ToString()

	// The run gets its own deadline so a cancellation regression fails this test
	// with a precise cause instead of stalling until the suite timeout. The
	// deadline check below proves the run ended on its own: without it the
	// deadline alone would cancel the heartbeat and make a broken executor look
	// correct.
	runCtx, cancelRun := context.WithTimeout(t.Context(), claimedRunBound)
	defer cancelRun()

	if err := fixture.repo.runClaimedWorkflow(runCtx, claim, time.Now(), 0); err != nil {
		t.Fatalf("runClaimedWorkflow() error = %v, want nil once the claim is released", err)
	}
	if err := runCtx.Err(); err != nil {
		t.Fatalf("run context ended with %v, want the renewal failure alone to stop the run", err)
	}

	assertHeartbeatStartedAndCanceled(t, heartbeat)
	assertSingleSettlement(t, fixture.log, commandReleaseRetry)
	commands := fixture.log.snapshot()
	release, ok := firstOp(commands, commandReleaseRetry)
	if !ok {
		t.Fatal("no ReleaseJobForRetry command was recorded")
	}
	if got := release.ErrorCode; got != codes.Canceled.String() {
		t.Fatalf("release error code = %q, want %q so recovery treats it as a system fault", got, codes.Canceled.String())
	}
	if got := release.ErrorMessage; got == "" {
		t.Fatal("release carried no error message, want the lease-loss cause")
	}
	assertFutureBackoff(t, release.NextAttemptAt)
	if got := countOp(commands, commandCompleteJob); got != 0 {
		t.Fatalf("completions = %d, want 0: a lost lease must never report success", got)
	}
	assertNoOwnershipStealing(t, fixture.log)

	if got := fixture.repo.handoffs.size(); got != 0 {
		t.Fatalf("handoff permits = %d, want 0 once the jobs service accepted the release", got)
	}
}

// loseLeaseAfterFirstRenewal builds a renewal hook that proves the lease once and
// then loses it, but only once a workload is in flight. Waiting for the workload
// before failing is what makes the mid-run cancellation deterministic instead of a
// race against the renewal ticker.
func loseLeaseAfterFirstRenewal(heartbeat *blockingHeartbeatSvc) func(context.Context, *jobspb.RenewJobLeaseRequest) error {
	var renewals atomic.Int32

	return func(ctx context.Context, _ *jobspb.RenewJobLeaseRequest) error {
		if renewals.Add(1) == 1 {
			return nil
		}
		select {
		case <-heartbeat.started:
		case <-ctx.Done():
		}

		return status.Error(codes.FailedPrecondition, "lease is no longer held")
	}
}

// assertHeartbeatStartedAndCanceled proves the execution seam both ran and was
// stopped by the executor's own cancellation, without waiting: after runClaimedWorkflow
// returns, the channels already carry the outcome.
func assertHeartbeatStartedAndCanceled(t *testing.T, heartbeat *blockingHeartbeatSvc) {
	t.Helper()

	select {
	case <-heartbeat.started:
	default:
		t.Fatal("heartbeat execution never started, so the test proved nothing")
	}
	select {
	case stopped := <-heartbeat.stopped:
		if status.Code(stopped) != codes.Canceled {
			t.Fatalf("heartbeat cancellation = %s, want %s: %v", status.Code(stopped), codes.Canceled, stopped)
		}
	default:
		t.Fatal("a failed lease renewal did not stop the in-flight execution")
	}
}

// assertFutureBackoff proves a release scheduled a bounded, RFC3339Nano retry
// instant in the future, so a released job cannot spin.
func assertFutureBackoff(t *testing.T, nextAttemptAt string) {
	t.Helper()

	if nextAttemptAt == "" {
		t.Fatal("release carried no retry instant, want a bounded backoff")
	}
	nextAttempt, err := time.Parse(time.RFC3339Nano, nextAttemptAt)
	if err != nil {
		t.Fatalf("release retry instant %q is not RFC3339Nano: %v", nextAttemptAt, err)
	}
	if !nextAttempt.After(time.Now()) {
		t.Fatalf("release retry instant = %s, want a future backoff", nextAttempt)
	}
}

// TestRunClaimedWorkflowSeparatesUserTerminationFromTransientReadiness proves
// the three readiness outcomes stay distinct. A terminated workflow is a user
// decision that cancels the job outright, while an unreadable or unbuilt workflow
// is transient and must release the claim so another attempt can still succeed.
func TestRunClaimedWorkflowSeparatesUserTerminationFromTransientReadiness(t *testing.T) {
	t.Parallel()

	terminated := func() *scriptedWorkflowsClient {
		flow := containerWorkflow()
		flow.TerminatedAt = time.Now().Format(time.RFC3339Nano)
		return &scriptedWorkflowsClient{flow: flow}
	}
	building := func() *scriptedWorkflowsClient {
		flow := containerWorkflow()
		flow.BuildStatus = workflowsmodel.WorkflowBuildStatusStarted.ToString()
		return &scriptedWorkflowsClient{flow: flow}
	}

	tests := []struct {
		name          string
		workflows     *scriptedWorkflowsClient
		want          jobCommand
		wantErrorCode string
	}{
		{
			name:          "terminated workflow cancels the claimed job",
			workflows:     terminated(),
			want:          commandCancelClaim,
			wantErrorCode: "",
		},
		{
			name:          "unavailable workflow read releases the claim",
			workflows:     &scriptedWorkflowsClient{failure: status.Error(codes.Unavailable, "workflows service is draining")},
			want:          commandReleaseRetry,
			wantErrorCode: codes.Unavailable.String(),
		},
		{
			name:          "missing workflow releases the claim",
			workflows:     &scriptedWorkflowsClient{failure: status.Error(codes.NotFound, "workflow not found")},
			want:          commandReleaseRetry,
			wantErrorCode: codes.NotFound.String(),
		},
		{
			name:          "unfinished build releases the claim",
			workflows:     building(),
			want:          commandReleaseRetry,
			wantErrorCode: codes.FailedPrecondition.String(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fixture := newClaimedRunFixture(&scriptedJobsClient{}, tt.workflows, nil, nil)

			if err := fixture.repo.runClaimedWorkflow(t.Context(), claimedJob(1), time.Now(), 4); err != nil {
				t.Fatalf("runClaimedWorkflow() error = %v, want nil once the claim settled", err)
			}

			assertSingleSettlement(t, fixture.log, tt.want)
			commands := fixture.log.snapshot()
			assertNoOwnershipStealing(t, fixture.log)

			if got := countOp(commands, commandScheduleJob); got != 0 {
				t.Fatalf("follow-up schedules = %d, want 0: a claim that cannot run must not extend the schedule", got)
			}
			if got := fixture.lookups.count(); got != 1 {
				t.Fatalf("workflow reads = %d, want exactly 1", got)
			}
			if got := fixture.container.executionCount(); got != 0 {
				t.Fatalf("container executions = %d, want 0", got)
			}
			if got := fixture.repo.handoffs.size(); got != 0 {
				t.Fatalf("handoff permits = %d, want 0 once the claim settled", got)
			}

			settled, ok := firstOp(commands, tt.want)
			if !ok {
				t.Fatalf("no %s command was recorded", tt.want)
			}
			if tt.want == commandCancelClaim {
				if got := settled.TerminalReasonCode; got != terminalreason.WorkflowTerminated.String() {
					t.Fatalf("cancel terminal reason = %q, want %q", got, terminalreason.WorkflowTerminated.String())
				}
				return
			}
			if got := settled.ErrorCode; got != tt.wantErrorCode {
				t.Fatalf("release error code = %q, want %q", got, tt.wantErrorCode)
			}
			if got := settled.ErrorMessage; got == "" {
				t.Fatal("release carried no error message, want the readiness cause")
			}
		})
	}
}

// TestRunClaimedWorkflowReleasesUnreadyClaimUntilRetryBudgetIsSpent proves an
// unbuilt workflow is retried while budget remains and only becomes a terminal
// system failure once the budget is exhausted. Canceling it instead would silently
// drop a job the user never canceled.
func TestRunClaimedWorkflowReleasesUnreadyClaimUntilRetryBudgetIsSpent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		attempts    int32
		want        jobCommand
		wantErrCode codes.Code
	}{
		{name: "first attempt is released", attempts: 1, want: commandReleaseRetry, wantErrCode: codes.OK},
		{name: "last allowed attempt is released", attempts: 2, want: commandReleaseRetry, wantErrCode: codes.OK},
		{name: "exhausted budget fails terminally", attempts: 3, want: commandFailJob, wantErrCode: codes.FailedPrecondition},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			flow := containerWorkflow()
			flow.BuildStatus = workflowsmodel.WorkflowBuildStatusQueued.ToString()
			fixture := newClaimedRunFixture(&scriptedJobsClient{}, &scriptedWorkflowsClient{flow: flow}, nil, nil)

			// An exhausted budget reports the readiness cause back to the caller;
			// a released claim has nothing left to report.
			err := fixture.repo.runClaimedWorkflow(t.Context(), claimedJob(tt.attempts), time.Now(), 2)
			if status.Code(err) != tt.wantErrCode {
				t.Fatalf("runClaimedWorkflow() code = %s, want %s: %v", status.Code(err), tt.wantErrCode, err)
			}

			assertSingleSettlement(t, fixture.log, tt.want)
			commands := fixture.log.snapshot()
			if got := countOp(commands, commandScheduleJob); got != 0 {
				t.Fatalf("follow-up schedules = %d, want 0 while the workflow is unbuilt", got)
			}
			if got := fixture.container.executionCount(); got != 0 {
				t.Fatalf("container executions = %d, want 0 while the workflow is unbuilt", got)
			}
			if got := fixture.container.removals(); len(got) != 0 {
				t.Fatalf("container removals = %v, want none: no container was created", got)
			}
			assertNoOwnershipStealing(t, fixture.log)

			settled, ok := firstOp(commands, tt.want)
			if !ok {
				t.Fatalf("no %s command was recorded", tt.want)
			}
			if got := settled.ErrorMessage; got == "" {
				t.Fatal("settled command carried no error message, want the readiness cause")
			}
		})
	}
}

// TestRunClaimedWorkflowAutomaticScheduleHonorsClaimGeneration proves the
// follow-up schedule carries exactly the generation the job was dispatched for and
// the workflow's own owner. Forwarding any other generation would let a superseded
// worker resurrect a schedule the user already replaced.
func TestRunClaimedWorkflowAutomaticScheduleHonorsClaimGeneration(t *testing.T) {
	t.Parallel()

	const claimGeneration = 7
	lastScheduledAt := time.Now().UTC().Truncate(time.Second)

	flow := containerWorkflow()
	flow.Interval = 5
	flow.UserId = "user-owner"
	fixture := newClaimedRunFixture(&scriptedJobsClient{}, &scriptedWorkflowsClient{flow: flow}, nil, nil)

	if err := fixture.repo.runClaimedWorkflow(t.Context(), claimedJob(1), lastScheduledAt, claimGeneration); err != nil {
		t.Fatalf("runClaimedWorkflow() error = %v, want nil", err)
	}

	commands := fixture.log.snapshot()
	schedule, ok := firstOp(commands, commandScheduleJob)
	if !ok {
		t.Fatal("no follow-up ScheduleJob command was recorded")
	}
	if got := countOp(commands, commandScheduleJob); got != 1 {
		t.Fatalf("follow-up schedules = %d, want exactly 1", got)
	}
	if got := schedule.WorkflowID; got != flow.GetId() {
		t.Fatalf("follow-up workflow id = %q, want %q", got, flow.GetId())
	}
	if got := schedule.UserID; got != flow.GetUserId() {
		t.Fatalf("follow-up user id = %q, want the workflow owner %q", got, flow.GetUserId())
	}
	if got := schedule.Trigger; got != jobsmodel.JobTriggerAutomatic.ToString() {
		t.Fatalf("follow-up trigger = %q, want %q", got, jobsmodel.JobTriggerAutomatic.ToString())
	}
	if got := schedule.WorkflowGeneration; got != claimGeneration {
		t.Fatalf("follow-up workflow generation = %d, want the claimed generation %d", got, claimGeneration)
	}

	scheduledAt, err := time.Parse(time.RFC3339Nano, schedule.ScheduledAt)
	if err != nil {
		t.Fatalf("follow-up scheduled_at %q is not RFC3339Nano: %v", schedule.ScheduledAt, err)
	}
	if want := lastScheduledAt.UTC().Add(time.Duration(flow.GetInterval()) * time.Minute); !scheduledAt.UTC().Equal(want) {
		t.Fatalf("follow-up scheduled_at = %s, want %s (one interval after the dispatched run)", scheduledAt.UTC(), want)
	}

	assertSingleSettlement(t, fixture.log, commandCompleteJob)
	assertNoOwnershipStealing(t, fixture.log)
	if got := fixture.container.executionCount(); got != 1 {
		t.Fatalf("container executions = %d, want 1", got)
	}
}

// TestRunClaimedWorkflowAutomaticScheduleSurvivesSupersededGeneration proves a
// stale-generation rejection is not a run failure: the job the user superseded
// still completes and only the follow-up is dropped. The same rejection without a
// generation guard is a genuine error and must not be swallowed, otherwise the
// schedule would stop silently with no recorded cause.
func TestRunClaimedWorkflowAutomaticScheduleSurvivesSupersededGeneration(t *testing.T) {
	t.Parallel()

	rejected := status.Error(codes.FailedPrecondition, "workflow generation 7 is superseded by 9")
	tests := []struct {
		name       string
		generation int64
		want       jobCommand
	}{
		{name: "superseded generation drops only the follow-up", generation: 7, want: commandCompleteJob},
		{name: "unguarded rejection releases the claim", generation: 0, want: commandReleaseRetry},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			jobs := &scriptedJobsClient{
				scheduleJob: func(context.Context, *jobspb.ScheduleJobRequest) error {
					return rejected
				},
			}
			fixture := newClaimedRunFixture(jobs, &scriptedWorkflowsClient{flow: containerWorkflow()}, nil, nil)

			if err := fixture.repo.runClaimedWorkflow(t.Context(), claimedJob(1), time.Now(), tt.generation); err != nil {
				t.Fatalf("runClaimedWorkflow() error = %v, want nil once the claim settled", err)
			}

			assertSingleSettlement(t, fixture.log, tt.want)
			commands := fixture.log.snapshot()
			assertNoOwnershipStealing(t, fixture.log)

			if tt.want == commandCompleteJob {
				if got := fixture.container.executionCount(); got != 1 {
					t.Fatalf("container executions = %d, want 1: a dropped follow-up must not skip the run", got)
				}
				if got := fixture.container.removals(); len(got) != 1 || got[0] != "container-1" {
					t.Fatalf("container removals = %v, want exactly the executed container", got)
				}
				return
			}

			if got := fixture.container.executionCount(); got != 0 {
				t.Fatalf("container executions = %d, want 0 when the follow-up was rejected", got)
			}
			released, ok := firstOp(commands, commandReleaseRetry)
			if !ok {
				t.Fatal("no ReleaseJobForRetry command was recorded")
			}
			if got := released.ErrorCode; got != codes.FailedPrecondition.String() {
				t.Fatalf("release error code = %q, want %q", got, codes.FailedPrecondition.String())
			}
		})
	}
}

// TestRunClaimedWorkflowRejectsUnknownTriggerWithoutScheduling proves an
// unrecognized trigger is a user error that fails the claim outright. Releasing it
// would retry forever, and completing it would hide the misconfiguration.
func TestRunClaimedWorkflowRejectsUnknownTriggerWithoutScheduling(t *testing.T) {
	t.Parallel()

	fixture := newClaimedRunFixture(&scriptedJobsClient{}, &scriptedWorkflowsClient{flow: containerWorkflow()}, nil, nil)
	claim := claimedJob(1)
	claim.Trigger = "SCHEDULED_BY_CRON"

	err := fixture.repo.runClaimedWorkflow(t.Context(), claim, time.Now(), 3)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("runClaimedWorkflow() code = %s, want %s: %v", status.Code(err), codes.FailedPrecondition, err)
	}

	assertSingleSettlement(t, fixture.log, commandFailJob)
	commands := fixture.log.snapshot()
	failed, ok := firstOp(commands, commandFailJob)
	if !ok {
		t.Fatal("no FailJob command was recorded")
	}
	if got := failed.FailureKind; got != jobsmodel.FailureKindUser.ToString() {
		t.Fatalf("failure kind = %q, want %q", got, jobsmodel.FailureKindUser.ToString())
	}
	if got := failed.TerminalReasonCode; got != terminalreason.ExecutionFailed.String() {
		t.Fatalf("terminal reason = %q, want %q", got, terminalreason.ExecutionFailed.String())
	}
	if got := countOp(commands, commandScheduleJob); got != 0 {
		t.Fatalf("follow-up schedules = %d, want 0 for an unknown trigger", got)
	}
	if got := fixture.container.executionCount(); got != 0 {
		t.Fatalf("container executions = %d, want 0", got)
	}
	assertNoOwnershipStealing(t, fixture.log)
}

// TestRunClaimedWorkflowKeepsReconciliationWhenSettlementIsRejected proves the
// handoff permit survives a rejected settlement. The jobs service still owns the
// ambiguous row, so releasing the permit would let a redelivered record claim it
// again while this worker may still hold runtime resources for it.
func TestRunClaimedWorkflowKeepsReconciliationWhenSettlementIsRejected(t *testing.T) {
	t.Parallel()

	flow := containerWorkflow()
	flow.TerminatedAt = time.Now().Format(time.RFC3339Nano)
	jobs := &scriptedJobsClient{
		cancelClaimedJob: func(context.Context, *jobspb.CancelClaimedJobRequest) error {
			return status.Error(codes.Unavailable, "jobs service is unavailable")
		},
	}
	fixture := newClaimedRunFixture(jobs, &scriptedWorkflowsClient{flow: flow}, nil, nil)

	err := fixture.repo.runClaimedWorkflow(t.Context(), claimedJob(1), time.Now(), 1)
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("runClaimedWorkflow() code = %s, want %s: %v", status.Code(err), codes.Unavailable, err)
	}
	if got := fixture.repo.handoffs.size(); got != 1 {
		t.Fatalf("handoff permits = %d, want 1 after a rejected cancellation", got)
	}
	assertNoOwnershipStealing(t, fixture.log)
}

// TestRunClaimedWorkflowReleasesContainerClaimWithoutRuntimeEndpoint proves a
// claim the jobs service could not hand a runtime endpoint is retried instead of
// executed or failed. Attempting execution without a runtime would strand the
// claim until its lease expired.
func TestRunClaimedWorkflowReleasesContainerClaimWithoutRuntimeEndpoint(t *testing.T) {
	t.Parallel()

	fixture := newClaimedRunFixture(&scriptedJobsClient{}, &scriptedWorkflowsClient{flow: containerWorkflow()}, nil, nil)
	claim := claimedJob(1)
	claim.RuntimeEndpoint = ""

	if err := fixture.repo.runClaimedWorkflow(t.Context(), claim, time.Now(), 1); err != nil {
		t.Fatalf("runClaimedWorkflow() error = %v, want nil once the claim settled", err)
	}

	assertSingleSettlement(t, fixture.log, commandReleaseRetry)
	commands := fixture.log.snapshot()
	released, ok := firstOp(commands, commandReleaseRetry)
	if !ok {
		t.Fatal("no ReleaseJobForRetry command was recorded")
	}
	if got := released.ErrorCode; got != codes.Unavailable.String() {
		t.Fatalf("release error code = %q, want %q", got, codes.Unavailable.String())
	}
	if got := fixture.container.executionCount(); got != 0 {
		t.Fatalf("container executions = %d, want 0 without a runtime endpoint", got)
	}
	if got := fixture.repo.handoffs.size(); got != 0 {
		t.Fatalf("handoff permits = %d, want 0 after the release", got)
	}
	assertNoOwnershipStealing(t, fixture.log)
}

// TestRunClaimedWorkflowFailsContainerExecutionAndCleansUp proves a non-zero
// workload exit is reported as a user failure and that the container is removed
// afterwards, including when the jobs service rejects nothing and the cleanup
// itself fails.
func TestRunClaimedWorkflowFailsContainerExecutionAndCleansUp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		removeErr error
	}{
		{name: "cleanup succeeds", removeErr: nil},
		{name: "cleanup failure does not hide the execution failure", removeErr: status.Error(codes.Unavailable, "docker daemon unreachable")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			container := &failingContainerSvc{removeErr: tt.removeErr}
			fixture := newClaimedRunFixture(&scriptedJobsClient{}, &scriptedWorkflowsClient{flow: containerWorkflow()}, nil, container)

			err := fixture.repo.runClaimedWorkflow(t.Context(), claimedJob(1), time.Now(), 1)
			if status.Code(err) != codes.Aborted {
				t.Fatalf("runClaimedWorkflow() code = %s, want %s: %v", status.Code(err), codes.Aborted, err)
			}

			assertSingleSettlement(t, fixture.log, commandFailJob)
			commands := fixture.log.snapshot()
			failed, ok := firstOp(commands, commandFailJob)
			if !ok {
				t.Fatal("no FailJob command was recorded")
			}
			if got := failed.FailureKind; got != jobsmodel.FailureKindUser.ToString() {
				t.Fatalf("failure kind = %q, want %q", got, jobsmodel.FailureKindUser.ToString())
			}
			if got := failed.TerminalReasonCode; got != terminalreason.NonZeroExit.String() {
				t.Fatalf("terminal reason = %q, want %q", got, terminalreason.NonZeroExit.String())
			}
			if got := container.removals(); len(got) != 1 || got[0] != "container-1" {
				t.Fatalf("container removals = %v, want exactly the executed container", got)
			}
			if got := container.executionCount(); got != 1 {
				t.Fatalf("container executions = %d, want 1", got)
			}
			assertNoOwnershipStealing(t, fixture.log)
		})
	}
}

// TestRunClaimedWorkflowClassifiesRetryableContainerFailure pins both outcomes of
// a runtime fault a workload can retry. Releasing keeps the job alive and must
// remove the container first, so the re-dispatch cannot inherit a container the
// previous run left on the node. Once the budget is spent the same fault becomes a
// terminal system failure rather than a user mistake. Only a bare retryable code
// separates the two: a wrapped terminal reason classifies as a user fault whatever
// the retry budget says.
func TestRunClaimedWorkflowClassifiesRetryableContainerFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		attempts        int32
		wantSettlement  jobCommand
		wantFailureKind string
		wantReasonCode  string
	}{
		{
			name:           "retryable fault with budget left releases for system retry",
			attempts:       1,
			wantSettlement: commandReleaseRetry,
		},
		{
			name:            "retryable fault with the budget spent fails as a system fault",
			attempts:        3,
			wantSettlement:  commandFailJob,
			wantFailureKind: jobsmodel.FailureKindSystem.ToString(),
			wantReasonCode:  terminalreason.SystemError.String(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			container := &failingContainerSvc{executeErr: status.Error(codes.Unavailable, "runtime node lost the container")}
			fixture := newClaimedRunFixture(&scriptedJobsClient{}, &scriptedWorkflowsClient{flow: containerWorkflow()}, nil, container)

			err := fixture.repo.runClaimedWorkflow(t.Context(), claimedJob(tt.attempts), time.Now(), 1)
			if tt.wantSettlement == commandReleaseRetry {
				if err != nil {
					t.Fatalf("runClaimedWorkflow() error = %v, want nil once the claim is released", err)
				}
			} else if status.Code(err) != codes.Unavailable {
				// A terminal failure hands the runtime fault back so the caller can
				// see why the run ended rather than an opaque settlement rejection.
				t.Fatalf("runClaimedWorkflow() code = %s, want %s: %v", status.Code(err), codes.Unavailable, err)
			}

			assertSingleSettlement(t, fixture.log, tt.wantSettlement)
			commands := fixture.log.snapshot()

			settlement, ok := firstOp(commands, tt.wantSettlement)
			if !ok {
				t.Fatalf("no %s command was recorded", tt.wantSettlement)
			}
			if got := settlement.ErrorCode; got != codes.Unavailable.String() {
				t.Fatalf("settlement error code = %q, want %q so recovery sees the runtime fault", got, codes.Unavailable.String())
			}
			if tt.wantSettlement == commandFailJob {
				if got := settlement.FailureKind; got != tt.wantFailureKind {
					t.Fatalf("failure kind = %q, want %q", got, tt.wantFailureKind)
				}
				if got := settlement.TerminalReasonCode; got != tt.wantReasonCode {
					t.Fatalf("terminal reason = %q, want %q", got, tt.wantReasonCode)
				}
			} else {
				assertFutureBackoff(t, settlement.NextAttemptAt)
				if got := countOp(commands, commandFailJob); got != 0 {
					t.Fatalf("terminal failures = %d, want 0 while the retry budget lasts", got)
				}
			}

			// Both paths remove the container: leaving it behind would strand it on
			// the node for whichever run is dispatched next.
			if got := container.removals(); len(got) != 1 || got[0] != "container-1" {
				t.Fatalf("container removals = %v, want exactly the executed container", got)
			}
			if got := container.executionCount(); got != 1 {
				t.Fatalf("container executions = %d, want 1", got)
			}
			if got := fixture.repo.handoffs.size(); got != 0 {
				t.Fatalf("handoff permits = %d, want 0 once the jobs service accepted the settlement", got)
			}
			assertNoOwnershipStealing(t, fixture.log)
		})
	}
}

// TestRunClaimedWorkflowSettlesNothingWhenAuthorizationIsUnavailable proves a
// worker whose signing key is broken settles nothing at all. It cannot prove lease
// authority, cannot read the workflow, and cannot authorize the release itself, so
// the honest outcome is a returned error with the durable row untouched and the
// handoff permit retained for reconciliation.
func TestRunClaimedWorkflowSettlesNothingWhenAuthorizationIsUnavailable(t *testing.T) {
	t.Parallel()

	jobs := &scriptedJobsClient{}
	fixture := newClaimedRunFixtureWithAuth(
		jobs,
		&scriptedWorkflowsClient{flow: containerWorkflow()},
		nil,
		nil,
		failingTokenAuth{},
	)

	err := fixture.repo.runClaimedWorkflow(t.Context(), claimedJob(1), time.Now(), 1)
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("runClaimedWorkflow() code = %s, want %s: %v", status.Code(err), codes.Unavailable, err)
	}

	commands := fixture.log.snapshot()
	if got := settleOps(commands); len(got) != 0 {
		t.Fatalf("claim settlements = %v, want none: an unauthorized worker owns nothing", got)
	}
	if got := countOp(commands, commandScheduleJob); got != 0 {
		t.Fatalf("follow-up schedules = %d, want 0 from an unauthorized worker", got)
	}
	if got := fixture.lookups.count(); got != 0 {
		t.Fatalf("workflow reads = %d, want 0 from an unauthorized worker", got)
	}
	if got := fixture.container.executionCount(); got != 0 {
		t.Fatalf("container executions = %d, want 0 from an unauthorized worker", got)
	}
	if got := fixture.repo.handoffs.size(); got != 1 {
		t.Fatalf("handoff permits = %d, want 1: reconciliation must still observe the unsettled claim", got)
	}
}

// TestRunClaimedWorkflowFailsUnsupportedWorkflowKind proves an unrecognized kind is
// a user configuration error that fails the claim immediately. Retrying it would
// loop on data that can never become valid, and completing it would report a run
// that never happened.
func TestRunClaimedWorkflowFailsUnsupportedWorkflowKind(t *testing.T) {
	t.Parallel()

	flow := containerWorkflow()
	flow.Kind = "KUBERNETES"
	claim := claimedJob(1)
	claim.Trigger = jobsmodel.JobTriggerManual.ToString()
	fixture := newClaimedRunFixture(&scriptedJobsClient{}, &scriptedWorkflowsClient{flow: flow}, nil, nil)

	err := fixture.repo.runClaimedWorkflow(t.Context(), claim, time.Now(), 1)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("runClaimedWorkflow() code = %s, want %s: %v", status.Code(err), codes.InvalidArgument, err)
	}

	assertSingleSettlement(t, fixture.log, commandFailJob)
	commands := fixture.log.snapshot()
	failed, ok := firstOp(commands, commandFailJob)
	if !ok {
		t.Fatal("no FailJob command was recorded")
	}
	if got := failed.FailureKind; got != jobsmodel.FailureKindUser.ToString() {
		t.Fatalf("failure kind = %q, want %q", got, jobsmodel.FailureKindUser.ToString())
	}
	if got := fixture.container.executionCount(); got != 0 {
		t.Fatalf("container executions = %d, want 0 for an unsupported kind", got)
	}
	if got := fixture.container.removals(); len(got) != 0 {
		t.Fatalf("container removals = %v, want none: no container was created", got)
	}
	assertNoOwnershipStealing(t, fixture.log)
}

// TestRunClaimedWorkflowReleasesContainerClaimWithoutRuntimeService proves a claim
// the worker cannot reach a container runtime for is released for another attempt.
// Executing anyway would either panic or strand the claim until its lease expires.
func TestRunClaimedWorkflowReleasesContainerClaimWithoutRuntimeService(t *testing.T) {
	t.Parallel()

	fixture := newClaimedRunFixture(&scriptedJobsClient{}, &scriptedWorkflowsClient{flow: containerWorkflow()}, nil, nil)
	fixture.repo.svc.CsvcForEndpoint = nil

	if err := fixture.repo.runClaimedWorkflow(t.Context(), claimedJob(1), time.Now(), 1); err != nil {
		t.Fatalf("runClaimedWorkflow() error = %v, want nil once the claim was released", err)
	}

	assertSingleSettlement(t, fixture.log, commandReleaseRetry)
	commands := fixture.log.snapshot()
	released, ok := firstOp(commands, commandReleaseRetry)
	if !ok {
		t.Fatal("no ReleaseJobForRetry command was recorded")
	}
	if got := released.ErrorCode; got != codes.FailedPrecondition.String() {
		t.Fatalf("release error code = %q, want %q", got, codes.FailedPrecondition.String())
	}
	if got := fixture.container.executionCount(); got != 0 {
		t.Fatalf("container executions = %d, want 0 without a container service", got)
	}
	assertNoOwnershipStealing(t, fixture.log)
}
