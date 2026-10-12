//nolint:testpackage // Fault helpers for the users repository integration tests.
package users

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

// newTracedRepository records returned errors in repository spans.
func newTracedRepository(t *testing.T, issuer auth.IAuth, pg *postgres.Postgres) (*Repository, *tracetest.SpanRecorder) {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	repo := New(issuer, pg)
	repo.tp = provider.Tracer("user-fault-injection")

	return repo, recorder
}

// closedPostgres faults a dedicated pool without interrupting the shared pool.
func closedPostgres(t *testing.T) *postgres.Postgres {
	t.Helper()

	ctx := context.Background()
	cfg, err := pgconn.ParseConfig(testkit.PostgresDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	pg, err := postgres.New(ctx, &postgres.Config{
		Host:        cfg.Host,
		Port:        int(cfg.Port),
		User:        cfg.User,
		Password:    cfg.Password,
		Database:    cfg.Database,
		DialTimeout: 5 * time.Second,
		TLSConfig:   &postgres.TLSConfig{Enabled: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	pg.Close()

	return pg
}

// installUsersReadFault preserves the updatable table and faults only projected reads.
func installUsersReadFault(t *testing.T, pg *postgres.Postgres, body string) func() {
	t.Helper()
	ctx := context.Background()
	const base = "users_read_fault_base"
	const fn = "users_read_fault"
	tx, err := pg.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	//nolint:errcheck // Rollback is a no-op after commit.
	defer tx.Rollback(ctx)
	for _, statement := range []string{
		fmt.Sprintf(`CREATE FUNCTION %s() RETURNS timestamp LANGUAGE plpgsql AS $$ BEGIN %s END $$`, fn, body),
		"ALTER TABLE users RENAME TO " + base,
		"CREATE VIEW users AS SELECT id, email, password, notification_preference, created_at, " + fn + "() AS updated_at FROM " + base,
	} {
		if _, err := tx.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	removed := false
	remove := func() {
		if removed {
			return
		}
		cleanup, err := pg.BeginTx(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		//nolint:errcheck // Rollback is a no-op after commit.
		defer cleanup.Rollback(ctx)
		for _, statement := range []string{
			"DROP VIEW users", "ALTER TABLE " + base + " RENAME TO users", "DROP FUNCTION " + fn + "()",
		} {
			if _, err := cleanup.Exec(ctx, statement); err != nil {
				t.Error(err)
				return
			}
		}
		if err := cleanup.Commit(ctx); err != nil {
			t.Error(err)
			return
		}
		removed = true
	}
	t.Cleanup(remove)
	return remove
}

// insertUser creates an account without depending on registration.
func insertUser(t *testing.T, pg *postgres.Postgres, email string) string {
	t.Helper()

	var id string
	query := "INSERT INTO users (email, password) VALUES ($1, $2) RETURNING id::text"
	if err := pg.QueryRow(context.Background(), query, email, "hash").Scan(&id); err != nil {
		t.Fatal(err)
	}

	return id
}

// readPreference checks whether an update changed the stored account.
func readPreference(t *testing.T, pg *postgres.Postgres, id string) (preference string, updatedAt time.Time) {
	t.Helper()

	query := fmt.Sprintf("SELECT notification_preference, updated_at FROM %s WHERE id = $1", postgres.TableUsers)
	if err := pg.QueryRow(context.Background(), query, id).Scan(&preference, &updatedAt); err != nil {
		t.Fatal(err)
	}

	return preference, updatedAt
}

// waitForSleepingUserQuery synchronizes interruption with database execution.
func waitForSleepingUserQuery(ctx context.Context, t *testing.T, pg *postgres.Postgres, pattern string) {
	t.Helper()

	probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	query := `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
  WHERE datname = current_database() AND wait_event = 'PgSleep' AND query ILIKE $1)`
	testkit.Eventually(t, 4*time.Second, 10*time.Millisecond, func() bool {
		var sleeping bool
		if err := pg.QueryRow(probeCtx, query, pattern).Scan(&sleeping); err != nil {
			t.Fatalf("probe sleeping user query: %v", err)
		}
		return sleeping
	})
}
