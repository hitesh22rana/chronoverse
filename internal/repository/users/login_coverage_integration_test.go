//nolint:testpackage // Covers login credential, token, and query failure paths.
package users

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

const oversizedPasswordLength = 73

//nolint:gocyclo // Covers credential failures and recovery in one table.
func TestIntegrationLoginRejectsUnverifiablePassword(t *testing.T) {
	for _, tc := range []struct {
		name     string
		corrupt  bool
		unknown  bool
		password string
		code     codes.Code
		message  string
	}{
		{name: "corrupt stored hash", corrupt: true, code: codes.Internal, message: "failed to verify password"},
		{name: "oversized password", password: strings.Repeat("x", oversizedPasswordLength), code: codes.Unauthenticated, message: "invalid email or password"},
		{name: "unknown account with oversized password", unknown: true, password: strings.Repeat("x", oversizedPasswordLength), code: codes.Unauthenticated, message: "invalid email or password"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			pg := testkit.Postgres(t)

			email := "login-" + uuid.NewString() + "@chronoverse.test"
			password := "s3cure-password"
			registered, _, err := newTestRepository(t).RegisterUser(ctx, email, password, "register-"+uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			mockAuth := authmock.NewMockIAuth(gomock.NewController(t))
			repo, recorder := newTracedRepository(t, mockAuth, pg)

			stored := ""
			if tc.corrupt {
				if err = pg.QueryRow(ctx, "SELECT password FROM users WHERE email = $1", email).Scan(&stored); err != nil {
					t.Fatal(err)
				}
				if _, err = pg.Exec(ctx, "UPDATE users SET password = 'corrupt' WHERE email = $1", email); err != nil {
					t.Fatal(err)
				}
			}

			loginEmail, loginPassword := email, password
			if tc.unknown {
				loginEmail = "unknown-" + uuid.NewString() + "@chronoverse.test"
			}
			if tc.password != "" {
				loginPassword = tc.password
			}
			user, token, err := repo.LoginUser(ctx, loginEmail, loginPassword)
			if status.Code(err) != tc.code || !strings.Contains(err.Error(), tc.message) || user != nil || token != "" {
				t.Fatalf("login = %v, %q, %v; want %s %q", user, token, err, tc.code, tc.message)
			}
			assertUserErrorSpan(t, recorder, "LoginUser", err)

			// The account must be untouched, so restoring the hash restores login.
			if tc.corrupt {
				if _, err = pg.Exec(ctx, "UPDATE users SET password = $1 WHERE email = $2", stored, email); err != nil {
					t.Fatal(err)
				}
			}
			mockAuth.EXPECT().IssueToken(gomock.Any(), registered.ID, auth.ServiceNameServer).Return("issued-token", nil)
			recovered, token, err := repo.LoginUser(ctx, email, password)
			if err != nil || recovered == nil || recovered.ID != registered.ID || token != "issued-token" {
				t.Fatalf("login after restore = %v, %q, %v", recovered, token, err)
			}
		})
	}
}

func TestIntegrationLoginTokenFailureReturnsNoMaterial(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)

	email := "token-" + uuid.NewString() + "@chronoverse.test"
	password := "s3cure-password"
	registered, _, err := newTestRepository(t).RegisterUser(ctx, email, password, "register-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}

	mockAuth := authmock.NewMockIAuth(gomock.NewController(t))
	repo, recorder := newTracedRepository(t, mockAuth, pg)

	tokenErr := status.Error(codes.Unavailable, "token issuer unavailable")
	mockAuth.EXPECT().IssueToken(gomock.Any(), registered.ID, auth.ServiceNameServer).Return("", tokenErr)
	user, token, err := repo.LoginUser(ctx, email, password)
	if user != nil || token != "" || !errors.Is(err, tokenErr) || status.Code(err) != codes.Unavailable {
		t.Fatalf("login = %v, %q, %v", user, token, err)
	}
	assertUserErrorSpan(t, recorder, "LoginUser", err)

	mockAuth.EXPECT().IssueToken(gomock.Any(), registered.ID, auth.ServiceNameServer).Return("recovered-token", nil)
	user, token, err = repo.LoginUser(ctx, email, password)
	if err != nil || user == nil || user.ID != registered.ID || token != "recovered-token" {
		t.Fatalf("login retry = %v, %q, %v", user, token, err)
	}
}

func TestIntegrationLoginFailsWhenDatabaseUnavailable(t *testing.T) {
	ctx := context.Background()
	shared := testkit.Postgres(t)
	repo, recorder := newTracedRepository(t, authmock.NewMockIAuth(gomock.NewController(t)), closedPostgres(t))

	user, token, err := repo.LoginUser(ctx, "offline@chronoverse.test", "s3cure-password")
	if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to fetch user") || user != nil || token != "" {
		t.Fatalf("login = %v, %q, %v", user, token, err)
	}
	assertUserErrorSpan(t, recorder, "LoginUser", err)

	// The shared pool still answers, so the fault stayed inside its own pool.
	var users int
	if err := shared.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&users); err != nil {
		t.Fatal(err)
	}
}

// issuingAuth supplies a token for tests without an authentication failure.
func issuingAuth(t *testing.T) auth.IAuth {
	t.Helper()

	mockAuth := authmock.NewMockIAuth(gomock.NewController(t))
	mockAuth.EXPECT().IssueToken(gomock.Any(), gomock.Any(), auth.ServiceNameServer).Return("test-token", nil).AnyTimes()

	return mockAuth
}

func TestIntegrationLoginMapsDatabaseInvalidText(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	email := "invalid-text-" + uuid.NewString() + "@chronoverse.test"
	id := insertUser(t, pg, email)
	repo, recorder := newTracedRepository(t, authmock.NewMockIAuth(gomock.NewController(t)), pg)
	remove := installUsersReadFault(t, pg, "RAISE EXCEPTION 'invalid stored value' USING ERRCODE = '22P02';")
	user, token, err := repo.LoginUser(ctx, email, "password")
	if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "invalid user ID") || user != nil || token != "" {
		t.Fatalf("login = %v, %q, %v", user, token, err)
	}
	assertUserErrorSpan(t, recorder, "LoginUser", err)
	remove()
	if _, err := newTestRepository(t).GetUser(ctx, id); err != nil {
		t.Fatalf("account after fault: %v", err)
	}
}
