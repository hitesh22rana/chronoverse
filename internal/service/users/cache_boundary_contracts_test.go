package users_test

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

	usersmodel "github.com/hitesh22rana/chronoverse/internal/model/users"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/service/users"
	usersmock "github.com/hitesh22rana/chronoverse/internal/service/users/mock"
	userpb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"
)

// unavailableCache reproduces the Redis store's gRPC status wrapping.
type unavailableCache struct{ err error }

func (c *unavailableCache) Get(context.Context, string, any) (any, error) { return nil, c.err }

func (*unavailableCache) Set(context.Context, string, any, time.Duration) error { return nil }

func (*unavailableCache) Delete(context.Context, string) error { return nil }

func (*unavailableCache) DeleteByPattern(context.Context, string) (int64, error) { return 0, nil }

func TestGetUserCacheCancellationDoesNotReachRepository(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		name, wantCode := "deadline", codes.DeadlineExceeded
		if canceled {
			name, wantCode = "canceled", codes.Canceled
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := usersmock.NewMockRepository(ctrl)
				var ctx context.Context
				var cancel context.CancelFunc
				if canceled {
					ctx, cancel = context.WithCancel(t.Context())
				} else {
					ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
				}
				cancel()
				// The real store wraps failures in a gRPC status, so the cache error
				// never matched errors.Is; the caller's context carries the cause.
				cache := &unavailableCache{err: status.Errorf(codes.Internal, "failed to get value from redis: %v", ctx.Err())}
				got, err := users.New(validator.New(), repo, cache).
					GetUser(ctx, &userpb.GetUserRequest{Id: "user-id"})
				if got != nil || status.Code(err) != wantCode {
					t.Fatalf("response=%+v err=%v, want %s", got, err, wantCode)
				}

				synctest.Wait()
			})
		})
	}
}

func TestGetUserCacheFailureWithLiveContextFallsBackToRepository(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := usersmock.NewMockRepository(ctrl)
	want := &usersmodel.GetUserResponse{ID: "user-id", Email: "user@example.com"}
	repo.EXPECT().GetUser(gomock.Any(), "user-id").Return(want, nil)
	// A cache-internal timeout is wrapped in a gRPC status while the caller's
	// context stays healthy, so the read must fall back to the repository.
	cache := &unavailableCache{err: status.Errorf(codes.Internal, "failed to get value from redis: %v", context.DeadlineExceeded)}
	got, err := users.New(validator.New(), repo, cache).GetUser(t.Context(), &userpb.GetUserRequest{Id: "user-id"})
	if err != nil || got != want {
		t.Fatalf("response=%+v err=%v", got, err)
	}
}

// writingCache reports a miss and exposes the detached write outcome.
type writingCache struct {
	err  error
	done chan struct{}
}

func (*writingCache) Get(context.Context, string, any) (any, error) {
	return nil, status.Error(codes.NotFound, "miss")
}

func (c *writingCache) Set(context.Context, string, any, time.Duration) error {
	defer close(c.done)
	return c.err
}

func (*writingCache) Delete(context.Context, string) error { return nil }

func (*writingCache) DeleteByPattern(context.Context, string) (int64, error) { return 0, nil }

func TestGetUserCacheWriteOutcomeIsLogged(t *testing.T) {
	for _, writeErr := range []error{nil, errors.New("cache unavailable")} {
		name, wantLevel := "success", zap.DebugLevel
		if writeErr != nil {
			name, wantLevel = "failure", zap.WarnLevel
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := usersmock.NewMockRepository(ctrl)
				cache := &writingCache{err: writeErr, done: make(chan struct{})}
				core, entries := observer.New(zap.DebugLevel)
				provider := sdklog.NewLoggerProvider()
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
					defer cancel()
					require.NoError(t, provider.Shutdown(ctx))
				})
				ctx, logger := loggerpkg.Init(t.Context(), "users-cache-write", provider)
				*logger = *logger.WithOptions(zap.WrapCore(func(zapcore.Core) zapcore.Core { return core }))
				want := &usersmodel.GetUserResponse{ID: "user-id"}
				repo.EXPECT().GetUser(gomock.Any(), "user-id").Return(want, nil)
				got, err := users.New(validator.New(), repo, cache).GetUser(ctx, &userpb.GetUserRequest{Id: "user-id"})
				require.NoError(t, err)
				require.Same(t, want, got)
				synctest.Wait()
				select {
				case <-cache.done:
				default:
					t.Fatal("cache write did not finish")
				}
				require.Len(t, entries.All(), 1)
				require.Equal(t, wantLevel, entries.All()[0].Level)
				if writeErr != nil {
					require.Equal(t, writeErr.Error(), entries.All()[0].ContextMap()["error"])
				}
			})
		})
	}
}
