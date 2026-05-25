package joblogs

import (
	"context"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/datastructures/countminsketch"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

const logAnalyticsConsumer = "joblogs-processor:analytics"

type logAnalyticsPartition struct {
	topic     string
	partition int32
}

type logAnalyticsEstimate struct {
	cms           *countminsketch.CountMinSketch
	workflowUsers map[string]string
	maxOffset     int64
}

// processLogsAnalytics updates durable log counters using Kafka source offsets as replay-safe identity.
func (r *Repository) processLogsAnalytics(ctx context.Context, batch []*queueData) error {
	ctx, span := r.tp.Start(ctx, "joblogs.Run.processLogsAnalytics")
	defer span.End()

	recordsByPartition := groupLogAnalyticsRecords(batch)
	if len(recordsByPartition) == 0 {
		return nil
	}

	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to start log analytics transaction: %v", err)
	}
	//nolint:errcheck // Rollback is a no-op after commit.
	defer tx.Rollback(ctx)

	for _, partition := range sortedLogAnalyticsPartitions(recordsByPartition) {
		if partitionErr := r.processLogsAnalyticsPartition(ctx, tx, partition, recordsByPartition[partition]); partitionErr != nil {
			return partitionErr
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return status.Errorf(codes.Internal, "failed to commit log analytics transaction: %v", err)
	}

	return nil
}

func groupLogAnalyticsRecords(batch []*queueData) map[logAnalyticsPartition][]*queueData {
	recordsByPartition := make(map[logAnalyticsPartition][]*queueData)
	for _, item := range batch {
		if item == nil || item.record == nil || item.logEntry == nil {
			continue
		}

		key := logAnalyticsPartition{
			topic:     item.record.Topic,
			partition: item.record.Partition,
		}
		recordsByPartition[key] = append(recordsByPartition[key], item)
	}

	return recordsByPartition
}

func sortedLogAnalyticsPartitions(recordsByPartition map[logAnalyticsPartition][]*queueData) []logAnalyticsPartition {
	partitions := make([]logAnalyticsPartition, 0, len(recordsByPartition))
	for partition := range recordsByPartition {
		partitions = append(partitions, partition)
	}
	sort.Slice(partitions, func(i, j int) bool {
		if partitions[i].topic == partitions[j].topic {
			return partitions[i].partition < partitions[j].partition
		}
		return partitions[i].topic < partitions[j].topic
	})

	return partitions
}

func (r *Repository) processLogsAnalyticsPartition(
	ctx context.Context,
	tx pgx.Tx,
	partition logAnalyticsPartition,
	records []*queueData,
) error {
	if _, err := tx.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s (consumer, topic, partition, last_offset)
		VALUES ($1, $2, $3, -1)
		ON CONFLICT DO NOTHING;
	`, postgres.TableLogAnalyticsOffsets), logAnalyticsConsumer, partition.topic, partition.partition); err != nil {
		return status.Errorf(codes.Internal, "failed to initialize log analytics offset: %v", err)
	}

	lastOffset, err := lockLogAnalyticsOffset(ctx, tx, partition)
	if err != nil {
		return err
	}

	estimate := estimatePartitionLogCounts(records, lastOffset)
	if updateErr := updatePartitionLogCounts(ctx, tx, estimate.cms, estimate.workflowUsers); updateErr != nil {
		return updateErr
	}

	if estimate.maxOffset <= lastOffset {
		return nil
	}

	if _, err = tx.Exec(ctx, fmt.Sprintf(`
		UPDATE %s
		SET last_offset = $4
		WHERE consumer = $1 AND topic = $2 AND partition = $3;
	`, postgres.TableLogAnalyticsOffsets), logAnalyticsConsumer, partition.topic, partition.partition, estimate.maxOffset); err != nil {
		return status.Errorf(codes.Internal, "failed to advance log analytics offset: %v", err)
	}

	return nil
}

func lockLogAnalyticsOffset(ctx context.Context, tx pgx.Tx, partition logAnalyticsPartition) (int64, error) {
	var lastOffset int64
	if err := tx.QueryRow(ctx, fmt.Sprintf(`
		SELECT last_offset
		FROM %s
		WHERE consumer = $1 AND topic = $2 AND partition = $3
		FOR UPDATE;
	`, postgres.TableLogAnalyticsOffsets), logAnalyticsConsumer, partition.topic, partition.partition).Scan(&lastOffset); err != nil {
		return 0, status.Errorf(codes.Internal, "failed to lock log analytics offset: %v", err)
	}

	return lastOffset, nil
}

func estimatePartitionLogCounts(records []*queueData, lastOffset int64) *logAnalyticsEstimate {
	cms := countminsketch.NewCountMinSketch(Epsilon, Delta)
	workflowUsers := make(map[string]string)
	maxOffset := lastOffset

	for _, item := range records {
		if item.record.Offset <= lastOffset {
			continue
		}

		if item.record.Offset > maxOffset {
			maxOffset = item.record.Offset
		}

		if item.logEntry.WorkflowID == "" || item.logEntry.UserID == "" {
			continue
		}

		cms.Add(item.logEntry.WorkflowID)
		if _, ok := workflowUsers[item.logEntry.WorkflowID]; !ok {
			workflowUsers[item.logEntry.WorkflowID] = item.logEntry.UserID
		}
	}

	return &logAnalyticsEstimate{
		cms:           cms,
		workflowUsers: workflowUsers,
		maxOffset:     maxOffset,
	}
}

func updatePartitionLogCounts(
	ctx context.Context,
	tx pgx.Tx,
	cms *countminsketch.CountMinSketch,
	workflowUsers map[string]string,
) error {
	for workflowID, userID := range workflowUsers {
		estimateLogsCount := cms.Estimate(workflowID)
		if estimateLogsCount == 0 {
			continue
		}

		if _, err := tx.Exec(ctx, fmt.Sprintf(`
			INSERT INTO %s
				(user_id, workflow_id, logs_count)
			VALUES ($1, $2, $3)
			ON CONFLICT (user_id, workflow_id)
			DO UPDATE SET
				logs_count = %s.logs_count + EXCLUDED.logs_count;
		`, postgres.TableAnalytics, postgres.TableAnalytics), userID, workflowID, estimateLogsCount); err != nil {
			return status.Errorf(codes.Internal, "failed to update logs analytics: %v", err)
		}
	}

	return nil
}
