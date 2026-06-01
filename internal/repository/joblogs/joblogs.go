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
	"github.com/hitesh22rana/chronoverse/internal/pkg/joblogevents"
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
	tp            trace.Tracer
	cfg           *Config
	pg            *postgres.Postgres
	ch            *clickhouse.Client
	ms            meilisearch.ServiceManager
	kfk           *kgo.Client
	livePublisher *joblogevents.LivePublisher
	runner        *kafkapkg.PartitionRunner
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
		tp:            otel.Tracer(svcpkg.Info().GetName()),
		cfg:           cfg,
		pg:            pg,
		ch:            ch,
		ms:            ms,
		kfk:           kfk,
		livePublisher: joblogevents.NewLivePublisher(rdb, joblogevents.LivePublisherConfig{}),
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

	liveCtx, cancelLive := context.WithCancel(ctx)
	liveDone := r.runLiveJobLogPublisher(liveCtx, logger)

	err := r.runner.Run(ctx)
	cancelLive()
	if liveDone != nil {
		<-liveDone
	}
	return err
}

func (r *Repository) runLiveJobLogPublisher(ctx context.Context, logger *zap.Logger) <-chan struct{} {
	if r.livePublisher == nil {
		return nil
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		r.livePublisher.Run(ctx, logger)
	}()

	return done
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
	err := retrypkg.Do(ctx, 2, retryBackoff, func() error {
		var processErr error
		processedData, processErr = r.processLogsBatch(ctx, data)
		return processErr
	})
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
		attribute.Bool("live_published", logEntry.LivePublished),
	)

	return &queueData{
		record:   record,
		logEntry: &logEntry,
	}
}

func (r *Repository) publishLog(ctx context.Context, logEntry *jobsmodel.JobLogEvent) {
	if r.livePublisher == nil || logEntry == nil || !logEntry.Retention || logEntry.LivePublished {
		return
	}

	if r.livePublisher.TryPublish(logEntry) {
		return
	}

	loggerpkg.FromContext(ctx).Debug("dropped live job log",
		zap.String("job_id", logEntry.JobID),
		zap.String("workflow_id", logEntry.WorkflowID),
		zap.String("event_key", logEntry.EventKey),
		zap.String("stream", logEntry.Stream),
		zap.Uint32("sequence_num", logEntry.SequenceNum),
	)
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
