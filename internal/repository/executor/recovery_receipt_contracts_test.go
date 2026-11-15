//nolint:testpackage // Exercises recovery's existing executor and runtime boundaries.
package executor

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	containerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

func recoveryReceiptJob() *jobspb.ExpiredJobLease {
	return &jobspb.ExpiredJobLease{Id: claimedJobID, WorkflowId: claimedWorkflowID, ContainerId: "container-1", RuntimeNodeId: "runtime-1", RuntimeEndpoint: "tcp://runtime:2376"}
}

func TestRecoveryCleanupPreservesLeaseCancellation(t *testing.T) {
	for _, phase := range []string{"inspect", "terminate", "remove"} {
		t.Run(phase, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				wait := func(operationCtx context.Context, _ string) error {
					cancel()
					<-operationCtx.Done()
					return operationCtx.Err()
				}
				container := &recoveryContainer{state: containerpkg.State{Running: true}}
				switch phase {
				case "inspect":
					container.onInspect = func(ctx context.Context, id string) (*containerpkg.State, error) { return nil, wait(ctx, id) }
				case "terminate":
					container.onTerminate = wait
				case "remove":
					container.onRemove = wait
				}
				jobs := &scriptedJobsClient{}
				fixture := newClaimedRunFixture(jobs, &scriptedWorkflowsClient{}, nil, container)
				started := time.Now()
				err := fixture.repo.recoverExpiredLeaseWithClaim(ctx, recoveryReceiptJob(), claimedJob(1))
				if status.Code(err) != codes.Canceled || !time.Now().Equal(started) {
					t.Fatalf("canceled recovery elapsed=%v err=%v", time.Since(started), err)
				}
				if _, recorded := jobs.cleanupReceipts.Load(claimedJobID + ":container-1"); recorded {
					t.Fatal("canceled cleanup recorded completion")
				}
				if ops := settleOps(fixture.log.snapshot()); len(ops) != 0 {
					t.Fatalf("canceled recovery settled claim: %v", ops)
				}
			})
		})
	}
}

func TestRecoveryAttemptBoundsBlockingOperations(t *testing.T) {
	for _, phase := range []string{"receipt read", "inspect", "absence receipt write"} {
		t.Run(phase, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				container := &recoveryContainer{}
				jobs := &scriptedJobsClient{}
				want := 10 * time.Second
				switch phase {
				case "receipt read":
					jobs.getReceipt = func(ctx context.Context, _ *jobspb.ContainerCleanupReceiptRequest) (*jobspb.GetContainerCleanupReceiptResponse, error) {
						<-ctx.Done()
						return nil, ctx.Err()
					}
				case "inspect":
					want = 3 * time.Minute
					container.onInspect = func(ctx context.Context, _ string) (*containerpkg.State, error) { <-ctx.Done(); return nil, ctx.Err() }
				case "absence receipt write":
					container.inspectErr = status.Error(codes.NotFound, "gone")
					jobs.recordReceipt = func(ctx context.Context, _ *jobspb.ContainerCleanupReceiptRequest) error {
						<-ctx.Done()
						return ctx.Err()
					}
				}
				fixture := newClaimedRunFixture(jobs, &scriptedWorkflowsClient{}, nil, container)
				started := time.Now()
				err := fixture.repo.recoverExpiredLeaseWithClaim(t.Context(), recoveryReceiptJob(), claimedJob(1))
				if status.Code(err) != codes.DeadlineExceeded || time.Since(started) != want {
					t.Fatalf("recovery elapsed=%v err=%v", time.Since(started), err)
				}
				if ops := settleOps(fixture.log.snapshot()); len(ops) != 0 {
					t.Fatalf("timed-out recovery settled claim: %v", ops)
				}
			})
		})
	}
}

func TestRecoveryReceiptAuthorizationFailurePreservesClaim(t *testing.T) {
	fixture := newClaimedRunFixture(&scriptedJobsClient{}, &scriptedWorkflowsClient{}, nil, nil)
	fixture.repo.auth = failingTokenAuth{}
	fixture.repo.svc.CsvcForEndpoint = func(string, string) (ContainerSvc, error) {
		t.Fatal("receipt authorization failure reached Docker")
		return nil, status.Error(codes.Unavailable, "unexpected runtime lookup")
	}
	if err := fixture.repo.recoverExpiredLeaseWithClaim(t.Context(), recoveryReceiptJob(), claimedJob(1)); status.Code(err) != codes.Unavailable {
		t.Fatalf("authorization failure=%v", err)
	}
	if ops := settleOps(fixture.log.snapshot()); len(ops) != 0 {
		t.Fatalf("unauthorized recovery settled claim: %v", ops)
	}
}
