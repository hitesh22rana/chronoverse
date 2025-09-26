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
	meilisearchpkg "github.com/hitesh22rana/chronoverse/internal/pkg/meilisearch"
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
	documents := make([]*map[string]any, 0, len(batch))

	for _, item := range batch {
		log := item.logEntry

		logs = append(logs, log)
		records = append(records, item.record)
		documents = append(documents, &map[string]any{
			"id":           fmt.Sprintf("%s-%d-%s", log.JobID, log.SequenceNum, log.Stream),
			"job_id":       log.JobID,
			"workflow_id":  log.WorkflowID,
			"user_id":      log.UserID,
			"message":      log.Message,
			"timestamp":    log.TimeStamp,
			"sequence_num": log.SequenceNum,
			"stream":       log.Stream,
		})
	}

	// Insert logs into ClickHouse
	if err := r.insertLogsBatchToClickhouse(ctx, logs); err != nil {
		logger.Error("failed to insert logs batch", zap.Error(err))
		return err
	}

	// Don't fail the complete flow only log the error as we don't want to block the process
	// Insert logs documents into the meilisearch database.
	if err := r.insertLogsDocumentsToMeiliSearch(ctx, documents); err != nil {
		logger.Error("failed to add documents", zap.Error(err))
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

// insertLogsBatchToClickhouse inserts a batch of logs into the clickhouse database.
func (r *Repository) insertLogsBatchToClickhouse(ctx context.Context, logs []*jobsmodel.JobLogEvent) error {
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

// insertLogsDocumentsToMeiliSearch inserts logs documents into the meilisearch database.
func (r *Repository) insertLogsDocumentsToMeiliSearch(ctx context.Context, documents []*map[string]any) error {
	if len(documents) == 0 {
		return nil
	}

	indexJobLogs := meilisearchpkg.Indexes[meilisearchpkg.IndexJobLogs]

	if _, err := r.ms.Index(indexJobLogs.Name).AddDocumentsWithContext(ctx, documents, &indexJobLogs.PrimaryKey); err != nil {
		return status.Errorf(codes.Internal, "failed to add documents: %v", err)
	}

	return nil
}
