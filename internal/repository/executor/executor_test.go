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
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	containerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
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

func TestClassifyExecutionFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		err       error
		retryable bool
		kind      string
	}{
		{
			name:      "docker unavailable is retryable system failure",
			err:       status.Error(codes.Unavailable, "docker daemon unavailable"),
			retryable: true,
			kind:      jobsmodel.FailureKindSystem.ToString(),
		},
		{
			name:      "workflow timeout is user failure",
			err:       status.Error(codes.DeadlineExceeded, "container execution timed out: context deadline exceeded"),
			retryable: false,
			kind:      jobsmodel.FailureKindUser.ToString(),
		},
		{
			name:      "canceled container context is retryable system failure",
			err:       status.Error(codes.DeadlineExceeded, "container execution timed out: context canceled"),
			retryable: true,
			kind:      jobsmodel.FailureKindSystem.ToString(),
		},
		{
			name:      "canceled execution is retryable system failure",
			err:       status.Error(codes.Canceled, "lease renewal failed"),
			retryable: true,
			kind:      jobsmodel.FailureKindSystem.ToString(),
		},
		{
			name:      "non-zero exit is user failure",
			err:       status.Error(codes.Aborted, "container exited with non-zero code: 1"),
			retryable: false,
			kind:      jobsmodel.FailureKindUser.ToString(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := classifyExecutionFailure(tt.err)
			if got.Retryable != tt.retryable || got.Kind != tt.kind {
				t.Fatalf("classifyExecutionFailure() = %+v, want retryable=%v kind=%s", got, tt.retryable, tt.kind)
			}
		})
	}
}

func TestProcessContainerExecutionDrainsLogsBeforeReturningExecutionError(t *testing.T) {
	t.Parallel()

	logs := make(chan *jobsmodel.JobLog)
	errs := make(chan error, 1)
	jobsClient := &blockingJobLogClient{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	repo := &Repository{
		auth: fakeAuth{},
		svc: &Services{
			Jobs: jobsClient,
		},
	}
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

	select {
	case <-jobsClient.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for log enqueue to start")
	}

	errs <- status.Error(codes.Aborted, "container exited with non-zero code: 1")
	close(errs)

	select {
	case err := <-done:
		t.Fatalf("processContainerExecution returned before log enqueue drained: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(jobsClient.release)

	err := <-done
	if status.Code(err) != codes.Aborted {
		t.Fatalf("processContainerExecution() error code = %v, want %v", status.Code(err), codes.Aborted)
	}
}

func TestProcessContainerExecutionDrainsExecutionErrorsUntilClosed(t *testing.T) {
	t.Parallel()

	logs := make(chan *jobsmodel.JobLog)
	errs := make(chan error)
	repo := &Repository{}

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
			Csvc: &blockingRecoveryContainerSvc{
				replayCanFinish: replayCanFinish,
			},
		},
	}

	done := make(chan error, 1)
	go func() {
		done <- repo.recoverExpiredLease(context.Background(), &jobspb.ExpiredJobLease{
			Id:          "job-1",
			WorkflowId:  "workflow-1",
			UserId:      "user-1",
			ContainerId: "container-1",
			LeaseToken:  "lease-1",
			Trigger:     jobsmodel.JobTriggerAutomatic.ToString(),
			Attempts:    1,
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
			Csvc: &blockingRecoveryContainerSvc{
				replayCanFinish: replayCanFinish,
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

func (fakeAuth) IssueToken(context.Context, string) (string, error) {
	return "token", nil
}

func (fakeAuth) ValidateToken(context.Context) (*jwt.Token, error) {
	return &jwt.Token{}, nil
}

type blockingJobLogClient struct {
	jobspb.JobsServiceClient

	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (c *blockingJobLogClient) EnqueueJobLog(ctx context.Context, _ *jobspb.EnqueueJobLogRequest, _ ...grpc.CallOption) (*jobspb.EnqueueJobLogResponse, error) {
	c.once.Do(func() { close(c.started) })
	select {
	case <-c.release:
		return &jobspb.EnqueueJobLogResponse{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
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

	renewOnce      sync.Once
	renewAttempted chan struct{}
}

func (c *lateFailingRenewalJobsClient) RenewJobLease(context.Context, *jobspb.RenewJobLeaseRequest, ...grpc.CallOption) (*jobspb.RenewJobLeaseResponse, error) {
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
				Id:          "job-1",
				WorkflowId:  "workflow-1",
				UserId:      "user-1",
				ContainerId: "container-1",
				LeaseToken:  "lease-1",
				Trigger:     jobsmodel.JobTriggerAutomatic.ToString(),
				Attempts:    1,
			},
			{
				Id:          "job-2",
				WorkflowId:  "workflow-1",
				UserId:      "user-1",
				ContainerId: "container-2",
				LeaseToken:  "lease-2",
				Trigger:     jobsmodel.JobTriggerAutomatic.ToString(),
				Attempts:    1,
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
