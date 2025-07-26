package joblogs

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
)

// processLogsBatch processes accumulated logs in a batch.
func (r *Repository) processLogsBatch(ctx context.Context, batch []*queueData) error {
	logger := loggerpkg.FromContext(ctx)

	ctx, span := r.tp.Start(ctx, "joblogs.Run.processLogsBatch")
	defer span.End()

	// If batch is empty, nothing to do
	if len(batch) == 0 {
		return nil
	}

	logger.Info("processing batch", zap.Int("batch_size", len(batch)))

	// Extract logs and records
	logs := make([]*jobsmodel.JobLogEvent, 0, len(batch))
	records := make([]*kgo.Record, 0, len(batch))

	for _, item := range batch {
		logs = append(logs, item.logEntry)
		records = append(records, item.record)
	}

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

	logger.Info(
		"successfully processed batch of logs and committed offsets",
		zap.Int("records", len(records)),
	)

	return nil
}

// insertLogsBatch inserts a batch of logs into the database.
func (r *Repository) insertLogsBatch(ctx context.Context, logs []*jobsmodel.JobLogEvent) error {
	if len(logs) == 0 {
		return nil
	}

	// Prepare batch statement
	stmt := fmt.Sprintf(`
        INSERT INTO %s 
        (job_id, workflow_id, user_id, timestamp, message, sequence_num, stream)
        VALUES (?, ?, ?, ?, ?, ?, ?);
    `, clickhouse.TableJobLogs)

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
					log.Stream,
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
