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

// Config represents the repository constants configuration.
type Config struct {
	FetchLimit int
	BatchSize  int
}

// Repository provides scheduler repository.
type Repository struct {
	tp  trace.Tracer
	cfg *Config
	pg  *postgres.Postgres
}

type jobDispatchOutboxEvent struct {
	jobID    string
	eventKey string
	payload  json.RawMessage
}

// New creates a new scheduler repository.
func New(cfg *Config, pg *postgres.Postgres) *Repository {
	return &Repository{
		tp:  otel.Tracer(svcpkg.Info().GetName()),
		cfg: cfg,
		pg:  pg,
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

	query := fmt.Sprintf(`
		UPDATE %s
		SET status = 'QUEUED'
		WHERE id IN (
			SELECT id
			FROM %s
			WHERE status = 'PENDING' AND scheduled_at <= NOW()
			FOR UPDATE SKIP LOCKED
			LIMIT %d
		)
		RETURNING id, workflow_id, scheduled_at;
	`, postgres.TableJobs, postgres.TableJobs, r.cfg.FetchLimit)

	// Execute query
	rows, err := tx.Query(ctx, query)
	if err != nil {
		err = status.Errorf(codes.Internal, "failed to query jobs: %v", err)
		return 0, err
	}
	defer rows.Close()

	events := make([]jobDispatchOutboxEvent, 0, r.cfg.FetchLimit)
	for rows.Next() {
		var id string
		var workflowID string
		var scheduledAt time.Time
		if err = rows.Scan(&id, &workflowID, &scheduledAt); err != nil {
			err = status.Errorf(codes.Internal, "failed to scan job: %v", err)
			return 0, err
		}

		eventKey := idempotency.JobDispatchEventKey(id)
		scheduledJobEntry := &jobsmodel.ScheduledJobEntry{
			EventKey:    eventKey,
			JobID:       id,
			WorkflowID:  workflowID,
			ScheduledAt: scheduledAt.Format(time.RFC3339Nano),
		}
		scheduledJobEntryBytes, _err := json.Marshal(scheduledJobEntry)
		if _err != nil {
			continue
		}

		events = append(events, jobDispatchOutboxEvent{
			jobID:    id,
			eventKey: eventKey,
			payload:  json.RawMessage(scheduledJobEntryBytes),
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
			KafkaKey:      event.jobID,
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
