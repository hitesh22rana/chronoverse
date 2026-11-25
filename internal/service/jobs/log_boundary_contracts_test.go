//nolint:testpackage // Verifies the private live-event forwarding boundary.
package jobs

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-playground/validator/v10"
	redisclient "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	jobsmock "github.com/hitesh22rana/chronoverse/internal/service/jobs/mock"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

func TestLiveForwardingSkipsMalformedEventsAndPreservesIdentity(t *testing.T) {
	t.Parallel()
	in := make(chan *redisclient.Message, 2)
	out := make(chan *jobsmodel.JobLog, 1)
	in <- &redisclient.Message{Payload: "invalid JSON"}
	in <- &redisclient.Message{Payload: `{"EventKey":"event-7","TimeStamp":"2026-11-06T01:02:03.000000004Z","Message":"hello","SequenceNum":7,"Stream":"stderr"}`}
	close(in)
	forwardLiveJobLogs(t.Context(), in, out)
	require.Len(t, out, 1)
	require.Equal(t,
		&jobsmodel.JobLog{
			EventID: "event-7",
			Timestamp: time.Date(2026,
				time.November,
				6,
				1,
				2,
				3,
				4,
				time.UTC),
			Message:     "hello",
			SequenceNum: 7,
			Stream:      "stderr",
		},
		<-out)
}

func TestLiveForwardingCancellationReleasesIdleAndBlockedConsumers(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "blocked consumer"}[blocked], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				in := make(chan *redisclient.Message, 1)
				out := make(chan *jobsmodel.JobLog)
				if blocked {
					in <- &redisclient.Message{Payload: `{"EventKey":"event"}`}
				}
				done := make(chan struct{})
				go func() { defer close(done); forwardLiveJobLogs(ctx, in, out) }()
				synctest.Wait()
				cancel()
				synctest.Wait()
				select {
				case <-done:
				default:
					t.Fatal("forwarder did not stop")
				}
			})
		})
	}
}

func TestJobLogBoundaryValidationStopsBeforeRepository(t *testing.T) {
	t.Parallel()
	svc := New(validator.New(), jobsmock.NewMockRepository(gomock.NewController(t)))
	for _, trigger := range []string{"INVALID", "manual"} {
		id, err := svc.ScheduleJob(t.Context(), &jobspb.ScheduleJobRequest{WorkflowId: "workflow", UserId: "user", ScheduledAt: "2026-11-06T01:02:03Z", Trigger: trigger, IdempotencyKey: "event"})
		require.Empty(t, id)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
		require.Contains(t, err.Error(), "invalid trigger")
	}
	id, err := svc.ScheduleJob(t.Context(), &jobspb.ScheduleJobRequest{WorkflowId: "workflow", UserId: "user", ScheduledAt: "2026-11-06T01:02:03Z", Trigger: "MANUAL"})
	require.Empty(t, id)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Contains(t, err.Error(), "IdempotencyKey")
}

func TestRetainedLogReadsRecheckPolicyWhileOlderReadIsPending(t *testing.T) {
	for _, method := range []string{"get", "search"} {
		t.Run(method, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				repo := jobsmock.NewMockRepository(gomock.NewController(t))
				svc := New(validator.New(), repo)
				const user, workflow, job = "user", "workflow", "job"
				entered, release := make(chan struct{}), make(chan struct{})
				defer close(release)
				page := &jobsmodel.GetJobLogsResponse{ID: job, WorkflowID: workflow}
				read := func() (*jobsmodel.GetJobLogsResponse, error) {
					select {
					case <-entered:
						return nil, status.Error(codes.NotFound, "workflow no longer visible")
					default:
						close(entered)
						<-release
						return page, nil
					}
				}
				var call func() (*jobsmodel.GetJobLogsResponse, error)
				if method == "get" {
					req := &jobspb.GetJobLogsRequest{Id: job, WorkflowId: workflow, UserId: user, Filters: &jobspb.GetJobLogsFilters{Stream: jobspb.LogStream_LOG_STREAM_ALL}}
					repo.EXPECT().GetJobLogs(gomock.Any(), job, workflow, user, "", jobsmodel.JobLogsSortOrderDesc, gomock.Any()).Times(2).
						DoAndReturn(func(
							context.Context,
							string, string, string, string,
							jobsmodel.JobLogsSortOrder,
							*jobsmodel.GetJobLogsFilters,
						) (*jobsmodel.GetJobLogsResponse, error) {
							return read()
						})
					call = func() (*jobsmodel.GetJobLogsResponse, error) { return svc.GetJobLogs(t.Context(), req) }
				} else {
					req := &jobspb.SearchJobLogsRequest{Id: job, WorkflowId: workflow, UserId: user, Filters: &jobspb.SearchJobLogsFilters{Stream: jobspb.LogStream_LOG_STREAM_ALL, Message: "log"}}
					repo.EXPECT().SearchJobLogs(gomock.Any(), job, workflow, user, "", gomock.Any(), gomock.Any()).Times(2).
						DoAndReturn(func(
							context.Context,
							string, string, string, string,
							*jobsmodel.SearchJobLogsFilters,
							jobsmodel.SearchJobLogsOptions,
						) (*jobsmodel.GetJobLogsResponse, error) {
							return read()
						})
					call = func() (*jobsmodel.GetJobLogsResponse, error) { return svc.SearchJobLogs(t.Context(), req) }
				}
				older := make(chan error, 1)
				go func() { _, err := call(); older <- err }()
				<-entered
				latest := make(chan error, 1)
				go func() { _, err := call(); latest <- err }()
				synctest.Wait()
				select {
				case err := <-latest:
					require.Equal(t, codes.NotFound, status.Code(err))
				default:
					t.Error("new read waited on an older result instead of checking current policy")
				}
				// The request that had already applied policy may complete.
				release <- struct{}{}
				require.NoError(t, <-older)
			})
		})
	}
}

func TestRetainedLogReadPreservesCancellationOnStorageFailure(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	repo := jobsmock.NewMockRepository(gomock.NewController(t))
	repo.EXPECT().GetJobLogs(gomock.Any(), "job", "workflow", "user", "", jobsmodel.JobLogsSortOrderDesc, gomock.Any()).
		DoAndReturn(func(
			context.Context,
			string, string, string, string,
			jobsmodel.JobLogsSortOrder,
			*jobsmodel.GetJobLogsFilters,
		) (*jobsmodel.GetJobLogsResponse, error) {
			cancel()
			return nil, status.Error(codes.Internal, "log transport interrupted")
		})
	result, err := New(validator.New(), repo).GetJobLogs(ctx, &jobspb.GetJobLogsRequest{
		Id: "job", WorkflowId: "workflow", UserId: "user",
		Filters: &jobspb.GetJobLogsFilters{Stream: jobspb.LogStream_LOG_STREAM_ALL},
	})
	require.Nil(t, result)
	require.Equal(t, codes.Canceled, status.Code(err))
}
