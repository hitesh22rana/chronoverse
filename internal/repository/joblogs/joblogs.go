package joblogs

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/meilisearch/meilisearch-go"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	retrypkg "github.com/hitesh22rana/chronoverse/internal/pkg/retry"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	// retryBackoff is the duration to wait before retrying an operation.
	retryBackoff = time.Second
)

// Config represents the repository constants configuration.
type Config struct {
	BatchJobLogsSizeLimit      int
	BatchJobLogsTimeInterval   time.Duration
	BatchAnalyticsTimeInterval time.Duration
}

// Repository provides joblogs repository.
type Repository struct {
	tp  trace.Tracer
	cfg *Config
	rdb *redis.Store
	pg  *postgres.Postgres
	ch  *clickhouse.Client
	ms  meilisearch.ServiceManager
	kfk *kgo.Client

	batchMu sync.Mutex
}

// New creates a new joblogs repository.
func New(
	cfg *Config,
	rdb *redis.Store,
	pg *postgres.Postgres,
	ch *clickhouse.Client,
	ms meilisearch.ServiceManager,
	kfk *kgo.Client,
) *Repository {
	return &Repository{
		tp:  otel.Tracer(svcpkg.Info().GetName()),
		cfg: cfg,
		rdb: rdb,
		pg:  pg,
		ch:  ch,
		ms:  ms,
		kfk: kfk,
	}
}

// Queue for collecting messages before batch processing.
type queueData struct {
	record   *kgo.Record
	logEntry *jobsmodel.JobLogEvent
}

// Run start the joblogs execution.
//
//nolint:gocyclo // This function is complex due to the nature of processing logs in batches and analytics.
func (r *Repository) Run(ctx context.Context) error {
	logger := loggerpkg.FromContext(ctx)

	var (
		exitCh = make(chan error, 1)

		queue   = make([]*queueData, 0, r.cfg.BatchJobLogsSizeLimit)
		queueMu sync.Mutex
	)

	// Context with cancellation for graceful shutdown
	processingCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Goroutine to process data
	go func() {
		logsProcessingTicker := time.NewTicker(r.cfg.BatchJobLogsTimeInterval)
		defer logsProcessingTicker.Stop()

		analyticsTicker := time.NewTicker(r.cfg.BatchAnalyticsTimeInterval)
		defer analyticsTicker.Stop()

		for {
			select {
			case <-processingCtx.Done():
				// Process final batch before exiting

				// Copy the queue to a new slice for processing
				queueMu.Lock()
				data := make([]*queueData, len(queue))
				copy(data, queue)

				// Reset the queue
				queue = make([]*queueData, 0, r.cfg.BatchJobLogsSizeLimit)
				queueMu.Unlock()

				//nolint:errcheck // Ignore error as we are exiting
				retrypkg.Once(func() error {
					return r.processLogsBatch(ctx, data)
				}, retryBackoff)

				exitCh <- nil
				return

			case <-logsProcessingTicker.C:
				// Process batch on ticker interval

				// Copy the queue to a new slice for processing
				queueMu.Lock()
				data := make([]*queueData, len(queue))
				copy(data, queue)

				// Reset the queue
				queue = make([]*queueData, 0, r.cfg.BatchJobLogsSizeLimit)
				queueMu.Unlock()

				if err := retrypkg.Once(func() error {
					return r.processLogsBatch(ctx, data)
				}, retryBackoff); err != nil {
					logger.Error("error processing batch", zap.Error(err))
					queueMu.Lock()
					queue = prependQueueData(queue, data)
					queueMu.Unlock()
				}

			case <-analyticsTicker.C:
				// Durable log analytics are processed with log batches so Kafka source offsets
				// can be used as the replay-safe identity. Keep this ticker only as a pacing
				// hook for compatibility with the existing configuration.
			}
		}
	}()

	for {
		// Check context cancellation before processing
		select {
		case <-ctx.Done():
			// Wait for processor to finish final batch
			cancel()
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
					"joblogs.Run.processRecord",
					trace.WithAttributes(
						attribute.String("topic", record.Topic),
						attribute.Int64("offset", record.Offset),
						attribute.Int64("partition", int64(record.Partition)),
						attribute.String("key", string(record.Key)),
					),
				)
				defer span.End()

				logger := loggerpkg.FromContext(ctxWithTrace)

				// Parse log entry from Kafka record
				var logEntry jobsmodel.JobLogEvent
				if err := json.Unmarshal(record.Value, &logEntry); err != nil {
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
					attribute.String("event_key", logEntry.EventKey),
					attribute.String("job_id", logEntry.JobID),
					attribute.String("workflow_id", logEntry.WorkflowID),
					attribute.String("user_id", logEntry.UserID),
					attribute.String("stream", logEntry.Stream),
					attribute.Int64("sequence_num", int64(logEntry.SequenceNum)),
					attribute.Bool("retention", logEntry.Retention),
				)

				// Add log entry to the queue
				queueMu.Lock()

				// If the queue is full, process the batch
				if len(queue) >= r.cfg.BatchJobLogsSizeLimit {
					// Copy the queue to a new slice for processing
					data := make([]*queueData, len(queue))
					copy(data, queue)

					// Reset the queue
					queue = make([]*queueData, 0, r.cfg.BatchJobLogsSizeLimit)
					queueMu.Unlock()

					if err := retrypkg.Once(func() error {
						return r.processLogsBatch(context.WithoutCancel(ctx), data)
					}, retryBackoff); err != nil {
						logger.Error("error processing batch", zap.Error(err))
						queueMu.Lock()
						queue = prependQueueData(queue, data)
						queueMu.Unlock()
					}

					queueMu.Lock()
				}

				queue = append(queue, &queueData{
					record:   record,
					logEntry: &logEntry,
				})
				queueMu.Unlock()

				// Publish logs to Redis Pub/Sub in a separate goroutine
				// This allows to continue processing without waiting for Redis, since it's not critical to block the batch processing on Redis publishing.
				go func(logEntry *jobsmodel.JobLogEvent) {
					// Skip if log retention is disabled
					if !logEntry.Retention {
						return
					}

					data, err := json.Marshal(logEntry)
					if err != nil {
						return // Skip this log entry
					}

					// Publish to job-specific channel
					//nolint:errcheck // Ignore error as we are not blocking on Redis publish
					r.rdb.Publish(context.WithoutCancel(ctx), redis.GetJobLogsChannel(logEntry.JobID), data)
				}(&logEntry)
			}(iter.Next())
		}
	}
}

func prependQueueData(queue, data []*queueData) []*queueData {
	if len(data) == 0 {
		return queue
	}

	merged := make([]*queueData, 0, len(queue)+len(data))
	merged = append(merged, data...)
	merged = append(merged, queue...)

	return merged
}
