package analyticsprocessor

import (
	"context"
	"encoding/json"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	analyticsmodel "github.com/hitesh22rana/chronoverse/internal/model/analytics"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
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
				// Parse analytics event from Kafka record
				var event analyticsmodel.AnalyticEvent
				if err := json.Unmarshal(record.Value, &event); err != nil {
					logger.Error("failed to unmarshal record",
						zap.Any("ctx", ctx),
						zap.String("topic", record.Topic),
						zap.Int64("offset", record.Offset),
						zap.Int32("partition", record.Partition),
						zap.String("value", string(record.Value)),
						zap.Error(err),
					)

					// Skip this record and commit it to avoid reprocessing
					if err := r.kfk.CommitRecords(ctx, record); err != nil {
						logger.Error("failed to commit record",
							zap.Any("ctx", ctx),
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

				switch event.EventType {
				case analyticsmodel.EventTypeLogs:
					// Process logs analytics event
					if err := withRetry(func() error {
						return r.processLogsEvent(ctx, &event)
					}); err != nil {
						logger.Error("error processing logs event",
							zap.Any("ctx", ctx),
							zap.Any("event", event),
							zap.Error(err),
						)
					}
				case analyticsmodel.EventTypeJobs:
					// Process jobs analytics event
					if err := withRetry(func() error {
						return r.processJobsEvent(ctx, &event)
					}); err != nil {
						logger.Error("error processing jobs event",
							zap.Any("ctx", ctx),
							zap.Any("event", event),
							zap.Error(err),
						)
					}
				case analyticsmodel.EventTypeWorkflows:
					// Process workflows analytics event
					if err := withRetry(func() error {
						return r.processWorkflowsEvent(ctx, &event)
					}); err != nil {
						logger.Error("error processing workflows event",
							zap.Any("ctx", ctx),
							zap.Any("event", event),
							zap.Error(err),
						)
					}
				default:
					logger.Warn("unknown event type",
						zap.Any("ctx", ctx),
						zap.Any("event", event),
					)
				}

				// Commit the record after processing
				if err := r.kfk.CommitRecords(ctx, record); err != nil {
					logger.Error("failed to commit record",
						zap.Any("ctx", ctx),
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

// withRetry executes the given function and retries once if it fails with an error
// other than codes.FailedPrecondition.
func withRetry(fn func() error) error {
	err := fn()
	if err == nil {
		return nil
	}

	// If the error is FailedPrecondition or InvalidArgument, do not retry
	if status.Code(err) == codes.FailedPrecondition || status.Code(err) == codes.InvalidArgument {
		return err
	}

	// Wait for the retry backoff duration
	time.Sleep(retryBackoff)

	// Execute the function again
	return fn()
}
