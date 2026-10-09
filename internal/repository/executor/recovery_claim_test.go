//nolint:testpackage // Exercises lease recovery through existing executor seams.
package executor

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	containerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

type recoveryContainer struct {
	observedContainerSvc
	state                                        containerpkg.State
	inspectErr, terminateErr, logsErr, removeErr error
	calls                                        []string
}

func (s *recoveryContainer) Inspect(_ context.Context, id string) (*containerpkg.State, error) {
	s.calls = append(s.calls, "inspect:"+id)
	return &s.state, s.inspectErr
}

func (s *recoveryContainer) Terminate(_ context.Context, id string) error {
	s.calls = append(s.calls, "terminate:"+id)
	return s.terminateErr
}

func (s *recoveryContainer) Logs(_ context.Context, id string) (logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	s.calls = append(s.calls, "logs:"+id)
	return nil, nil, s.logsErr
}

func (s *recoveryContainer) Remove(_ context.Context, id string) error {
	s.calls = append(s.calls, "remove:"+id)
	return s.removeErr
}

func TestRecoverExpiredLeaseWithClaim(t *testing.T) {
	unavailable := status.Error(codes.Unavailable, "runtime unavailable")
	denied := status.Error(codes.PermissionDenied, "runtime denied")
	tests := []struct {
		name       string
		changeJob  func(*jobspb.ExpiredJobLease)
		container  recoveryContainer
		factoryErr error
		attempts   int32
		settlement jobCommand
		code       codes.Code
		calls      []string
	}{
		{name: "unavailable runtime retries", changeJob: func(j *jobspb.ExpiredJobLease) { j.RuntimeUnavailable = true }, settlement: commandReleaseRetry},
		{name: "missing container retries", changeJob: func(j *jobspb.ExpiredJobLease) { j.ContainerId = "" }, settlement: commandReleaseRetry},
		{name: "missing endpoint retries", changeJob: func(j *jobspb.ExpiredJobLease) { j.RuntimeEndpoint = "" }, settlement: commandReleaseRetry},
		{name: "factory failure retries", factoryErr: unavailable, settlement: commandReleaseRetry},
		{
			name:       "missing container at runtime retries",
			container:  recoveryContainer{inspectErr: status.Error(codes.NotFound, "gone")},
			settlement: commandReleaseRetry,
			calls:      []string{"inspect:container-1"},
		},
		{name: "inspect failure preserves lease", container: recoveryContainer{inspectErr: denied}, code: codes.PermissionDenied, calls: []string{"inspect:container-1"}},
		{
			name:      "terminate failure preserves lease",
			container: recoveryContainer{state: containerpkg.State{Running: true}, terminateErr: denied},
			code:      codes.PermissionDenied,
			calls:     []string{"inspect:container-1", "terminate:container-1"},
		},
		{
			name:      "running log failure preserves lease",
			container: recoveryContainer{state: containerpkg.State{Running: true}, logsErr: denied},
			code:      codes.PermissionDenied,
			calls:     []string{"inspect:container-1", "terminate:container-1", "logs:container-1"},
		},
		{
			name:      "running removal failure preserves lease",
			container: recoveryContainer{state: containerpkg.State{Running: true}, removeErr: denied},
			code:      codes.PermissionDenied,
			calls:     []string{"inspect:container-1", "terminate:container-1", "logs:container-1", "remove:container-1"},
		},
		{
			name:       "running container retries after cleanup",
			container:  recoveryContainer{state: containerpkg.State{Running: true}},
			settlement: commandReleaseRetry,
			calls:      []string{"inspect:container-1", "terminate:container-1", "logs:container-1", "remove:container-1"},
		},
		{name: "stopped log failure preserves lease", container: recoveryContainer{logsErr: denied}, code: codes.PermissionDenied, calls: []string{"inspect:container-1", "logs:container-1"}},
		{name: "clean exit completes", settlement: commandCompleteJob, calls: []string{"inspect:container-1", "logs:container-1", "remove:container-1"}},
		{
			name:       "nonzero exit fails",
			container:  recoveryContainer{state: containerpkg.State{ExitCode: 17}},
			settlement: commandFailJob,
			code:       codes.Aborted,
			calls:      []string{"inspect:container-1", "logs:container-1", "remove:container-1"},
		},
		{name: "exhausted runtime failure fails", changeJob: func(j *jobspb.ExpiredJobLease) { j.RuntimeUnavailable = true }, attempts: 3, settlement: commandFailJob, code: codes.Unavailable},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			csvc := &tt.container
			fixture := newClaimedRunFixture(&scriptedJobsClient{}, &scriptedWorkflowsClient{}, nil, csvc)
			claim := claimedJob(tt.attempts)
			job := &jobspb.ExpiredJobLease{
				Id: claim.GetId(), WorkflowId: claim.GetWorkflowId(),
				UserId:      claim.GetUserId(),
				ContainerId: "container-1", RuntimeNodeId: "runtime-1", RuntimeEndpoint: "tcp://docker-proxy:2375",
			}
			if tt.changeJob != nil {
				tt.changeJob(job)
			}
			fixture.repo.svc.CsvcForEndpoint = func(node, endpoint string) (ContainerSvc, error) {
				if node != job.GetRuntimeNodeId() || endpoint != job.GetRuntimeEndpoint() {
					t.Fatalf("runtime factory got %q, %q", node, endpoint)
				}
				return csvc, tt.factoryErr
			}
			err := fixture.repo.recoverExpiredLeaseWithClaim(context.Background(), job, claim)
			if status.Code(err) != tt.code {
				t.Fatalf("recovery error = %v, want %s", err, tt.code)
			}
			if tt.settlement != "" {
				assertSingleSettlement(t, fixture.log, tt.settlement)
			} else if ops := settleOps(fixture.log.snapshot()); len(ops) != 0 {
				t.Fatalf("unexpected settlement: %v", ops)
			}
			assertNoOwnershipStealing(t, fixture.log)
			if !reflect.DeepEqual(csvc.calls, tt.calls) {
				t.Fatalf("runtime operations = %v, want %v", csvc.calls, tt.calls)
			}
			if failed, ok := firstOp(fixture.log.snapshot(), commandFailJob); ok && failed.ErrorCode != tt.code.String() {
				t.Fatalf("failure code = %s, want %s", failed.ErrorCode, tt.code)
			}
		})
	}
}

func TestRecoveredLeaseSettlementRejectsStaleOwnership(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "complete"
		if fail {
			name = "fail"
		}
		t.Run(name, func(t *testing.T) {
			fenced := status.Error(codes.FailedPrecondition, "lease changed")
			jobs := &scriptedJobsClient{
				completeJob: func(context.Context, *jobspb.CompleteJobRequest) error { return fenced },
				failJob:     func(context.Context, *jobspb.FailJobRequest) error { return fenced },
			}
			csvc := &recoveryContainer{}
			if fail {
				csvc.state.ExitCode = 2
			}
			fixture := newClaimedRunFixture(jobs, &scriptedWorkflowsClient{}, nil, csvc)
			claim := claimedJob(1)
			job := &jobspb.ExpiredJobLease{Id: claim.GetId(), WorkflowId: claim.GetWorkflowId(), ContainerId: "container-1", RuntimeNodeId: "runtime-1", RuntimeEndpoint: "tcp://docker-proxy:2375"}
			err := fixture.repo.recoverExpiredLeaseWithClaim(context.Background(), job, claim)
			if !errors.Is(err, fenced) {
				t.Fatalf("recovery error = %v, want fencing error", err)
			}
			want := commandCompleteJob
			if fail {
				want = commandFailJob
			}
			assertSingleSettlement(t, fixture.log, want)
			assertNoOwnershipStealing(t, fixture.log)
			if !reflect.DeepEqual(csvc.calls, []string{"inspect:container-1", "logs:container-1"}) {
				t.Fatalf("stale lease cleaned up runtime: %v", csvc.calls)
			}
			if fixture.repo.handoffs.size() != 1 {
				t.Fatal("rejected settlement consumed the active handoff")
			}
		})
	}
}
