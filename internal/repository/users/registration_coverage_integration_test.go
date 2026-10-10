//nolint:testpackage // Tests registration transaction and replay boundaries.
package users

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestIntegrationRegistrationRejectsLongPassword(t *testing.T) {
	repo := newTestRepository(t)
	user, token, err := repo.RegisterUser(context.Background(), "long@chronoverse.test", strings.Repeat("x", 73), "long-password")
	if status.Code(err) != codes.InvalidArgument || user != nil || token != "" {
		t.Fatalf("registration = %v, %q, %v", user, token, err)
	}
}

func TestIntegrationRegistrationCanceledBeforeTransaction(t *testing.T) {
	repo := newTestRepository(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	user, token, err := repo.RegisterUser(ctx, "canceled@chronoverse.test", "password", "canceled-registration")
	if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "start registration transaction") || user != nil || token != "" {
		t.Fatalf("registration = %v, %q, %v", user, token, err)
	}
}

func TestIntegrationRegistrationReplayRejectsMissingOrCorruptUser(t *testing.T) {
	for _, tc := range []struct{ name, mutation, message string }{
		{"missing", "UPDATE command_idempotency_keys SET resource_id = '00000000-0000-0000-0000-000000000000' WHERE idempotency_key = $1", "load registration replay"},
		{"corrupt hash", "UPDATE users SET password = 'corrupt' WHERE email = $1", "verify registration replay"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			repo := newTestRepository(t)
			key := "register-" + uuid.NewString()
			email := key + "@chronoverse.test"
			original, _, err := repo.RegisterUser(ctx, email, "password", key)
			if err != nil {
				t.Fatal(err)
			}
			arg := key
			if tc.name == "corrupt hash" {
				arg = email
			}
			if _, err = repo.pg.Exec(ctx, tc.mutation, arg); err != nil {
				t.Fatal(err)
			}
			user, token, err := repo.RegisterUser(ctx, email, "password", key)
			if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), tc.message) || user != nil || token != "" {
				t.Fatalf("replay = %v, %q, %v", user, token, err)
			}
			var count int
			if err := repo.pg.QueryRow(ctx, "SELECT count(*) FROM users WHERE id = $1", original.ID).Scan(&count); err != nil || count != 1 {
				t.Fatalf("original user count = %d, %v", count, err)
			}
		})
	}
}

func TestIntegrationRegistrationTokenFailurePreservesAccountAndReplay(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	mockAuth := authmock.NewMockIAuth(gomock.NewController(t))
	repo := New(mockAuth, pg)
	key := "register-" + uuid.NewString()
	email := key + "@chronoverse.test"
	var userID string
	tokenErr := status.Error(codes.Unavailable, "token issuer unavailable")
	mockAuth.EXPECT().IssueToken(gomock.Any(), gomock.Any(), auth.ServiceNameServer).DoAndReturn(func(_ context.Context, id, _ string) (string, error) {
		userID = id
		assertRegistrationState(ctx, t, pg, email, key, 1, 1)
		return "", tokenErr
	})
	user, token, err := repo.RegisterUser(ctx, email, "password", key)
	if user != nil || token != "" || !errors.Is(err, tokenErr) {
		t.Fatalf("registration = %v, %q, %v", user, token, err)
	}
	mockAuth.EXPECT().IssueToken(gomock.Any(), userID, auth.ServiceNameServer).Return("", tokenErr)
	replay, token, err := repo.RegisterUser(ctx, email, "password", key)
	if replay == nil || replay.ID != userID || token != "" || !errors.Is(err, tokenErr) {
		t.Fatalf("replay = %v, %q, %v", replay, token, err)
	}
	mockAuth.EXPECT().IssueToken(gomock.Any(), userID, auth.ServiceNameServer).Return("recovered-token", nil)
	replay, token, err = repo.RegisterUser(ctx, email, "password", key)
	if err != nil || replay.ID != userID || token != "recovered-token" {
		t.Fatalf("retry = %v, %q, %v", replay, token, err)
	}
	assertRegistrationState(ctx, t, pg, email, key, 1, 1)
}

func TestIntegrationRegistrationReplayCommitFailure(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	mockAuth := authmock.NewMockIAuth(gomock.NewController(t))
	repo := New(mockAuth, pg)
	key := "register-" + uuid.NewString()
	email := key + "@chronoverse.test"
	mockAuth.EXPECT().IssueToken(gomock.Any(), gomock.Any(), auth.ServiceNameServer).Return("test-token", nil)
	registered, _, err := repo.RegisterUser(ctx, email, "password", key)
	if err != nil {
		t.Fatal(err)
	}
	// A replay transaction writes nothing, so the fault is queued by the
	// reservation's BEFORE INSERT trigger and only surfaces when it commits.
	remove := installRegistrationReplayCommitFault(t, pg)
	replay, token, err := repo.RegisterUser(ctx, email, "password", key)
	if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "commit registration replay") || replay != nil || token != "" {
		t.Fatalf("replay = %v, %q, %v", replay, token, err)
	}
	remove()
	assertRegistrationState(ctx, t, pg, email, key, 1, 1)
	// No token may be issued for the aborted replay, and the account, ledger
	// row, and password stay usable by the next replay.
	mockAuth.EXPECT().IssueToken(gomock.Any(), registered.ID, auth.ServiceNameServer).Return("recovered-token", nil)
	replay, token, err = repo.RegisterUser(ctx, email, "password", key)
	if err != nil || replay.ID != registered.ID || token != "recovered-token" {
		t.Fatalf("retry = %v, %q, %v", replay, token, err)
	}
}

func TestIntegrationRegistrationRollsBackDatabaseFaults(t *testing.T) {
	for _, tc := range []struct {
		name, table, event, condition, body, message string
		code                                         codes.Code
		deferred                                     bool
	}{
		{"reserve", postgres.TableCommandIdempotencyKeys, "INSERT", "NEW.idempotency_key", "RAISE EXCEPTION 'registration fault';", "reserve command", codes.Internal, false},
		{"complete", postgres.TableCommandIdempotencyKeys, "UPDATE", "NEW.idempotency_key", "RAISE EXCEPTION 'registration fault';", "complete command", codes.Internal, false},
		{"commit", postgres.TableCommandIdempotencyKeys, "UPDATE", "NEW.idempotency_key", "RAISE EXCEPTION 'registration fault';", "commit registration transaction", codes.Internal, true},
		{"invalid input", postgres.TableUsers, "INSERT", "NEW.email", "RAISE EXCEPTION 'registration fault' USING ERRCODE = '22P02';", "invalid user ID", codes.InvalidArgument, false},
		{"insert failure", postgres.TableUsers, "INSERT", "NEW.email", "RAISE EXCEPTION 'registration fault';", "failed to fetch user", codes.Internal, false},
		{"missing returning row", postgres.TableUsers, "INSERT", "NEW.email", "RETURN NULL;", "user not found", codes.NotFound, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			pg := testkit.Postgres(t)
			mockAuth := authmock.NewMockIAuth(gomock.NewController(t))
			repo := New(mockAuth, pg)
			key := "register-" + uuid.NewString()
			email := key + "@chronoverse.test"
			target := key
			if tc.table == postgres.TableUsers {
				target = email
			}
			remove := installRegistrationFault(t, pg, tc.table, tc.event, tc.condition, target, tc.body, tc.deferred)
			user, token, err := repo.RegisterUser(ctx, email, "password", key)
			if status.Code(err) != tc.code || !strings.Contains(err.Error(), tc.message) || user != nil || token != "" {
				t.Fatalf("registration = %v, %q, %v", user, token, err)
			}
			assertRegistrationState(ctx, t, pg, email, key, 0, 0)
			remove()
			mockAuth.EXPECT().IssueToken(gomock.Any(), gomock.Any(), auth.ServiceNameServer).Return("test-token", nil)
			user, token, err = repo.RegisterUser(ctx, email, "password", key)
			if err != nil || user == nil || token != "test-token" {
				t.Fatalf("retry = %v, %q, %v", user, token, err)
			}
			assertRegistrationState(ctx, t, pg, email, key, 1, 1)
		})
	}
}

func assertRegistrationState(ctx context.Context, t *testing.T, pg *postgres.Postgres, email, key string, wantUsers, wantCommands int) {
	t.Helper()
	var users, commands int
	if err := pg.QueryRow(ctx, "SELECT count(*) FROM users WHERE email = $1", email).Scan(&users); err != nil {
		t.Fatal(err)
	}
	query := `SELECT count(*) FROM command_idempotency_keys
 WHERE scope = 'public' AND operation = 'user.register' AND idempotency_key = $1`
	if err := pg.QueryRow(ctx, query, key).Scan(&commands); err != nil {
		t.Fatal(err)
	}
	if wantCommands == 1 {
		var completed bool
		query := `SELECT keys.status = 'COMPLETED' AND keys.resource_id = users.id::text
  FROM command_idempotency_keys AS keys JOIN users ON users.email = $2
  WHERE keys.scope = 'public' AND keys.operation = 'user.register' AND keys.idempotency_key = $1`
		if err := pg.QueryRow(ctx, query, key, email).Scan(&completed); err != nil || !completed {
			t.Fatalf("completed ledger/account identity = %t, %v", completed, err)
		}
	}
	if users != wantUsers || commands != wantCommands {
		t.Fatalf("durable users/commands = %d/%d, want %d/%d", users, commands, wantUsers, wantCommands)
	}
}

func installRegistrationReplayCommitFault(t *testing.T, pg *postgres.Postgres) func() {
	t.Helper()
	ctx := context.Background()
	const table = "registration_replay_fault"
	statements := []string{
		"DROP TABLE IF EXISTS " + table,
		fmt.Sprintf("CREATE TABLE %s (id int PRIMARY KEY DEFERRABLE INITIALLY DEFERRED)", table),
		fmt.Sprintf("INSERT INTO %s VALUES (1)", table),
		fmt.Sprintf(`CREATE FUNCTION %s_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN INSERT INTO %s VALUES (1); RETURN NEW; END $$`, table, table),
		fmt.Sprintf("CREATE TRIGGER %s_fault BEFORE INSERT ON %s FOR EACH ROW EXECUTE FUNCTION %s_fault()", table, postgres.TableCommandIdempotencyKeys, table),
	}
	remove := func() {
		for _, statement := range []string{
			fmt.Sprintf("DROP TRIGGER IF EXISTS %s_fault ON %s", table, postgres.TableCommandIdempotencyKeys),
			fmt.Sprintf("DROP FUNCTION IF EXISTS %s_fault()", table),
			"DROP TABLE IF EXISTS " + table,
		} {
			if _, err := pg.Exec(ctx, statement); err != nil {
				t.Error(err)
			}
		}
	}
	for _, statement := range statements {
		if _, err := pg.Exec(ctx, statement); err != nil {
			remove()
			t.Fatal(err)
		}
	}
	t.Cleanup(remove)
	return remove
}

func installRegistrationFault(t *testing.T, pg *postgres.Postgres, table, event, condition, target, body string, deferred bool) func() {
	t.Helper()
	ctx := context.Background()
	name := "registration_fault_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := pg.Exec(ctx, fmt.Sprintf("CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN %s END $$", name, body)); err != nil {
		t.Fatal(err)
	}
	remove := func() {
		if _, err := pg.Exec(ctx, fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s", name, table)); err != nil {
			t.Error(err)
		}
		if _, err := pg.Exec(ctx, fmt.Sprintf("DROP FUNCTION IF EXISTS %s()", name)); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(remove)
	kind, timing, when := "", "", "BEFORE"
	if deferred {
		kind, timing, when = "CONSTRAINT ", "DEFERRABLE INITIALLY DEFERRED", "AFTER"
	}
	statement := fmt.Sprintf("CREATE %sTRIGGER %s %s %s ON %s %s FOR EACH ROW WHEN (%s = '%s') EXECUTE FUNCTION %s()",
		kind, name, when, event, table, timing, condition, target, name)
	if _, err := pg.Exec(ctx, statement); err != nil {
		t.Fatal(err)
	}
	return remove
}
