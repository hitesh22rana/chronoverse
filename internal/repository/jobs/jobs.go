package jobs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
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

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	meilisearchpkg "github.com/hitesh22rana/chronoverse/internal/pkg/meilisearch"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	delimiter             = '$'
	jobStatusUpdateBuffer = time.Minute // 1 minute
)

// Config represents the repository constants configuration.
type Config struct {
	FetchLimit     int
	LogsFetchLimit int
}

// Repository provides jobs repository.
type Repository struct {
	tp  trace.Tracer
	cfg *Config
	pg  *postgres.Postgres
	rdb *redis.Store
	ch  *clickhouse.Client
	ms  meilisearch.ServiceManager
}

// New creates a new jobs repository.
func New(cfg *Config, pg *postgres.Postgres, rdb *redis.Store, ch *clickhouse.Client, ms meilisearch.ServiceManager) *Repository {
	return &Repository{
		tp:  otel.Tracer(svcpkg.Info().GetName()),
		cfg: cfg,
		pg:  pg,
		rdb: rdb,
		ch:  ch,
		ms:  ms,
	}
}

// ScheduleJob schedules a job.
func (r Repository) ScheduleJob(ctx context.Context, workflowID, userID, scheduledAt, trigger string) (jobID string, err error) {
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

	// Insert job into database
	query := fmt.Sprintf(`
		INSERT INTO %s (workflow_id, user_id, scheduled_at, trigger)
		VALUES ($1, $2, $3, $4)
		RETURNING id;
	`, postgres.TableJobs)

	row := r.pg.QueryRow(ctx, query, workflowID, userID, scheduledAtTime, trigger)
	if err = row.Scan(&jobID); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = status.Error(codes.DeadlineExceeded, err.Error())
			return "", err
		} else if errors.Is(err, context.Canceled) {
			err = status.Error(codes.Canceled, err.Error())
			return "", err
		}

		err = status.Errorf(codes.Internal, "failed to insert job: %v", err)
		return "", err
	}

	return jobID, nil
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
		query += `, completed_at = $2
		WHERE id = $3;`
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
		SELECT id, workflow_id, container_id, user_id, status, trigger, scheduled_at, started_at, completed_at, created_at, updated_at
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

	logQueryArgs := []any{jobID, workflowID, userID}
	logsQuery := fmt.Sprintf(`
	SELECT timestamp, message, sequence_num, stream
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
		sequenceNum, _err := extractDataFromGetJobLogsCursor(cursor)
		if _err != nil {
			err = _err
			return nil, "", err
		}

		logsQuery += ` AND sequence_num >= $4`
		logQueryArgs = append(logQueryArgs, sequenceNum)
	}

	logsQuery += fmt.Sprintf(` ORDER BY sequence_num ASC LIMIT %d;`, r.cfg.LogsFetchLimit+1)

	statusQueryArgs := []any{jobID, workflowID, userID}
	statusQuery := fmt.Sprintf(`
		SELECT status, completed_at
		FROM %s
		WHERE id = $1 AND workflow_id = $2 AND user_id = $3
	`, postgres.TableJobs)

	eg, egCtx := errgroup.WithContext(ctx)
	var (
		logs          []*jobsmodel.JobLog
		nextCursorSeq uint32
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
		for rows.Next() {
			var (
				ts   time.Time
				msg  string
				seq  uint32
				strm string
			)
			if scanErr := rows.Scan(&ts, &msg, &seq, &strm); scanErr != nil {
				return status.Errorf(codes.Internal, "failed to scan logs: %v", scanErr)
			}
			tmp = append(tmp, &jobsmodel.JobLog{
				Timestamp:   ts,
				Message:     msg,
				SequenceNum: seq,
				Stream:      strm,
			})
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			return status.Errorf(codes.Internal, "rows error: %v", rowsErr)
		}

		// Check if there are more logs
		if len(tmp) > r.cfg.LogsFetchLimit {
			nextCursorSeq = tmp[r.cfg.LogsFetchLimit].SequenceNum
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
		Cursor:     encodeJobLogsCursor(nextCursorSeq),
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
		sequenceNum, _err := extractDataFromGetJobLogsCursor(cursor)
		if _err != nil {
			err = _err
			return nil, "", err
		}

		filter += fmt.Sprintf(` AND sequence_num >= %d`, sequenceNum)
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
		nextCursorSeq uint32
		fetchedStatus string
		completedAt   sql.NullTime
	)

	eg.Go(func() error {
		searchRes, searchErr := r.ms.Index(meilisearchpkg.IndexJobLogs).SearchWithContext(
			egCtx,
			searchJobLogsFilters.Message,
			&meilisearch.SearchRequest{
				Filter:                filter,
				AttributesToRetrieve:  []string{"message", "sequence_num", "stream", "timestamp"},
				AttributesToHighlight: []string{"message"},
				AttributesToSearchOn:  []string{"message"},
				Sort:                  []string{"sequence_num:asc"},
				Limit:                 int64(r.cfg.LogsFetchLimit + 1),
			},
		)
		if searchErr != nil {
			return status.Errorf(codes.Internal, "failed to search job logs: %v", searchErr)
		}

		tmp := make([]*jobsmodel.JobLog, 0, len(searchRes.Hits))
		for _, hit := range searchRes.Hits {
			var source map[string]any

			// Prefer _formatted if present
			if formattedRaw, ok := hit["_formatted"]; ok {
				if scanErr := json.Unmarshal(formattedRaw, &source); scanErr != nil {
					return status.Errorf(codes.Internal, "failed to unmarshal data: %v", scanErr)
				}
			} else {
				source = make(map[string]any)
				for k, v := range hit {
					var val any
					if scanErr := json.Unmarshal(v, &val); scanErr != nil {
						return status.Errorf(codes.Internal, "failed to unmarshal data: %v", scanErr)
					}
					source[k] = val
				}
			}

			log := &jobsmodel.JobLog{}
			if ts, ok := source["timestamp"].(string); ok {
				parsed, scanErr := time.Parse(time.RFC3339Nano, ts)
				if scanErr != nil {
					return status.Errorf(codes.Internal, "invalid timestamp format: %v", scanErr)
				}
				log.Timestamp = parsed
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
		}

		// Check if there are more logs
		if len(tmp) > r.cfg.LogsFetchLimit {
			nextCursorSeq = tmp[r.cfg.LogsFetchLimit].SequenceNum
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
		Cursor:     encodeJobLogsCursor(nextCursorSeq),
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
        SELECT id, workflow_id, container_id, status, trigger, scheduled_at, started_at, completed_at, created_at, updated_at
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

// parseTime parses the time.
func parseTime(t string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, t)
}

// encodeJobLogsCursor encodes the cursor.
func encodeJobLogsCursor(sequenceNum uint32) string {
	if sequenceNum == 0 {
		return ""
	}

	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, sequenceNum)
	return base64.StdEncoding.EncodeToString(buf)
}

// encodeListJobsCursor encodes the cursor.
func encodeListJobsCursor(cursor string) string {
	if cursor == "" {
		return ""
	}

	return base64.StdEncoding.EncodeToString([]byte(cursor))
}

// extractDataFromGetJobLogsCursor extracts the data from the cursor.
func extractDataFromGetJobLogsCursor(cursor string) (uint32, error) {
	decodedBytes, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return 0, status.Errorf(codes.InvalidArgument, "invalid cursor: %v", err)
	}

	// Must be exactly 4 bytes for a uint32
	if len(decodedBytes) != 4 {
		return 0, status.Errorf(codes.InvalidArgument, "invalid cursor format")
	}

	return binary.BigEndian.Uint32(decodedBytes), nil
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
