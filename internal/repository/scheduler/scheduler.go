package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/outbox"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const defaultBatchSize = 1000

// Config represents the repository constants configuration.
type Config struct {
	BatchSize int
}

// Repository provides scheduler repository.
type Repository struct {
	tp  trace.Tracer
	cfg *Config
	pg  *postgres.Postgres
}

type jobDispatchOutboxEvent struct {
	jobID      string
	workflowID string
	eventKey   string
	payload    json.RawMessage
}

// New creates a new scheduler repository.
func New(cfg *Config, pg *postgres.Postgres) *Repository {
	return &Repository{
		tp:  otel.Tracer(svcpkg.Info().GetName()),
		cfg: cfg,
		pg:  pg,
	}
}

func (r *Repository) batchSize() int {
	if r.cfg == nil || r.cfg.BatchSize <= 0 {
		return defaultBatchSize
	}

	return r.cfg.BatchSize
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

	query := fmt.Sprintf(`
            WITH candidate_workflows AS (
                SELECT w.id, w.generation
            FROM %s AS w
            WHERE w.build_status = 'COMPLETED'
                AND w.terminated_at IS NULL
                AND NOT EXISTS (
                    SELECT 1
                    FROM %s AS active
                    WHERE active.workflow_id = w.id
                        AND active.status IN ('QUEUED', 'RUNNING')
                )
                AND EXISTS (
                    SELECT 1
                    FROM %s AS due
                    WHERE due.workflow_id = w.id
                        AND due.status = 'PENDING'
                        AND due.scheduled_at <= (now() AT TIME ZONE 'utc')
                        AND (due.next_attempt_at IS NULL OR due.next_attempt_at <= (now() AT TIME ZONE 'utc'))
                        AND NOT EXISTS (
                            SELECT 1
                            FROM %s AS blocker
                            WHERE blocker.workflow_id = due.workflow_id
                                AND blocker.status IN ('PENDING', 'QUEUED', 'RUNNING')
                                AND (
                                    blocker.scheduled_at < due.scheduled_at
                                    OR (blocker.scheduled_at = due.scheduled_at AND blocker.created_at < due.created_at)
                                    OR (blocker.scheduled_at = due.scheduled_at AND blocker.created_at = due.created_at AND blocker.id < due.id)
                                )
                        )
                )
            ORDER BY (
                SELECT due.scheduled_at
                FROM %s AS due
                WHERE due.workflow_id = w.id
                    AND due.status = 'PENDING'
                    AND due.scheduled_at <= (now() AT TIME ZONE 'utc')
                    AND (due.next_attempt_at IS NULL OR due.next_attempt_at <= (now() AT TIME ZONE 'utc'))
                    AND NOT EXISTS (
                        SELECT 1
                        FROM %s AS blocker
                        WHERE blocker.workflow_id = due.workflow_id
                            AND blocker.status IN ('PENDING', 'QUEUED', 'RUNNING')
                            AND (
                                blocker.scheduled_at < due.scheduled_at
                                OR (blocker.scheduled_at = due.scheduled_at AND blocker.created_at < due.created_at)
                                OR (blocker.scheduled_at = due.scheduled_at AND blocker.created_at = due.created_at AND blocker.id < due.id)
                            )
                    )
                ORDER BY
                    due.scheduled_at ASC,
                    due.created_at ASC,
                    due.id ASC
                LIMIT 1
            ) ASC
            FOR UPDATE OF w SKIP LOCKED
            LIMIT %d
        ),
            selected AS (
                SELECT due.id, w.generation
            FROM candidate_workflows AS w
            CROSS JOIN LATERAL (
                SELECT j.id
                FROM %s AS j
                WHERE j.workflow_id = w.id
                    AND j.status = 'PENDING'
                    AND j.scheduled_at <= (now() AT TIME ZONE 'utc')
                    AND (j.next_attempt_at IS NULL OR j.next_attempt_at <= (now() AT TIME ZONE 'utc'))
                    AND NOT EXISTS (
                        SELECT 1
                        FROM %s AS blocker
                        WHERE blocker.workflow_id = j.workflow_id
                            AND blocker.status IN ('PENDING', 'QUEUED', 'RUNNING')
                            AND (
                                blocker.scheduled_at < j.scheduled_at
                                OR (blocker.scheduled_at = j.scheduled_at AND blocker.created_at < j.created_at)
                                OR (blocker.scheduled_at = j.scheduled_at AND blocker.created_at = j.created_at AND blocker.id < j.id)
                            )
                    )
                ORDER BY
                    j.scheduled_at ASC,
                    j.created_at ASC,
                    j.id ASC
                FOR UPDATE OF j SKIP LOCKED
                LIMIT 1
            ) AS due
        )
            UPDATE %s AS j
            SET status = 'QUEUED',
                queued_at = now() AT TIME ZONE 'utc',
                dispatch_attempts = dispatch_attempts + 1
            FROM selected
            WHERE j.id = selected.id
            RETURNING j.id, j.workflow_id, j.scheduled_at, j.dispatch_attempts, selected.generation;
    `,
		postgres.TableWorkflows,
		postgres.TableJobs,
		postgres.TableJobs,
		postgres.TableJobs,
		postgres.TableJobs,
		postgres.TableJobs,
		r.batchSize(),
		postgres.TableJobs,
		postgres.TableJobs,
		postgres.TableJobs,
	)

	// Execute query
	rows, err := tx.Query(ctx, query)
	if err != nil {
		err = status.Errorf(codes.Internal, "failed to query jobs: %v", err)
		return 0, err
	}
	defer rows.Close()

	events := make([]jobDispatchOutboxEvent, 0, r.batchSize())
	for rows.Next() {
		var id string
		var workflowID string
		var scheduledAt time.Time
		var dispatchAttempt int32
		var workflowGeneration int64
		if err = rows.Scan(&id, &workflowID, &scheduledAt, &dispatchAttempt, &workflowGeneration); err != nil {
			err = status.Errorf(codes.Internal, "failed to scan job: %v", err)
			return 0, err
		}

		eventKey := idempotency.JobDispatchEventKey(id, dispatchAttempt)
		scheduledJobEntry := &jobsmodel.ScheduledJobEntry{
			EventKey:           eventKey,
			JobID:              id,
			WorkflowID:         workflowID,
			ScheduledAt:        scheduledAt.Format(time.RFC3339Nano),
			DispatchAttempt:    dispatchAttempt,
			WorkflowGeneration: workflowGeneration,
		}
		scheduledJobEntryBytes, _err := json.Marshal(scheduledJobEntry)
		if _err != nil {
			continue
		}

		events = append(events, jobDispatchOutboxEvent{
			jobID:      id,
			workflowID: workflowID,
			eventKey:   eventKey,
			payload:    json.RawMessage(scheduledJobEntryBytes),
		})
		total++
	}

	// Handle any errors that may have occurred during iteration
	if err = rows.Err(); err != nil {
		err = status.Errorf(codes.Internal, "failed to iterate over jobs: %v", err)
		return 0, err
	}
	rows.Close()

	for _, event := range events {
		insertErr := outbox.InsertTx(ctx, tx, &outbox.Event{
			Topic:         kafka.TopicJobs,
			KafkaKey:      event.workflowID,
			EventKey:      event.eventKey,
			AggregateType: "job",
			AggregateID:   event.jobID,
			Payload:       event.payload,
		})
		if insertErr != nil {
			return 0, insertErr
		}
	}

	if total == 0 {
		return 0, nil
	}

	// Commit transaction
	if err = tx.Commit(ctx); err != nil {
		err = status.Errorf(codes.Internal, "failed to commit transaction: %v", err)
		return 0, err
	}

	return total, nil
}
