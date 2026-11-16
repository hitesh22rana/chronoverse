//nolint:testpackage // Shared fake supports executor boundary tests, not persistence proofs.
package executor

import (
	"context"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	containerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

type intentTestLifecycle struct{ request containerpkg.CreateRequest }

func (*intentTestLifecycle) RuntimeInstance(context.Context) (string, error) {
	return "test-daemon", nil
}

//nolint:gocritic // The ContainerSvc interface requires CreateRequest by value.
func (s *intentTestLifecycle) Create(_ context.Context, req containerpkg.CreateRequest) (string, error) {
	s.request = req
	return "container-1", nil
}

func (s *intentTestLifecycle) Inspect(_ context.Context, id string) (*containerpkg.State, error) {
	return &containerpkg.State{
		ID: id, Status: "created",
		Labels: map[string]string{
			"chronoverse.intent_id": s.request.IntentID,
			"chronoverse.job_id":    s.request.JobID,
			"chronoverse.attempt":   strconv.Itoa(int(s.request.Attempt)),
		},
	}, nil
}

func (s *observedContainerSvc) Start(ctx context.Context, d time.Duration, _ string) (logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	_, logs, errs, err = s.Execute(ctx, d, s.request.Image, s.request.Cmd, s.request.Env)
	return logs, errs, err
}

func (s *failingContainerSvc) Start(ctx context.Context, d time.Duration, _ string) (logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	_, logs, errs, err = s.Execute(ctx, d, s.request.Image, s.request.Cmd, s.request.Env)
	return logs, errs, err
}

func (s *recordingContainerSvc) Start(ctx context.Context, d time.Duration, _ string) (logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	_, logs, errs, err = s.Execute(ctx, d, s.request.Image, s.request.Cmd, s.request.Env)
	return logs, errs, err
}

func (s *blockingRecoveryContainerSvc) Start(ctx context.Context, d time.Duration, _ string) (logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	_, logs, errs, err = s.Execute(ctx, d, s.request.Image, s.request.Cmd, s.request.Env)
	return logs, errs, err
}

//nolint:gocritic // The ContainerSvc interface requires CreateRequest by value.
func (s *boundaryContainer) Create(ctx context.Context, req containerpkg.CreateRequest) (string, error) {
	if status.Code(s.executeErr) == codes.FailedPrecondition {
		return "", terminalreason.Wrap(terminalreason.ExecutionFailed, s.executeErr)
	}
	return s.intentTestLifecycle.Create(ctx, req)
}

func (s *boundaryContainer) Start(ctx context.Context, d time.Duration, _ string) (logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	_, logs, errs, err = s.Execute(ctx, d, s.request.Image, s.request.Cmd, s.request.Env)
	return logs, errs, err
}

func fakeIntent(req *jobspb.ContainerIntentCommand) *jobspb.ContainerIntentResponse {
	id := req.GetIntentId()
	if id == "" {
		id = "00000000-0000-4000-8000-000000000015"
	}
	return &jobspb.ContainerIntentResponse{
		Granted: true,
		Intent: &jobspb.ContainerIntent{
			Id: id, JobId: req.GetJobId(), Name: "cv-test", Attempt: max(1, req.GetAttempt()),
			RuntimeNodeId: "runtime-1", RuntimeEndpoint: "tcp://docker-proxy:2375", RuntimeInstance: "test-daemon",
			ContainerId: req.GetContainerId(), StartReserved: true,
		},
	}
}

func (c *scriptedJobsClient) EnsureContainerIntent(_ context.Context, req *jobspb.ContainerIntentCommand, _ ...grpc.CallOption) (*jobspb.ContainerIntentResponse, error) {
	return fakeIntent(req), nil
}

func (c *scriptedJobsClient) GetContainerIntent(_ context.Context, req *jobspb.ContainerIntentCommand, _ ...grpc.CallOption) (*jobspb.ContainerIntentResponse, error) {
	return fakeIntent(req), nil
}

func (c *scriptedJobsClient) ReserveContainerCreate(_ context.Context, req *jobspb.ContainerIntentCommand, _ ...grpc.CallOption) (*jobspb.ContainerIntentResponse, error) {
	return fakeIntent(req), nil
}

func (c *scriptedJobsClient) ReserveContainerStart(_ context.Context, req *jobspb.ContainerIntentCommand, _ ...grpc.CallOption) (*jobspb.ContainerIntentResponse, error) {
	return fakeIntent(req), nil
}

func (c *scriptedJobsClient) ObserveContainer(_ context.Context, req *jobspb.ContainerIntentCommand, _ ...grpc.CallOption) (*jobspb.ContainerIntentResponse, error) {
	return fakeIntent(req), nil
}

func (c *scriptedJobsClient) CloseContainerIntent(_ context.Context, req *jobspb.ContainerIntentCommand, _ ...grpc.CallOption) (*jobspb.ContainerIntentResponse, error) {
	return fakeIntent(req), nil
}
