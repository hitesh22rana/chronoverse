//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package databasemigration

import (
	"context"

	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Repository provides database migration related operations.
type Repository interface {
	MigratePostgres(ctx context.Context) error
	MigrateClickHouse(ctx context.Context) error
}

// Service provides database migration related operations.
type Service struct {
	tp   trace.Tracer
	repo Repository
}

// New creates a new database migration service.
func New(repo Repository) *Service {
	return &Service{
		tp:   otel.Tracer(svcpkg.Info().GetName()),
		repo: repo,
	}
}

// Run executes the database migration service.
func (s *Service) Run(ctx context.Context) (err error) {
	ctx, span := s.tp.Start(ctx, "Service.Run")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Migrate PostgreSQL database.
	if migrateErr := s.repo.MigratePostgres(ctx); migrateErr != nil {
		return migrateErr
	}

	// Migrate ClickHouse database.
	return s.repo.MigrateClickHouse(ctx)
}
