package joblogs

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	jobLogsPublishChannel = "job_logs"
)

// Config represents the repository constants configuration.
type Config struct {
	BatchSizeLimit int
	BatchTimeLimit time.Duration
}

// Repository provides joblogs repository.
type Repository struct {
	tp  trace.Tracer
	cfg *Config
	rdb *redis.Store
	ch  *clickhouse.Client
	kfk *kgo.Client
}

// New creates a new joblogs repository.
func New(cfg *Config, rdb *redis.Store, ch *clickhouse.Client, kfk *kgo.Client) *Repository {
	return &Repository{
		tp:  otel.Tracer(svcpkg.Info().GetName()),
		cfg: cfg,
		rdb: rdb,
		ch:  ch,
		kfk: kfk,
	}
}

// Queue for collecting messages before batch processing.
type queueData struct {
	record   *kgo.Record
	logEntry *jobsmodel.JobLogEntry
}

// Run start the joblogs execution.
func (r *Repository) Run(ctx context.Context) error {
	logger := loggerpkg.FromContext(ctx)

	var (
		queue   = make([]queueData, 0, r.cfg.BatchSizeLimit)
		queueMu sync.Mutex
	)

	// Context with cancellation for graceful shutdown
	processingCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Goroutine to process batched messages
	processingErrCh := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(r.cfg.BatchTimeLimit)
		defer ticker.Stop()

		for {
			select {
			case <-processingCtx.Done():
				// Process final batch before exiting
				//nolint:errcheck // Ignore error as we are exiting
				r.processBatch(ctx, &queue, &queueMu, logger)
				processingErrCh <- nil
				return

			case <-ticker.C:
				// Process batch on ticker interval
				if err := r.processBatch(ctx, &queue, &queueMu, logger); err != nil {
					// Continue processing despite errors
					logger.Error("error processing batch", zap.Error(err))
				}
			}
		}
	}()

	for {
		// Check context cancellation before processing
		select {
		case <-ctx.Done():
			// Wait for processor to finish final batch
			cancel()
			err := <-processingErrCh
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
				var logEntry jobsmodel.JobLogEntry
				if err := json.Unmarshal(record.Value, &logEntry); err != nil {
					logger.Error("failed to unmarshal record",
						zap.Any("ctx", ctx),
						zap.String("topic", record.Topic),
						zap.Int64("offset", record.Offset),
						zap.Int32("partition", record.Partition),
						zap.String("message", string(record.Value)),
						zap.Error(err),
					)

					// Skip this record and commit it to avoid reprocessing
					if err := r.kfk.CommitRecords(ctx, record); err != nil {
						logger.Error("failed to commit record",
							zap.Any("ctx", ctx),
							zap.String("topic", record.Topic),
							zap.Int64("offset", record.Offset),
							zap.Int32("partition", record.Partition),
							zap.String("message", string(record.Value)),
							zap.Error(err),
						)
					}

					// Skip processing this record
					return
				}

				// Add log entry to the queue
				queueMu.Lock()
				defer queueMu.Unlock()
				queue = append(queue, queueData{
					record:   record,
					logEntry: &logEntry,
				})
			}(iter.Next())
		}
	}
}

// processBatch processes accumulated messages in a batch.
func (r *Repository) processBatch(ctx context.Context, queue *[]queueData, mutex *sync.Mutex, logger *zap.Logger) error {
	ctx, span := r.tp.Start(ctx, "joblogs.Run.processBatch")
	defer span.End()

	// Lock the queue and get current items
	mutex.Lock()

	// If queue is empty, nothing to do
	if len(*queue) == 0 {
		mutex.Unlock()
		return nil
	}

	// Take the current queue and reset it
	currentBatch := *queue
	*queue = make([]queueData, 0, r.cfg.BatchSizeLimit)

	// Unlock to allow more additions while we process
	mutex.Unlock()

	logger.Info("processing batch", zap.Int("batch_size", len(currentBatch)))

	// Extract logs and records
	logs := make([]*jobsmodel.JobLogEntry, 0, len(currentBatch))
	records := make([]*kgo.Record, 0, len(currentBatch))

	for _, item := range currentBatch {
		logs = append(logs, item.logEntry)
		records = append(records, item.record)
	}

	// Publish logs to Redis Pub/Sub in a separate goroutine
	// This allows us to continue processing without waiting for Redis, since it's not critical to block the batch processing on Redis publishing.
	go func() {
		// Send logs to Redis Pub/Sub
		for _, log := range logs {
			// Marshal log entry to JSON
			data, err := json.Marshal(log)
			if err != nil {
				logger.Error("failed to marshal log entry",
					zap.Any("log", log),
					zap.Error(err),
				)
				continue // Skip this log entry
			}

			// Publish to Redis Pub/Sub
			if err := r.rdb.Publish(context.WithoutCancel(ctx), jobLogsPublishChannel, data); err != nil {
				logger.Error("failed to publish log entry to Redis",
					zap.Any("log", log),
					zap.Error(err),
				)
			}
		}
	}()

	// Insert logs into ClickHouse
	if err := r.insertLogsBatch(ctx, logs); err != nil {
		logger.Error("failed to insert logs batch", zap.Error(err))
		return err
	}

	// Commit Kafka offsets after successful insertion
	if err := r.kfk.CommitRecords(ctx, records...); err != nil {
		logger.Error("failed to commit records batch", zap.Error(err))
		return err
	}

	logger.Info("successfully processed and committed batch", zap.Int("records", len(records)))

	return nil
}

// insertLogsBatch inserts a batch of logs into the database.
func (r *Repository) insertLogsBatch(ctx context.Context, logs []*jobsmodel.JobLogEntry) error {
	if len(logs) == 0 {
		return nil
	}

	// Prepare batch statement
	stmt := `
        INSERT INTO job_logs 
        (job_id, workflow_id, user_id, timestamp, message, sequence_num)
        VALUES (?, ?, ?, ?, ?, ?);
    `

	if err := r.ch.BatchInsert(
		ctx,
		stmt,
		func(batch driver.Batch) error {
			for _, log := range logs {
				err := batch.Append(
					log.JobID,
					log.WorkflowID,
					log.UserID,
					log.TimeStamp,
					log.Message,
					log.SequenceNum,
				)
				if err != nil {
					return err
				}
			}
			return nil
		},
	); err != nil {
		return status.Errorf(codes.Internal, "failed to prepare batch: %v", err)
	}

	return nil
}
