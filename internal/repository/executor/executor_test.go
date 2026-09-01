//nolint:testpackage // These tests cover unexported executor state-machine helpers.
package executor

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	containerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

func TestExtractFieldFromRecordValueRequiresDispatchAttempt(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(&jobsmodel.ScheduledJobEntry{
		JobID:              "job-1",
		WorkflowID:         "workflow-1",
		ScheduledAt:        time.Now().Format(time.RFC3339Nano),
		DispatchAttempt:    2,
		WorkflowGeneration: 5,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	record, err := extractFieldFromRecordValue(payload)
	if err != nil {
		t.Fatalf("extractFieldFromRecordValue() error = %v", err)
	}
	if record.jobID != "job-1" || record.workflowID != "workflow-1" || record.dispatchAttempt != 2 || record.workflowGeneration != 5 {
		t.Fatalf(
			"extractFieldFromRecordValue() = %q, %q, %d, %d",
			record.jobID,
			record.workflowID,
			record.dispatchAttempt,
			record.workflowGeneration,
		)
	}

	payload, err = json.Marshal(&jobsmodel.ScheduledJobEntry{
		JobID:       "job-1",
		WorkflowID:  "workflow-1",
		ScheduledAt: time.Now().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	invalidRecord, err := extractFieldFromRecordValue(payload)
	if invalidRecord.jobID != "" ||
		invalidRecord.workflowID != "" ||
		!invalidRecord.lastScheduledAt.IsZero() ||
		invalidRecord.dispatchAttempt != 0 ||
		invalidRecord.workflowGeneration != 0 {
		t.Fatalf(
			"extractFieldFromRecordValue() invalid result = %q, %q, %s, %d, %d",
			invalidRecord.jobID,
			invalidRecord.workflowID,
			invalidRecord.lastScheduledAt,
			invalidRecord.dispatchAttempt,
			invalidRecord.workflowGeneration,
		)
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("extractFieldFromRecordValue() error code = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
}

func TestProcessRecordPropagatesUnavailableClaimError(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(&jobsmodel.ScheduledJobEntry{
		JobID:              "job-1",
		WorkflowID:         "workflow-1",
		ScheduledAt:        time.Now().Format(time.RFC3339Nano),
		DispatchAttempt:    1,
		WorkflowGeneration: 1,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	repo := &Repository{
		tp: otel.Tracer("executor-test"),
		cfg: Config{
			WorkerID:      "worker-1",
			Concurrency:   1,
			LeaseDuration: 30 * time.Second,
		},
		auth:  fakeAuth{},
		slots: make(chan struct{}, 1),
		svc: &Services{
			Jobs: &claimErrorJobsClient{
				err: status.Error(codes.Unavailable, "no healthy runtime node is available"),
			},
		},
	}

	err = repo.processRecord(context.Background(), &kgo.Record{
		Topic:     "jobs",
		Partition: 0,
		Offset:    1,
		Key:       []byte("workflow-1"),
		Value:     payload,
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("processRecord() error code = %s, want %s: %v", status.Code(err), codes.Unavailable, err)
	}
	if got := len(repo.slots); got != 0 {
		t.Fatalf("execution slots held = %d, want 0", got)
	}
}

func TestClassifyExecutionFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		err       error
		retryable bool
		kind      string
		reason    string
	}{
		{
			name:      "docker unavailable is retryable system failure",
			err:       status.Error(codes.Unavailable, "docker daemon unavailable"),
			retryable: true,
			kind:      jobsmodel.FailureKindSystem.ToString(),
			reason:    terminalreason.SystemError.String(),
		},
		{
			name:      "workflow timeout is user failure",
			err:       terminalreason.Wrap(terminalreason.TimeLimitExceeded, status.Error(codes.DeadlineExceeded, "container execution timed out: context deadline exceeded")),
			retryable: false,
			kind:      jobsmodel.FailureKindUser.ToString(),
			reason:    terminalreason.TimeLimitExceeded.String(),
		},
		{
			name:      "canceled container context is retryable system failure",
			err:       status.Error(codes.DeadlineExceeded, "container execution timed out: context canceled"),
			retryable: true,
			kind:      jobsmodel.FailureKindSystem.ToString(),
			reason:    terminalreason.SystemError.String(),
		},
		{
			name:      "canceled execution is retryable system failure",
			err:       status.Error(codes.Canceled, "lease renewal failed"),
			retryable: true,
			kind:      jobsmodel.FailureKindSystem.ToString(),
			reason:    terminalreason.SystemError.String(),
		},
		{
			name:      "image pull lock exhaustion is retryable system failure",
			err:       status.Error(codes.ResourceExhausted, "timed out waiting for image pull lock"),
			retryable: true,
			kind:      jobsmodel.FailureKindSystem.ToString(),
			reason:    terminalreason.SystemError.String(),
		},
		{
			name:      "non-zero exit is user failure",
			err:       terminalreason.Wrap(terminalreason.NonZeroExit, status.Error(codes.Aborted, "container exited with non-zero code: 1")),
			retryable: false,
			kind:      jobsmodel.FailureKindUser.ToString(),
			reason:    terminalreason.NonZeroExit.String(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := classifyExecutionFailure(tt.err)
			if got.Retryable != tt.retryable || got.Kind != tt.kind || got.ReasonCode != tt.reason {
				t.Fatalf("classifyExecutionFailure() = %+v, want retryable=%v kind=%s reason=%s", got, tt.retryable, tt.kind, tt.reason)
			}
		})
	}
}

func TestProcessContainerExecutionReturnsExecutionErrorAfterLogDrain(t *testing.T) {
	t.Parallel()

	logs := make(chan *jobsmodel.JobLog)
	errs := make(chan error, 1)
	repo := &Repository{cfg: defaultConfig()}
	workflow := &workflowspb.GetWorkflowByIDResponse{
		Id:           "workflow-1",
		UserId:       "user-1",
		LogRetention: true,
	}

	done := make(chan error, 1)
	go func() {
		done <- repo.processContainerExecution(context.Background(), "job-1", 1, workflow, logs, errs)
	}()

	go func() {
		logs <- &jobsmodel.JobLog{
			Message:     "last log",
			Stream:      "stdout",
			SequenceNum: 1,
			Timestamp:   time.Now(),
		}
		close(logs)
	}()

	errs <- status.Error(codes.Aborted, "container exited with non-zero code: 1")
	close(errs)

	err := <-done
	if status.Code(err) != codes.Aborted {
		t.Fatalf("processContainerExecution() error code = %v, want %v", status.Code(err), codes.Aborted)
	}
}

func TestProcessContainerExecutionDrainsExecutionErrorsUntilClosed(t *testing.T) {
	t.Parallel()

	logs := make(chan *jobsmodel.JobLog)
	errs := make(chan error)
	repo := &Repository{cfg: defaultConfig()}

	done := make(chan error, 1)
	go func() {
		done <- repo.processContainerExecution(context.Background(), "job-1", 1, nil, logs, errs)
	}()

	go func() {
		errs <- status.Error(codes.Unavailable, "failed to read container logs")
		errs <- status.Error(codes.Aborted, "container exited with non-zero code: 1")
		close(errs)
		close(logs)
	}()

	select {
	case err := <-done:
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("processContainerExecution() error code = %v, want %v", status.Code(err), codes.Unavailable)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for processContainerExecution to drain execution errors")
	}
}

func TestExecuteContainerWorkflowEnsuresResolvedImageBeforeExecute(t *testing.T) {
	t.Parallel()

	csvc := &recordingContainerSvc{
		logs: make(chan *jobsmodel.JobLog),
		errs: make(chan error),
	}
	close(csvc.logs)
	close(csvc.errs)

	repo := &Repository{cfg: defaultConfig()}
	workflow := &workflowspb.GetWorkflowByIDResponse{
		Id:                  "workflow-1",
		UserId:              "user-1",
		Payload:             `{"image":"alpine:3.22","cmd":["echo","ok"],"env":{"A":"B"},"timeout":"1s"}`,
		ResolvedImageDigest: "alpine@sha256:abc",
		LogRetention:        true,
	}

	containerID, err := repo.executeContainerWorkflow(t.Context(), csvc, "job-1", "lease-1", "runtime-1", 1, workflow)
	if err != nil {
		t.Fatalf("executeContainerWorkflow() error = %v", err)
	}
	if containerID != "" {
		t.Fatalf("containerID = %q, want empty", containerID)
	}
	if got, want := csvc.buildImage, "alpine@sha256:abc"; got != want {
		t.Fatalf("Build image = %q, want %q", got, want)
	}
	if got, want := csvc.executeImage, "alpine@sha256:abc"; got != want {
		t.Fatalf("Execute image = %q, want %q", got, want)
	}
	if !csvc.buildBeforeExecute {
		t.Fatal("Build was not called before Execute")
	}
}

func TestRecoverExpiredLeaseRenewsLeaseWhileReplayingLogs(t *testing.T) {
	t.Parallel()

	replayCanFinish := make(chan struct{})
	jobsClient := &renewingRecoveryJobsClient{
		replayCanFinish: replayCanFinish,
		released:        make(chan struct{}),
	}
	repo := &Repository{
		cfg: Config{
			WorkerID:           "worker-1",
			LeaseDuration:      time.Second,
			LeaseRenewInterval: 10 * time.Millisecond,
			SystemRetryLimit:   3,
			SystemRetryBackoff: time.Millisecond,
		},
		auth: fakeAuth{},
		svc: &Services{
			Jobs: jobsClient,
			CsvcForEndpoint: func(string, string) (ContainerSvc, error) {
				return &blockingRecoveryContainerSvc{
					replayCanFinish: replayCanFinish,
				}, nil
			},
		},
	}

	done := make(chan error, 1)
	go func() {
		done <- repo.recoverExpiredLease(context.Background(), &jobspb.ExpiredJobLease{
			Id:              "job-1",
			WorkflowId:      "workflow-1",
			UserId:          "user-1",
			ContainerId:     "container-1",
			RuntimeNodeId:   "runtime-1",
			RuntimeEndpoint: "tcp://docker-proxy:2375",
			LeaseToken:      "lease-1",
			Trigger:         jobsmodel.JobTriggerAutomatic.ToString(),
			Attempts:        1,
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("recoverExpiredLease() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for recovered lease to renew during log replay")
	}

	if got := jobsClient.renewals.Load(); got < 2 {
		t.Fatalf("RenewJobLease calls = %d, want at least 2", got)
	}

	select {
	case <-jobsClient.released:
	default:
		t.Fatal("expected recovered job to be released for retry")
	}
}

func TestRecoveredLeaseRenewalFailureAfterSuccessfulRecoveryIsIgnored(t *testing.T) {
	t.Parallel()

	renewAttempted := make(chan struct{})
	jobsClient := &lateFailingRenewalJobsClient{
		renewAttempted: renewAttempted,
	}
	repo := &Repository{
		cfg: Config{
			WorkerID:           "worker-1",
			LeaseDuration:      time.Second,
			LeaseRenewInterval: time.Millisecond,
		},
		auth: fakeAuth{},
		svc: &Services{
			Jobs: jobsClient,
		},
	}

	err := repo.withRecoveredLeaseRenewal(
		context.Background(),
		&jobspb.ClaimJobResponse{
			Id:         "job-1",
			WorkflowId: "workflow-1",
			LeaseToken: "lease-1",
		},
		func(context.Context) error {
			select {
			case <-renewAttempted:
				return nil
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for recovered lease renewal attempt")
				return nil
			}
		},
	)
	if err != nil {
		t.Fatalf("withRecoveredLeaseRenewal() error = %v, want nil", err)
	}
}

func TestRecoveredLeaseRequiresImmediateRenewalBeforeContainerAccess(t *testing.T) {
	t.Parallel()

	recoveryCalled := false
	repo := &Repository{
		cfg:  Config{LeaseDuration: time.Second, LeaseRenewInterval: time.Millisecond},
		auth: fakeAuth{},
		svc:  &Services{Jobs: &failedImmediateRenewalJobsClient{}},
	}
	err := repo.withRecoveredLeaseRenewal(
		context.Background(),
		&jobspb.ClaimJobResponse{Id: "job-1", WorkflowId: "workflow-1", LeaseToken: "stale-lease"},
		func(context.Context) error {
			recoveryCalled = true
			return nil
		},
	)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("withRecoveredLeaseRenewal() code = %s, want %s: %v", status.Code(err), codes.FailedPrecondition, err)
	}
	if recoveryCalled {
		t.Fatal("recovery accessed the container before proving lease authority")
	}
}

func TestReconcileHandoffsRetainsLiveAuthorityAndReleasesInactiveAuthority(t *testing.T) {
	t.Parallel()

	gate := newHandoffRegistry(1)
	request := &jobspb.ClaimJobRequest{Id: "job-1", CommandId: "claim-1"}
	entry, owner, err := gate.getOrReserve("claim-1", request)
	if err != nil || !owner {
		t.Fatalf("getOrReserve() = (%v, %v), want owner", owner, err)
	}
	if !gate.activate(entry, &jobspb.ClaimJobResponse{Claimed: true, Id: "job-1", LeaseToken: "lease-1"}) {
		t.Fatal("activate() = false")
	}
	gate.markAwaiting("claim-1")

	jobsClient := &reconciliationJobsClient{}
	repo := &Repository{
		handoffs: gate,
		auth:     fakeAuth{},
		svc:      &Services{Jobs: jobsClient},
	}
	repo.reconcileHandoffs(t.Context())
	if gate.size() != 1 {
		t.Fatal("live database authority released the awaiting permit")
	}
	repo.reconcileHandoffs(t.Context())
	if gate.size() != 0 {
		t.Fatal("inactive database authority retained the awaiting permit")
	}
}

func TestRecoverExpiredLeaseBatchStartsRenewalForEveryClaimedJob(t *testing.T) {
	t.Parallel()

	replayCanFinish := make(chan struct{})
	jobsClient := &batchRecoveryJobsClient{
		replayCanFinish: replayCanFinish,
		renewed:         make(map[string]struct{}),
	}
	repo := &Repository{
		cfg: Config{
			WorkerID:           "worker-1",
			Concurrency:        2,
			LeaseDuration:      time.Second,
			LeaseRenewInterval: 10 * time.Millisecond,
			SystemRetryLimit:   3,
			SystemRetryBackoff: time.Millisecond,
			RecoveryBatchSize:  100,
		},
		auth: fakeAuth{},
		svc: &Services{
			Jobs: jobsClient,
			CsvcForEndpoint: func(string, string) (ContainerSvc, error) {
				return &blockingRecoveryContainerSvc{
					replayCanFinish: replayCanFinish,
				}, nil
			},
		},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		repo.recoverExpiredLeaseBatch(context.Background())
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for recovery batch to renew every claimed job")
	}

	if got := jobsClient.requestedBatchSize.Load(); got != 2 {
		t.Fatalf("RecoverExpiredJobLeases batch size = %d, want 2", got)
	}
	if got := jobsClient.releases.Load(); got != 2 {
		t.Fatalf("ReleaseJobForRetry calls = %d, want 2", got)
	}
	if got := jobsClient.claimCalls.Load(); got < 2 {
		t.Fatalf("RecoverExpiredJobLeases calls = %d, want at least 2 to drain waves", got)
	}
}

type fakeAuth struct{}

func (fakeAuth) IssueToken(context.Context, string, ...string) (string, error) {
	return "token", nil
}

func (fakeAuth) ValidateToken(context.Context, string) (context.Context, *jwt.Token, error) {
	return context.Background(), &jwt.Token{}, nil
}

type claimErrorJobsClient struct {
	jobspb.JobsServiceClient

	err error
}

func (c *claimErrorJobsClient) ClaimJob(context.Context, *jobspb.ClaimJobRequest, ...grpc.CallOption) (*jobspb.ClaimJobResponse, error) {
	return nil, c.err
}

type renewingRecoveryJobsClient struct {
	jobspb.JobsServiceClient

	renewals        atomic.Int32
	replayCloseOnce sync.Once
	releaseOnce     sync.Once
	replayCanFinish chan struct{}
	released        chan struct{}
}

func (c *renewingRecoveryJobsClient) RenewJobLease(context.Context, *jobspb.RenewJobLeaseRequest, ...grpc.CallOption) (*jobspb.RenewJobLeaseResponse, error) {
	if c.renewals.Add(1) >= 2 {
		c.replayCloseOnce.Do(func() { close(c.replayCanFinish) })
	}

	return &jobspb.RenewJobLeaseResponse{}, nil
}

func (c *renewingRecoveryJobsClient) ReleaseJobForRetry(context.Context, *jobspb.ReleaseJobForRetryRequest, ...grpc.CallOption) (*jobspb.ReleaseJobForRetryResponse, error) {
	c.releaseOnce.Do(func() { close(c.released) })

	return &jobspb.ReleaseJobForRetryResponse{}, nil
}

type lateFailingRenewalJobsClient struct {
	jobspb.JobsServiceClient

	renewals       atomic.Int32
	renewOnce      sync.Once
	renewAttempted chan struct{}
}

type failedImmediateRenewalJobsClient struct {
	jobspb.JobsServiceClient
}

type reconciliationJobsClient struct {
	jobspb.JobsServiceClient

	calls atomic.Int32
}

func (c *reconciliationJobsClient) ClaimJob(context.Context, *jobspb.ClaimJobRequest, ...grpc.CallOption) (*jobspb.ClaimJobResponse, error) {
	if c.calls.Add(1) == 1 {
		return &jobspb.ClaimJobResponse{Claimed: true, Id: "job-1", LeaseToken: "lease-1"}, nil
	}
	return &jobspb.ClaimJobResponse{Claimed: false, Reason: "stored lease is no longer active"}, nil
}

func (*failedImmediateRenewalJobsClient) RenewJobLease(context.Context, *jobspb.RenewJobLeaseRequest, ...grpc.CallOption) (*jobspb.RenewJobLeaseResponse, error) {
	return nil, status.Error(codes.FailedPrecondition, "lease not held")
}

func (c *lateFailingRenewalJobsClient) RenewJobLease(context.Context, *jobspb.RenewJobLeaseRequest, ...grpc.CallOption) (*jobspb.RenewJobLeaseResponse, error) {
	if c.renewals.Add(1) == 1 {
		return &jobspb.RenewJobLeaseResponse{}, nil
	}
	c.renewOnce.Do(func() { close(c.renewAttempted) })

	return nil, status.Error(codes.FailedPrecondition, "lease not held")
}

type batchRecoveryJobsClient struct {
	jobspb.JobsServiceClient

	claimCalls         atomic.Int32
	requestedBatchSize atomic.Int32
	releases           atomic.Int32
	replayCloseOnce    sync.Once
	replayCanFinish    chan struct{}

	mu      sync.Mutex
	renewed map[string]struct{}
}

func (c *batchRecoveryJobsClient) RecoverExpiredJobLeases(_ context.Context, req *jobspb.RecoverExpiredJobLeasesRequest, _ ...grpc.CallOption) (*jobspb.RecoverExpiredJobLeasesResponse, error) {
	if c.claimCalls.Add(1) > 1 {
		return &jobspb.RecoverExpiredJobLeasesResponse{}, nil
	}

	c.requestedBatchSize.Store(req.GetBatchSize())
	return &jobspb.RecoverExpiredJobLeasesResponse{
		Jobs: []*jobspb.ExpiredJobLease{
			{
				Id:              "job-1",
				WorkflowId:      "workflow-1",
				UserId:          "user-1",
				ContainerId:     "container-1",
				RuntimeEndpoint: "tcp://docker-proxy:2375",
				LeaseToken:      "lease-1",
				Trigger:         jobsmodel.JobTriggerAutomatic.ToString(),
				Attempts:        1,
			},
			{
				Id:              "job-2",
				WorkflowId:      "workflow-1",
				UserId:          "user-1",
				ContainerId:     "container-2",
				RuntimeEndpoint: "tcp://docker-proxy:2375",
				LeaseToken:      "lease-2",
				Trigger:         jobsmodel.JobTriggerAutomatic.ToString(),
				Attempts:        1,
			},
		},
	}, nil
}

func (c *batchRecoveryJobsClient) RenewJobLease(_ context.Context, req *jobspb.RenewJobLeaseRequest, _ ...grpc.CallOption) (*jobspb.RenewJobLeaseResponse, error) {
	c.mu.Lock()
	c.renewed[req.GetId()] = struct{}{}
	renewedCount := len(c.renewed)
	c.mu.Unlock()

	if renewedCount >= 2 {
		c.replayCloseOnce.Do(func() { close(c.replayCanFinish) })
	}

	return &jobspb.RenewJobLeaseResponse{}, nil
}

func (c *batchRecoveryJobsClient) ReleaseJobForRetry(context.Context, *jobspb.ReleaseJobForRetryRequest, ...grpc.CallOption) (*jobspb.ReleaseJobForRetryResponse, error) {
	c.releases.Add(1)

	return &jobspb.ReleaseJobForRetryResponse{}, nil
}

type blockingRecoveryContainerSvc struct {
	replayCanFinish <-chan struct{}
}

func (*blockingRecoveryContainerSvc) Build(context.Context, string) error {
	return nil
}

func (*blockingRecoveryContainerSvc) ImageExists(context.Context, string) (bool, error) {
	return true, nil
}

func (*blockingRecoveryContainerSvc) DockerHost() string {
	return "tcp://docker-proxy:2375"
}

func (*blockingRecoveryContainerSvc) Execute(context.Context, time.Duration, string, []string, []string) (containerID string, logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	return "", nil, nil, nil
}

func (c *blockingRecoveryContainerSvc) Logs(ctx context.Context, _ string) (logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	logsCh := make(chan *jobsmodel.JobLog)
	errsCh := make(chan error)
	go func() {
		defer close(logsCh)
		defer close(errsCh)

		select {
		case <-c.replayCanFinish:
		case <-ctx.Done():
		}
	}()

	return logsCh, errsCh, nil
}

func (*blockingRecoveryContainerSvc) Inspect(context.Context, string) (*containerpkg.State, error) {
	return &containerpkg.State{Running: true}, nil
}

func (*blockingRecoveryContainerSvc) Remove(context.Context, string) error {
	return nil
}

func (*blockingRecoveryContainerSvc) Terminate(context.Context, string) error {
	return nil
}

type recordingContainerSvc struct {
	buildImage         string
	executeImage       string
	buildBeforeExecute bool
	logs               chan *jobsmodel.JobLog
	errs               chan error
}

func (s *recordingContainerSvc) Build(_ context.Context, image string) error {
	s.buildImage = image
	return nil
}

func (*recordingContainerSvc) ImageExists(context.Context, string) (bool, error) {
	return false, nil
}

func (*recordingContainerSvc) DockerHost() string {
	return "tcp://docker-proxy:2375"
}

func (s *recordingContainerSvc) Execute(_ context.Context, _ time.Duration, image string, _, _ []string) (containerID string, logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	s.executeImage = image
	s.buildBeforeExecute = s.buildImage != ""
	return "", s.logs, s.errs, nil
}

func (*recordingContainerSvc) Logs(context.Context, string) (logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	return nil, nil, nil
}

func (*recordingContainerSvc) Inspect(context.Context, string) (*containerpkg.State, error) {
	return &containerpkg.State{}, nil
}

func (*recordingContainerSvc) Remove(context.Context, string) error {
	return nil
}

func (*recordingContainerSvc) Terminate(context.Context, string) error {
	return nil
}
