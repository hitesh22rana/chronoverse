//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package users

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestMain(m *testing.M) {
	testkit.Run(m, testkit.WithPostgres())
}

// newTestRepository builds a users repository against the shared PostgreSQL
// container with a stub IAuth that always issues a token.
func newTestRepository(t *testing.T) *Repository {
	t.Helper()

	ctrl := gomock.NewController(t)
	_auth := authmock.NewMockIAuth(ctrl)
	_auth.EXPECT().
		IssueToken(gomock.Any(), gomock.Any()).
		Return("test-token", nil).
		AnyTimes()

	return New(_auth, testkit.Postgres(t))
}

//nolint:gocyclo // The lifecycle test exercises register, login, get and update in one flow.
func TestIntegrationRegisterLoginGetUpdateUser(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository(t)

	email := "integration-" + t.Name() + "@chronoverse.test"
	password := "s3cure-password"
	idempotencyKey := "register-" + t.Name()

	// Register a new user.
	registered, token, err := repo.RegisterUser(ctx, email, password, idempotencyKey)
	if err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	if registered.ID == "" {
		t.Fatal("expected a user id")
	}
	if registered.Email != email {
		t.Fatalf("email = %q, want %q", registered.Email, email)
	}
	if registered.NotificationPreference != "ALERTS" {
		t.Fatalf("notification_preference = %q, want %q", registered.NotificationPreference, "ALERTS")
	}
	if token != "test-token" {
		t.Fatalf("token = %q, want %q", token, "test-token")
	}

	// Replaying the same idempotency key with the same credentials returns the
	// same user instead of creating a duplicate row.
	replayed, replayToken, replayErr := repo.RegisterUser(ctx, email, password, idempotencyKey)
	if replayErr != nil {
		t.Fatalf("RegisterUser (idempotent replay): %v", replayErr)
	}
	if replayed.ID != registered.ID {
		t.Fatalf("idempotent replay id = %q, want %q", replayed.ID, registered.ID)
	}
	if replayToken != "test-token" {
		t.Fatalf("replay token = %q, want %q", replayToken, "test-token")
	}

	// Reusing the idempotency key with different credentials conflicts.
	if _, _, conflictErr := repo.RegisterUser(ctx, email, "different-password", idempotencyKey); status.Code(conflictErr) != codes.AlreadyExists {
		t.Fatalf("RegisterUser(key reuse) code = %v, want %v (err: %v)", status.Code(conflictErr), codes.AlreadyExists, conflictErr)
	}

	// Duplicate registration with a fresh key is rejected by the unique email.
	if _, _, dupErr := repo.RegisterUser(ctx, email, password, idempotencyKey+"-2"); status.Code(dupErr) != codes.AlreadyExists {
		t.Fatalf("duplicate RegisterUser code = %v, want %v (err: %v)", status.Code(dupErr), codes.AlreadyExists, dupErr)
	}

	// Login with the right credentials.
	loggedIn, token, err := repo.LoginUser(ctx, email, password)
	if err != nil {
		t.Fatalf("LoginUser: %v", err)
	}
	if loggedIn.ID != registered.ID {
		t.Fatalf("login id = %q, want %q", loggedIn.ID, registered.ID)
	}
	if token != "test-token" {
		t.Fatalf("login token = %q, want %q", token, "test-token")
	}

	// Login with a wrong password is rejected.
	if _, _, wrongPassErr := repo.LoginUser(ctx, email, "wrong-password"); status.Code(wrongPassErr) != codes.Unauthenticated {
		t.Fatalf("LoginUser(wrong password) code = %v, want %v (err: %v)", status.Code(wrongPassErr), codes.Unauthenticated, wrongPassErr)
	}

	// Login with an unknown email is rejected the same way.
	if _, _, unknownErr := repo.LoginUser(ctx, "nobody@chronoverse.test", password); status.Code(unknownErr) != codes.Unauthenticated {
		t.Fatalf("LoginUser(unknown email) code = %v, want %v (err: %v)", status.Code(unknownErr), codes.Unauthenticated, unknownErr)
	}

	// GetUser returns the registered user.
	got, err := repo.GetUser(ctx, registered.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.Email != email {
		t.Fatalf("GetUser email = %q, want %q", got.Email, email)
	}

	// GetUser with an unknown id is not found.
	if _, unknownErr := repo.GetUser(ctx, "00000000-0000-0000-0000-000000000000"); status.Code(unknownErr) != codes.NotFound {
		t.Fatalf("GetUser(unknown) code = %v, want %v (err: %v)", status.Code(unknownErr), codes.NotFound, unknownErr)
	}

	// UpdateUser changes the notification preference.
	if updateErr := repo.UpdateUser(ctx, registered.ID, "NONE"); updateErr != nil {
		t.Fatalf("UpdateUser: %v", updateErr)
	}
	updated, err := repo.GetUser(ctx, registered.ID)
	if err != nil {
		t.Fatalf("GetUser after update: %v", err)
	}
	if updated.NotificationPreference != "NONE" {
		t.Fatalf("notification_preference = %q, want %q", updated.NotificationPreference, "NONE")
	}

	// UpdateUser on an unknown id is not found.
	if updateErr := repo.UpdateUser(ctx, "00000000-0000-0000-0000-000000000000", "NONE"); status.Code(updateErr) != codes.NotFound {
		t.Fatalf("UpdateUser(unknown) code = %v, want %v (err: %v)", status.Code(updateErr), codes.NotFound, updateErr)
	}
}
