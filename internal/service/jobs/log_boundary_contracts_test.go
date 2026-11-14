//nolint:testpackage // Verifies the private live-event forwarding boundary.
package jobs

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-playground/validator/v10"
	redisclient "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
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
	svc := New(validator.New(), jobsmock.NewMockRepository(gomock.NewController(t)), nil)
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
	for _, state := range []string{"COMPLETED", "CANCELED", "FAILED"} {
		require.True(t, isTerminalJobStatus(state))
	}
	for _, state := range []string{"PENDING", "RUNNING", "QUEUED", "unknown"} {
		require.False(t, isTerminalJobStatus(state))
	}
}

func TestLogReadCacheCancellationDoesNotReachRepository(t *testing.T) {
	t.Parallel()
	for _, search := range []bool{false, true} {
		for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				wantCode := codes.Canceled
				if errors.Is(cause, context.DeadlineExceeded) {
					cancel()
					ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
					wantCode = codes.DeadlineExceeded
				}
				cancel()
				ctrl := gomock.NewController(t)
				cache := jobsmock.NewMockCache(ctrl)
				repo := jobsmock.NewMockRepository(ctrl)
				svc := New(validator.New(), repo, cache)
				cache.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
					func(got context.Context, _ string, _ any) (any, error) {
						require.ErrorIs(t, got.Err(), cause)
						return nil, status.Errorf(codes.Internal, "failed to get value from redis: %v", cause)
					},
				)
				var result *jobsmodel.GetJobLogsResponse
				var err error
				if search {
					result,
						err = svc.SearchJobLogs(ctx,
						&jobspb.SearchJobLogsRequest{
							Id:         "job",
							WorkflowId: "workflow",
							UserId:     "user",
							Cursor:     "page",
							Filters: &jobspb.SearchJobLogsFilters{
								Stream:  jobspb.LogStream_LOG_STREAM_ALL,
								Message: "query",
							},
						})
				} else {
					result,
						err = svc.GetJobLogs(ctx,
						&jobspb.GetJobLogsRequest{
							Id:         "job",
							WorkflowId: "workflow",
							UserId:     "user",
							Cursor:     "page",
							Filters:    &jobspb.GetJobLogsFilters{Stream: jobspb.LogStream_LOG_STREAM_ALL},
						})
				}
				require.Nil(t, result)
				require.Equal(t, wantCode, status.Code(err))
				require.Contains(t, err.Error(), cause.Error())
				synctest.Wait()
			})
		}
	}
}

func TestLogReadCacheFailureWithLiveContextFallsBackToRepository(t *testing.T) {
	t.Parallel()
	for _, search := range []bool{false, true} {
		t.Run(map[bool]string{false: "get", true: "search"}[search], func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			cache, repo := jobsmock.NewMockCache(ctrl), jobsmock.NewMockRepository(ctrl)
			svc := New(validator.New(), repo, cache)
			// A cache-internal timeout is wrapped in a gRPC status while the caller's
			// context stays healthy, so the read must fall back to the repository.
			cache.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(
				nil, status.Errorf(codes.Internal, "failed to get value from redis: %v", context.DeadlineExceeded),
			)
			response := &jobsmodel.GetJobLogsResponse{ID: "job", WorkflowID: "workflow"}
			var result *jobsmodel.GetJobLogsResponse
			var err error
			if search {
				repo.EXPECT().SearchJobLogs(gomock.Any(), "job", "workflow", "user", "page", gomock.Any(), gomock.Any()).Return(response, "RUNNING", nil)
				result,
					err = svc.SearchJobLogs(t.Context(),
					&jobspb.SearchJobLogsRequest{
						Id:         "job",
						WorkflowId: "workflow",
						UserId:     "user",
						Cursor:     "page",
						Filters: &jobspb.SearchJobLogsFilters{
							Stream:  jobspb.LogStream_LOG_STREAM_ALL,
							Message: "query",
						},
					})
			} else {
				repo.EXPECT().GetJobLogs(gomock.Any(), "job", "workflow", "user", "page", gomock.Any(), gomock.Any()).Return(response, "RUNNING", nil)
				result,
					err = svc.GetJobLogs(t.Context(),
					&jobspb.GetJobLogsRequest{
						Id:         "job",
						WorkflowId: "workflow",
						UserId:     "user",
						Cursor:     "page",
						Filters:    &jobspb.GetJobLogsFilters{Stream: jobspb.LogStream_LOG_STREAM_ALL},
					})
			}
			require.NoError(t, err)
			require.Same(t, response, result)
		})
	}
}

// logContractCache makes asynchronous completion observable without sleeps.
type logContractCache struct {
	err  error
	done chan struct{}
}

func (*logContractCache) Get(context.Context, string, any) (any, error) {
	return nil, errors.New("cache miss")
}

func (c *logContractCache) Set(context.Context, string, any, time.Duration) error {
	defer close(c.done)
	return c.err
}

func TestStableLogPagesReportCacheWriteFailuresAndSuccess(t *testing.T) {
	// Debug logging exercises the successful asynchronous write acknowledgement.
	for _, search := range []bool{false, true} {
		for _, writeErr := range []error{nil, errors.New("cache unavailable")} {
			synctest.Test(t, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := jobsmock.NewMockRepository(ctrl)
				cache := &logContractCache{err: writeErr, done: make(chan struct{})}
				core, entries := observer.New(zap.DebugLevel)
				provider := sdklog.NewLoggerProvider()
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
					defer cancel()
					require.NoError(t, provider.Shutdown(ctx))
				})
				ctx, logger := loggerpkg.Init(t.Context(), "log-contract", provider)
				*logger = *logger.WithOptions(zap.WrapCore(func(zapcore.Core) zapcore.Core { return core }))
				svc := New(validator.New(), repo, cache)
				response := &jobsmodel.GetJobLogsResponse{ID: "job", WorkflowID: "workflow", Cursor: "next"}
				var result *jobsmodel.GetJobLogsResponse
				var err error
				if search {
					repo.EXPECT().SearchJobLogs(gomock.Any(), "job", "workflow", "user", "page", gomock.Any(), gomock.Any()).Return(response, "RUNNING", nil)
					result,
						err = svc.SearchJobLogs(ctx,
						&jobspb.SearchJobLogsRequest{
							Id:         "job",
							WorkflowId: "workflow",
							UserId:     "user",
							Cursor:     "page",
							Filters: &jobspb.SearchJobLogsFilters{
								Stream:  jobspb.LogStream_LOG_STREAM_ALL,
								Message: "query",
							},
						})
				} else {
					repo.EXPECT().GetJobLogs(gomock.Any(), "job", "workflow", "user", "page", gomock.Any(), gomock.Any()).Return(response, "RUNNING", nil)
					result,
						err = svc.GetJobLogs(ctx,
						&jobspb.GetJobLogsRequest{
							Id:         "job",
							WorkflowId: "workflow",
							UserId:     "user",
							Cursor:     "page",
							Filters:    &jobspb.GetJobLogsFilters{Stream: jobspb.LogStream_LOG_STREAM_ALL},
						})
				}
				require.NoError(t, err)
				require.Same(t, response, result)
				synctest.Wait()
				select {
				case <-cache.done:
				default:
					t.Fatal("cache write did not finish")
				}
				require.Len(t, entries.All(), 1)
				if writeErr != nil {
					require.Equal(t, zap.WarnLevel, entries.All()[0].Level)
					require.Equal(t, writeErr.Error(), entries.All()[0].ContextMap()["error"])
				} else {
					require.Equal(t, zap.DebugLevel, entries.All()[0].Level)
				}
			})
		}
	}
}
