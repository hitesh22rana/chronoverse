//nolint:testpackage // Checks private invalidation through public service operations.
package workflows

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/require"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	model "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	mocks "github.com/hitesh22rana/chronoverse/internal/service/workflows/mock"
	pb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

const lifecyclePayload = `{"endpoint":"https://example.com"}`

type lifecycleContextKey struct{}

func TestWorkflowPayloadAndBuildValidationPrecedesRepository(t *testing.T) {
	t.Parallel()
	service := New(validator.New(), mocks.NewMockRepository(gomock.NewController(t)), nil)
	for _, kind := range []string{"HEARTBEAT", "CONTAINER", "UNKNOWN"} {
		result, err := service.CreateWorkflow(t.Context(), &pb.CreateWorkflowRequest{
			UserId: "user", Name: "workflow", Payload: `{}`, Kind: kind, Interval: 60,
			MaxConsecutiveJobFailuresAllowed: 3, IdempotencyKey: "create",
		})
		require.Empty(t, result)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	}
	err := service.UpdateWorkflow(t.Context(), &pb.UpdateWorkflowRequest{
		Id: "workflow", UserId: "user", Name: "updated", Payload: `{}`, Interval: 60,
		MaxConsecutiveJobFailuresAllowed: 3, IdempotencyKey: "update",
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	err = service.UpdateWorkflowBuildStatus(t.Context(), &pb.UpdateWorkflowBuildStatusRequest{
		Id: "workflow", UserId: "user", BuildStatus: "INVALID", Generation: 1,
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestWorkflowReadCancellationUsesActualCallerDespiteWrappedRedisError(t *testing.T) {
	t.Parallel()
	for _, deadline := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		want := codes.Canceled
		if deadline {
			cancel()
			ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			want = codes.DeadlineExceeded
		}
		cancel()
		ctrl := gomock.NewController(t)
		cache := mocks.NewMockCache(ctrl)
		cache.EXPECT().Get(gomock.Any(), "workflow:user:workflow", gomock.Any()).DoAndReturn(
			func(got context.Context, _ string, _ any) (any, error) {
				require.Error(t, got.Err())
				return nil, status.Errorf(codes.Internal, "failed to get value from redis: %v", got.Err())
			},
		)
		result, err := New(validator.New(), mocks.NewMockRepository(ctrl), cache).GetWorkflow(ctx, &pb.GetWorkflowRequest{Id: "workflow", UserId: "user"})
		require.Nil(t, result)
		require.Equal(t, want, status.Code(err))
	}
}

func lifecycleLogger(t *testing.T) (context.Context, *observer.ObservedLogs) {
	t.Helper()
	provider := sdklog.NewLoggerProvider()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
		defer cancel()
		require.NoError(t, provider.Shutdown(ctx))
	})
	ctx, logger := loggerpkg.Init(t.Context(), "workflow-lifecycle-contract", provider)
	core, entries := observer.New(zap.DebugLevel)
	*logger = *logger.WithOptions(zap.WrapCore(func(zapcore.Core) zapcore.Core { return core }))
	return context.WithValue(ctx, lifecycleContextKey{}, "caller"), entries
}

func TestWorkflowMutationsBoundCacheWorkAndPreserveResults(t *testing.T) {
	for _, operation := range []string{"create", "read", "update", "build", "create replay"} {
		for _, failure := range []bool{false, true} {
			t.Run(operation+map[bool]string{false: " success", true: " cache failure"}[failure], func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ctrl := gomock.NewController(t)
					repo := mocks.NewMockRepository(ctrl)
					cache := mocks.NewMockCache(ctrl)
					service := New(validator.New(), repo, cache)
					parent, entries := lifecycleLogger(t)
					ctx, cancel := context.WithCancel(parent)
					defer cancel()
					checkContext := func(bg context.Context) {
						t.Helper()
						require.NoError(t, bg.Err())
						require.Equal(t, "caller", bg.Value(lifecycleContextKey{}))
						deadline, ok := bg.Deadline()
						require.True(t, ok)
						require.Positive(t, time.Until(deadline))
						require.LessOrEqual(t, time.Until(deadline), cacheTimeout)
					}
					var cause error
					if failure {
						cause = errors.New("cache unavailable")
					}
					cache.EXPECT().DeleteByPattern(gomock.Any(), "workflows:user:*").DoAndReturn(
						func(bg context.Context, _ string) (int64, error) { checkContext(bg); return 4, cause },
					)
					response := &model.GetWorkflowResponse{ID: "workflow", Name: "workflow", Kind: "HEARTBEAT", Payload: lifecyclePayload, Interval: 60}
					if operation == "create replay" {
						response.IdempotencyReplay = true
					}
					if operation == "create" || operation == "read" {
						cache.EXPECT().Set(gomock.Any(), "workflow:user:workflow", response, gomock.Any()).DoAndReturn(
							func(bg context.Context, _ string, value any, expiration time.Duration) error {
								checkContext(bg)
								require.Same(t, response, value)
								require.Positive(t, expiration)
								return cause
							},
						)
					} else if operation != "create replay" {
						cache.EXPECT().Delete(gomock.Any(), "workflow:user:workflow").DoAndReturn(
							func(bg context.Context, _ string) error { checkContext(bg); return cause },
						)
					}
					switch operation {
					case "create", "create replay":
						request := &pb.CreateWorkflowRequest{UserId: "user", Name: "workflow", Payload: lifecyclePayload, Kind: "HEARTBEAT", Interval: 60, MaxConsecutiveJobFailuresAllowed: 3, IdempotencyKey: "create"}
						original := proto.Clone(request)
						repo.EXPECT().CreateWorkflow(gomock.Any(), "user", "workflow", lifecyclePayload, "HEARTBEAT", int32(60), int32(3), false, "create").Return(response, nil)
						result, err := service.CreateWorkflow(ctx, request)
						require.NoError(t, err)
						require.Equal(t, "workflow", result)
						require.True(t, proto.Equal(original, request))
					case "read":
						cache.EXPECT().Get(gomock.Any(), "workflow:user:workflow", gomock.Any()).Return(nil, status.Error(codes.NotFound, "miss"))
						repo.EXPECT().GetWorkflow(gomock.Any(), "workflow", "user").Return(response, nil)
						result, err := service.GetWorkflow(ctx, &pb.GetWorkflowRequest{Id: "workflow", UserId: "user"})
						require.NoError(t, err)
						require.Same(t, response, result)
					case "update":
						repo.EXPECT().UpdateWorkflow(gomock.Any(), "workflow", "user", "updated", lifecyclePayload, int32(60), int32(3), "update").Return(nil)
						request := &pb.UpdateWorkflowRequest{
							Id: "workflow", UserId: "user", Name: "updated", Payload: lifecyclePayload,
							Interval: 60, MaxConsecutiveJobFailuresAllowed: 3, IdempotencyKey: "update",
						}
						require.NoError(t, service.UpdateWorkflow(ctx, request))
					case "build":
						repo.EXPECT().UpdateWorkflowBuildStatus(gomock.Any(), "workflow", "user", "STARTED", int64(1), "", "").Return(nil)
						require.NoError(t, service.UpdateWorkflowBuildStatus(ctx, &pb.UpdateWorkflowBuildStatusRequest{Id: "workflow", UserId: "user", BuildStatus: "STARTED", Generation: 1}))
					}
					cancel()
					synctest.Wait()
					wantCount := 2
					if operation == "create replay" {
						wantCount = 1
					}
					require.Len(t, entries.All(), wantCount)
					for _, entry := range entries.All() {
						want := zap.DebugLevel
						if failure {
							want = zap.WarnLevel
							require.Equal(t, cause.Error(), entry.ContextMap()["error"])
						}
						require.Equal(t, want, entry.Level)
					}
				})
			})
		}
	}
}

func TestMissingWorkflowCacheIsSuccessfulInvalidation(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	cache := mocks.NewMockCache(ctrl)
	cache.EXPECT().Delete(gomock.Any(), "workflow:user:workflow").Return(status.Error(codes.NotFound, "expired"))
	core, entries := observer.New(zap.DebugLevel)
	New(validator.New(), nil, cache).invalidateWorkflowCache(t.Context(), "workflow", "user", zap.New(core))
	require.Len(t, entries.All(), 1)
	require.Equal(t, zap.DebugLevel, entries.All()[0].Level)
}
