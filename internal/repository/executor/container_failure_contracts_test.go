//nolint:testpackage // Asserts executor ordering across container and jobs boundaries.
package executor

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

type boundaryContainer struct {
	observedContainerSvc
	buildErr, executeErr error
	builds               int
	image                string
}

func (s *boundaryContainer) Build(_ context.Context, image string) error {
	s.builds++
	s.image = image
	return s.buildErr
}

func (s *boundaryContainer) Execute(ctx context.Context, timeout time.Duration, image string, cmd, env []string) (string, <-chan *jobsmodel.JobLog, <-chan error, error) {
	id, logs, errs, _ := s.observedContainerSvc.Execute(ctx, timeout, image, cmd, env)
	if s.executeErr != nil {
		if status.Code(s.executeErr) == codes.FailedPrecondition {
			return "", nil, nil, s.executeErr
		}
		return id, nil, nil, s.executeErr
	}
	return id, logs, errs, nil
}

func TestContainerExecutionFailurePreservesAttachmentOrdering(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"invalid payload", "build denied", "precondition before attach", "start failed after attach", "attach failed", "fallback image"} {
		t.Run(name, func(t *testing.T) {
			csvc := &boundaryContainer{}
			var attachErr error
			jobs := &scriptedJobsClient{attachJobContainer: func(_ context.Context, req *jobspb.AttachJobContainerRequest) error {
				assertAttachedRuntime(t, req)
				return attachErr
			}}
			fixture := newClaimedRunFixture(jobs, &scriptedWorkflowsClient{}, nil, csvc)
			workflow := containerWorkflow()
			wantCode, wantID, wantBuilds, wantExecutes, wantAttaches := codes.OK, "container-1", 1, 1, 1
			switch name {
			case "invalid payload":
				workflow.Payload = "{"
				wantCode = codes.InvalidArgument
				wantID = ""
				wantBuilds = 0
				wantExecutes = 0
				wantAttaches = 0
			case "build denied":
				csvc.buildErr = status.Error(codes.PermissionDenied, "registry denied")
				wantCode = codes.PermissionDenied
				wantID = ""
				wantExecutes = 0
				wantAttaches = 0
			case "precondition before attach":
				csvc.executeErr = status.Error(codes.FailedPrecondition, "runtime rejected execution")
				wantCode = codes.FailedPrecondition
				wantID = ""
				wantAttaches = 0
			case "start failed after attach":
				csvc.executeErr = status.Error(codes.Aborted, "failed to start container")
				wantCode = codes.Aborted
			case "attach failed":
				attachErr = status.Error(codes.Aborted, "lease changed")
				wantCode = codes.Aborted
			case "fallback image":
				workflow.ResolvedImageDigest = ""
			}
			id, err := fixture.repo.executeContainerWorkflow(t.Context(), csvc, claimedJobID, claimedLeaseToken, "runtime-1", 1, workflow)
			if id != wantID || status.Code(err) != wantCode {
				t.Fatalf("execution=(%q,%v), want %q/%s", id, err, wantID, wantCode)
			}
			assertNoOwnershipStealing(t, fixture.log)
			gotCounts := [3]int{csvc.builds, csvc.executionCount(), countOp(fixture.log.snapshot(), commandAttach)}
			wantCounts := [3]int{wantBuilds, wantExecutes, wantAttaches}
			if gotCounts != wantCounts {
				t.Fatalf("build/execute/attach=%v, want %v", gotCounts, wantCounts)
			}
			if name == "fallback image" && csvc.image != "alpine:3.22" {
				t.Fatalf("fallback image=%q", csvc.image)
			}
			if len(settleOps(fixture.log.snapshot())) != 0 || len(csvc.removals()) != 0 {
				t.Fatal("execution helper settled or removed container")
			}
		})
	}
}

func TestSettlementAuthorizationFailureLeavesContainerAndAuthority(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"cancel", "release", "complete", "fail", "attach"} {
		t.Run(name, func(t *testing.T) {
			csvc := &observedContainerSvc{}
			fixture := newClaimedRunFixture(&scriptedJobsClient{}, &scriptedWorkflowsClient{}, nil, csvc)
			fixture.repo.auth = failingTokenAuth{}
			claim := claimedJob(1)
			cause := status.Error(codes.InvalidArgument, "invalid workload")
			var err error
			switch name {
			case "cancel":
				err = fixture.repo.cancelClaimedJob(t.Context(), claim)
			case "release":
				err = fixture.repo.releaseClaimForSystemRetry(t.Context(), claim, cause)
			case "complete":
				err = fixture.repo.completeClaimedJob(t.Context(), claim, csvc, "container-1")
			case "fail":
				err = fixture.repo.failClaimedJob(t.Context(), claim, csvc, cause, "container-1")
			case "attach":
				err = fixture.repo.attachJobContainer(t.Context(), claim.GetId(), claim.GetLeaseToken(), "container-1", "runtime-1")
			}
			if err == nil {
				t.Fatal("authorization failure was ignored")
			}
			if len(fixture.log.snapshot()) != 0 || len(csvc.removals()) != 0 || fixture.repo.handoffs.size() != 1 {
				t.Fatal("authorization failure mutated jobs or containers")
			}
		})
	}
}

func TestSettlementSucceedsDespiteContainerRemovalFailure(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"complete", "terminal failure", "retryable failure"} {
		t.Run(name, func(t *testing.T) {
			csvc := &recoveryContainer{removeErr: status.Error(codes.Unavailable, "runtime cleanup unavailable")}
			fixture := newClaimedRunFixture(&scriptedJobsClient{}, &scriptedWorkflowsClient{}, nil, csvc)
			claim := claimedJob(1)
			cause := status.Error(codes.InvalidArgument, "invalid workload")
			wantOp, wantCode := commandCompleteJob, codes.OK
			var err error
			switch name {
			case "complete":
				err = fixture.repo.completeClaimedJob(t.Context(), claim, csvc, "container-1")
			case "terminal failure":
				wantOp = commandFailJob
				wantCode = codes.InvalidArgument
				err = fixture.repo.failClaimedJob(t.Context(), claim, csvc, cause, "container-1")
			case "retryable failure":
				wantOp = commandReleaseRetry
				cause = status.Error(codes.Unavailable, "runtime wait unavailable")
				err = fixture.repo.failClaimedJob(t.Context(), claim, csvc, cause, "container-1")
			}
			if status.Code(err) != wantCode {
				t.Fatalf("settlement error=%v, want %s", err, wantCode)
			}
			assertNoOwnershipStealing(t, fixture.log)
			assertSingleSettlement(t, fixture.log, wantOp)
			if fixture.repo.handoffs.size() != 0 {
				t.Fatal("successful settlement retained handoff authority")
			}
			if len(csvc.calls) != 1 || csvc.calls[0] != "remove:container-1" {
				t.Fatalf("cleanup calls=%v", csvc.calls)
			}
		})
	}
}

func TestJobLogRecordRejectsTimestampOutsideJSONRange(t *testing.T) {
	t.Parallel()
	repo := &Repository{}
	record := repo.jobLogRecord(t.Context(), "job-1", 1, containerWorkflow(), &jobsmodel.JobLog{Stream: "stdout", SequenceNum: 1, Timestamp: time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)})
	if record != nil {
		t.Fatalf("unencodable timestamp produced record=%v", record)
	}
}

func TestContainerLogPublisherStopsWhenOwnerFinishes(t *testing.T) {
	t.Parallel()
	repo := &Repository{cfg: defaultConfig()}
	logs := make(chan *jobsmodel.JobLog)
	done := make(chan struct{})
	stopped := repo.publishContainerLogs(t.Context(), "job-1", 1, containerWorkflow(), logs, done)
	close(done)
	select {
	case <-stopped:
	case <-time.After(claimedRunBound):
		t.Fatal("publisher outlived owner")
	}
}

func TestClaimedWorkflowRetriesWhenFollowUpScheduleIsUnavailable(t *testing.T) {
	t.Parallel()
	jobs := &scriptedJobsClient{scheduleJob: func(context.Context, *jobspb.ScheduleJobRequest) error {
		return status.Error(codes.Unavailable, "scheduler database unavailable")
	}}
	fixture := newClaimedRunFixture(jobs, &scriptedWorkflowsClient{flow: containerWorkflow()}, nil, nil)
	done := make(chan error, 1)
	go func() { done <- fixture.repo.runClaimedWorkflow(t.Context(), claimedJob(1), time.Now(), 7) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("retry settlement=%v", err)
		}
	case <-time.After(claimedRunBound):
		t.Fatal("follow-up failure did not finish")
	}
	assertNoOwnershipStealing(t, fixture.log)
	assertSingleSettlement(t, fixture.log, commandReleaseRetry)
	if countOp(fixture.log.snapshot(), commandScheduleJob) != 1 || fixture.container.executionCount() != 0 || len(fixture.container.removals()) != 0 {
		t.Fatal("schedule failure executed or removed a container")
	}
	release, _ := firstOp(fixture.log.snapshot(), commandReleaseRetry)
	if release.ErrorCode != codes.Unavailable.String() {
		t.Fatalf("retry cause=%s", release.ErrorCode)
	}
}

func assertAttachedRuntime(t *testing.T, req *jobspb.AttachJobContainerRequest) {
	t.Helper()
	if req.GetContainerId() != "container-1" || req.GetRuntimeNodeId() != "runtime-1" {
		t.Fatalf("attached runtime/container=%q/%q, want runtime-1/container-1", req.GetRuntimeNodeId(), req.GetContainerId())
	}
}
