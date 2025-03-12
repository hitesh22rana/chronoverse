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

	"github.com/hitesh22rana/chronoverse/internal/model"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	jobsTable          = "jobs"
	scheduledJobsTable = "scheduled_jobs"
	delimiter          = '$'
)

// Config represents the repository constants configuration.
type Config struct {
	FetchLimit int
}

// Repository provides jobs repository.
type Repository struct {
	tp   trace.Tracer
	cfg  *Config
	auth *auth.Auth
	pg   *postgres.Postgres
}

// New creates a new jobs repository.
func New(cfg *Config, auth *auth.Auth, pg *postgres.Postgres) *Repository {
	return &Repository{
		tp:   otel.Tracer(svcpkg.Info().GetName()),
		cfg:  cfg,
		auth: auth,
		pg:   pg,
	}
}

// CreateJob creates a new job.
func (r *Repository) CreateJob(ctx context.Context, userID, name, payload, kind string, interval int32) (jobID string, err error) {
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
		RETURNING id;
	`, jobsTable)

	row := tx.QueryRow(ctx, query, userID, name, payload, kind, interval)
	if err = row.Scan(&jobID); err != nil {
		err = status.Errorf(codes.Internal, "failed to insert job: %v", err)
		return "", err
	}

	// Calculate the next run time for the job (interval in minutes)
	scheduleAtTime := time.Now().Add(time.Duration(interval) * time.Minute)

	// Add job to scheduled jobs
	query = fmt.Sprintf(`
		INSERT INTO %s (job_id, user_id, scheduled_at)
		VALUES ($1, $2, $3);
	`, scheduledJobsTable)

	if _, err = tx.Exec(ctx, query, jobID, userID, scheduleAtTime); err != nil {
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
func (r *Repository) UpdateJob(ctx context.Context, jobID, userID, name, payload string, interval int32) (err error) {
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
		SET name = $1, payload = $2, interval = $3
		WHERE id = $4 AND user_id = $5;
	`, jobsTable)

	// Execute the query
	ct, err := r.pg.Exec(ctx, query, name, payload, interval, jobID, userID)
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
		WHERE id = $1 AND user_id = $2;
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
//
//nolint:dupl // It's okay to have similar code for different methods.
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
		SELECT id, user_id, name, payload, kind, interval, created_at, updated_at, terminated_at
		FROM %s
		WHERE id = $1;
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

// ScheduleJob schedules a job.
func (r Repository) ScheduleJob(ctx context.Context, jobID, userID, scheduledAt string) (scheduledJobID string, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.ScheduleJob")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	scheduledAtTime, err := parseTime(scheduledAt)

	// Insert scheduled job into database
	query := fmt.Sprintf(`
		INSERT INTO %s (job_id, user_id, scheduled_at)
		VALUES ($1, $2, $3)
		RETURNING id;
	`, scheduledJobsTable)

	row := r.pg.QueryRow(ctx, query, jobID, userID, scheduledAtTime)
	if err = row.Scan(&scheduledJobID); err != nil {
		err = status.Errorf(codes.Internal, "failed to insert scheduled job: %v", err)
		return "", err
	}

	return scheduledJobID, nil
}

// UpdateScheduledJobStatus updates the scheduled job details.
func (r *Repository) UpdateScheduledJobStatus(ctx context.Context, scheduledJobID, scheduledJobStatus string) (err error) {
	ctx, span := r.tp.Start(ctx, "Repository.UpdateScheduledJobStatus")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	query := fmt.Sprintf(`
	UPDATE %s
	SET status = $1`, scheduledJobsTable)
	args := []any{scheduledJobStatus}

	switch scheduledJobStatus {
	case model.StatusRunning.ToString():
		query += `, started_at = $2
		WHERE id = $3;`
		args = append(args, time.Now(), scheduledJobID)
	case model.StatusCompleted.ToString(), model.StatusFailed.ToString():
		query += `, completed_at = $2
		WHERE id = $3;`
		args = append(args, time.Now(), scheduledJobID)
	default:
		query += ` WHERE id = $2;`
		args = append(args, scheduledJobID)
	}

	// Execute the query
	ct, err := r.pg.Exec(ctx, query, args...)
	if err != nil {
		if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid scheduled job ID: %v", err)

			return err
		}

		err = status.Errorf(codes.Internal, "failed to update scheduled job: %v", err)
		return err
	}

	if ct.RowsAffected() == 0 {
		err = status.Errorf(codes.NotFound, "scheduled job not found")
		return err
	}

	return nil
}

// GetScheduledJobByID returns the scheduled job details by ID.
//
//nolint:dupl // It's okay to have similar code in the same file.
func (r *Repository) GetScheduledJobByID(ctx context.Context, scheduledJobID string) (res *model.GetScheduledJobByIDResponse, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.GetScheduledJobByID")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	query := fmt.Sprintf(`
		SELECT id, job_id, user_id, status, scheduled_at, started_at, completed_at, created_at, updated_at
		FROM %s
		WHERE id = $1;
	`, scheduledJobsTable)

	//nolint:errcheck // The error is handled in the next line
	rows, _ := r.pg.Query(ctx, query, scheduledJobID)
	res, err = pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByName[model.GetScheduledJobByIDResponse])
	if err != nil {
		if r.pg.IsNoRows(err) {
			err = status.Errorf(codes.NotFound, "scheduled job not found: %v", err)
			return nil, err
		} else if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid scheduled job ID: %v", err)
			return nil, err
		}

		err = status.Errorf(codes.Internal, "failed to get scheduled job: %v", err)
		return nil, err
	}

	return res, nil
}

// ListJobsByUserID returns jobs by user ID.
func (r *Repository) ListJobsByUserID(ctx context.Context, userID, cursor string) (res *model.ListJobsByUserIDResponse, err error) {
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

	if cursor != "" {
		id, createdAt, _err := extractDataFromCursor(cursor)
		if _err != nil {
			err = _err
			return nil, err
		}

		query += ` AND (created_at, id) <= ($2, $3)`
		args = append(args, createdAt, id)
	}

	query += fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT %d;`, r.cfg.FetchLimit+1)

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
	cursor = ""
	if len(data) > r.cfg.FetchLimit {
		cursor = fmt.Sprintf(
			"%s%c%s",
			data[r.cfg.FetchLimit].ID,
			delimiter, data[r.cfg.FetchLimit].CreatedAt.Format(time.RFC3339Nano),
		)
		data = data[:r.cfg.FetchLimit]
	}

	return &model.ListJobsByUserIDResponse{
		Jobs:   data,
		Cursor: encodeCursor(cursor),
	}, nil
}

// ListScheduledJobs returns scheduled jobs.
func (r *Repository) ListScheduledJobs(ctx context.Context, jobID, userID, cursor string) (res *model.ListScheduledJobsResponse, err error) {
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
		SELECT id, status, scheduled_at, started_at, completed_at, created_at, updated_at
		FROM %s
		WHERE job_id = $1 AND user_id = $2
	`, scheduledJobsTable)
	args := []any{jobID, userID}

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
	cursor = ""
	if len(data) > r.cfg.FetchLimit {
		cursor = fmt.Sprintf(
			"%s%c%s",
			data[r.cfg.FetchLimit].ID,
			delimiter, data[r.cfg.FetchLimit].CreatedAt.Format(time.RFC3339Nano),
		)
		data = data[:r.cfg.FetchLimit]
	}

	return &model.ListScheduledJobsResponse{
		ScheduledJobs: data,
		Cursor:        encodeCursor(cursor),
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
