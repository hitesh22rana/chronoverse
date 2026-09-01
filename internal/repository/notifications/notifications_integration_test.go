//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package notifications

import (
	"context"
	"fmt"
	"testing"

	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	userspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"

	notificationsmodel "github.com/hitesh22rana/chronoverse/internal/model/notifications"
	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestMain(m *testing.M) {
	testkit.Run(m, testkit.WithPostgres())
}

// fakeUsersService is a minimal userspb.UsersServiceClient stub that always
// reports the given notification preference.
type fakeUsersService struct {
	preference string
}

func (f *fakeUsersService) RegisterUser(context.Context, *userspb.RegisterUserRequest, ...grpc.CallOption) (*userspb.RegisterUserResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeUsersService) LoginUser(context.Context, *userspb.LoginUserRequest, ...grpc.CallOption) (*userspb.LoginUserResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeUsersService) GetUser(context.Context, *userspb.GetUserRequest, ...grpc.CallOption) (*userspb.GetUserResponse, error) {
	return &userspb.GetUserResponse{NotificationPreference: f.preference}, nil
}

func (f *fakeUsersService) UpdateUser(context.Context, *userspb.UpdateUserRequest, ...grpc.CallOption) (*userspb.UpdateUserResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

// newTestRepository builds a notifications repository against the shared
// PostgreSQL container.
func newTestRepository(t *testing.T, preference string) *Repository {
	t.Helper()

	ctrl := gomock.NewController(t)
	_auth := authmock.NewMockIAuth(ctrl)
	_auth.EXPECT().
		IssueToken(gomock.Any(), gomock.Any(), gomock.Any()).
		Return("test-token", nil).
		AnyTimes()

	return New(&Config{FetchLimit: 20}, _auth, testkit.Postgres(t), &Services{UsersService: &fakeUsersService{preference: preference}})
}

func TestIntegrationCreateListMarkReadNotifications(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository(t, "ALERTS")

	userID := seedUser(ctx, t, testkit.Postgres(t))

	// Create notifications of different kinds.
	alertID, err := repo.CreateNotification(ctx, userID, notificationsmodel.KindWebAlert.ToString(), `{"message":"disk full"}`, "idem-alert-"+t.Name())
	if err != nil {
		t.Fatalf("CreateNotification(alert): %v", err)
	}
	if alertID == "" {
		t.Fatal("expected a notification id")
	}

	// The same idempotency key returns the same notification.
	replayedID, err := repo.CreateNotification(ctx, userID, notificationsmodel.KindWebAlert.ToString(), `{"message":"disk full"}`, "idem-alert-"+t.Name())
	if err != nil {
		t.Fatalf("CreateNotification(idempotent): %v", err)
	}
	if replayedID != alertID {
		t.Fatalf("idempotent replay id = %q, want %q", replayedID, alertID)
	}

	if _, infoErr := repo.CreateNotification(ctx, userID, notificationsmodel.KindWebInfo.ToString(), `{"message":"info"}`, "idem-info-"+t.Name()); infoErr != nil {
		t.Fatalf("CreateNotification(info): %v", infoErr)
	}
	if _, errorErr := repo.CreateNotification(ctx, userID, notificationsmodel.KindWebError.ToString(), `{"message":"boom"}`, "idem-error-"+t.Name()); errorErr != nil {
		t.Fatalf("CreateNotification(error): %v", errorErr)
	}

	// ListNotifications only returns kinds matching the ALERTS preference,
	// i.e. web_alert, while the info notification stays hidden.
	list, err := repo.ListNotifications(ctx, userID, "")
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(list.Notifications) != 1 {
		t.Fatalf("ListNotifications returned %d notifications, want 1", len(list.Notifications))
	}
	if list.Notifications[0].ID != alertID {
		t.Fatalf("notification id = %q, want %q", list.Notifications[0].ID, alertID)
	}

	// MarkNotificationsRead clears the unread list.
	if markErr := repo.MarkNotificationsRead(ctx, []string{alertID}, userID); markErr != nil {
		t.Fatalf("MarkNotificationsRead: %v", markErr)
	}
	after, err := repo.ListNotifications(ctx, userID, "")
	if err != nil {
		t.Fatalf("ListNotifications after read: %v", err)
	}
	if len(after.Notifications) != 0 {
		t.Fatalf("ListNotifications after read returned %d notifications, want 0", len(after.Notifications))
	}
}

func TestIntegrationListNotificationsHonorsNonePreference(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository(t, "NONE")

	userID := seedUser(ctx, t, testkit.Postgres(t))
	if _, err := repo.CreateNotification(ctx, userID, notificationsmodel.KindWebAlert.ToString(), `{"message":"ignored"}`, "idem-none-"+t.Name()); err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}

	list, err := repo.ListNotifications(ctx, userID, "")
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(list.Notifications) != 0 {
		t.Fatalf("ListNotifications with NONE preference returned %d notifications, want 0", len(list.Notifications))
	}
}

// seedUser inserts a fresh user and returns its id. Notifications reference
// users through the notifications.user_id foreign key, so the user must exist.
func seedUser(ctx context.Context, t *testing.T, pg *postgres.Postgres) string {
	t.Helper()

	return testkit.SeedUser(ctx, t, pg, fmt.Sprintf("notifications-%s@chronoverse.test", t.Name()))
}
