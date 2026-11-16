//nolint:testpackage // Exercise the independent discovery and recovery owners.
package executor

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	containerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

type discoveryQueueJobs struct {
	jobspb.JobsServiceClient
	claimed     bool
	recovered   int
	report      *jobspb.ContainerIntentCommand
	reportBound time.Duration
}

func (j *discoveryQueueJobs) ClaimContainerIntents(context.Context, *jobspb.ContainerIntentCommand, ...grpc.CallOption) (*jobspb.ListContainerIntentsResponse, error) {
	if j.claimed {
		return &jobspb.ListContainerIntentsResponse{}, nil
	}
	j.claimed = true
	return &jobspb.ListContainerIntentsResponse{Intents: []*jobspb.ContainerIntent{{
		Id: "intent", Name: "cv-intent", RuntimeNodeId: "runtime-1", RuntimeEndpoint: "tcp://docker-proxy:2375", RuntimeInstance: "original",
	}}}, nil
}

func (j *discoveryQueueJobs) ReportContainerInspection(ctx context.Context, req *jobspb.ContainerIntentCommand, _ ...grpc.CallOption) (*jobspb.ContainerIntentResponse, error) {
	j.report = req
	if deadline, ok := ctx.Deadline(); ok && ctx.Err() == nil {
		j.reportBound = time.Until(deadline)
	}
	return &jobspb.ContainerIntentResponse{}, nil
}

func (j *discoveryQueueJobs) RecoverExpiredJobLeases(context.Context, *jobspb.RecoverExpiredJobLeasesRequest, ...grpc.CallOption) (*jobspb.RecoverExpiredJobLeasesResponse, error) {
	j.recovered++
	return &jobspb.RecoverExpiredJobLeasesResponse{}, nil
}

type blockedDiscoveryRuntime struct {
	observedContainerSvc
	entered chan struct{}
}

func (c *blockedDiscoveryRuntime) RuntimeInstance(ctx context.Context) (string, error) {
	close(c.entered)
	<-ctx.Done()
	return "", ctx.Err()
}

func TestDiscoveryStallDoesNotOccupyLeaseRecoveryAndShutdownJoins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runtime := &blockedDiscoveryRuntime{entered: make(chan struct{})}
		fixture := newClaimedRunFixture(&scriptedJobsClient{}, &scriptedWorkflowsClient{}, nil, runtime)
		jobs := &discoveryQueueJobs{}
		fixture.repo.svc.Jobs = jobs
		fixture.repo.cfg.RecoveryInterval = time.Second
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		stopped := make(chan struct{})
		go func() { defer close(stopped); fixture.repo.reconcileContainerIntents(ctx) }()
		<-runtime.entered
		// The actual expired-lease owner can claim while discovery is stuck at a daemon.
		fixture.repo.recoverExpiredLeaseBatch(ctx)
		if jobs.recovered != 1 {
			t.Fatal("discovery consumed lease recovery capacity")
		}
		cancel()
		<-stopped
		if jobs.report.GetOutcome() != "INTERRUPTED" || jobs.reportBound <= 0 || jobs.reportBound > 5*time.Second {
			t.Fatalf("shutdown report was not detached and bounded: outcome=%q bound=%s", jobs.report.GetOutcome(), jobs.reportBound)
		}
	})
}

type repointedDiscoveryRuntime struct {
	observedContainerSvc
	identities int
}

func (c *repointedDiscoveryRuntime) RuntimeInstance(context.Context) (string, error) {
	c.identities++
	if c.identities == 1 {
		return "original", nil
	}
	return "replacement", nil
}

func (*repointedDiscoveryRuntime) Inspect(context.Context, string) (*containerpkg.State, error) {
	return nil, status.Error(codes.NotFound, "replacement daemon has no such container")
}

func TestDiscoveryRejectsAbsenceAcrossDaemonRepoint(t *testing.T) {
	runtime := &repointedDiscoveryRuntime{}
	fixture := newClaimedRunFixture(&scriptedJobsClient{}, &scriptedWorkflowsClient{}, nil, runtime)
	id, err := fixture.repo.resolveIntentContainer(t.Context(), runtime, &jobspb.ContainerIntent{
		Id: "intent", Name: "cv-intent", RuntimeInstance: "original",
	})
	if status.Code(err) != codes.Unavailable || id != "" || runtime.identities != 2 {
		t.Fatalf("replacement daemon supplied absence proof: id=%q identities=%d error=%v", id, runtime.identities, err)
	}
}
