package databasemigration

import (
	"context"
	"errors"

	_ "github.com/golang-migrate/migrate/v4/database/clickhouse"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	clickhousepkg "github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	postgrespkg "github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Config holds the database migration configuration.
type Config struct {
	PostgresDSN   string
	ClickHouseDSN string
}

// Repository provides database migration repository.
type Repository struct {
	tp  trace.Tracer
	cfg *Config
}

// New creates a new database migration repository.
func New(cfg *Config) *Repository {
	return &Repository{
		tp:  otel.Tracer(svcpkg.Info().GetName()),
		cfg: cfg,
	}
}

// MigratePostgres migrates the PostgreSQL database.
//
//nolint:dupl // This function is similar to MigrateClickHouse but for PostgreSQL.
func (r *Repository) MigratePostgres(ctx context.Context) (err error) {
	_, span := r.tp.Start(ctx, "Repository.MigratePostgres")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// IOFS source instance for embedded migrations
	sourceInstance, err := iofs.New(postgrespkg.MigrationsFS, "migrations")
	if err != nil {
		return status.Errorf(codes.Internal, "failed to create IOFS source instance: %v", err)
	}

	// Migrate instance for postgres
	m, err := migrate.NewWithSourceInstance("iofs", sourceInstance, r.cfg.PostgresDSN)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to create migrate instance: %v", err)
	}

	// Execute migration
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return status.Errorf(codes.Internal, "failed to run postgres migration: %v", err)
	}

	return nil
}

// MigrateClickHouse migrates the ClickHouse database.
//
//nolint:dupl // This function is similar to MigratePostgres but for ClickHouse.
func (r *Repository) MigrateClickHouse(ctx context.Context) (err error) {
	_, span := r.tp.Start(ctx, "Repository.MigrateClickHouse")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// IOFS source instance for embedded migrations
	sourceInstance, err := iofs.New(clickhousepkg.MigrationsFS, "migrations")
	if err != nil {
		return status.Errorf(codes.Internal, "failed to create IOFS source instance: %v", err)
	}

	// Migrate instance for clickhouse
	m, err := migrate.NewWithSourceInstance("iofs", sourceInstance, r.cfg.ClickHouseDSN)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to create migrate instance: %v", err)
	}

	// Execute migration
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return status.Errorf(codes.Internal, "failed to run clickhouse migration: %v", err)
	}

	return nil
}
