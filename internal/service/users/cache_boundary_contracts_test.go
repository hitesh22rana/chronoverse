package users_test

import (
	"context"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	usersmodel "github.com/hitesh22rana/chronoverse/internal/model/users"
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
