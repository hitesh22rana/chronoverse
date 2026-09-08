//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package notifications

import (
	"context"
	"encoding/base64"
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
func newTestRepository(t *testing.T, preference string, fetchLimit int) *Repository {
	t.Helper()

	ctrl := gomock.NewController(t)
	_auth := authmock.NewMockIAuth(ctrl)
	_auth.EXPECT().
		IssueToken(gomock.Any(), gomock.Any(), gomock.Any()).
		Return("test-token", nil).
		AnyTimes()

	return New(&Config{FetchLimit: fetchLimit}, _auth, testkit.Postgres(t), &Services{UsersService: &fakeUsersService{preference: preference}})
}

func TestIntegrationCreateListMarkReadNotifications(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository(t, "ALERTS", 20)

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
	repo := newTestRepository(t, "NONE", 20)

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

func TestIntegrationListNotificationsHonorsAllPreference(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository(t, "ALL", 20)

	userID := seedUser(ctx, t, testkit.Postgres(t))

	kinds := []string{
		notificationsmodel.KindWebAlert.ToString(),
		notificationsmodel.KindWebInfo.ToString(),
		notificationsmodel.KindWebError.ToString(),
	}
	for i, kind := range kinds {
		if _, err := repo.CreateNotification(ctx, userID, kind, `{"message":"hello"}`, fmt.Sprintf("idem-all-%s-%d", t.Name(), i)); err != nil {
			t.Fatalf("CreateNotification(%s): %v", kind, err)
		}
	}

	list, err := repo.ListNotifications(ctx, userID, "")
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(list.Notifications) != len(kinds) {
		t.Fatalf("ListNotifications returned %d notifications, want %d", len(list.Notifications), len(kinds))
	}
	if list.Cursor != "" {
		t.Fatalf("expected no cursor, got %q", list.Cursor)
	}
}

func TestIntegrationListNotificationsPaginatesWithoutDuplicatesOrSkips(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository(t, "ALERTS", 2)

	userID := seedUser(ctx, t, testkit.Postgres(t))

	const total = 5
	want := make(map[string]struct{}, total)
	for i := range total {
		id, err := repo.CreateNotification(ctx, userID, notificationsmodel.KindWebAlert.ToString(), fmt.Sprintf(`{"message":"alert-%d"}`, i), fmt.Sprintf("idem-page-%s-%d", t.Name(), i))
		if err != nil {
			t.Fatalf("CreateNotification: %v", err)
		}
		want[id] = struct{}{}
	}

	var pages int
	cursor := ""
	for {
		page, err := repo.ListNotifications(ctx, userID, cursor)
		if err != nil {
			t.Fatalf("ListNotifications: %v", err)
		}
		pages++
		if len(page.Notifications) > 2 {
			t.Fatalf("page returned %d notifications, want at most 2", len(page.Notifications))
		}
		for _, n := range page.Notifications {
			if _, ok := want[n.ID]; !ok {
				t.Fatalf("unexpected notification id %q", n.ID)
			}
			delete(want, n.ID)
		}
		if page.Cursor == "" {
			break
		}
		decoded, err := base64.StdEncoding.DecodeString(page.Cursor)
		if err != nil {
			t.Fatalf("decode cursor: %v", err)
		}
		cursor = string(decoded)
		if pages > total {
			t.Fatal("pagination did not terminate")
		}
	}

	if len(want) != 0 {
		t.Fatalf("pagination skipped %d notifications", len(want))
	}
	if pages != 3 {
		t.Fatalf("pagination took %d pages, want 3", pages)
	}
}

func TestIntegrationListNotificationsRejectsMalformedCursor(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository(t, "ALERTS", 20)

	userID := seedUser(ctx, t, testkit.Postgres(t))

	cursors := map[string]string{
		"missing delimiter": "not-a-cursor",
		"malformed id":      "not-a-uuid$2024-01-01T00:00:00Z",
		"malformed time":    "123e4567-e89b-12d3-a456-426614174000$not-a-time",
	}
	for name, cursor := range cursors {
		if _, err := repo.ListNotifications(ctx, userID, cursor); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("%s: ListNotifications err code = %v, want %v", name, status.Code(err), codes.InvalidArgument)
		}
	}
}
