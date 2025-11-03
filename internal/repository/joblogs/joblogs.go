package joblogs

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/meilisearch/meilisearch-go"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	"github.com/hitesh22rana/chronoverse/internal/pkg/datastructures/countminsketch"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	// retryBackoff is the duration to wait before retrying an operation.
	retryBackoff = time.Second

	// Epsilon is the error rate parameter for the CountMinSketch.
	// It determines the maximum allowable error in the frequency estimation of elements.
	// Specifically, with probability at least (1 - Delta), the estimated count for any element
	// will exceed the true count by at most Epsilon * total count.
	// A smaller Epsilon increases accuracy but requires more memory.
	// The value 0.0002 was chosen to balance memory usage and estimation accuracy,
	// ensuring that frequency estimates are within 0.02% of the total count, which is suitable
	// for the expected data volume and application requirements.
	Epsilon = 0.0002
	// Delta is the confidence level for the CountMinSketch.
	Delta = 0.001
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
	ch  *clickhouse.Client
	ms  meilisearch.ServiceManager
	kfk *kgo.Client
}

// New creates a new joblogs repository.
func New(
	cfg *Config,
	rdb *redis.Store,
	ch *clickhouse.Client,
	ms meilisearch.ServiceManager,
	kfk *kgo.Client,
) *Repository {
	return &Repository{
		tp:  otel.Tracer(svcpkg.Info().GetName()),
		cfg: cfg,
		rdb: rdb,
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

		cms             = countminsketch.NewCountMinSketch(Epsilon, Delta)
		uniqueWorkflows = sync.Map{}
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
				withRetry(func() error {
					return r.processLogsBatch(ctx, data)
				})

				// Process final analytics before exiting

				// Copy the unique workflows to a new map for processing and empty the map
				uniqueWorkflowsCopy := sync.Map{}
				uniqueWorkflows.Range(func(key, value any) bool {
					uniqueWorkflowsCopy.Store(key, value)
					return true
				})

				// Clear the original map to avoid reprocessing
				uniqueWorkflows.Clear()

				// Atomically snapshot and reset the countminsketch for processing
				cmsCopy := cms.SnapshotAndReset()

				//nolint:errcheck // Ignore error as we are exiting
				withRetry(func() error {
					return r.processLogsAnalytics(ctx, cmsCopy, &uniqueWorkflowsCopy)
				})

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

				if err := withRetry(func() error {
					return r.processLogsBatch(ctx, data)
				}); err != nil {
					// Continue processing despite errors
					logger.Error("error processing batch", zap.Error(err))
				}

			case <-analyticsTicker.C:
				// Process analytics on ticker interval

				// Copy the unique workflows to a new map for processing and empty the map
				uniqueWorkflowsCopy := sync.Map{}
				uniqueWorkflows.Range(func(key, value any) bool {
					uniqueWorkflowsCopy.Store(key, value)
					return true
				})

				// Clear the original map to avoid reprocessing
				uniqueWorkflows.Clear()

				// Atomically snapshot and reset the countminsketch for processing
				cmsCopy := cms.SnapshotAndReset()

				//nolint:errcheck // Ignore error as we are exiting
				withRetry(func() error {
					return r.processLogsAnalytics(ctx, cmsCopy, &uniqueWorkflowsCopy)
				})
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
				// Parse log entry from Kafka record
				var logEntry jobsmodel.JobLogEvent
				if err := json.Unmarshal(record.Value, &logEntry); err != nil {
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

				// Add log entry to the queue
				queueMu.Lock()

				// If the queue is full, process the batch
				if len(queue) >= r.cfg.BatchJobLogsSizeLimit {
					// Copy the queue to a new slice for processing
					data := make([]*queueData, len(queue))
					copy(data, queue)

					// Reset the queue
					queue = make([]*queueData, 0, r.cfg.BatchJobLogsSizeLimit)

					go func() {
						if err := withRetry(func() error {
							return r.processLogsBatch(context.WithoutCancel(ctx), data)
						}); err != nil {
							// Continue processing despite errors
							logger.Error("error processing batch", zap.Error(err))
						}
					}()
				}

				queue = append(queue, &queueData{
					record:   record,
					logEntry: &logEntry,
				})
				queueMu.Unlock()

				// Publish logs to Redis Pub/Sub in a separate goroutine
				// This allows to continue processing without waiting for Redis, since it's not critical to block the batch processing on Redis publishing.
				go func(logEntry *jobsmodel.JobLogEvent) {
					data, err := json.Marshal(logEntry)
					if err != nil {
						return // Skip this log entry
					}

					// Publish to job-specific channel
					//nolint:errcheck // Ignore error as we are not blocking on Redis publish
					r.rdb.Publish(context.WithoutCancel(ctx), redis.GetJobLogsChannel(logEntry.JobID), data)
				}(&logEntry)

				// Count the log entry in the CountMinSketch
				// This is done before processing to ensure we count all logs, even if they fail
				// This allows to track the number of logs per workflow
				// This is useful for analytics and monitoring purposes
				cms.Add(logEntry.WorkflowID)
				uniqueWorkflows.Store(logEntry.WorkflowID, logEntry.UserID)
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
