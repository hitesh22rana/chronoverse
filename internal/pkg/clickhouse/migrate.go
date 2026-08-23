package clickhouse

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Migration represents a single embedded migration file.
type Migration struct {
	Version int
	Name    string
	Content string
}

// Migrate applies every pending embedded migration to the ClickHouse database
// through the given client. Bookkeeping mirrors golang-migrate: a
// schema_migrations table records applied versions, migrations are marked dirty
// while running and clean once finished, and statements are executed one at a
// time (the native driver rejects multi-statement queries).
//
// It is safe to call repeatedly; already-applied migrations are skipped.
// A migration left in the dirty state by a previous failed run is a hard error,
// mirroring golang-migrate: partially applied migrations cannot be safely
// re-run, so manual resolution of schema_migrations is required.
func Migrate(ctx context.Context, client *Client) error {
	if err := createSchemaMigrationsTable(ctx, client); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	applied, err := getAppliedMigrations(ctx, client)
	if err != nil {
		return fmt.Errorf("get applied migrations: %w", err)
	}

	pending, err := getPendingMigrations(applied)
	if err != nil {
		return fmt.Errorf("get pending migrations: %w", err)
	}

	for _, migration := range pending {
		if err := applyMigration(ctx, client, migration); err != nil {
			return fmt.Errorf("apply migration %s: %w", migration.Name, err)
		}
	}

	return nil
}

// createSchemaMigrationsTable creates the schema_migrations table if it does not exist.
func createSchemaMigrationsTable(ctx context.Context, client *Client) error {
	query := `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version UInt32 NOT NULL,
			dirty UInt8 NOT NULL DEFAULT 0,
			applied_at DateTime DEFAULT now()
		) ENGINE = MergeTree()
		ORDER BY version
	`
	return client.Exec(ctx, query)
}

// getAppliedMigrations returns the versions of cleanly applied migrations and
// fails if a previous run left a migration in the dirty state.
func getAppliedMigrations(ctx context.Context, client *Client) (map[int]bool, error) {
	applied := make(map[int]bool)

	rows, err := client.Query(ctx, "SELECT version, dirty FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			version uint32
			dirty   uint8
		)
		if err := rows.Scan(&version, &dirty); err != nil {
			return nil, err
		}
		if dirty == 1 {
			return nil, fmt.Errorf("migration %d is marked dirty: partially applied, manual intervention required", version)
		}
		applied[int(version)] = true
	}

	return applied, rows.Err()
}

// getPendingMigrations returns the embedded migrations that have not been applied yet.
func getPendingMigrations(applied map[int]bool) ([]Migration, error) {
	var migrations []Migration

	// Read migration files from the embedded filesystem.
	err := fs.WalkDir(MigrationsFS, "migrations", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || !strings.HasSuffix(path, ".up.sql") {
			return nil
		}

		// Extract version from filename (e.g., "000001_table_job_logs_create.up.sql").
		filename := filepath.Base(path)
		parts := strings.Split(filename, "_")
		if len(parts) < 2 {
			return fmt.Errorf("invalid migration filename format: %s", filename)
		}

		version, err := strconv.Atoi(parts[0])
		if err != nil {
			return fmt.Errorf("invalid version in filename %s: %w", filename, err)
		}

		// Skip if already applied.
		if applied[version] {
			return nil
		}

		// Read migration content.
		content, err := fs.ReadFile(MigrationsFS, path)
		if err != nil {
			return fmt.Errorf("read migration file %s: %w", path, err)
		}

		migrations = append(migrations, Migration{
			Version: version,
			Name:    filename,
			Content: string(content),
		})

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Sort migrations by version.
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	return migrations, nil
}

// applyMigration applies a single migration.
func applyMigration(ctx context.Context, client *Client, migration Migration) error {
	// Mark migration as dirty (in progress).
	if err := client.Exec(ctx, "INSERT INTO schema_migrations (version, dirty) VALUES (?, 1)", migration.Version); err != nil {
		return fmt.Errorf("mark migration %d dirty: %w", migration.Version, err)
	}

	// Execute migration SQL statements one by one. The native ClickHouse driver
	// rejects multi-statement query strings.
	for _, statement := range splitMigrationStatements(migration.Content) {
		if err := client.Exec(ctx, statement); err != nil {
			return fmt.Errorf("execute migration SQL: %w", err)
		}
	}

	// Mark migration as clean (completed). The mutation is made synchronous so
	// that subsequent runs never observe a stale dirty flag.
	if err := client.Exec(ctx, "ALTER TABLE schema_migrations UPDATE dirty = 0 WHERE version = ? SETTINGS mutations_sync = 1", migration.Version); err != nil {
		return fmt.Errorf("mark migration %d clean: %w", migration.Version, err)
	}

	return nil
}

// splitMigrationStatements splits a migration file into individual statements.
// The split is a plain ";" separator: string literals and comments inside
// migration files must not contain semicolons.
func splitMigrationStatements(content string) []string {
	parts := strings.Split(content, ";")
	statements := make([]string, 0, len(parts))
	for _, part := range parts {
		statement := strings.TrimSpace(part)
		if statement == "" {
			continue
		}
		statements = append(statements, statement)
	}
	return statements
}
