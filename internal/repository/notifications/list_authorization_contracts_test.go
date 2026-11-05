//nolint:testpackage // Verifies repository guards before any database access.
package notifications

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
	userspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"
)

type notificationListUsersClient struct {
	userspb.UsersServiceClient
	get func(context.Context, *userspb.GetUserRequest) (*userspb.GetUserResponse, error)
}

func (c *notificationListUsersClient) GetUser(ctx context.Context, req *userspb.GetUserRequest, _ ...grpc.CallOption) (*userspb.GetUserResponse, error) {
	return c.get(ctx, req)
}

func TestListNotificationsPreservesAuthorizationAndUserFailures(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"authorization", "user lookup"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			const owner = "00000000-0000-4000-8000-000000000041"
			failure := status.Error(codes.Unavailable, "upstream unavailable")
			issuer := authmock.NewMockIAuth(gomock.NewController(t))
			issuer.EXPECT().IssueToken(gomock.Any(), authSubject, auth.ServiceNameUsers).
				DoAndReturn(func(ctx context.Context, _ string, _ ...string) (string, error) {
					role, err := auth.ExtractRoleFromContext(ctx)
					if err != nil || role != auth.RoleAdmin.String() {
						t.Errorf("internal role=%q error=%v", role, err)
					}
					if stage == "authorization" {
						return "", failure
					}
					return "notification-list-token", nil
				})
			calls := 0
			users := &notificationListUsersClient{get: func(ctx context.Context, req *userspb.GetUserRequest) (*userspb.GetUserResponse, error) {
				calls++
				if req.GetId() != owner {
					t.Errorf("lookup user=%q, want %q", req.GetId(), owner)
				}
				md, ok := metadata.FromOutgoingContext(ctx)
				if !ok || len(md.Get("authorization")) != 1 || md.Get("authorization")[0] != "Bearer notification-list-token" {
					t.Errorf("authorization metadata=%v", md)
				}
				return nil, failure
			}}
			// A database must never be needed after either upstream failure.
			repo := New(&Config{FetchLimit: 20}, issuer, nil, &Services{UsersService: users})
			response, err := repo.ListNotifications(t.Context(), owner, "")
			if response != nil || !errors.Is(err, failure) {
				t.Fatalf("response=%+v error=%v, want unchanged upstream failure", response, err)
			}
			wantCalls := 1
			if stage == "authorization" {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatalf("user lookups=%d, want %d", calls, wantCalls)
			}
		})
	}
}

func TestListNotificationsRejectsUnsupportedRPCPreference(t *testing.T) {
	t.Parallel()
	const owner = "00000000-0000-4000-8000-000000000041"
	issuer := authmock.NewMockIAuth(gomock.NewController(t))
	issuer.EXPECT().IssueToken(gomock.Any(), authSubject, auth.ServiceNameUsers).Return("notification-list-token", nil)
	calls := 0
	users := &notificationListUsersClient{get: func(_ context.Context, req *userspb.GetUserRequest) (*userspb.GetUserResponse, error) {
		calls++
		if req.GetId() != owner {
			t.Errorf("lookup user=%q, want %q", req.GetId(), owner)
		}
		// Reject a malformed peer response; the current database enum cannot store it.
		return &userspb.GetUserResponse{NotificationPreference: "UNSUPPORTED"}, nil
	}}
	repo := New(&Config{FetchLimit: 20}, issuer, nil, &Services{UsersService: users})
	response, err := repo.ListNotifications(t.Context(), owner, "")
	if response != nil || status.Code(err) != codes.InvalidArgument {
		t.Fatalf("response=%+v error=%v, want nil/InvalidArgument", response, err)
	}
	if status.Convert(err).Message() != "invalid notification preference: UNSUPPORTED" {
		t.Fatalf("unexpected rejection: %v", err)
	}
	if calls != 1 {
		t.Fatalf("user lookups=%d, want 1", calls)
	}
}
