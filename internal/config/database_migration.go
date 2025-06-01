package config

import (
	"github.com/kelseyhightower/envconfig"
)

// DatabaseMigration holds the database migration configuration.
type DatabaseMigration struct {
	Environment

	Postgres
	ClickHouse
}

// InitDatabaseMigrationConfig initializes the database migration configuration.
func InitDatabaseMigrationConfig() (*DatabaseMigration, error) {
	var cfg DatabaseMigration
	if err := envconfig.Process(envPrefix, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
