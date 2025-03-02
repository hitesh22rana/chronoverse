package scheduler

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Repository provides jobs repository.
type Repository struct {
	tp trace.Tracer
	pg *postgres.Postgres
}

// New creates a new jobs repository.
func New(pg *postgres.Postgres) *Repository {
	return &Repository{
		tp: otel.Tracer(svcpkg.Info().GetName()),
		pg: pg,
	}
}

// Run starts the scheduler.
func (r *Repository) Run(ctx context.Context) (total int, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.Run")
	defer func() {
		if err != nil {
			span.RecordError(err)
		}
		span.End()
	}()

	// Start transaction
	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		err = status.Errorf(codes.Internal, "failed to start transaction: %v", err)
		return 0, err
	}
	//nolint:errcheck // The error is handled in the next line
	defer tx.Rollback(ctx)

	query := `
		UPDATE scheduled_jobs
		SET status = 'QUEUED', updated_at = NOW()
		WHERE id IN (
			SELECT id
			FROM scheduled_jobs
			WHERE status = 'PENDING' AND scheduled_at <= NOW()
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id;
	`

	// Execute query
	rows, err := tx.Query(ctx, query)
	if err != nil {
		err = status.Errorf(codes.Internal, "failed to query scheduled jobs: %v", err)
		return 0, err
	}
	defer rows.Close()

	// Iterate over the rows and collect the IDs
	//nolint:prealloc // We don't know the number of rows
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			err = status.Errorf(codes.Internal, "failed to scan scheduled job: %v", err)
			return 0, err
		}
		ids = append(ids, id)
	}

	// Handle any errors that may have occurred during iteration
	if err = rows.Err(); err != nil {
		err = status.Errorf(codes.Internal, "failed to iterate over scheduled jobs: %v", err)
		return 0, err
	}

	// Commit transaction
	if err = tx.Commit(ctx); err != nil {
		err = status.Errorf(codes.Internal, "failed to commit transaction: %v", err)
		return 0, err
	}

	total = len(ids)
	return total, nil
}
