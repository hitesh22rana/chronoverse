package jobs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jackc/pgx/v5"
	"github.com/meilisearch/meilisearch-go"
	goredis "github.com/redis/go-redis/v9"

	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	"github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	meilisearchpkg "github.com/hitesh22rana/chronoverse/internal/pkg/meilisearch"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	authSubject           = "internal/jobs"
	delimiter             = '$'
	jobStatusUpdateBuffer = time.Minute // 1 minute
)

type jobLogsCursor struct {
	SequenceNum uint32 `json:"sequence_num"`
	Stream      string `json:"stream,omitempty"`
	EventID     string `json:"event_id,omitempty"`
}

// Services represents the services used by the executor.
type Services struct {
	Workflows workflowspb.WorkflowsServiceClient
}

// Config represents the repository constants configuration.
type Config struct {
	FetchLimit     int
	LogsFetchLimit int
}

// Repository provides jobs repository.
type Repository struct {
	tp   trace.Tracer
	cfg  *Config
	auth auth.IAuth
	pg   *postgres.Postgres
	rdb  *redis.Store
	ch   *clickhouse.Client
	ms   meilisearch.ServiceManager
	svc  *Services
}

// New creates a new jobs repository.
func New(cfg *Config, auth auth.IAuth, pg *postgres.Postgres, rdb *redis.Store, ch *clickhouse.Client, ms meilisearch.ServiceManager, svc *Services) *Repository {
	return &Repository{
		tp:   otel.Tracer(svcpkg.Info().GetName()),
		cfg:  cfg,
		auth: auth,
		pg:   pg,
		rdb:  rdb,
		ch:   ch,
		ms:   ms,
		svc:  svc,
	}
}

// ScheduleJob schedules a job.
func (r *Repository) ScheduleJob(
	ctx context.Context,
	workflowID,
	userID,
	scheduledAt,
	trigger,
	idempotencyKey string,
	workflowGeneration int64,
) (jobID string, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.ScheduleJob")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	scheduledAtTime, err := parseTime(scheduledAt)
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "invalid scheduled_at time format: %v", err)
		return "", err
	}

	idempotencyKey, automaticIdempotencyKeyProvided := normalizeScheduleJobIdempotencyKey(
		workflowID,
		scheduledAtTime,
		trigger,
		idempotencyKey,
	)
	query, args, err := scheduleJobInsertStatement(
		workflowID,
		userID,
		scheduledAtTime,
		trigger,
		idempotencyKey,
		workflowGeneration,
		automaticIdempotencyKeyProvided,
	)
	if err != nil {
		return "", err
	}

	row := r.pg.QueryRow(ctx, query, args...)
	if err = row.Scan(&jobID); err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			err = status.Error(codes.DeadlineExceeded, err.Error())
			return "", err
		case errors.Is(err, context.Canceled):
			err = status.Error(codes.Canceled, err.Error())
			return "", err
		case r.pg.IsNoRows(err) && trigger == jobsmodel.JobTriggerAutomatic.ToString() && workflowGeneration > 0:
			err = status.Errorf(codes.FailedPrecondition, "workflow generation mismatch or workflow is not schedulable")
			return "", err
		}

		err = status.Errorf(codes.Internal, "failed to insert job: %v", err)
		return "", err
	}

	return jobID, nil
}

func normalizeScheduleJobIdempotencyKey(
	workflowID string,
	scheduledAt time.Time,
	trigger,
	idempotencyKey string,
) (string, bool) {
	automaticIdempotencyKeyProvided := trigger == jobsmodel.JobTriggerAutomatic.ToString() && idempotencyKey != ""
	if trigger == jobsmodel.JobTriggerAutomatic.ToString() && !automaticIdempotencyKeyProvided {
		idempotencyKey = idempotency.JobDispatchEventKey(fmt.Sprintf("%s:%s", workflowID, scheduledAt.Format(time.RFC3339Nano)))
	}

	return idempotencyKey, automaticIdempotencyKeyProvided
}

func scheduleJobInsertStatement(
	workflowID,
	userID string,
	scheduledAt time.Time,
	trigger,
	idempotencyKey string,
	workflowGeneration int64,
	automaticIdempotencyKeyProvided bool,
) (query string, args []any, err error) {
	args = []any{workflowID, userID, scheduledAt, trigger, idempotencyKey}
	if trigger == jobsmodel.JobTriggerAutomatic.ToString() && workflowGeneration > 0 {
		args = append(args, workflowGeneration)
	}

	switch {
	case trigger == jobsmodel.JobTriggerManual.ToString():
		return fmt.Sprintf(`
            INSERT INTO %s (workflow_id, user_id, scheduled_at, trigger, idempotency_key)
            VALUES ($1, $2, $3, $4, $5)
            ON CONFLICT (user_id, workflow_id, idempotency_key)
            WHERE trigger = 'MANUAL' AND idempotency_key IS NOT NULL
            DO UPDATE SET idempotency_key = EXCLUDED.idempotency_key
            RETURNING id;
        `, postgres.TableJobs), args, nil
	case automaticIdempotencyKeyProvided:
		guard := automaticScheduleGuardSQL(workflowGeneration)
		return fmt.Sprintf(`
            INSERT INTO %s (workflow_id, user_id, scheduled_at, trigger, idempotency_key)
            SELECT $1, $2, $3, $4, $5
            %s
            ON CONFLICT (workflow_id, idempotency_key)
            WHERE trigger = 'AUTOMATIC' AND idempotency_key IS NOT NULL
            DO UPDATE SET idempotency_key = EXCLUDED.idempotency_key
            RETURNING id;
        `, postgres.TableJobs, guard), args, nil
	case trigger == jobsmodel.JobTriggerAutomatic.ToString():
		guard := automaticScheduleGuardSQL(workflowGeneration)
		return fmt.Sprintf(`
            INSERT INTO %s (workflow_id, user_id, scheduled_at, trigger, idempotency_key)
            SELECT $1, $2, $3, $4, $5
            %s
            ON CONFLICT (workflow_id, scheduled_at, trigger)
            WHERE trigger = 'AUTOMATIC'
            DO UPDATE SET workflow_id = EXCLUDED.workflow_id
            RETURNING id;
        `, postgres.TableJobs, guard), args, nil
	default:
		return "", nil, status.Errorf(codes.InvalidArgument, "invalid job trigger: %s", trigger)
	}
}

func automaticScheduleGuardSQL(workflowGeneration int64) string {
	if workflowGeneration <= 0 {
		return ""
	}

	return fmt.Sprintf(`
            FROM %s AS w
            WHERE w.id = $1
                AND w.user_id = $2
                AND w.generation = $6
                AND w.terminated_at IS NULL
                AND w.build_status = 'COMPLETED'
            FOR SHARE
    `, postgres.TableWorkflows)
}

// UpdateJobStatus updates the job details.
func (r *Repository) UpdateJobStatus(ctx context.Context, jobID, containerID, jobStatus string) (err error) {
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
    SET status = $1`, postgres.TableJobs)
	args := []any{jobStatus}

	switch jobStatus {
	case jobsmodel.JobStatusRunning.ToString():
		if containerID == "" {
			query += `, started_at = $2
            WHERE id = $3;`
			args = append(args, time.Now(), jobID)
		} else {
			query += `, started_at = $2, container_id = $3
            WHERE id = $4;`
			args = append(args, time.Now(), containerID, jobID)
		}
	case jobsmodel.JobStatusCompleted.ToString(), jobsmodel.JobStatusFailed.ToString():
		query += `, completed_at = $2,
            lease_token = NULL,
            leased_by = NULL,
            lease_expires_at = NULL,
            last_heartbeat_at = NULL
        WHERE id = $3;`
		args = append(args, time.Now(), jobID)
	case jobsmodel.JobStatusCanceled.ToString():
		query += `, completed_at = $2,
                lease_token = NULL,
                leased_by = NULL,
                lease_expires_at = NULL,
                last_heartbeat_at = NULL
            WHERE id = $3
                AND status IN ('PENDING', 'QUEUED', 'RUNNING');`
		args = append(args, time.Now(), jobID)
	default:
		query += ` WHERE id = $2;`
		args = append(args, jobID)
	}

	// Execute the query
	ct, err := r.pg.Exec(ctx, query, args...)
	//nolint:gocritic // Ifelse is used to handle different error types
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = status.Error(codes.DeadlineExceeded, err.Error())
			return err
		} else if errors.Is(err, context.Canceled) {
			err = status.Error(codes.Canceled, err.Error())
			return err
		} else if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid job ID: %v", err)
			return err
		}

		err = status.Errorf(codes.Internal, "failed to update job: %v", err)
		return err
	}

	if ct.RowsAffected() == 0 {
		if jobStatus == jobsmodel.JobStatusCanceled.ToString() {
			err = status.Errorf(codes.FailedPrecondition, "job not found or not cancellable")
			return err
		}

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
        SELECT id, workflow_id, status, trigger, scheduled_at, started_at, completed_at, created_at, updated_at
        FROM %s
        WHERE id = $1 AND workflow_id = $2 AND user_id = $3
        LIMIT 1;
    `, postgres.TableJobs)

	rows, err := r.pg.Query(ctx, query, jobID, workflowID, userID)
	if errors.Is(err, context.DeadlineExceeded) {
		err = status.Error(codes.DeadlineExceeded, err.Error())
		return nil, err
	} else if errors.Is(err, context.Canceled) {
		err = status.Error(codes.Canceled, err.Error())
		return nil, err
	}

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
        SELECT id, workflow_id, container_id, user_id, status, trigger, scheduled_at, started_at, completed_at, attempts, created_at, updated_at
        FROM %s
        WHERE id = $1
        LIMIT 1;
    `, postgres.TableJobs)

	rows, err := r.pg.Query(ctx, query, jobID)
	if errors.Is(err, context.DeadlineExceeded) {
		err = status.Error(codes.DeadlineExceeded, err.Error())
		return nil, err
	} else if errors.Is(err, context.Canceled) {
		err = status.Error(codes.Canceled, err.Error())
		return nil, err
	}

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

// GetJobLogs returns the job logs by ID.
//
//nolint:gocyclo  // This function is complex due to the nature of having two separate queries.
func (r *Repository) GetJobLogs(
	ctx context.Context,
	jobID,
	workflowID,
	userID,
	cursor string,
	sortOrder jobsmodel.JobLogsSortOrder,
	getJobLogsFilters *jobsmodel.GetJobLogsFilters,
) (res *jobsmodel.GetJobLogsResponse, jobStatus string, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.GetJobLogs")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Issue necessary headers and tokens for authorization
	ctx, ctxErr := r.withAuthorization(ctx)
	if ctxErr != nil {
		err = ctxErr
		return nil, "", err
	}

	// Validate workflow retention policy
	workflow, workflowErr := r.svc.Workflows.GetWorkflow(ctx, &workflowspb.GetWorkflowRequest{
		Id:     workflowID,
		UserId: userID,
	})
	if workflowErr != nil {
		err = workflowErr
		return nil, "", err
	}
	if !workflow.GetLogRetention() {
		err = status.Errorf(codes.FailedPrecondition, "logs retention is disabled for workflow: %s", workflowID)
		return nil, "", err
	}

	ascending := sortOrder == jobsmodel.JobLogsSortOrderAsc
	logQueryArgs := []any{jobID, workflowID, userID}
	logsQuery := fmt.Sprintf(`
    SELECT timestamp, message, sequence_num, stream, event_id
    FROM %s
    WHERE job_id = $1 AND workflow_id = $2 AND user_id = $3
    `, clickhouse.TableJobLogs)

	switch getJobLogsFilters.Stream {
	case 1:
		logsQuery += ` AND stream = 'stdout'`
	case 2:
		logsQuery += ` AND stream = 'stderr'`
	}

	if cursor != "" {
		logsCursor, _err := extractDataFromGetJobLogsCursor(cursor)
		if _err != nil {
			err = _err
			return nil, "", err
		}

		sequenceOperator := "<"
		if ascending {
			sequenceOperator = ">"
		}
		logsQuery += ` AND (
                sequence_num ` + sequenceOperator + ` $4
                OR (sequence_num = $4 AND stream > $5)
                OR (sequence_num = $4 AND stream = $5 AND event_id >= $6)
            )`
		logQueryArgs = append(logQueryArgs, logsCursor.SequenceNum, logsCursor.Stream, logsCursor.EventID)
	}

	sequenceDirection := "DESC"
	if ascending {
		sequenceDirection = "ASC"
	}
	// Keep the newest unmerged ReplacingMergeTree duplicate before LIMIT BY collapses event IDs.
	logsQuery += fmt.Sprintf(`
    ORDER BY sequence_num %s, stream ASC, event_id ASC, timestamp DESC
    LIMIT 1 BY event_id
    LIMIT %d;
    `, sequenceDirection, r.cfg.LogsFetchLimit+1)

	statusQueryArgs := []any{jobID, workflowID, userID}
	statusQuery := fmt.Sprintf(`
        SELECT status, completed_at
        FROM %s
        WHERE id = $1 AND workflow_id = $2 AND user_id = $3
    `, postgres.TableJobs)

	eg, egCtx := errgroup.WithContext(ctx)
	var (
		logs          []*jobsmodel.JobLog
		nextCursor    jobLogsCursor
		fetchedStatus string
		completedAt   sql.NullTime
	)

	eg.Go(func() error {
		rows, qErr := r.ch.Query(egCtx, logsQuery, logQueryArgs...)
		if qErr != nil {
			if errors.Is(qErr, context.DeadlineExceeded) {
				return status.Error(codes.DeadlineExceeded, qErr.Error())
			} else if errors.Is(qErr, context.Canceled) {
				return status.Error(codes.Canceled, qErr.Error())
			}
			return status.Errorf(codes.NotFound, "no logs found for job: %v", qErr)
		}
		defer rows.Close()

		tmp := make([]*jobsmodel.JobLog, 0, r.cfg.LogsFetchLimit+1)
		tmpCursors := make([]jobLogsCursor, 0, r.cfg.LogsFetchLimit+1)
		for rows.Next() {
			var (
				ts      time.Time
				msg     string
				seq     uint32
				strm    string
				eventID string
			)
			if scanErr := rows.Scan(&ts, &msg, &seq, &strm, &eventID); scanErr != nil {
				return status.Errorf(codes.Internal, "failed to scan logs: %v", scanErr)
			}
			tmp = append(tmp, &jobsmodel.JobLog{
				EventID:     eventID,
				Timestamp:   ts,
				Message:     msg,
				SequenceNum: seq,
				Stream:      strm,
			})
			tmpCursors = append(tmpCursors, jobLogsCursor{
				SequenceNum: seq,
				Stream:      strm,
				EventID:     eventID,
			})
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			return status.Errorf(codes.Internal, "rows error: %v", rowsErr)
		}

		// Check if there are more logs
		if len(tmp) > r.cfg.LogsFetchLimit {
			nextCursor = tmpCursors[r.cfg.LogsFetchLimit]
			tmp = tmp[:r.cfg.LogsFetchLimit]
		}
		logs = tmp
		return nil
	})

	eg.Go(func() error {
		row := r.pg.QueryRow(egCtx, statusQuery, statusQueryArgs...)

		//nolint:gocritic // Ifelse is used to handle different error types
		if scanErr := row.Scan(&fetchedStatus, &completedAt); scanErr != nil {
			if errors.Is(scanErr, context.DeadlineExceeded) {
				return status.Error(codes.DeadlineExceeded, scanErr.Error())
			} else if errors.Is(scanErr, context.Canceled) {
				return status.Error(codes.Canceled, scanErr.Error())
			} else if r.pg.IsNoRows(scanErr) {
				return status.Errorf(codes.NotFound, "job not found or not owned by user: %v", scanErr)
			} else if r.pg.IsInvalidTextRepresentation(scanErr) {
				return status.Errorf(codes.InvalidArgument, "invalid job ID: %v", scanErr)
			}

			return status.Errorf(codes.Internal, "failed to get job: %v", scanErr)
		}

		return nil
	})

	err = eg.Wait()
	if err != nil {
		return nil, "", err
	}

	// Buffer-based status override:
	// If the job just completed within the buffer window, we may not have all logs yet.
	// Treat it as "RUNNING" temporarily.
	if completedAt.Valid && time.Since(completedAt.Time) <= jobStatusUpdateBuffer {
		fetchedStatus = jobsmodel.JobStatusRunning.ToString()
	}

	return &jobsmodel.GetJobLogsResponse{
		ID:         jobID,
		WorkflowID: workflowID,
		JobLogs:    logs,
		Cursor:     encodeJobLogsCursor(nextCursor),
	}, fetchedStatus, nil
}

// StreamJobLogs returns a subscription to stream job logs.
func (r *Repository) StreamJobLogs(ctx context.Context, jobID, workflowID, userID string) (sub *goredis.PubSub, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.StreamJobLogs")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Issue necessary headers and tokens for authorization
	ctx, ctxErr := r.withAuthorization(ctx)
	if ctxErr != nil {
		err = ctxErr
		return nil, err
	}

	// Validate workflow retention policy
	workflow, workflowErr := r.svc.Workflows.GetWorkflow(ctx, &workflowspb.GetWorkflowRequest{
		Id:     workflowID,
		UserId: userID,
	})
	if workflowErr != nil {
		err = workflowErr
		return nil, err
	}
	if !workflow.GetLogRetention() {
		err = status.Errorf(codes.FailedPrecondition, "logs retention is disabled for workflow: %s", workflowID)
		return nil, err
	}

	// Validate whether the user has access to the job
	query := fmt.Sprintf(`
        SELECT id, status
        FROM %s
        WHERE id = $1 AND workflow_id = $2 AND user_id = $3
        LIMIT 1;
    `, postgres.TableJobs)
	row := r.pg.QueryRow(ctx, query, jobID, workflowID, userID)
	var id string
	var jobStatus string
	//nolint:gocritic // Ifelse is used to handle different error types
	if err = row.Scan(&id, &jobStatus); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = status.Error(codes.DeadlineExceeded, err.Error())
			return nil, err
		} else if errors.Is(err, context.Canceled) {
			err = status.Error(codes.Canceled, err.Error())
			return nil, err
		} else if r.pg.IsNoRows(err) {
			err = status.Errorf(codes.NotFound, "job not found or not owned by user: %v", err)
			return nil, err
		} else if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid job ID: %v", err)
			return nil, err
		}

		err = status.Errorf(codes.Internal, "failed to validate job: %v", err)
		return nil, err
	}

	if jobStatus != jobsmodel.JobStatusRunning.ToString() {
		err = status.Errorf(codes.FailedPrecondition, "job is not running: %s", jobStatus)
		return nil, err
	}

	// Subscribe to job-specific channel
	return r.rdb.Subscribe(ctx, redis.GetJobLogsChannel(jobID)), nil
}

// SearchJobLogs returns the filtered logs of a job.
//
//nolint:gocyclo // This function is complex and has multiple responsibilities.
func (r *Repository) SearchJobLogs(
	ctx context.Context,
	jobID,
	workflowID,
	userID,
	cursor string,
	searchJobLogsFilters *jobsmodel.SearchJobLogsFilters,
) (res *jobsmodel.GetJobLogsResponse, jobStatus string, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.SearchJobLogs")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Issue necessary headers and tokens for authorization
	ctx, ctxErr := r.withAuthorization(ctx)
	if ctxErr != nil {
		err = ctxErr
		return nil, "", err
	}

	// Validate workflow retention policy
	workflow, workflowErr := r.svc.Workflows.GetWorkflow(ctx, &workflowspb.GetWorkflowRequest{
		Id:     workflowID,
		UserId: userID,
	})
	if workflowErr != nil {
		err = workflowErr
		return nil, "", err
	}
	if !workflow.GetLogRetention() {
		err = status.Errorf(codes.FailedPrecondition, "logs retention is disabled for workflow: %s", workflowID)
		return nil, "", err
	}

	filter := fmt.Sprintf(
		`user_id = %q AND workflow_id = %q AND job_id = %q`,
		userID,
		workflowID,
		jobID,
	)

	switch searchJobLogsFilters.Stream {
	case 1:
		filter += fmt.Sprintf(` AND stream = %q`, "stdout")
	case 2:
		filter += fmt.Sprintf(` AND stream = %q`, "stderr")
	}

	if cursor != "" {
		logsCursor, _err := extractDataFromGetJobLogsCursor(cursor)
		if _err != nil {
			err = _err
			return nil, "", err
		}

		filter += fmt.Sprintf(
			` AND (sequence_num < %d OR (sequence_num = %d AND id >= %q))`,
			logsCursor.SequenceNum,
			logsCursor.SequenceNum,
			logsCursor.EventID,
		)
	}

	statusQueryArgs := []any{jobID, workflowID, userID}
	statusQuery := fmt.Sprintf(`
        SELECT status, completed_at
        FROM %s
        WHERE id = $1 AND workflow_id = $2 AND user_id = $3
    `, postgres.TableJobs)

	eg, egCtx := errgroup.WithContext(ctx)
	var (
		logs          []*jobsmodel.JobLog
		nextCursor    jobLogsCursor
		fetchedStatus string
		completedAt   sql.NullTime
	)

	eg.Go(func() error {
		searchRes, searchErr := r.ms.Index(meilisearchpkg.IndexJobLogs).SearchWithContext(
			egCtx,
			searchJobLogsFilters.Message,
			&meilisearch.SearchRequest{
				Filter:                filter,
				AttributesToRetrieve:  []string{"id", "event_id", "message", "sequence_num", "stream", "timestamp"},
				AttributesToHighlight: []string{"message"},
				AttributesToSearchOn:  []string{"message"},
				Sort:                  []string{"sequence_num:desc", "id:asc"},
				Limit:                 int64(r.cfg.LogsFetchLimit + 1),
			},
		)
		if searchErr != nil {
			return status.Errorf(codes.Internal, "failed to search job logs: %v", searchErr)
		}

		tmp := make([]*jobsmodel.JobLog, 0, len(searchRes.Hits))
		tmpCursors := make([]jobLogsCursor, 0, len(searchRes.Hits))
		for _, hit := range searchRes.Hits {
			source, scanErr := searchHitSource(hit)
			if scanErr != nil {
				return scanErr
			}

			log := &jobsmodel.JobLog{}
			if ts, ok := source["timestamp"].(string); ok {
				parsed, scanErr := time.Parse(time.RFC3339Nano, ts)
				if scanErr != nil {
					return status.Errorf(codes.Internal, "invalid timestamp format: %v", scanErr)
				}
				log.Timestamp = parsed
			}

			log.EventID = searchHitString(source, "event_id")
			if log.EventID == "" {
				log.EventID = searchHitString(source, "id")
			}

			if msg, ok := source["message"].(string); ok {
				log.Message = msg
			}

			switch sn := source["sequence_num"].(type) {
			case string:
				snVal, scanErr := strconv.ParseUint(sn, 10, 32)
				if scanErr != nil {
					return status.Errorf(codes.Internal, "invalid sequence_num format: %v", sn)
				}
				log.SequenceNum = uint32(snVal)
			case float64:
				log.SequenceNum = uint32(sn)
			}

			if stream, ok := source["stream"].(string); ok {
				log.Stream = stream
			}

			tmp = append(tmp, log)
			tmpCursors = append(tmpCursors, jobLogsCursor{
				SequenceNum: log.SequenceNum,
				Stream:      log.Stream,
				EventID:     searchHitString(source, "id"),
			})
		}

		// Check if there are more logs
		if len(tmp) > r.cfg.LogsFetchLimit {
			nextCursor = tmpCursors[r.cfg.LogsFetchLimit]
			tmp = tmp[:r.cfg.LogsFetchLimit]
		}
		logs = tmp
		return nil
	})

	eg.Go(func() error {
		row := r.pg.QueryRow(egCtx, statusQuery, statusQueryArgs...)

		//nolint:gocritic // Ifelse is used to handle different error types
		if scanErr := row.Scan(&fetchedStatus, &completedAt); scanErr != nil {
			if errors.Is(scanErr, context.DeadlineExceeded) {
				return status.Error(codes.DeadlineExceeded, scanErr.Error())
			} else if errors.Is(scanErr, context.Canceled) {
				return status.Error(codes.Canceled, scanErr.Error())
			} else if r.pg.IsNoRows(scanErr) {
				return status.Errorf(codes.NotFound, "job not found or not owned by user: %v", scanErr)
			} else if r.pg.IsInvalidTextRepresentation(scanErr) {
				return status.Errorf(codes.InvalidArgument, "invalid job ID: %v", scanErr)
			}

			return status.Errorf(codes.Internal, "failed to get job: %v", scanErr)
		}

		return nil
	})

	err = eg.Wait()
	if err != nil {
		return nil, "", err
	}

	// Buffer-based status override:
	// If the job just completed within the buffer window, we may not have all logs yet.
	// Treat it as "RUNNING" temporarily.
	if completedAt.Valid && time.Since(completedAt.Time) <= jobStatusUpdateBuffer {
		fetchedStatus = jobsmodel.JobStatusRunning.ToString()
	}

	return &jobsmodel.GetJobLogsResponse{
		ID:         jobID,
		WorkflowID: workflowID,
		JobLogs:    logs,
		Cursor:     encodeJobLogsCursor(nextCursor),
	}, fetchedStatus, nil
}

// ListJobs returns jobs.
func (r *Repository) ListJobs(ctx context.Context, workflowID, userID, cursor string, filters *jobsmodel.ListJobsFilters) (res *jobsmodel.ListJobsResponse, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.ListJobs")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Add the cursor to the query
	query := fmt.Sprintf(`
        SELECT id, workflow_id, container_id, status, trigger, attempts, scheduled_at, started_at, completed_at, created_at, updated_at
        FROM %s
        WHERE workflow_id = $1 AND user_id = $2
    `, postgres.TableJobs)
	args := []any{workflowID, userID}
	// This is used to track the parameter index for the query dynamically
	paramIndex := 3

	// Apply filters if provided
	if filters != nil {
		if filters.Status != "" {
			query += fmt.Sprintf(` AND status = $%d`, paramIndex)
			args = append(args, filters.Status)
			paramIndex++
		}

		if filters.Trigger != "" {
			query += fmt.Sprintf(` AND trigger = $%d`, paramIndex)
			args = append(args, filters.Trigger)
			paramIndex++
		}
	}

	if cursor != "" {
		id, createdAt, _err := extractDataFromListJobsCursor(cursor)
		if _err != nil {
			err = _err
			return nil, err
		}

		query += fmt.Sprintf(` AND (created_at, id) <= ($%d, $%d)`, paramIndex, paramIndex+1)
		args = append(args, createdAt, id)
	}

	query += fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT %d;`, r.cfg.FetchLimit+1)

	rows, err := r.pg.Query(ctx, query, args...)
	if errors.Is(err, context.DeadlineExceeded) {
		err = status.Error(codes.DeadlineExceeded, err.Error())
		return nil, err
	} else if errors.Is(err, context.Canceled) {
		err = status.Error(codes.Canceled, err.Error())
		return nil, err
	}

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
			delimiter,
			data[r.cfg.FetchLimit].CreatedAt.Format(time.RFC3339Nano),
		)
		data = data[:r.cfg.FetchLimit]
	}

	return &jobsmodel.ListJobsResponse{
		Jobs:   data,
		Cursor: encodeListJobsCursor(cursor),
	}, nil
}

// withAuthorization issues the necessary headers and tokens for authorization.
func (r *Repository) withAuthorization(ctx context.Context) (context.Context, error) {
	return auth.WithInternalServiceAuthorization(ctx, r.auth, authSubject)
}

// parseTime parses the time.
func parseTime(t string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, t)
}

// encodeJobLogsCursor encodes the cursor for descending log pagination.
func encodeJobLogsCursor(cursor jobLogsCursor) string {
	if cursor.SequenceNum == 0 && cursor.Stream == "" && cursor.EventID == "" {
		return ""
	}

	payload, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}

	return base64.StdEncoding.EncodeToString(payload)
}

// encodeListJobsCursor encodes the cursor.
func encodeListJobsCursor(cursor string) string {
	if cursor == "" {
		return ""
	}

	return base64.StdEncoding.EncodeToString([]byte(cursor))
}

// extractDataFromGetJobLogsCursor extracts the data from the cursor.
func extractDataFromGetJobLogsCursor(cursor string) (jobLogsCursor, error) {
	decodedBytes, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return jobLogsCursor{}, status.Errorf(codes.InvalidArgument, "invalid cursor: %v", err)
	}

	var logsCursor jobLogsCursor
	if err = json.Unmarshal(decodedBytes, &logsCursor); err != nil {
		return jobLogsCursor{}, status.Errorf(codes.InvalidArgument, "invalid cursor format: %v", err)
	}

	if logsCursor.Stream == "" || logsCursor.EventID == "" {
		return jobLogsCursor{}, status.Errorf(codes.InvalidArgument, "invalid cursor format")
	}

	return logsCursor, nil
}

func searchHitString(source map[string]any, field string) string {
	if source == nil {
		return ""
	}
	value, ok := source[field].(string)
	if !ok {
		return ""
	}

	return value
}

func searchHitSource(hit map[string]json.RawMessage) (map[string]any, error) {
	source := make(map[string]any)
	for k, v := range hit {
		if k == "_formatted" {
			continue
		}

		var val any
		if err := json.Unmarshal(v, &val); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to unmarshal data: %v", err)
		}
		source[k] = val
	}

	formattedRaw, ok := hit["_formatted"]
	if !ok {
		return source, nil
	}

	var formatted map[string]any
	if err := json.Unmarshal(formattedRaw, &formatted); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to unmarshal data: %v", err)
	}
	if msg, ok := formatted["message"].(string); ok {
		source["message"] = msg
	}

	return source, nil
}

// extractDataFromListJobsCursor extracts the data from the cursor.
func extractDataFromListJobsCursor(cursor string) (string, time.Time, error) {
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
