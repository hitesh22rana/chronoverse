package executor

import (
	"context"
	"encoding/json"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/joblogevents"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	retrypkg "github.com/hitesh22rana/chronoverse/internal/pkg/retry"
)

func (r *Repository) publishLiveJobLog(ctx context.Context, event *jobsmodel.JobLogEvent) {
	if r.livePublisher == nil || event == nil || !event.Retention {
		return
	}

	if r.livePublisher.TryPublish(event) {
		event.LivePublished = true
		return
	}

	loggerpkg.FromContext(ctx).Debug("dropped live job log",
		zap.String("job_id", event.JobID),
		zap.String("workflow_id", event.WorkflowID),
		zap.String("event_key", event.EventKey),
		zap.String("stream", event.Stream),
		zap.Uint32("sequence_num", event.SequenceNum),
	)
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

func (r *Repository) publishJobLogBatch(ctx context.Context, records []*kgo.Record) {
	if len(records) == 0 {
		return
	}
	logger := loggerpkg.FromContext(ctx)
	fields := jobLogBatchFields(records)
	fields = append(fields, zap.Int("records", len(records)))
	if r.kfk == nil {
		logger.Error("failed to publish job log batch; dropping logs",
			zap.Error(status.Error(codes.FailedPrecondition, "kafka client is not configured")),
			zap.Int("attempts", 0),
			zap.Int("records", len(records)),
		)
		return
	}

	ctxTimeout, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.cfg.JobLogPublishTimeout)
	defer cancel()

	attempts := r.cfg.JobLogPublishRetries
	if attempts <= 0 {
		attempts = 1
	}

	attempt := 0
	err := retrypkg.Do(ctxTimeout, attempts, r.cfg.JobLogPublishBackoff, func() error {
		attempt++
		if err := r.kfk.ProduceSync(ctxTimeout, records...).FirstErr(); err != nil {
			if attempt < attempts && ctxTimeout.Err() == nil {
				logger.Warn("failed to publish job log batch; retrying",
					append(fields,
						zap.Int("attempt", attempt),
						zap.Int("attempts", attempts),
						zap.Error(err),
					)...,
				)
			}
			return err
		}

		return nil
	})
	if err == nil {
		return
	}

	logger.Error("failed to publish job log batch; dropping logs",
		append(fields,
			zap.Int("attempts", attempts),
			zap.Error(err),
		)...,
	)
}

func jobLogBatchFields(records []*kgo.Record) []zap.Field {
	fields := make([]zap.Field, 0, 6)
	if len(records) == 0 || records[0] == nil {
		return fields
	}

	first := records[0]
	fields = append(fields,
		zap.String("topic", first.Topic),
		zap.String("job_id", string(first.Key)),
	)

	var event jobsmodel.JobLogEvent
	if err := json.Unmarshal(first.Value, &event); err != nil {
		return fields
	}

	return append(fields,
		zap.String("workflow_id", event.WorkflowID),
		zap.String("event_key", event.EventKey),
		zap.String("stream", event.Stream),
		zap.Uint32("sequence_num", event.SequenceNum),
	)
}

func (r *Repository) publishJobLogEvent(ctx context.Context, event *jobsmodel.JobLogEvent) {
	record, err := joblogevents.KafkaRecord(event)
	if err != nil {
		loggerpkg.FromContext(ctx).Error("failed to create job log kafka record",
			zap.String("job_id", event.JobID),
			zap.String("workflow_id", event.WorkflowID),
			zap.String("event_key", event.EventKey),
			zap.String("stream", event.Stream),
			zap.Uint32("sequence_num", event.SequenceNum),
			zap.Error(err),
		)
		return
	}

	r.publishJobLogBatch(ctx, []*kgo.Record{record})
}
