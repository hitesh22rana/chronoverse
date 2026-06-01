package analyticsprocessor

import (
	"context"
	"encoding/json"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	analyticsmodel "github.com/hitesh22rana/chronoverse/internal/model/analytics"
	kafkapkg "github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	retrypkg "github.com/hitesh22rana/chronoverse/internal/pkg/retry"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	// retryBackoff is the duration to wait before retrying a failed operation.
	retryBackoff = time.Second
)

// Repository provides analytics processor repository.
type Repository struct {
	tp     trace.Tracer
	pg     *postgres.Postgres
	kfk    *kgo.Client
	runner *kafkapkg.PartitionRunner
}

// New creates a new analytics processor repository.
func New(pg *postgres.Postgres, kfk *kgo.Client, lifecycle *kafkapkg.PartitionLifecycle) *Repository {
	r := &Repository{
		tp:  otel.Tracer(svcpkg.Info().GetName()),
		pg:  pg,
		kfk: kfk,
	}
	r.runner = kafkapkg.NewPartitionRunner(kfk, r.processRecord, &kafkapkg.PartitionRunnerConfig{
		Name:         "analyticsprocessor",
		RetryBackoff: retryBackoff,
		Tracer:       r.tp,
	}, lifecycle)

	return r
}

// Run starts the analytics processing logic.
func (r *Repository) Run(ctx context.Context) error {
	logger := loggerpkg.FromContext(ctx)
	r.runner.SetLogger(logger)
	return r.runner.Run(ctx)
}

func (r *Repository) processRecord(ctx context.Context, record *kgo.Record) error {
	ctxWithTrace, span := r.tp.Start(
		ctx,
		"analyticsprocessor.Run.processRecord",
		trace.WithAttributes(
			attribute.String("topic", record.Topic),
			attribute.Int64("offset", record.Offset),
			attribute.Int64("partition", int64(record.Partition)),
			attribute.String("key", string(record.Key)),
		),
	)
	defer span.End()

	logger := loggerpkg.FromContext(ctxWithTrace)

	var event analyticsmodel.AnalyticEvent
	if err := json.Unmarshal(record.Value, &event); err != nil {
		logger.Error("failed to unmarshal record",
			zap.Any("ctx", ctxWithTrace),
			zap.String("topic", record.Topic),
			zap.Int64("offset", record.Offset),
			zap.Int32("partition", record.Partition),
			zap.String("value", string(record.Value)),
			zap.Error(err),
		)
		return nil
	}
	span.SetAttributes(
		attribute.String("event_type", event.EventType.ToString()),
		attribute.String("event_key", event.EventKey),
		attribute.String("user_id", event.UserID),
		attribute.String("workflow_id", event.WorkflowID),
	)

	var processErr error
	switch event.EventType {
	case analyticsmodel.EventTypeLogs:
		processErr = retrypkg.Do(ctxWithTrace, 2, retryBackoff, func() error {
			return r.processLogsEvent(ctxWithTrace, &event)
		})
	case analyticsmodel.EventTypeJobs:
		processErr = retrypkg.Do(ctxWithTrace, 2, retryBackoff, func() error {
			return r.processJobsEvent(ctxWithTrace, &event)
		})
	case analyticsmodel.EventTypeWorkflows:
		processErr = retrypkg.Do(ctxWithTrace, 2, retryBackoff, func() error {
			return r.processWorkflowsEvent(ctxWithTrace, &event)
		})
	default:
		logger.Warn("unknown event type",
			zap.Any("ctx", ctxWithTrace),
			zap.Any("event", event),
		)
		return nil
	}

	if processErr != nil {
		logger.Error("error processing analytics event",
			zap.Any("ctx", ctxWithTrace),
			zap.Any("event", event),
			zap.Error(processErr),
		)
	}

	return processErr
}
