//nolint:testpackage // Exercise execution authority at the adapter/service boundary.
package executor

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	containerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

type previouslyExecutedContainer struct{ observedContainerSvc }

func (c *previouslyExecutedContainer) Inspect(ctx context.Context, id string) (*containerpkg.State, error) {
	state, err := c.observedContainerSvc.Inspect(ctx, id)
	if state != nil {
		state.Status = "exited"
	}
	return state, err
}

func TestContainerWithoutStartAuthorityCannotBeRunAgain(t *testing.T) {
	runtime := &previouslyExecutedContainer{}
	fixture := newClaimedRunFixture(&scriptedJobsClient{}, &scriptedWorkflowsClient{}, nil, runtime)
	_, err := fixture.repo.executeContainerWorkflow(t.Context(), runtime, claimedJobID, claimedLeaseToken, "runtime-1", 1, containerWorkflow())
	if status.Code(err) != codes.Unavailable || runtime.executionCount() != 0 {
		t.Fatalf("unauthorized prior execution was rerun: starts=%d error=%v", runtime.executionCount(), err)
	}
}

type failingIntentReportClient struct {
	jobspb.JobsServiceClient
	reports int
}

func (c *failingIntentReportClient) CloseContainerIntent(
	context.Context,
	*jobspb.ContainerIntentCommand,
	...grpc.CallOption,
) (
	*jobspb.ContainerIntentResponse,
	error,
) {
	c.reports++
	return nil, status.Error(codes.Unavailable, "report response lost")
}

func TestExecutionFailureSurvivesIntentReportFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason terminalreason.Code
		code   codes.Code
		kind   string
	}{
		{"workload", terminalreason.NonZeroExit, codes.Aborted, jobsmodel.FailureKindUser.ToString()},
		{"infrastructure", terminalreason.SystemError, codes.NotFound, jobsmodel.FailureKindSystem.ToString()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := terminalreason.Wrap(tc.reason, status.Error(tc.code, "original operation diagnostic"))
			runtime := &failingContainerSvc{executeErr: original}
			jobs := &scriptedJobsClient{}
			fixture := newClaimedRunFixture(jobs, &scriptedWorkflowsClient{}, nil, runtime)
			reporting := &failingIntentReportClient{JobsServiceClient: jobs}
			fixture.repo.svc.Jobs = reporting
			_, err := fixture.repo.executeContainerWorkflow(t.Context(), runtime, claimedJobID, claimedLeaseToken, "runtime-1", 1, containerWorkflow())
			failure := classifyExecutionFailure(err)
			if reporting.reports != 1 || failure.Kind != tc.kind || failure.ReasonCode != tc.reason.String() || status.Code(err) != tc.code || status.Convert(err).Message() != "original operation diagnostic" {
				t.Fatalf("auxiliary report changed operation outcome: reports=%d failure=%+v error=%v", reporting.reports, failure, err)
			}
		})
	}
}
