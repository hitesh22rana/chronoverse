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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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

// Config represents the repository constants configuration.
type Config struct{}

// Repository provides analytics processor repository.
type Repository struct {
	tp  trace.Tracer
	cfg *Config
	pg  *postgres.Postgres
	kfk *kgo.Client
}

// New creates a new analytics processor repository.
func New(cfg *Config, pg *postgres.Postgres, kfk *kgo.Client) *Repository {
	return &Repository{
		tp:  otel.Tracer(svcpkg.Info().GetName()),
		cfg: cfg,
		pg:  pg,
		kfk: kfk,
	}
}

// Run starts the analytics processing logic.
//
//nolint:gocyclo // Ignore cyclomatic complexity as it is required for the processing logic
func (r *Repository) Run(ctx context.Context) error {
	logger := loggerpkg.FromContext(ctx)

	exitCh := make(chan error, 1)

	for {
		// Check context cancellation before processing
		select {
		case <-ctx.Done():
			err := <-exitCh
			if err != nil {
				return err
			}
			return ctx.Err()
		default:
			// Continue processing
		}

		fetches := r.kfk.PollFetches(ctx)
		if fetches.IsClientClosed() {
			return status.Error(codes.Canceled, "client closed")
		}

		if fetches.Empty() {
			continue
		}

		iter := fetches.RecordIter()
		for _, fetchErr := range fetches.Errors() {
			logger.Error("error while fetching records",
				zap.String("topic", fetchErr.Topic),
				zap.Int32("partition", fetchErr.Partition),
				zap.Error(fetchErr.Err),
			)
			continue
		}

		for !iter.Done() {
			func(record *kgo.Record) {
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

				// Parse analytics event from Kafka record
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

					// Skip this record and commit it to avoid reprocessing
					if err := r.kfk.CommitRecords(ctxWithTrace, record); err != nil {
						logger.Error("failed to commit record",
							zap.Any("ctx", ctxWithTrace),
							zap.String("topic", record.Topic),
							zap.Int64("offset", record.Offset),
							zap.Int32("partition", record.Partition),
							zap.String("value", string(record.Value)),
							zap.Error(err),
						)
					}

					// Skip processing this record
					return
				}
				span.SetAttributes(
					attribute.String("event_type", event.EventType.ToString()),
					attribute.String("event_key", event.EventKey),
					attribute.String("user_id", event.UserID),
					attribute.String("workflow_id", event.WorkflowID),
				)

				shouldCommit := true
				var processErr error
				switch event.EventType {
				case analyticsmodel.EventTypeLogs:
					// Process logs analytics event
					if processErr = retrypkg.Once(func() error {
						return r.processLogsEvent(ctxWithTrace, &event)
					}, retryBackoff); processErr != nil {
						logger.Error("error processing logs event",
							zap.Any("ctx", ctxWithTrace),
							zap.Any("event", event),
							zap.Error(processErr),
						)
					}
				case analyticsmodel.EventTypeJobs:
					// Process jobs analytics event
					if processErr = retrypkg.Once(func() error {
						return r.processJobsEvent(ctxWithTrace, &event)
					}, retryBackoff); processErr != nil {
						logger.Error("error processing jobs event",
							zap.Any("ctx", ctxWithTrace),
							zap.Any("event", event),
							zap.Error(processErr),
						)
					}
				case analyticsmodel.EventTypeWorkflows:
					// Process workflows analytics event
					if processErr = retrypkg.Once(func() error {
						return r.processWorkflowsEvent(ctxWithTrace, &event)
					}, retryBackoff); processErr != nil {
						logger.Error("error processing workflows event",
							zap.Any("ctx", ctxWithTrace),
							zap.Any("event", event),
							zap.Error(processErr),
						)
					}
				default:
					logger.Warn("unknown event type",
						zap.Any("ctx", ctxWithTrace),
						zap.Any("event", event),
					)
				}

				if processErr != nil {
					shouldCommit = kafkapkg.ShouldCommitOnError(processErr)
				}
				if !shouldCommit {
					logger.Warn("analytics processing failed with retryable error, leaving record uncommitted",
						zap.Any("ctx", ctxWithTrace),
						zap.String("topic", record.Topic),
						zap.Int64("offset", record.Offset),
						zap.Int32("partition", record.Partition),
						zap.String("value", string(record.Value)),
						zap.Error(processErr),
					)
					return
				}

				// Commit the record after processing
				if err := r.kfk.CommitRecords(ctxWithTrace, record); err != nil {
					logger.Error("failed to commit record",
						zap.Any("ctx", ctxWithTrace),
						zap.String("topic", record.Topic),
						zap.Int64("offset", record.Offset),
						zap.Int32("partition", record.Partition),
						zap.String("value", string(record.Value)),
						zap.Error(err),
					)
				}
			}(iter.Next())
		}
	}
}
