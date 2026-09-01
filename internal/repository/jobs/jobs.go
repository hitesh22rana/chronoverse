package jobs

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
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
	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
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

	jobLogsHighlightTokenBytes = 16
	jobLogsHighlightStart      = "__CV_HL_START_"
	jobLogsHighlightEnd        = "__CV_HL_END_"
	jobLogsHighlightSuffix     = "__"
	jobLogsMessageField        = "message"
	terminalReasonCodeField    = "terminal_reason_code"
	leaseTokenField            = "lease_token"
	claimResultField           = "claimed"
	claimReasonField           = "reason"
	jobLogsEventIDField        = "event_id"
	jobLogsSequenceNumField    = "sequence_num"
	jobLogsStreamField         = "stream"
	jobLogsTimestampField      = "timestamp"
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
	FetchLimit            int
	LogsFetchLimit        int
	RuntimeHeartbeatTTL   time.Duration
	RuntimeLostAfter      time.Duration
	EventCommandRetention time.Duration
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
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.RuntimeHeartbeatTTL <= 0 {
		cfg.RuntimeHeartbeatTTL = 30 * time.Second
	}
	if cfg.RuntimeLostAfter <= 0 {
		cfg.RuntimeLostAfter = 5 * time.Minute
	}
	if cfg.EventCommandRetention <= 0 {
		cfg.EventCommandRetention = commandidempotency.DefaultEventCommandRetention
	}
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
//
//nolint:gocyclo // Scheduling combines validation, ledger reservation, and one atomic insert.
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
	workflowID, err = commandidempotency.CanonicalUUID(workflowID, "workflow ID")
	if err != nil {
		return "", err
	}
	userID, err = commandidempotency.CanonicalUUID(userID, "user ID")
	if err != nil {
		return "", err
	}

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
	idempotencyKey, err = commandidempotency.NormalizeKey(idempotencyKey)
	if err != nil {
		return "", err
	}
	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to start schedule transaction: %v", err)
	}
	//nolint:errcheck // Rollback is a no-op after commit.
	defer tx.Rollback(ctx)

	// scheduled_at is server-generated output for automatic occurrences, so the
	// deterministic event identity—not wall-clock retry timing—defines the command.
	hashFields := scheduleJobHashFields(workflowID, userID, trigger, workflowGeneration)
	var scope, operation string
	automatic := trigger == jobsmodel.JobTriggerAutomatic.ToString()
	if automatic {
		scope = commandidempotency.WorkflowScope(workflowID)
		operation = commandidempotency.OperationJobScheduleAutomatic
	} else {
		scope = commandidempotency.UserScope(userID)
		operation = commandidempotency.ManualScheduleOperation(workflowID)
	}
	requestHash, err := idempotency.HashCanonical(hashFields)
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to hash schedule command: %v", err)
	}
	reservation, err := commandidempotency.Reserve(ctx, tx, scope, operation, idempotencyKey, requestHash)
	if err != nil {
		return "", err
	}
	if reservation.Replay {
		if err = tx.Commit(ctx); err != nil {
			return "", status.Errorf(codes.Internal, "failed to commit schedule replay: %v", err)
		}
		return reservation.ResourceID, nil
	}
	var (
		storedWorkflowID         string
		storedUserID             string
		storedTrigger            string
		storedWorkflowGeneration sql.NullInt64
	)
	if automatic {
		legacyQuery := fmt.Sprintf(`
			SELECT id, workflow_id, user_id, trigger, workflow_generation
			FROM %s
			WHERE workflow_id = $1
			  AND trigger = 'AUTOMATIC'
			  AND idempotency_key IS NOT NULL
			  AND btrim(idempotency_key, ' ') = $2
			FOR UPDATE
			LIMIT 1;
		`, postgres.TableJobs)
		legacyErr := tx.QueryRow(ctx, legacyQuery, workflowID, idempotencyKey).Scan(
			&jobID,
			&storedWorkflowID,
			&storedUserID,
			&storedTrigger,
			&storedWorkflowGeneration,
		)
		switch {
		case legacyErr == nil:
			if validateErr := validateStoredScheduleCommand(
				requestHash,
				storedWorkflowID,
				storedUserID,
				storedTrigger,
				storedWorkflowGeneration,
			); validateErr != nil {
				return "", validateErr
			}
			if completeErr := commandidempotency.Complete(
				ctx, tx, scope, operation, idempotencyKey, requestHash,
				jobID, map[string]string{"id": jobID}, r.cfg.EventCommandRetention,
			); completeErr != nil {
				return "", completeErr
			}
			if err = tx.Commit(ctx); err != nil {
				return "", status.Errorf(codes.Internal, "failed to commit legacy schedule replay: %v", err)
			}
			return jobID, nil
		case !r.pg.IsNoRows(legacyErr):
			return "", status.Errorf(codes.Internal, "failed to read legacy schedule command: %v", legacyErr)
		}
	}
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

	row := tx.QueryRow(ctx, query, args...)
	if err = row.Scan(
		&jobID,
		&storedWorkflowID,
		&storedUserID,
		&storedTrigger,
		&storedWorkflowGeneration,
	); err != nil {
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
		case r.pg.IsNoRows(err) && trigger == jobsmodel.JobTriggerManual.ToString():
			err = status.Errorf(codes.NotFound, "workflow not found, not owned by user, or not schedulable")
			return "", err
		}

		err = status.Errorf(codes.Internal, "failed to insert job: %v", err)
		return "", err
	}
	if validateErr := validateStoredScheduleCommand(
		requestHash,
		storedWorkflowID,
		storedUserID,
		storedTrigger,
		storedWorkflowGeneration,
	); validateErr != nil {
		return "", validateErr
	}

	retention := commandidempotency.ClientCommandRetention
	if automatic {
		retention = r.cfg.EventCommandRetention
	}
	if completeErr := commandidempotency.Complete(ctx, tx, scope, operation, idempotencyKey, requestHash, jobID, map[string]string{"id": jobID}, retention); completeErr != nil {
		return "", completeErr
	}
	if err = tx.Commit(ctx); err != nil {
		return "", status.Errorf(codes.Internal, "failed to commit schedule command: %v", err)
	}
	return jobID, nil
}

func validateStoredScheduleCommand(
	requestHash,
	workflowID,
	userID,
	trigger string,
	workflowGeneration sql.NullInt64,
) error {
	if trigger == jobsmodel.JobTriggerAutomatic.ToString() && !workflowGeneration.Valid {
		return status.Error(codes.AlreadyExists, "legacy automatic job input cannot be verified")
	}
	storedGeneration := int64(0)
	if workflowGeneration.Valid {
		storedGeneration = workflowGeneration.Int64
	}
	storedRequestHash, err := idempotency.HashCanonical(
		scheduleJobHashFields(workflowID, userID, trigger, storedGeneration),
	)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to hash stored schedule command: %v", err)
	}
	if storedRequestHash != requestHash {
		return status.Error(codes.AlreadyExists, "idempotency key was already used with a different request")
	}
	return nil
}

func scheduleJobHashFields(workflowID, userID, trigger string, workflowGeneration int64) map[string]any {
	fields := map[string]any{
		"workflow_id": workflowID,
		"user_id":     userID,
		"trigger":     trigger,
	}
	if trigger == jobsmodel.JobTriggerAutomatic.ToString() {
		fields["workflow_generation"] = workflowGeneration
	}
	return fields
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
	if trigger == jobsmodel.JobTriggerAutomatic.ToString() {
		args = append(args, workflowGeneration)
	}

	switch {
	case trigger == jobsmodel.JobTriggerManual.ToString():
		// Ownership guard: the caller must own the target workflow and the
		// workflow must be schedulable. Mirrors automaticScheduleGuardSQL so
		// cross-user scheduling cannot insert job rows.
		return fmt.Sprintf(`
            INSERT INTO %s (workflow_id, user_id, scheduled_at, trigger, idempotency_key, workflow_generation)
            SELECT $1, $2, $3, $4, $5, NULL
            FROM %s AS w
            WHERE w.id = $1
                AND w.user_id = $2
                AND w.terminated_at IS NULL
                AND w.build_status = 'COMPLETED'
            FOR SHARE
            RETURNING id, workflow_id, user_id, trigger, workflow_generation;
        `, postgres.TableJobs, postgres.TableWorkflows), args, nil
	case automaticIdempotencyKeyProvided:
		guard := automaticScheduleGuardSQL(workflowGeneration)
		return fmt.Sprintf(`
            INSERT INTO %s (workflow_id, user_id, scheduled_at, trigger, idempotency_key, workflow_generation)
            SELECT $1, $2, $3, $4, $5, $6
            %s
            ON CONFLICT (workflow_id, idempotency_key)
            WHERE trigger = 'AUTOMATIC' AND idempotency_key IS NOT NULL
            DO UPDATE SET idempotency_key = EXCLUDED.idempotency_key
            RETURNING id, workflow_id, user_id, trigger, workflow_generation;
        `, postgres.TableJobs, guard), args, nil
	case trigger == jobsmodel.JobTriggerAutomatic.ToString():
		guard := automaticScheduleGuardSQL(workflowGeneration)
		return fmt.Sprintf(`
            INSERT INTO %s (workflow_id, user_id, scheduled_at, trigger, idempotency_key, workflow_generation)
            SELECT $1, $2, $3, $4, $5, $6
            %s
            ON CONFLICT (workflow_id, scheduled_at, trigger)
            WHERE trigger = 'AUTOMATIC'
            DO UPDATE SET workflow_id = EXCLUDED.workflow_id
            RETURNING id, workflow_id, user_id, trigger, workflow_generation;
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

// CancelJob applies a deterministic workflow-driven cancellation and persists
// the pre-cancellation cleanup snapshot for response-loss replay.
//
//nolint:gocyclo // Cancellation persists and replays a complete cleanup snapshot atomically.
func (r *Repository) CancelJob(ctx context.Context, jobID, commandID, terminalReasonCode string) (snapshot *jobsmodel.CancelJobSnapshot, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.CancelJob")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()
	jobID, err = commandidempotency.CanonicalUUID(jobID, "job ID")
	if err != nil {
		return nil, err
	}

	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to start cancel-job transaction: %v", err)
	}
	//nolint:errcheck // Rollback is a no-op after commit.
	defer tx.Rollback(ctx)

	requestHash, err := idempotency.HashCanonical(map[string]string{
		"job_id":                jobID,
		terminalReasonCodeField: terminalReasonCode,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to hash cancel command: %v", err)
	}
	scope := commandidempotency.JobScope(jobID)
	reservation, err := commandidempotency.Reserve(ctx, tx, scope, commandidempotency.OperationJobCancel, commandID, requestHash)
	if err != nil {
		return nil, err
	}
	if reservation.Replay {
		snapshot = &jobsmodel.CancelJobSnapshot{}
		if err = json.Unmarshal(reservation.Response, snapshot); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to decode cancel replay: %v", err)
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to commit cancel replay: %v", err)
		}
		return snapshot, nil
	}

	snapshot = &jobsmodel.CancelJobSnapshot{}
	query := fmt.Sprintf(`
		SELECT id, status, container_id, runtime_node_id, runtime_endpoint, attempts
		FROM %s
		WHERE id = $1
		FOR UPDATE;
	`, postgres.TableJobs)
	if err = tx.QueryRow(ctx, query, jobID).Scan(
		&snapshot.ID,
		&snapshot.PreviousStatus,
		&snapshot.ContainerID,
		&snapshot.RuntimeNodeID,
		&snapshot.RuntimeEndpoint,
		&snapshot.Attempt,
	); err != nil {
		if r.pg.IsNoRows(err) {
			return nil, status.Error(codes.NotFound, "job not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to read cancellation snapshot: %v", err)
	}

	if snapshot.PreviousStatus == jobsmodel.JobStatusPending.ToString() ||
		snapshot.PreviousStatus == jobsmodel.JobStatusQueued.ToString() ||
		snapshot.PreviousStatus == jobsmodel.JobStatusRunning.ToString() {
		query = fmt.Sprintf(`
			UPDATE %s
			SET status = 'CANCELED',
				completed_at = clock_timestamp() AT TIME ZONE 'utc',
				lease_token = NULL,
				leased_by = NULL,
				lease_process_instance_id = NULL,
				lease_expires_at = NULL,
				last_heartbeat_at = NULL,
				terminal_reason_code = $2
			WHERE id = $1;
		`, postgres.TableJobs)
		if _, err = tx.Exec(ctx, query, jobID, terminalReasonCode); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to cancel job: %v", err)
		}
		if snapshot.PreviousStatus == jobsmodel.JobStatusRunning.ToString() {
			if decrementErr := r.decrementRuntimeSlotForJob(ctx, tx, jobID); decrementErr != nil {
				return nil, decrementErr
			}
		}
	}

	if completeErr := commandidempotency.Complete(ctx, tx, scope, commandidempotency.OperationJobCancel, commandID, requestHash, jobID, snapshot, r.cfg.EventCommandRetention); completeErr != nil {
		return nil, completeErr
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to commit cancel command: %v", err)
	}
	return snapshot, nil
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
	        SELECT id, workflow_id, status, trigger, scheduled_at, started_at, completed_at, created_at, updated_at,
	               terminal_reason_code, failure_kind, last_error_code, last_error_message
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

	// Subscribe to the job-specific channel and wait for the subscription
	// acknowledgement so events published right after this call are not dropped.
	return r.subscribeToJobLogsChannel(ctx, jobID)
}

// subscribeToJobLogsChannel subscribes to the job logs channel and waits for
// the subscription acknowledgement: go-redis returns from Subscribe before the
// server has applied it, so events published in that window would be dropped.
func (r *Repository) subscribeToJobLogsChannel(ctx context.Context, jobID string) (*goredis.PubSub, error) {
	channel := redis.GetJobLogsChannel(jobID)
	sub := r.rdb.Subscribe(ctx, channel)

	msg, err := sub.Receive(ctx)
	if err != nil {
		_ = sub.Close()
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, status.Error(codes.DeadlineExceeded, err.Error())
		} else if errors.Is(err, context.Canceled) {
			return nil, status.Error(codes.Canceled, err.Error())
		}

		return nil, status.Errorf(codes.Internal, "failed to subscribe to job logs stream: %v", err)
	}

	ack, ok := msg.(*goredis.Subscription)
	if !ok || ack.Kind != "subscribe" || ack.Channel != channel {
		_ = sub.Close()
		return nil, status.Errorf(codes.Internal, "unexpected subscription acknowledgement: %#v", msg)
	}

	return sub, nil
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
	options jobsmodel.SearchJobLogsOptions,
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

	ascending := options.SortOrder == jobsmodel.JobLogsSortOrderAsc

	if cursor != "" {
		logsCursor, _err := extractDataFromGetJobLogsCursor(cursor)
		if _err != nil {
			err = _err
			return nil, "", err
		}

		sequenceOperator := "<"
		idOperator := ">="
		if ascending {
			sequenceOperator = ">"
		}
		filter += fmt.Sprintf(
			` AND (sequence_num %s %d OR (sequence_num = %d AND id %s %q))`,
			sequenceOperator,
			logsCursor.SequenceNum,
			logsCursor.SequenceNum,
			idOperator,
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
	highlightToken := ""
	if !options.DisableHighlight {
		var tokenErr error
		highlightToken, tokenErr = newJobLogsHighlightToken()
		if tokenErr != nil {
			err = tokenErr
			return nil, "", err
		}
	}

	eg.Go(func() error {
		searchRes, searchErr := r.ms.Index(meilisearchpkg.IndexJobLogs).SearchWithContext(
			egCtx,
			searchJobLogsFilters.Message,
			newJobLogsSearchRequest(filter, highlightToken, int64(r.cfg.LogsFetchLimit+1), options),
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
			if ts, ok := source[jobLogsTimestampField].(string); ok {
				parsed, scanErr := time.Parse(time.RFC3339Nano, ts)
				if scanErr != nil {
					return status.Errorf(codes.Internal, "invalid timestamp format: %v", scanErr)
				}
				log.Timestamp = parsed
			}

			log.EventID = searchHitString(source, jobLogsEventIDField)
			if log.EventID == "" {
				log.EventID = searchHitString(source, "id")
			}

			if msg, ok := source[jobLogsMessageField].(string); ok {
				log.Message = msg
			}

			switch sn := source[jobLogsSequenceNumField].(type) {
			case string:
				snVal, scanErr := strconv.ParseUint(sn, 10, 32)
				if scanErr != nil {
					return status.Errorf(codes.Internal, "invalid sequence_num format: %v", sn)
				}
				log.SequenceNum = uint32(snVal)
			case float64:
				log.SequenceNum = uint32(sn)
			}

			if stream, ok := source[jobLogsStreamField].(string); ok {
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
		ID:             jobID,
		WorkflowID:     workflowID,
		JobLogs:        logs,
		Cursor:         encodeJobLogsCursor(nextCursor),
		HighlightToken: highlightToken,
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
	        SELECT id, workflow_id, container_id, status, trigger, attempts, scheduled_at, started_at, completed_at, created_at, updated_at, runtime_node_id, runtime_endpoint,
	               terminal_reason_code, failure_kind, last_error_code, last_error_message
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
// Job-log lookups call workflows-service to enforce workflow ownership.
func (r *Repository) withAuthorization(ctx context.Context) (context.Context, error) {
	return auth.WithInternalServiceAuthorization(ctx, r.auth, authSubject, "workflows-service")
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

func newJobLogsHighlightToken() (string, error) {
	token := make([]byte, jobLogsHighlightTokenBytes)
	if _, err := rand.Read(token); err != nil {
		return "", status.Errorf(codes.Internal, "failed to generate log highlight token: %v", err)
	}

	return hex.EncodeToString(token), nil
}

func jobLogsHighlightTags(token string) (startTag, endTag string) {
	return jobLogsHighlightStart + token + jobLogsHighlightSuffix,
		jobLogsHighlightEnd + token + jobLogsHighlightSuffix
}

func newJobLogsSearchRequest(filter, highlightToken string, limit int64, options jobsmodel.SearchJobLogsOptions) *meilisearch.SearchRequest {
	sequenceDirection := "desc"
	if options.SortOrder == jobsmodel.JobLogsSortOrderAsc {
		sequenceDirection = "asc"
	}

	req := &meilisearch.SearchRequest{
		Filter:               filter,
		AttributesToRetrieve: []string{"id", jobLogsEventIDField, jobLogsMessageField, jobLogsSequenceNumField, jobLogsStreamField, jobLogsTimestampField},
		AttributesToSearchOn: []string{jobLogsMessageField},
		Sort:                 []string{"sequence_num:" + sequenceDirection, "id:asc"},
		Limit:                limit,
	}

	if !options.DisableHighlight {
		highlightPreTag, highlightPostTag := jobLogsHighlightTags(highlightToken)
		req.AttributesToHighlight = []string{jobLogsMessageField}
		req.HighlightPreTag = highlightPreTag
		req.HighlightPostTag = highlightPostTag
	}

	return req
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
	if msg, ok := formatted[jobLogsMessageField].(string); ok {
		source[jobLogsMessageField] = msg
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
