package jobs

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	otelcodes "go.opentelemetry.io/otel/codes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	"github.com/hitesh22rana/chronoverse/internal/pkg/outbox"
)

// EnqueueJobLog stores a job log event in the durable outbox.
func (r *Repository) EnqueueJobLog(ctx context.Context, event *jobsmodel.JobLogEvent) (err error) {
	ctx, span := r.tp.Start(ctx, "Repository.EnqueueJobLog")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	if event == nil {
		return status.Error(codes.InvalidArgument, "job log event is required")
	}
	if event.EventKey == "" {
		event.EventKey = idempotency.LogEventKey(event.JobID, event.Stream, event.SequenceNum)
	}

	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		return r.mapJobLeaseWriteError(err, "start enqueue job log transaction")
	}
	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) && err == nil {
			err = r.mapJobLeaseWriteError(rollbackErr, "rollback enqueue job log transaction")
		}
	}()

	if err := outbox.InsertTx(ctx, tx, &outbox.Event{
		Topic:         kafka.TopicJobLogs,
		KafkaKey:      event.JobID,
		EventKey:      event.EventKey,
		AggregateType: "job_log",
		AggregateID:   event.JobID,
		Payload:       event,
	}); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return r.mapJobLeaseWriteError(err, "commit enqueue job log transaction")
	}

	return nil
}
