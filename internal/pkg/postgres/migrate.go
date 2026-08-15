package postgres

import (
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	// Register the PostgreSQL driver for golang-migrate.
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
)

// Migrate applies every pending embedded migration to the PostgreSQL database
// at the given DSN. It is safe to call repeatedly; already-applied migrations
// are a no-op.
func Migrate(dsn string) error {
	sourceInstance, err := iofs.New(MigrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", sourceInstance, dsn)
	if err != nil {
		return fmt.Errorf("create migrate instance: %w", err)
	}
	defer func() {
		_, _ = m.Close()
	}()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("run postgres migrations: %w", err)
	}

	return nil
}
