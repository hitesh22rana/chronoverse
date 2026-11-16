//nolint:testpackage // Exercises the shared RPC budget without a live workload timeout.
package executor

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

func TestContainerIntentRPCDeadlinePreservesEarlierCallerBound(t *testing.T) {
	for _, duration := range []time.Duration{time.Hour, time.Second} {
		t.Run(duration.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				parent, cancel := context.WithTimeout(t.Context(), duration)
				defer cancel()
				started := time.Now()
				_, err := callContainerIntent(parent, func(ctx context.Context, _ *jobspb.ContainerIntentCommand, _ ...grpc.CallOption) (*jobspb.ContainerIntentResponse, error) {
					<-ctx.Done()
					return nil, ctx.Err()
				}, &jobspb.ContainerIntentCommand{})
				want := min(duration, 5*time.Second)
				if status.Code(err) != codes.DeadlineExceeded || time.Since(started) != want {
					t.Fatalf("unbounded lifecycle RPC: elapsed=%s error=%v", time.Since(started), err)
				}
				if duration > 5*time.Second && parent.Err() != nil {
					t.Fatal("control-plane timeout canceled the workload context")
				}
			})
		})
	}
}

func TestContainerAttachmentHasIndependentDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		jobs := &scriptedJobsClient{attachJobContainer: func(ctx context.Context, _ *jobspb.AttachJobContainerRequest) error {
			<-ctx.Done()
			return status.FromContextError(ctx.Err()).Err()
		}}
		fixture := newClaimedRunFixture(jobs, &scriptedWorkflowsClient{}, nil, nil)
		started := time.Now()
		err := fixture.repo.attachJobContainer(t.Context(), claimedJobID, claimedLeaseToken, "container", "runtime-1")
		if status.Code(err) != codes.DeadlineExceeded || time.Since(started) != 5*time.Second || t.Context().Err() != nil {
			t.Fatalf("attachment blocked a live workload indefinitely: elapsed=%s error=%v", time.Since(started), err)
		}
	})
}

func TestContainerIntentRejectsSuccessAfterRPCBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		result, err := callContainerIntent(t.Context(), func(ctx context.Context, _ *jobspb.ContainerIntentCommand, _ ...grpc.CallOption) (*jobspb.ContainerIntentResponse, error) {
			<-ctx.Done()
			return &jobspb.ContainerIntentResponse{Granted: true}, nil
		}, &jobspb.ContainerIntentCommand{})
		if result != nil || status.Code(err) != codes.DeadlineExceeded {
			t.Fatalf("late reply became permission to mutate: %+v %v", result, err)
		}
	})
}
