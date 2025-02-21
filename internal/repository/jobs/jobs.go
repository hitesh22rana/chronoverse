package jobs

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	jobsTable          = "jobs"
	scheduledJobsTable = "scheduled_jobs"
)

// Repository provides jobs repository.
type Repository struct {
	tp   trace.Tracer
	auth *auth.Auth
	pg   *postgres.Postgres
}

// New creates a new jobs repository.
func New(auth *auth.Auth, pg *postgres.Postgres) *Repository {
	return &Repository{
		tp:   otel.Tracer(svcpkg.Info().GetName()),
		auth: auth,
		pg:   pg,
	}
}

// Create a new job.
func (r *Repository) Create(ctx context.Context, userID, name, payload, kind string, interval, maxRetry int32) (jobID string, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.Create")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Start transaction
	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		err = status.Errorf(codes.Internal, "failed to start transaction: %v", err)
		return "", err
	}
	//nolint:errcheck // The error is handled in the next line
	defer tx.Rollback(ctx)

	// Insert job into database
	query := fmt.Sprintf(`
		INSERT INTO %s (user_id, name, payload, kind, interval)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, jobsTable)

	row := tx.QueryRow(ctx, query, userID, name, payload, kind, interval)
	if err = row.Scan(&jobID); err != nil {
		err = status.Errorf(codes.Internal, "failed to insert job: %v", err)
		return "", err
	}

	// Calculate the next run time for the job (interval in minutes)
	scheduledAt := time.Now().Add(time.Duration(interval) * time.Minute)

	// Add job to scheduled jobs
	query = fmt.Sprintf(`
		INSERT INTO %s (job_id, scheduled_at, max_retry)
		VALUES ($1, $2, $3)
	`, scheduledJobsTable)

	if _, err = tx.Exec(ctx, query, jobID, scheduledAt, maxRetry); err != nil {
		err = status.Errorf(codes.Internal, "failed to insert scheduled job: %v", err)
		return "", err
	}

	// Commit transaction
	if err = tx.Commit(ctx); err != nil {
		err = status.Errorf(codes.Internal, "failed to commit transaction: %v", err)
		return "", err
	}

	return jobID, nil
}
