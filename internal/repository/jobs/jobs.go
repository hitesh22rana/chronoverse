package jobs

import (
	"bytes"
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

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	jobsTable = "jobs"
	delimiter = '$'
)

// Config represents the repository constants configuration.
type Config struct {
	FetchLimit int
}

// Repository provides jobs repository.
type Repository struct {
	tp  trace.Tracer
	cfg *Config
	pg  *postgres.Postgres
}

// New creates a new jobs repository.
func New(cfg *Config, pg *postgres.Postgres) *Repository {
	return &Repository{
		tp:  otel.Tracer(svcpkg.Info().GetName()),
		cfg: cfg,
		pg:  pg,
	}
}

// ScheduleJob schedules a job.
func (r Repository) ScheduleJob(ctx context.Context, workflowID, userID, scheduledAt string) (jobID string, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.ScheduleJob")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	scheduledAtTime, err := parseTime(scheduledAt)

	// Insert job into database
	query := fmt.Sprintf(`
		INSERT INTO %s (workflow_id, user_id, scheduled_at)
		VALUES ($1, $2, $3)
		RETURNING id;
	`, jobsTable)

	row := r.pg.QueryRow(ctx, query, workflowID, userID, scheduledAtTime)
	if err = row.Scan(&jobID); err != nil {
		err = status.Errorf(codes.Internal, "failed to insert job: %v", err)
		return "", err
	}

	return jobID, nil
}

// UpdateJobStatus updates the job details.
func (r *Repository) UpdateJobStatus(ctx context.Context, jobID, jobStatus string) (err error) {
	ctx, span := r.tp.Start(ctx, "Repository.UpdateJobStatus")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	query := fmt.Sprintf(`
	UPDATE %s
	SET status = $1`, jobsTable)
	args := []any{jobStatus}

	switch jobStatus {
	case jobsmodel.JobStatusRunning.ToString():
		query += `, started_at = $2
		WHERE id = $3;`
		args = append(args, time.Now(), jobID)
	case jobsmodel.JobStatusCompleted.ToString(), jobsmodel.JobStatusFailed.ToString():
		query += `, completed_at = $2
		WHERE id = $3;`
		args = append(args, time.Now(), jobID)
	default:
		query += ` WHERE id = $2;`
		args = append(args, jobID)
	}

	// Execute the query
	ct, err := r.pg.Exec(ctx, query, args...)
	if err != nil {
		if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid job ID: %v", err)

			return err
		}

		err = status.Errorf(codes.Internal, "failed to update job: %v", err)
		return err
	}

	if ct.RowsAffected() == 0 {
		err = status.Errorf(codes.NotFound, "job not found")
		return err
	}

	return nil
}

// GetJob returns the job details by ID and Job ID and user ID.
func (r *Repository) GetJob(ctx context.Context, jobID, workflowID, userID string) (res *jobsmodel.GetJobResponse, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.GetJob")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	query := fmt.Sprintf(`
		SELECT id, workflow_id, status, scheduled_at, started_at, completed_at, created_at, updated_at
		FROM %s
		WHERE id = $1 AND workflow_id = $2 AND user_id = $3
		LIMIT 1;
	`, jobsTable)

	//nolint:errcheck // The error is handled in the next line
	rows, _ := r.pg.Query(ctx, query, jobID, workflowID, userID)
	res, err = pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByName[jobsmodel.GetJobResponse])
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
func (r *Repository) GetJobByID(ctx context.Context, jobID string) (res *jobsmodel.GetJobByIDResponse, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.GetJobByID")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	query := fmt.Sprintf(`
		SELECT id, workflow_id, user_id, status, scheduled_at, started_at, completed_at, created_at, updated_at
		FROM %s
		WHERE id = $1
		LIMIT 1;
	`, jobsTable)

	//nolint:errcheck // The error is handled in the next line
	rows, _ := r.pg.Query(ctx, query, jobID)
	res, err = pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByName[jobsmodel.GetJobByIDResponse])
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

// ListJobs returns jobs.
func (r *Repository) ListJobs(ctx context.Context, workflowID, userID, cursor string) (res *jobsmodel.ListJobsResponse, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.ListJobs")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Add the next page token to the query
	query := fmt.Sprintf(`
		SELECT id, workflow_id, status, scheduled_at, started_at, completed_at, created_at, updated_at
		FROM %s
		WHERE workflow_id = $1 AND user_id = $2
	`, jobsTable)
	args := []any{workflowID, userID}

	if cursor != "" {
		id, createdAt, _err := extractDataFromCursor(cursor)
		if _err != nil {
			err = _err
			return nil, err
		}

		query += ` AND (created_at, id) <= ($3, $4)`
		args = append(args, createdAt, id)
	}

	query += fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT %d;`, r.cfg.FetchLimit+1)

	//nolint:errcheck // The error is handled in the next line
	rows, _ := r.pg.Query(ctx, query, args...)
	data, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[jobsmodel.JobByWorkflowIDResponse])
	if err != nil {
		if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid job ID: %v", err)
			return nil, err
		}

		err = status.Errorf(codes.Internal, "failed to list all jobs: %v", err)
		return nil, err
	}

	// Check if there are more jobs
	cursor = ""
	if len(data) > r.cfg.FetchLimit {
		cursor = fmt.Sprintf(
			"%s%c%s",
			data[r.cfg.FetchLimit].ID,
			delimiter, data[r.cfg.FetchLimit].CreatedAt.Format(time.RFC3339Nano),
		)
		data = data[:r.cfg.FetchLimit]
	}

	return &jobsmodel.ListJobsResponse{
		Jobs:   data,
		Cursor: encodeCursor(cursor),
	}, nil
}

func parseTime(t string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, t)
}

func encodeCursor(cursor string) string {
	return base64.StdEncoding.EncodeToString([]byte(cursor))
}

func extractDataFromCursor(cursor string) (string, time.Time, error) {
	parts := bytes.Split([]byte(cursor), []byte{delimiter})
	if len(parts) != 2 {
		return "", time.Time{}, status.Error(codes.InvalidArgument, "invalid cursor: expected two parts")
	}

	createdAt, err := time.Parse(time.RFC3339Nano, string(parts[1]))
	if err != nil {
		return "", time.Time{}, status.Errorf(codes.InvalidArgument, "invalid timestamp: %v", err)
	}

	return string(parts[0]), createdAt, nil
}
