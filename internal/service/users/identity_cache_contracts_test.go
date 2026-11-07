//nolint:testpackage // Verifies error normalization and detached cache work through service operations.
package users

import (
	"context"
	"errors"
	"fmt"
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

	model "github.com/hitesh22rana/chronoverse/internal/model/users"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	mocks "github.com/hitesh22rana/chronoverse/internal/service/users/mock"
	pb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"
)

const (
	identityUserID   = "550e8400-e29b-41d4-a716-446655440000"
	identityCacheKey = "user:550e8400-e29b-41d4-a716-446655440000"
	// #nosec G101 -- Public password fixture, never a deployed credential.
	identityPassword = "9gR!bQ7@zL2#vN5$"
)

type identityContextKey struct{}

func identityLogger(t *testing.T) (context.Context, *observer.ObservedLogs) {
	t.Helper()
	provider := sdklog.NewLoggerProvider()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
		defer cancel()
		require.NoError(t, provider.Shutdown(ctx))
	})
	ctx, logger := loggerpkg.Init(t.Context(), "identity-boundary", provider)
	core, entries := observer.New(zap.DebugLevel)
	*logger = *logger.WithOptions(zap.WrapCore(func(zapcore.Core) zapcore.Core { return core }))
	return context.WithValue(ctx, identityContextKey{}, "caller"), entries
}

func TestIdentityCacheOutcomesPreserveAuthenticationAndCallerContract(t *testing.T) {
	for _, operation := range []string{"register", "login", "read", "update"} {
		for _, failure := range []bool{false, true} {
			t.Run(operation+map[bool]string{false: " success", true: " cache failure"}[failure], func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ctrl := gomock.NewController(t)
					repo := mocks.NewMockRepository(ctrl)
					cache := mocks.NewMockCache(ctrl)
					service := New(validator.New(), repo, cache)
					parent, entries := identityLogger(t)
					ctx, cancel := context.WithCancel(parent)
					defer cancel()
					response := &model.GetUserResponse{ID: identityUserID, Email: "person@example.com", NotificationPreference: "ALL"}
					var cause error
					if failure {
						cause = errors.New("cache unavailable")
					}
					var (
						setCalled, setDeadlineOK, setDeadlineWithinTimeout bool
						setErr                                             error
						setContextValue, setValue                          any
						setExpiration                                      time.Duration
					)
					if operation == "update" {
						cache.EXPECT().Delete(gomock.Any(), identityCacheKey).DoAndReturn(func(got context.Context, _ string) error {
							require.Equal(t, ctx.Done(), got.Done())
							require.Equal(t, "caller", got.Value(identityContextKey{}))
							return cause
						})
					} else {
						cache.EXPECT().Set(gomock.Any(), identityCacheKey, response, gomock.Any()).DoAndReturn(func(bg context.Context, _ string, value any, expiration time.Duration) error {
							// This callback runs on the service's background cache goroutine,
							// so record values and let the test goroutine assert after Wait.
							setCalled = true
							setErr = bg.Err()
							setContextValue = bg.Value(identityContextKey{})
							setValue = value
							setExpiration = expiration
							var deadline time.Time
							deadline, setDeadlineOK = bg.Deadline()
							setDeadlineWithinTimeout = time.Until(deadline) > 0 && time.Until(deadline) <= cacheTimeout
							return cause
						})
					}
					switch operation {
					case "register":
						request := &pb.RegisterUserRequest{Email: " Person@Example.COM ", Password: identityPassword, IdempotencyKey: "registration"}
						original := proto.Clone(request)
						repo.EXPECT().RegisterUser(gomock.Any(), "person@example.com", identityPassword, "registration").Return(response, "private-token", nil)
						id, token, err := service.RegisterUser(ctx, request)
						require.NoError(t, err)
						require.Equal(t, identityUserID, id)
						require.Equal(t, "private-token", token)
						require.True(t, proto.Equal(original, request))
					case "login":
						request := &pb.LoginUserRequest{Email: " Person@Example.COM ", Password: identityPassword}
						original := proto.Clone(request)
						repo.EXPECT().LoginUser(gomock.Any(), "person@example.com", identityPassword).Return(response, "private-token", nil)
						id, token, err := service.LoginUser(ctx, request)
						require.NoError(t, err)
						require.Equal(t, identityUserID, id)
						require.Equal(t, "private-token", token)
						require.True(t, proto.Equal(original, request))
					case "read":
						cache.EXPECT().Get(gomock.Any(), identityCacheKey, gomock.Any()).Return(nil, status.Error(codes.NotFound, "expired"))
						repo.EXPECT().GetUser(gomock.Any(), identityUserID).Return(response, nil)
						result, err := service.GetUser(ctx, &pb.GetUserRequest{Id: identityUserID})
						require.NoError(t, err)
						require.Same(t, response, result)
					case "update":
						repo.EXPECT().UpdateUser(gomock.Any(), identityUserID, "ALL").Return(nil)
						err := service.UpdateUser(ctx, &pb.UpdateUserRequest{Id: identityUserID, NotificationPreference: "ALL"})
						if failure {
							require.Equal(t, codes.Internal, status.Code(err))
							require.Contains(t, err.Error(), cause.Error())
						} else {
							require.NoError(t, err)
						}
					}
					cancel()
					synctest.Wait()
					if operation != "update" {
						require.True(t, setCalled)
						require.NoError(t, setErr)
						require.Equal(t, "caller", setContextValue)
						require.Same(t, response, setValue)
						require.Positive(t, setExpiration)
						require.True(t, setDeadlineOK)
						require.True(t, setDeadlineWithinTimeout)
					}
					require.Len(t, entries.All(), 1)
					want := zap.DebugLevel
					if failure {
						want = zap.WarnLevel
						require.Equal(t, cause.Error(), entries.All()[0].ContextMap()["error"])
					}
					require.Equal(t, want, entries.All()[0].Level)
					details := fmt.Sprint(entries.All()[0].ContextMap())
					require.NotContains(t, details, identityPassword)
					require.NotContains(t, details, "private-token")
				})
			})
		}
	}
}

func TestIdentityReadCancellationDoesNotFallThroughWrappedRedisFailure(t *testing.T) {
	t.Parallel()
	for _, expired := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		want := codes.Canceled
		if expired {
			cancel()
			ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			want = codes.DeadlineExceeded
		}
		cancel()
		ctrl := gomock.NewController(t)
		repo := mocks.NewMockRepository(ctrl)
		cache := mocks.NewMockCache(ctrl)
		cache.EXPECT().Get(gomock.Any(), identityCacheKey, gomock.Any()).DoAndReturn(func(got context.Context, _ string, _ any) (any, error) {
			require.Error(t, got.Err())
			return nil, status.Errorf(codes.Internal, "failed to get value from redis: %v", got.Err())
		})
		result, err := New(validator.New(), repo, cache).GetUser(ctx, &pb.GetUserRequest{Id: identityUserID})
		require.Nil(t, result)
		require.Equal(t, want, status.Code(err))
	}
}

func TestMissingUserCacheDoesNotFailCompletedUpdate(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockRepository(ctrl)
	cache := mocks.NewMockCache(ctrl)
	repo.EXPECT().UpdateUser(gomock.Any(), identityUserID, "NONE").Return(nil)
	cache.EXPECT().Delete(gomock.Any(), identityCacheKey).Return(status.Error(codes.NotFound, "expired"))
	ctx, entries := identityLogger(t)
	require.NoError(t, New(validator.New(), repo, cache).UpdateUser(ctx, &pb.UpdateUserRequest{Id: identityUserID, NotificationPreference: "NONE"}))
	require.Len(t, entries.All(), 1)
	require.Equal(t, zap.DebugLevel, entries.All()[0].Level)
}

func TestLoginErrorNormalizationMasksIdentityLookupOnly(t *testing.T) {
	t.Parallel()
	for _, code := range []codes.Code{codes.NotFound, codes.InvalidArgument} {
		err := normalizeLoginError(status.Error(code, "private identity detail"))
		require.Equal(t, codes.Unauthenticated, status.Code(err))
		require.Equal(t, "invalid email or password", status.Convert(err).Message())
	}
	for _, code := range []codes.Code{codes.Internal, codes.Unavailable, codes.Unauthenticated} {
		original := status.Error(code, "original failure")
		require.Same(t, original, normalizeLoginError(original))
	}
	require.NoError(t, normalizeLoginError(nil))
}
