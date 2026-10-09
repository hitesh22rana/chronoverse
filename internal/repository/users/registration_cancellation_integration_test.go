//nolint:testpackage // Tests registration cancellation after ledger reservation.
package users

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	usersmodel "github.com/hitesh22rana/chronoverse/internal/model/users"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

type registrationResult struct {
	user  *usersmodel.GetUserResponse
	token string
	err   error
}

//nolint:gocyclo // The interruption, cleanup, and retry share one transaction fixture.
func TestIntegrationRegistrationInsertCancellationRollsBack(t *testing.T) {
	for _, code := range []codes.Code{codes.Canceled, codes.DeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			ctx := context.Background()
			pg := testkit.Postgres(t)
			mockAuth := authmock.NewMockIAuth(gomock.NewController(t))
			repo := New(mockAuth, pg)
			key := "register-" + uuid.NewString()
			email := key + "@chronoverse.test"
			lock, lockErr := pg.BeginTx(ctx)
			if lockErr != nil {
				t.Fatal(lockErr)
			}
			t.Cleanup(func() { rollbackRegistrationLock(ctx, t, lock) })
			if _, err := lock.Exec(ctx, "LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE"); err != nil {
				t.Fatal(err)
			}
			var blocker int
			if err := lock.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&blocker); err != nil {
				t.Fatal(err)
			}
			commandCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			done := make(chan registrationResult, 1)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				user, token, err := repo.RegisterUser(commandCtx, email, "password", key)
				done <- registrationResult{user: user, token: token, err: err}
			}()
			defer func() {
				cancel()
				cleanupCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer stop()
				rollbackRegistrationLock(cleanupCtx, t, lock)
				select {
				case <-finished:
				case <-cleanupCtx.Done():
					t.Error("registration did not stop during cleanup")
				}
			}()
			waitForRegistrationInsert(ctx, t, pg, blocker, done)
			if code == codes.Canceled {
				cancel()
			}
			resultCtx, stop := context.WithTimeout(ctx, 10*time.Second)
			defer stop()
			var result registrationResult
			select {
			case result = <-done:
			case <-resultCtx.Done():
				t.Fatal("registration did not return after interruption")
			}
			if result.user != nil || result.token != "" || status.Code(result.err) != code {
				t.Fatalf("registration = %v, %q, %v; want %s", result.user, result.token, result.err, code)
			}
			rollbackRegistrationLock(ctx, t, lock)
			assertRegistrationState(ctx, t, pg, email, key, 0, 0)
			mockAuth.EXPECT().IssueToken(gomock.Any(), gomock.Any(), auth.ServiceNameServer).Return("retry-token", nil)
			user, token, err := repo.RegisterUser(ctx, email, "password", key)
			if err != nil || user == nil || token != "retry-token" {
				t.Fatalf("retry = %v, %q, %v", user, token, err)
			}
			assertRegistrationState(ctx, t, pg, email, key, 1, 1)
		})
	}
}

func waitForRegistrationInsert(ctx context.Context, t *testing.T, pg *postgres.Postgres, blocker int, done <-chan registrationResult) {
	t.Helper()
	probeCtx, stop := context.WithTimeout(ctx, 4*time.Second)
	defer stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		query := `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
   WHERE datname = current_database() AND state = 'active' AND wait_event_type = 'Lock'
   AND query LIKE '%INSERT INTO users (email, password)%'
   AND $1 = ANY(pg_blocking_pids(pid)))`
		if err := pg.QueryRow(probeCtx, query, blocker).Scan(&blocked); err != nil {
			t.Fatalf("probe registration INSERT: %v", err)
		}
		if blocked {
			return
		}
		select {
		case result := <-done:
			t.Fatalf("registration returned before INSERT blocked: %v", result.err)
		case <-probeCtx.Done():
			t.Fatal("registration INSERT did not block")
		case <-ticker.C:
		}
	}
}

func rollbackRegistrationLock(ctx context.Context, t *testing.T, tx pgx.Tx) {
	t.Helper()
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		t.Errorf("release registration lock: %v", err)
	}
}
