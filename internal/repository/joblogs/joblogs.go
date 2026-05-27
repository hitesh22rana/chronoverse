package joblogs

import (
	"context"
	"encoding/json"
	"time"

	"github.com/meilisearch/meilisearch-go"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	kafkapkg "github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
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
	BatchJobLogsSizeLimit    int
	BatchJobLogsTimeInterval time.Duration
}

// Repository provides joblogs repository.
type Repository struct {
	tp     trace.Tracer
	cfg    *Config
	rdb    *redis.Store
	pg     *postgres.Postgres
	ch     *clickhouse.Client
	ms     meilisearch.ServiceManager
	kfk    *kgo.Client
	runner *kafkapkg.PartitionRunner
}

// New creates a new joblogs repository.
func New(
	cfg *Config,
	rdb *redis.Store,
	pg *postgres.Postgres,
	ch *clickhouse.Client,
	ms meilisearch.ServiceManager,
	kfk *kgo.Client,
	lifecycle *kafkapkg.PartitionLifecycle,
) *Repository {
	r := &Repository{
		tp:  otel.Tracer(svcpkg.Info().GetName()),
		cfg: cfg,
		rdb: rdb,
		pg:  pg,
		ch:  ch,
		ms:  ms,
		kfk: kfk,
	}
	r.runner = kafkapkg.NewPartitionBatchRunner(kfk, r.processRecordsBatch, &kafkapkg.PartitionRunnerConfig{
		Name:          "joblogs",
		RetryBackoff:  retryBackoff,
		BatchSize:     r.batchSize(),
		BatchInterval: r.batchInterval(),
		Tracer:        r.tp,
	}, lifecycle)

	return r
}

// Queue for collecting messages before batch processing.
type queueData struct {
	record   *kgo.Record
	logEntry *jobsmodel.JobLogEvent
}

// Run starts the joblogs execution.
func (r *Repository) Run(ctx context.Context) error {
	logger := loggerpkg.FromContext(ctx)
	r.runner.SetLogger(logger)
	return r.runner.Run(ctx)
}

func (r *Repository) processRecordsBatch(ctx context.Context, records []*kgo.Record) error {
	data := make([]*queueData, 0, len(records))
	for _, record := range records {
		item := r.parseRecord(ctx, record)
		if item == nil {
			continue
		}
		data = append(data, item)
	}
	if len(data) == 0 {
		return nil
	}

	logger := loggerpkg.FromContext(ctx)
	var processedData []*queueData
	err := retrypkg.Once(func() error {
		var processErr error
		processedData, processErr = r.processLogsBatch(ctx, data)
		return processErr
	}, retryBackoff)
	if err != nil {
		logger.Error("error processing batch", zap.Error(err))
		return err
	}

	for _, item := range processedData {
		r.publishLog(ctx, item.logEntry)
	}

	return nil
}

func (r *Repository) parseRecord(ctx context.Context, record *kgo.Record) *queueData {
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
		return nil
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

	return &queueData{
		record:   record,
		logEntry: &logEntry,
	}
}

func (r *Repository) publishLog(ctx context.Context, logEntry *jobsmodel.JobLogEvent) {
	go func() {
		if !logEntry.Retention {
			return
		}

		data, err := json.Marshal(logEntry)
		if err != nil {
			return
		}

		//nolint:errcheck // Redis pub/sub is best-effort and should not block durable processing.
		r.rdb.Publish(context.WithoutCancel(ctx), redis.GetJobLogsChannel(logEntry.JobID), data)
	}()
}

func (r *Repository) batchSize() int {
	if r.cfg.BatchJobLogsSizeLimit <= 0 {
		return 1
	}

	return r.cfg.BatchJobLogsSizeLimit
}

func (r *Repository) batchInterval() time.Duration {
	if r.cfg.BatchJobLogsTimeInterval <= 0 {
		return 2 * time.Second
	}

	return r.cfg.BatchJobLogsTimeInterval
}
