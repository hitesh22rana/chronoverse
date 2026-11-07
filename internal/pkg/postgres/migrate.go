package postgres

import (
	"errors"
	"fmt"
	"io/fs"

	migratepkg "github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	// Register the PostgreSQL driver for golang-migrate.
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
)

// Migrate applies every pending embedded migration to the PostgreSQL database
// at the given DSN. It is safe to call repeatedly; already-applied migrations
// are a no-op.
func Migrate(dsn string) error {
	return migrate(dsn, migrationsFS)
}

// migrate binds discovery and execution to the same migration source.
func migrate(dsn string, source fs.FS) error {
	sourceInstance, err := iofs.New(source, "migrations")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}

	m, err := migratepkg.NewWithSourceInstance("iofs", sourceInstance, dsn)
	if err != nil {
		return fmt.Errorf("create migrate instance: %w", err)
	}
	defer func() {
		_, _ = m.Close()
	}()

	if err := m.Up(); err != nil && !errors.Is(err, migratepkg.ErrNoChange) {
		return fmt.Errorf("run postgres migrations: %w", err)
	}

	return nil
}
