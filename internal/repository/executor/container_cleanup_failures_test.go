//nolint:testpackage // Checks that original-runtime proof failures cannot settle physical cleanup or recovery.
package executor

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	containerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

type provenanceFaultJobs struct {
	*scriptedJobsClient
	lookupErr error
}

func (j *provenanceFaultJobs) GetContainerIntent(ctx context.Context, r *jobspb.ContainerIntentCommand, o ...grpc.CallOption) (*jobspb.ContainerIntentResponse, error) {
	if j.lookupErr != nil {
		return nil, j.lookupErr
	}
	return j.scriptedJobsClient.GetContainerIntent(ctx, r, o...)
}

func TestInlineCleanupNeverRecordsProofAcrossRuntimeFailure(t *testing.T) {
	for _, scenario := range []string{"lookup", "initial identity failure", "initial identity mismatch", "final identity failure", "final identity mismatch"} {
		t.Run(scenario, func(t *testing.T) {
			receipts := 0
			jobs := &provenanceFaultJobs{scriptedJobsClient: &scriptedJobsClient{recordReceipt: func(context.Context, *jobspb.ContainerCleanupReceiptRequest) error { receipts++; return nil }}}
			runtime := &intentFaultRuntime{}
			switch scenario {
			case "lookup":
				jobs.lookupErr = status.Error(codes.Unavailable, "intent lookup failed")
			case "initial identity failure":
				runtime.instanceFn = func(int) (string, error) { return "", status.Error(codes.Unavailable, "identity read failed") }
			case "initial identity mismatch":
				runtime.instanceFn = func(int) (string, error) { return "replacement", nil }
			case "final identity failure":
				runtime.instanceFn = func(n int) (string, error) {
					if n == 2 {
						return "", status.Error(codes.Unavailable, "identity confirmation failed")
					}
					return "test-daemon", nil
				}
			case "final identity mismatch":
				runtime.instanceFn = func(n int) (string, error) {
					if n == 2 {
						return "replacement", nil
					}
					return "test-daemon", nil
				}
			}
			f := newClaimedRunFixture(jobs.scriptedJobsClient, &scriptedWorkflowsClient{}, nil, runtime)
			f.repo.svc.Jobs = jobs
			err := f.repo.cleanupContainer(t.Context(), runtime, claimedJobID, "container-1")
			if status.Code(err) != codes.Unavailable || receipts != 0 {
				t.Fatalf("unproven cleanup acknowledged: receipts=%d error=%v", receipts, err)
			}
			wantRemoves := 0
			if scenario == "final identity failure" || scenario == "final identity mismatch" {
				wantRemoves = 1
			}
			if len(runtime.removals()) != wantRemoves {
				t.Fatalf("wrong daemon was mutated: removes=%v", runtime.removals())
			}
		})
	}
}

func TestRecoveryIdentityFailureStaysSystemAndRetainsCleanupProof(t *testing.T) {
	for _, scenario := range []string{"lookup", "identity failure", "identity mismatch", "created cleanup failure"} {
		t.Run(scenario, func(t *testing.T) {
			runtime := &intentFaultRuntime{}
			jobs := &provenanceFaultJobs{scriptedJobsClient: &scriptedJobsClient{}}
			switch scenario {
			case "lookup":
				jobs.lookupErr = status.Error(codes.Unavailable, "intent unavailable")
			case "identity failure":
				runtime.instanceFn = func(int) (string, error) { return "", status.Error(codes.Unavailable, "identity endpoint failed") }
			case "identity mismatch":
				runtime.instanceFn = func(int) (string, error) { return "replacement", nil }
			case "created cleanup failure":
				runtime.inspectFn = func(int, string) (*containerpkg.State, error) { return &containerpkg.State{Status: "created"}, nil }
				receiptReads := 0
				jobs.getReceipt = func(context.Context, *jobspb.ContainerCleanupReceiptRequest) (*jobspb.GetContainerCleanupReceiptResponse, error) {
					receiptReads++
					if receiptReads == 1 {
						return &jobspb.GetContainerCleanupReceiptResponse{}, nil
					}
					return nil, status.Error(codes.Unavailable, "cleanup proof unavailable")
				}
			}
			f := newClaimedRunFixture(jobs.scriptedJobsClient, &scriptedWorkflowsClient{}, nil, runtime)
			f.repo.svc.Jobs = jobs
			err := f.repo.recoverExpiredLeaseWithClaim(t.Context(), recoveryReceiptJob(), claimedJob(1))
			if err != nil && status.Code(err) != codes.Unavailable {
				t.Fatalf("infrastructure failure changed classification: %v", err)
			}
			if runtime.startCalls != 0 || len(runtime.removals()) != 0 {
				t.Fatal("unproven recovery started/removed a container")
			}
			if scenario != "created cleanup failure" {
				found := false
				for _, op := range f.log.snapshot() {
					if op.Op == commandReleaseRetry {
						found = true
					}
				}
				if !found {
					t.Fatal("runtime identity failure did not take system retry path")
				}
			}
		})
	}
}

func TestRecoveryAbsenceRequiresStableDaemonAndReadableState(t *testing.T) {
	for _, scenario := range []string{"authorization", "nil state", "identity failure", "identity mismatch"} {
		t.Run(scenario, func(t *testing.T) {
			runtime := &intentFaultRuntime{inspectFn: func(int, string) (*containerpkg.State, error) {
				return nil, status.Error(codes.NotFound, "container absent")
			}}
			auth := &intentFaultAuth{}
			if scenario == "authorization" {
				auth.failAt = 1
			}
			if scenario == "nil state" {
				//nolint:nilnil // A malformed adapter success must not settle recovery.
				runtime.inspectFn = func(int, string) (*containerpkg.State, error) { return nil, nil }
			}
			if scenario == "identity failure" {
				runtime.instanceFn = func(int) (string, error) { return "", status.Error(codes.Unavailable, "identity unavailable") }
			}
			if scenario == "identity mismatch" {
				runtime.instanceFn = func(int) (string, error) { return "replacement", nil }
			}
			receipts := 0
			jobs := &scriptedJobsClient{recordReceipt: func(context.Context, *jobspb.ContainerCleanupReceiptRequest) error { receipts++; return nil }}
			f := newClaimedRunFixtureWithAuth(jobs, &scriptedWorkflowsClient{}, nil, runtime, auth)
			state, err := f.repo.inspectRecoveredContainer(t.Context(), runtime, &jobspb.ContainerCleanupReceiptRequest{JobId: claimedJobID, ContainerId: "container-1", RuntimeInstance: "test-daemon"})
			if state != nil || status.Code(err) != codes.Unavailable || receipts != 0 {
				t.Fatalf("false absence proof: state=%v receipts=%d error=%v", state, receipts, err)
			}
		})
	}
}
