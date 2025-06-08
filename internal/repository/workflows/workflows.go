package workflows

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jackc/pgx/v5"
	"github.com/twmb/franz-go/pkg/kgo"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	workflowsTable = "workflows"
	delimiter      = '$'
)

// Config represents the repository constants configuration.
type Config struct {
	FetchLimit    int
	ProducerTopic string
}

// Repository provides workflows repository.
type Repository struct {
	tp  trace.Tracer
	cfg *Config
	pg  *postgres.Postgres
	kfk *kgo.Client
}

// New creates a new workflows repository.
func New(cfg *Config, pg *postgres.Postgres, kfk *kgo.Client) *Repository {
	return &Repository{
		tp:  otel.Tracer(svcpkg.Info().GetName()),
		cfg: cfg,
		pg:  pg,
		kfk: kfk,
	}
}

// CreateWorkflow creates a new workflow.
func (r *Repository) CreateWorkflow(
	ctx context.Context,
	userID,
	name,
	payload,
	kind string,
	interval,
	maxConsecutiveJobFailuresAllowed int32,
) (res *workflowsmodel.GetWorkflowResponse, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.CreateWorkflow")
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
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			err = status.Error(codes.DeadlineExceeded, err.Error())
			return nil, err
		}

		err = status.Errorf(codes.Internal, "failed to start transaction: %v", err)
		return nil, err
	}
	//nolint:errcheck // The error is handled in the next line
	defer tx.Rollback(ctx)

	var query string
	var args []any

	if maxConsecutiveJobFailuresAllowed == 0 {
		query = fmt.Sprintf(`
			INSERT INTO %s (user_id, name, payload, kind, interval)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id, name, payload, kind, build_status, interval, consecutive_job_failures_count, max_consecutive_job_failures_allowed, created_at, updated_at, terminated_at;
			`, workflowsTable)
		args = []any{userID, name, payload, kind, interval}
	} else {
		query = fmt.Sprintf(`
			INSERT INTO %s (user_id, name, payload, kind, interval, max_consecutive_job_failures_allowed)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id, name, payload, kind, build_status, interval, consecutive_job_failures_count, max_consecutive_job_failures_allowed, created_at, updated_at, terminated_at;
			`, workflowsTable)
		args = []any{userID, name, payload, kind, interval, maxConsecutiveJobFailuresAllowed}
	}

	rows, err := tx.Query(ctx, query, args...)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		err = status.Error(codes.DeadlineExceeded, err.Error())
		return nil, err
	}

	res, err = pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByName[workflowsmodel.GetWorkflowResponse])
	if err != nil {
		err = status.Errorf(codes.Internal, "failed to insert workflow: %v", err)
		return nil, err
	}

	//nolint:errcheck // We don't expect an error here
	workflowEntryBytes, _ := json.Marshal(&workflowsmodel.WorkflowEntry{
		ID:     res.ID,
		UserID: userID,
		Action: workflowsmodel.ActionBuild,
	})

	record := &kgo.Record{
		Topic: r.cfg.ProducerTopic,
		Key:   []byte(res.ID),
		Value: workflowEntryBytes,
	}
	// Publish the workflowID to the Kafka topic for the build step
	if err = r.kfk.ProduceSync(ctx, record).FirstErr(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			err = status.Error(codes.DeadlineExceeded, err.Error())
			return nil, err
		}

		err = status.Errorf(codes.Internal, "failed to publish workflow entry to kafka: %v", err)
		return nil, err
	}

	// Commit transaction
	if err = tx.Commit(ctx); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			err = status.Error(codes.DeadlineExceeded, err.Error())
			return nil, err
		}

		err = status.Errorf(codes.Internal, "failed to commit transaction: %v", err)
		return nil, err
	}

	return res, nil
}

// UpdateWorkflow updates the workflow details.
//
//nolint:gocyclo // The cyclomatic complexity is high due to the different conditions and queries.
func (r *Repository) UpdateWorkflow(
	ctx context.Context,
	workflowID,
	userID,
	name,
	payload string,
	interval,
	maxConsecutiveJobFailuresAllowed int32,
) (err error) {
	ctx, span := r.tp.Start(ctx, "Repository.UpdateWorkflow")
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
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			err = status.Error(codes.DeadlineExceeded, err.Error())
			return err
		}

		err = status.Errorf(codes.Internal, "failed to start transaction: %v", err)
		return err
	}
	//nolint:errcheck // The error is handled in the next line
	defer tx.Rollback(ctx)

	// During update, we set the consecutive_job_failures_count to 0 and terminated_at to NULL
	// This is done to ensure that the workflow can be retried from the beginning.

	// Set all workflows to QUEUED so the worker can determine what to do
	var query string
	var args []any

	if maxConsecutiveJobFailuresAllowed == 0 {
		query = fmt.Sprintf(`
			UPDATE %s
			SET name = $1, payload = $2, interval = $3, build_status = $4, consecutive_job_failures_count = 0, terminated_at = NULL
			WHERE id = $5 AND user_id = $6;
		`, workflowsTable)
		args = []any{name, payload, interval, workflowsmodel.WorkflowBuildStatusQueued.ToString(), workflowID, userID}
	} else {
		query = fmt.Sprintf(`
			UPDATE %s
			SET name = $1, payload = $2, interval = $3, max_consecutive_job_failures_allowed = $4, build_status = $5, consecutive_job_failures_count = 0, terminated_at = NULL
			WHERE id = $6 AND user_id = $7;
		`, workflowsTable)
		args = []any{name, payload, interval, maxConsecutiveJobFailuresAllowed, workflowsmodel.WorkflowBuildStatusQueued.ToString(), workflowID, userID}
	}

	// Execute the query
	ct, err := tx.Exec(ctx, query, args...)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			err = status.Error(codes.DeadlineExceeded, err.Error())
			return err
		} else if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid workflow ID: %v", err)
			return err
		}

		err = status.Errorf(codes.Internal, "failed to update workflow: %v", err)
		return err
	}

	if ct.RowsAffected() == 0 {
		err = status.Errorf(codes.NotFound, "workflow not found")
		return err
	}

	//nolint:errcheck // We don't expect an error here
	workflowEntryBytes, _ := json.Marshal(&workflowsmodel.WorkflowEntry{
		ID:     workflowID,
		UserID: userID,
		Action: workflowsmodel.ActionBuild,
	})

	record := &kgo.Record{
		Topic: r.cfg.ProducerTopic,
		Key:   []byte(workflowID),
		Value: workflowEntryBytes,
	}
	// Publish the workflowID to the Kafka topic for the build step
	if err = r.kfk.ProduceSync(ctx, record).FirstErr(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			err = status.Error(codes.DeadlineExceeded, err.Error())
			return err
		}

		err = status.Errorf(codes.Internal, "failed to publish workflow entry to kafka: %v", err)
		return err
	}

	// Commit transaction
	if err = tx.Commit(ctx); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			err = status.Error(codes.DeadlineExceeded, err.Error())
			return err
		}

		err = status.Errorf(codes.Internal, "failed to commit transaction: %v", err)
		return err
	}

	return nil
}

// UpdateWorkflowBuildStatus updates the workflow build status.
func (r *Repository) UpdateWorkflowBuildStatus(ctx context.Context, workflowID, userID, buildStatus string) (err error) {
	ctx, span := r.tp.Start(ctx, "Repository.UpdateWorkflowBuildStatus")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	query := fmt.Sprintf(`
		UPDATE %s
		SET build_status = $1
		WHERE id = $2 AND user_id = $3
	`, workflowsTable)

	// Execute the query
	ct, err := r.pg.Exec(ctx, query, buildStatus, workflowID, userID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			err = status.Error(codes.DeadlineExceeded, err.Error())
			return err
		} else if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid workflow ID: %v", err)
			return err
		}

		err = status.Errorf(codes.Internal, "failed to update workflow build status: %v", err)
		return err
	}

	if ct.RowsAffected() == 0 {
		err = status.Errorf(codes.NotFound, "workflow not found")
		return err
	}

	return nil
}

// GetWorkflow returns the workflow details by ID and user ID.
func (r *Repository) GetWorkflow(ctx context.Context, workflowID, userID string) (res *workflowsmodel.GetWorkflowResponse, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.GetWorkflow")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	query := fmt.Sprintf(`
		SELECT id, name, payload, kind, build_status, interval, consecutive_job_failures_count, max_consecutive_job_failures_allowed, created_at, updated_at, terminated_at
		FROM %s
		WHERE id = $1 AND user_id = $2
		LIMIT 1;
	`, workflowsTable)

	rows, err := r.pg.Query(ctx, query, workflowID, userID)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		err = status.Error(codes.DeadlineExceeded, err.Error())
		return nil, err
	}

	res, err = pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByName[workflowsmodel.GetWorkflowResponse])
	if err != nil {
		if r.pg.IsNoRows(err) {
			err = status.Errorf(codes.NotFound, "workflow not found or not owned by user: %v", err)
			return nil, err
		} else if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid workflow ID: %v", err)
			return nil, err
		}

		err = status.Errorf(codes.Internal, "failed to get workflow: %v", err)
		return nil, err
	}

	return res, nil
}

// GetWorkflowByID returns the workflow details by ID.
func (r *Repository) GetWorkflowByID(ctx context.Context, workflowID string) (res *workflowsmodel.GetWorkflowByIDResponse, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.GetWorkflowByID")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	query := fmt.Sprintf(`
		SELECT id, user_id, name, payload, kind, build_status, interval, consecutive_job_failures_count, max_consecutive_job_failures_allowed, created_at, updated_at, terminated_at
		FROM %s
		WHERE id = $1
		LIMIT 1;
	`, workflowsTable)

	rows, err := r.pg.Query(ctx, query, workflowID)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		err = status.Error(codes.DeadlineExceeded, err.Error())
		return nil, err
	}

	res, err = pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByName[workflowsmodel.GetWorkflowByIDResponse])
	if err != nil {
		if r.pg.IsNoRows(err) {
			err = status.Errorf(codes.NotFound, "workflow not found: %v", err)
			return nil, err
		} else if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid workflow ID: %v", err)
			return nil, err
		}

		err = status.Errorf(codes.Internal, "failed to get workflow: %v", err)
		return nil, err
	}

	return res, nil
}

// IncrementWorkflowConsecutiveJobFailuresCount increments the consecutive failures counter.
// Returns whether threshold was reached or not.
func (r *Repository) IncrementWorkflowConsecutiveJobFailuresCount(ctx context.Context, workflowID, userID string) (thresholdReached bool, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.IncrementWorkflowConsecutiveJobFailuresCount")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	query := fmt.Sprintf(`
		UPDATE %s
		SET consecutive_job_failures_count = consecutive_job_failures_count + 1
		WHERE id = $1 AND user_id = $2 AND terminated_at IS NULL
		RETURNING consecutive_job_failures_count, max_consecutive_job_failures_allowed;
	`, workflowsTable)

	var consecutiveJobFailuresCount, maxConsecutiveJobFailuresAllowed int32
	err = r.pg.QueryRow(ctx, query, workflowID, userID).Scan(&consecutiveJobFailuresCount, &maxConsecutiveJobFailuresAllowed)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			err = status.Error(codes.DeadlineExceeded, err.Error())
			return false, err
		}

		if r.pg.IsNoRows(err) {
			err = status.Errorf(codes.NotFound, "workflow not found: %v", err)
			return false, err
		} else if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid workflow ID: %v", err)
			return false, err
		}

		err = status.Errorf(codes.Internal, "failed to increment consecutive job failures count: %v", err)
		return false, err
	}

	// Check if the threshold was reached
	thresholdReached = consecutiveJobFailuresCount >= maxConsecutiveJobFailuresAllowed
	return thresholdReached, nil
}

// ResetWorkflowConsecutiveJobFailuresCount resets the consecutive failures counter.
func (r *Repository) ResetWorkflowConsecutiveJobFailuresCount(ctx context.Context, workflowID, userID string) (err error) {
	ctx, span := r.tp.Start(ctx, "Repository.ResetWorkflowConsecutiveJobFailuresCount")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	query := fmt.Sprintf(`
		UPDATE %s
		SET consecutive_job_failures_count = 0
		WHERE id = $1 AND user_id = $2 AND terminated_at IS NULL;
	`, workflowsTable)

	// Execute the query
	ct, err := r.pg.Exec(ctx, query, workflowID, userID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			err = status.Error(codes.DeadlineExceeded, err.Error())
			return err
		} else if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid workflow ID: %v", err)
			return err
		}

		err = status.Errorf(codes.Internal, "failed to reset consecutive job failures count: %v", err)
		return err
	}

	if ct.RowsAffected() == 0 {
		err = status.Errorf(codes.NotFound, "workflow not found or already terminated")
		return err
	}

	return nil
}

// TerminateWorkflow terminates a workflow.
//
//nolint:gocyclo // The cyclomatic complexity is high due to the different conditions and queries.
func (r *Repository) TerminateWorkflow(ctx context.Context, workflowID, userID string) (err error) {
	ctx, span := r.tp.Start(ctx, "Repository.TerminateWorkflow")
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
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			err = status.Error(codes.DeadlineExceeded, err.Error())
			return err
		}

		err = status.Errorf(codes.Internal, "failed to start transaction: %v", err)
		return err
	}
	//nolint:errcheck // The error is handled in the next line
	defer tx.Rollback(ctx)

	query := fmt.Sprintf(`
		UPDATE %s
		SET terminated_at = NOW()
		WHERE id = $1 AND user_id = $2 AND terminated_at IS NULL;
	`, workflowsTable)

	// Execute the query
	ct, err := tx.Exec(ctx, query, workflowID, userID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			err = status.Error(codes.DeadlineExceeded, err.Error())
			return err
		}

		if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid workflow ID: %v", err)
			return err
		}

		err = status.Errorf(codes.Internal, "failed to terminate workflow: %v", err)
		return err
	}

	if ct.RowsAffected() == 0 {
		err = status.Errorf(codes.NotFound, "workflow not found or not owned by user")
		return err
	}

	//nolint:errcheck // We don't expect an error here
	workflowEntryBytes, _ := json.Marshal(&workflowsmodel.WorkflowEntry{
		ID:     workflowID,
		UserID: userID,
		Action: workflowsmodel.ActionTerminate,
	})

	record := &kgo.Record{
		Topic: r.cfg.ProducerTopic,
		Key:   []byte(workflowID),
		Value: workflowEntryBytes,
	}
	// Publish the workflowID to the Kafka topic for the build step
	if err = r.kfk.ProduceSync(ctx, record).FirstErr(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			err = status.Error(codes.DeadlineExceeded, err.Error())
			return err
		}

		err = status.Errorf(codes.Internal, "failed to publish workflow entry to kafka: %v", err)
		return err
	}

	// Commit transaction
	if err = tx.Commit(ctx); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			err = status.Error(codes.DeadlineExceeded, err.Error())
			return err
		}

		err = status.Errorf(codes.Internal, "failed to commit transaction: %v", err)
		return err
	}

	return nil
}

// ListWorkflows returns workflows by user ID.
//
//nolint:gocyclo // The cyclomatic complexity is high due to the different conditions and queries.
func (r *Repository) ListWorkflows(ctx context.Context, userID, cursor string, filters *workflowsmodel.ListWorkflowsFilters) (res *workflowsmodel.ListWorkflowsResponse, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.ListWorkflows")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Base query for user's workflows
	query := fmt.Sprintf(`
        SELECT id, name, payload, kind, build_status, interval, consecutive_job_failures_count, max_consecutive_job_failures_allowed, created_at, updated_at, terminated_at
        FROM %s
        WHERE user_id = $1
    `, workflowsTable)
	args := []any{userID}
	paramIndex := 2 // Start from $2 since $1 is already used for userID

	// Apply filters if provided
	//nolint:nestif // The filters are applied in the next lines
	if filters != nil {
		// Text search on workflow name
		if filters.Query != "" {
			query += fmt.Sprintf(` AND name ILIKE $%d`, paramIndex)
			args = append(args, "%"+filters.Query+"%")
			paramIndex++
		}

		// Filter by workflow kind
		if filters.Kind != "" {
			query += fmt.Sprintf(` AND kind = $%d`, paramIndex)
			args = append(args, filters.Kind)
			paramIndex++
		}

		// If build status is provided, filter by it
		// Otherwise, filter by termination status

		// Filter by build status
		if filters.BuildStatus != "" {
			query += fmt.Sprintf(` AND build_status = $%d`, paramIndex)
			args = append(args, filters.BuildStatus)
			paramIndex++
			query += ` AND terminated_at IS NULL`
		} else if filters.IsTerminated {
			query += ` AND terminated_at IS NOT NULL`
		}

		// Filter by interval range
		if filters.IntervalMin > 0 {
			query += fmt.Sprintf(` AND interval >= $%d`, paramIndex)
			args = append(args, filters.IntervalMin)
			paramIndex++
		}

		if filters.IntervalMax > 0 {
			query += fmt.Sprintf(` AND interval <= $%d`, paramIndex)
			args = append(args, filters.IntervalMax)
			paramIndex++
		}
	}

	if cursor != "" {
		id, createdAt, _err := extractDataFromCursor(cursor)
		if _err != nil {
			err = _err
			return nil, err
		}

		query += fmt.Sprintf(` AND (created_at, id) <= ($%d, $%d)`, paramIndex, paramIndex+1)
		args = append(args, createdAt, id)
	}

	// Always sort by created_at DESC, id DESC for consistency
	query += fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT %d;`, r.cfg.FetchLimit+1)

	rows, err := r.pg.Query(ctx, query, args...)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		err = status.Error(codes.DeadlineExceeded, err.Error())
		return nil, err
	}

	data, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[workflowsmodel.WorkflowByUserIDResponse])
	if err != nil {
		if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid user ID: %v", err)
			return nil, err
		}

		err = status.Errorf(codes.Internal, "failed to list all workflows: %v", err)
		return nil, err
	}

	// Check if there are more workflows
	cursor = ""
	if len(data) > r.cfg.FetchLimit {
		cursor = fmt.Sprintf(
			"%s%c%s",
			data[r.cfg.FetchLimit].ID,
			delimiter,
			data[r.cfg.FetchLimit].CreatedAt.Format(time.RFC3339Nano),
		)
		data = data[:r.cfg.FetchLimit]
	}

	return &workflowsmodel.ListWorkflowsResponse{
		Workflows: data,
		Cursor:    encodeCursor(cursor),
	}, nil
}

func encodeCursor(cursor string) string {
	if cursor == "" {
		return ""
	}

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
