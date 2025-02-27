package jobs

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jackc/pgx/v5"

	"github.com/hitesh22rana/chronoverse/internal/model"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	jobsTable          = "jobs"
	scheduledJobsTable = "scheduled_jobs"
	limit              = 10
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

// CreateJob creates a new job.
func (r *Repository) CreateJob(ctx context.Context, userID, name, payload, kind string, interval, maxRetry int32) (jobID string, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.CreateJob")
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
		INSERT INTO %s (job_id, user_id, scheduled_at, max_retry)
		VALUES ($1, $2, $3, $4)
	`, scheduledJobsTable)

	if _, err = tx.Exec(ctx, query, jobID, userID, scheduledAt, maxRetry); err != nil {
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

// UpdateJob updates the job details.
func (r *Repository) UpdateJob(ctx context.Context, jobID, userID, name, payload, kind string, interval int32) (err error) {
	ctx, span := r.tp.Start(ctx, "Repository.UpdateJob")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	query := fmt.Sprintf(`
		UPDATE %s
		SET name = $1, payload = $2, kind = $3, interval = $4
		WHERE id = $5 AND user_id = $6
	`, jobsTable)

	// Execute the query
	ct, err := r.pg.Exec(ctx, query, name, payload, kind, interval, jobID, userID)
	if err != nil {
		if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid job ID: %v", err)
			return err
		}

		err = status.Errorf(codes.Internal, "failed to update job: %v", err)
		return err
	}

	if ct.RowsAffected() == 0 {
		err = status.Errorf(codes.NotFound, "job not found or not owned by user")
		return err
	}

	return nil
}

// GetJob returns the job details by ID and user ID.
func (r *Repository) GetJob(ctx context.Context, jobID, userID string) (res *model.GetJobResponse, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.GetJob")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	query := fmt.Sprintf(`
		SELECT id, name, payload, kind, interval, created_at, updated_at, terminated_at
		FROM %s
		WHERE id = $1 AND user_id = $2
	`, jobsTable)

	//nolint:errcheck // The error is handled in the next line
	rows, _ := r.pg.Query(ctx, query, jobID, userID)
	res, err = pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByName[model.GetJobResponse])
	if err != nil {
		if r.pg.IsNoRows(err) {
			err = status.Errorf(codes.NotFound, "job not found or not owned by user: %v", err)
			return nil, err
		} else if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid job ID: %v", err)
			return nil, err
		}

		err = status.Errorf(codes.Internal, "failed to get job: %v", err)
		return nil, err
	}

	return res, nil
}

// GetJobByID returns the job details by ID.
func (r *Repository) GetJobByID(ctx context.Context, jobID string) (res *model.GetJobByIDResponse, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.GetJobByID")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	query := fmt.Sprintf(`
		SELECT user_id, name, payload, kind, interval, created_at, updated_at, terminated_at
		FROM %s
		WHERE id = $1
	`, jobsTable)

	//nolint:errcheck // The error is handled in the next line
	rows, _ := r.pg.Query(ctx, query, jobID)
	res, err = pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByName[model.GetJobByIDResponse])
	if err != nil {
		if r.pg.IsNoRows(err) {
			err = status.Errorf(codes.NotFound, "job not found: %v", err)
			return nil, err
		} else if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid job ID: %v", err)
			return nil, err
		}

		err = status.Errorf(codes.Internal, "failed to get job: %v", err)
		return nil, err
	}

	return res, nil
}

// ListJobsByUserID returns jobs by user ID.
func (r *Repository) ListJobsByUserID(ctx context.Context, userID, nextPageToken string) (res *model.ListJobsByUserIDResponse, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.ListJobsByUserID")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	query := fmt.Sprintf(`
		SELECT id, name, payload, kind, interval, created_at, updated_at, terminated_at
		FROM %s
		WHERE user_id = $1
	`, jobsTable)
	args := []any{userID}

	if nextPageToken != "" {
		query += ` AND id <= $2`
		args = append(args, nextPageToken)
	}

	query += fmt.Sprintf(` ORDER BY id DESC LIMIT %d`, limit+1)

	//nolint:errcheck // The error is handled in the next line
	rows, _ := r.pg.Query(ctx, query, args...)
	data, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[model.JobByUserIDResponse])
	if err != nil {
		if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid user ID: %v", err)
			return nil, err
		}

		err = status.Errorf(codes.Internal, "failed to list all jobs: %v", err)
		return nil, err
	}

	// Check if there are more jobs
	nextPageToken = ""
	if len(data) > limit {
		nextPageToken = data[limit].ID
		data = data[:limit]
	}

	return &model.ListJobsByUserIDResponse{
		Jobs:          data,
		NextPageToken: encodeNextPageToken(nextPageToken),
	}, nil
}

// ListScheduledJobs returns scheduled jobs.
func (r *Repository) ListScheduledJobs(ctx context.Context, jobID, userID, nextPageToken string) (res *model.ListScheduledJobsResponse, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.ListScheduledJobs")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Add the next page token to the query
	query := fmt.Sprintf(`
		SELECT id, status, scheduled_at, retry_count, max_retry, started_at, completed_at, created_at, updated_at
		FROM %s
		WHERE job_id = $1 AND user_id = $2
	`, scheduledJobsTable)
	args := []any{jobID, userID}

	if nextPageToken != "" {
		query += ` AND id <= $3`
		args = append(args, nextPageToken)
	}

	query += fmt.Sprintf(` ORDER BY id DESC LIMIT %d`, limit+1)

	//nolint:errcheck // The error is handled in the next line
	rows, _ := r.pg.Query(ctx, query, args...)
	data, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[model.ScheduledJobByJobIDResponse])
	if err != nil {
		if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid job ID: %v", err)
			return nil, err
		}

		err = status.Errorf(codes.Internal, "failed to list all scheduled jobs: %v", err)
		return nil, err
	}

	// Check if there are more scheduled jobs
	nextPageToken = ""
	if len(data) > limit {
		nextPageToken = data[limit].ID
		data = data[:limit]
	}

	return &model.ListScheduledJobsResponse{
		ScheduledJobs: data,
		NextPageToken: encodeNextPageToken(nextPageToken),
	}, nil
}

func encodeNextPageToken(id string) string {
	return base64.StdEncoding.EncodeToString([]byte(id))
}
