//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package databasemigration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	clickhousepkg "github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestMain(m *testing.M) {
	testkit.Run(m, testkit.WithPostgres(), testkit.WithClickHouse(), testkit.WithMeilisearch())
}

// newTestRepository wires the migration service to the shared containers.
func newTestRepository(t *testing.T) *Repository {
	t.Helper()

	return New(&Config{
		PostgresDSN:       testkit.PostgresDSN(t),
		ClickHouseClient:  testkit.ClickHouse(t),
		MeiliSearchClient: testkit.Meilisearch(t),
	})
}

func TestIntegrationMigrationsAreIdempotent(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository(t)

	// The shared containers are already migrated by the testkit; running the
	// service again must be a clean no-op.
	if err := repo.MigratePostgres(ctx); err != nil {
		t.Fatalf("MigratePostgres (idempotent): %v", err)
	}
	if err := repo.MigrateClickHouse(ctx); err != nil {
		t.Fatalf("MigrateClickHouse (idempotent): %v", err)
	}
	if err := repo.SetupMeiliSearch(ctx); err != nil {
		t.Fatalf("SetupMeiliSearch (idempotent): %v", err)
	}
}

func TestIntegrationPostgresMigrationsApplyToFreshDatabase(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)

	dbName := "chronoverse_migration_test"
	if _, err := pg.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName); err != nil {
		t.Fatalf("drop database: %v", err)
	}
	if _, err := pg.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pg.Exec(context.Background(), "DROP DATABASE IF EXISTS "+dbName); err != nil {
			t.Logf("cleanup: drop database %s: %v", dbName, err)
		}
	})

	dsn := strings.Replace(testkit.PostgresDSN(t), "/chronoverse?", "/"+dbName+"?", 1)
	repo := New(&Config{PostgresDSN: dsn})

	// Open a dedicated connection to the freshly created database so the
	// spot-checks below observe its schema. The shared pg pool stays bound to
	// the already-migrated chronoverse database, and information_schema is
	// per-database, so querying through it would silently pass regardless of
	// what the fresh migration produced.
	freshPG, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect fresh postgres database: %v", err)
	}
	t.Cleanup(freshPG.Close)

	if err := repo.MigratePostgres(ctx); err != nil {
		t.Fatalf("MigratePostgres: %v", err)
	}

	// Spot-check that the migration set created the expected tables.
	for _, table := range []string{"users", "workflows", "jobs", "notifications", "analytics", "outbox_events", "runtime_nodes"} {
		var exists bool
		if err := freshPG.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = 'public' AND table_name = $1
			)
		`, table).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("table %q was not created by migrations", table)
		}
	}

	// Applying again is a no-op.
	if err := repo.MigratePostgres(ctx); err != nil {
		t.Fatalf("MigratePostgres (replay): %v", err)
	}
}

func TestIntegrationClickHouseMigrationsApplyToFreshDatabase(t *testing.T) {
	ctx := context.Background()
	ch := testkit.ClickHouse(t)

	dbName := "chronoverse_ch_migration_test"
	if err := ch.Exec(ctx, "CREATE DATABASE IF NOT EXISTS "+dbName); err != nil {
		t.Fatalf("create clickhouse database: %v", err)
	}
	t.Cleanup(func() {
		if err := ch.Exec(context.Background(), "DROP DATABASE IF EXISTS "+dbName); err != nil {
			t.Logf("cleanup: drop clickhouse database %s: %v", dbName, err)
		}
	})

	freshClient, err := clickhousepkg.New(ctx, &clickhousepkg.Config{
		Hosts:           []string{testkit.ClickHouseAddr(t)},
		Database:        dbName,
		Username:        "chronoverse-client",
		Password:        "chronoverse",
		MaxOpenConns:    5,
		MaxIdleConns:    2,
		ConnMaxLifetime: time.Hour,
		DialTimeout:     5 * time.Second,
		TLSConfig:       &clickhousepkg.TLSConfig{Enabled: false},
	})
	if err != nil {
		t.Fatalf("connect fresh clickhouse: %v", err)
	}
	t.Cleanup(func() { _ = freshClient.Close() })

	repo := New(&Config{ClickHouseClient: freshClient})
	if migrateErr := repo.MigrateClickHouse(ctx); migrateErr != nil {
		t.Fatalf("MigrateClickHouse: %v", migrateErr)
	}

	// The job_logs table and the migration bookkeeping exist in the fresh DB.
	rows, err := freshClient.Query(ctx, `
		SELECT name FROM system.tables
		WHERE database = $1 AND name = 'job_logs'
	`, dbName)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		found = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tables: %v", err)
	}
	if !found {
		t.Fatal("job_logs table was not created by clickhouse migrations")
	}

	// Applying again is a no-op.
	if err := repo.MigrateClickHouse(ctx); err != nil {
		t.Fatalf("MigrateClickHouse (replay): %v", err)
	}
}
