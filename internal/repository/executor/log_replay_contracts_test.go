//nolint:testpackage // Exercises replay through the executor's container boundary.
package executor

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

type replayContractContainer struct {
	observedContainerSvc
	logs        <-chan *jobsmodel.JobLog
	errs        <-chan error
	startErr    error
	logsDone    <-chan struct{}
	containerID string
}

func (s *replayContractContainer) Logs(ctx context.Context, id string) (logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	s.logsDone, s.containerID = ctx.Done(), id
	return s.logs, s.errs, s.startErr
}

func replayContractIdentity() (claim *jobspb.ClaimJobResponse, workflow *workflowspb.GetWorkflowByIDResponse) {
	claim = &jobspb.ClaimJobResponse{
		Claimed: true, Id: "00000000-0000-0000-0000-0000000000c1",
		WorkflowId: "00000000-0000-0000-0000-0000000000c2", UserId: "00000000-0000-0000-0000-0000000000c3",
		Attempts: 2, LeaseToken: "replay-lease",
	}
	workflow = &workflowspb.GetWorkflowByIDResponse{Id: claim.GetWorkflowId(), UserId: claim.GetUserId(), LogRetention: true}
	return claim, workflow
}

func TestReplayContainerLogsPreservesDependencyFailures(t *testing.T) {
	t.Parallel()
	denied := status.Error(codes.PermissionDenied, "runtime access denied")
	unavailable := status.Error(codes.Unavailable, "log stream disconnected")
	for _, tt := range []struct {
		name      string
		startErr  error
		streamErr error
		want      error
	}{
		{name: "removed before reading", startErr: status.Error(codes.NotFound, "removed")},
		{name: "permission failure", startErr: denied, want: denied},
		{name: "removed during reading", streamErr: status.Error(codes.NotFound, "removed")},
		{name: "stream failure", streamErr: unavailable, want: unavailable},
		{name: "empty completed stream"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			logs := make(chan *jobsmodel.JobLog)
			streamErrors := make(chan error, 1)
			close(logs)
			if tt.streamErr != nil {
				streamErrors <- tt.streamErr
			}
			close(streamErrors)
			container := &replayContractContainer{logs: logs, errs: streamErrors, startErr: tt.startErr}
			repo := &Repository{}
			claim, workflow := replayContractIdentity()
			err := repo.replayContainerLogs(t.Context(), container, claim, workflow, "container-1")
			if !errors.Is(err, tt.want) {
				t.Fatalf("replay error = %v, want original %v", err, tt.want)
			}
			if container.containerID != "container-1" {
				t.Fatalf("requested container = %q", container.containerID)
			}
			select {
			case <-container.logsDone:
			default:
				t.Fatal("replay context remains active after return")
			}
		})
	}
}

func TestReplayContainerLogsClassifiesCallerCancellation(t *testing.T) {
	t.Parallel()
	for _, deadline := range []bool{false, true} {
		name := "canceled"
		if deadline {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			want := codes.Canceled
			if deadline {
				cancel()
				ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
				want = codes.DeadlineExceeded
			} else {
				cancel()
			}
			defer cancel()
			container := &replayContractContainer{logs: make(chan *jobsmodel.JobLog), errs: make(chan error)}
			claim, workflow := replayContractIdentity()
			err := (&Repository{}).replayContainerLogs(ctx, container, claim, workflow, "container-1")
			if status.Code(err) != want {
				t.Fatalf("replay error = %v, want %s", err, want)
			}
			select {
			case <-container.logsDone:
			default:
				t.Fatal("container stream did not receive cancellation")
			}
		})
	}
}

func TestContextErrorReturnsNilWithoutCause(t *testing.T) {
	t.Parallel()
	if err := contextError(nil); err != nil {
		t.Fatalf("contextError(nil) = %v, want nil", err)
	}
}

func TestReplayContainerLogsSkipsEmptyContainer(t *testing.T) {
	t.Parallel()
	container := &replayContractContainer{}
	claim, workflow := replayContractIdentity()
	if err := (&Repository{}).replayContainerLogs(t.Context(), container, claim, workflow, ""); err != nil {
		t.Fatalf("empty replay = %v, want nil", err)
	}
	if container.containerID != "" {
		t.Fatalf("empty replay requested container %q", container.containerID)
	}
}

func TestRecoveredLogTimestampFillsMissingTimestamp(t *testing.T) {
	t.Parallel()
	before := time.Now().UTC()
	for _, log := range []*jobsmodel.JobLog{nil, {}} {
		got := recoveredLogTimestamp(log)
		if got.IsZero() || got.Location() != time.UTC || got.Before(before) || time.Since(got) > time.Second {
			t.Fatalf("missing timestamp recovered as %v, want current UTC time", got)
		}
	}
	stamp := time.Date(2026, time.January, 2, 3, 4, 5, 123456789, time.UTC)
	if got := recoveredLogTimestamp(&jobsmodel.JobLog{Timestamp: stamp}); !got.Equal(stamp) {
		t.Fatalf("stored timestamp = %v, want %v", got, stamp)
	}
}

func TestPublishRecoveredContainerLogSkipsNilLog(t *testing.T) {
	t.Parallel()
	claim, workflow := replayContractIdentity()
	(&Repository{}).publishRecoveredContainerLog(t.Context(), claim, workflow, nil)
}

func TestPublishJobLogEventDropsUnencodableEvent(t *testing.T) {
	t.Parallel()
	(&Repository{}).publishJobLogEvent(t.Context(), &jobsmodel.JobLogEvent{
		EventKey:  "log:job-1:stdout:1",
		JobID:     "job-1",
		TimeStamp: time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC),
		Stream:    "stdout",
	})
}
