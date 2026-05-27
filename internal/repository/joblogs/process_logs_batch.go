package joblogs

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
	"github.com/meilisearch/meilisearch-go"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	meilisearchpkg "github.com/hitesh22rana/chronoverse/internal/pkg/meilisearch"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

type workflowLogPair struct {
	workflowID string
	userID     string
}

const (
	jobLogsExternalOperationTimeout = 2 * time.Minute
	jobLogsMeiliSearchPollPeriod    = 2 * time.Second
)

// processLogsBatch processes accumulated logs in a batch.
func (r *Repository) processLogsBatch(ctx context.Context, batch []*queueData) ([]*queueData, error) {
	logger := loggerpkg.FromContext(ctx)

	ctx, span := r.tp.Start(ctx, "joblogs.Run.processLogsBatch")
	defer span.End()

	// If batch is empty, nothing to do
	if len(batch) == 0 {
		return nil, nil
	}

	batch, skipped, err := r.filterActiveWorkflowLogs(ctx, batch)
	if err != nil {
		return nil, err
	}
	if cleanupErr := r.cleanupRetainedLogs(ctx, skipped); cleanupErr != nil {
		logger.Error("failed to cleanup skipped logs", zap.Error(cleanupErr))
		return nil, cleanupErr
	}
	if len(batch) == 0 {
		logger.Info("skipped log batch because no referenced workflows are active")
		return nil, nil
	}

	// Extract logs
	logs := make([]*jobsmodel.JobLogEvent, 0, len(batch))

	for _, item := range batch {
		log := item.logEntry
		if log.EventKey == "" {
			log.EventKey = idempotency.LogEventKey(log.JobID, log.Stream, log.SequenceNum)
		}

		// Skip if log retention is disabled
		if !log.Retention {
			continue
		}

		logs = append(logs, log)
	}

	logs = dedupeLogsInBatch(logs)
	documents := make([]*map[string]any, 0, len(logs))
	for _, log := range logs {
		documents = append(documents, &map[string]any{
			"id":           log.EventKey,
			"job_id":       log.JobID,
			"workflow_id":  log.WorkflowID,
			"user_id":      log.UserID,
			"message":      log.Message,
			"timestamp":    log.TimeStamp,
			"sequence_num": log.SequenceNum,
			"stream":       log.Stream,
		})
	}

	logger.Info("processing batch", zap.Int("batch_size", len(logs)))

	// Insert logs into ClickHouse
	if insertErr := r.insertLogsBatchToClickhouse(ctx, logs); insertErr != nil {
		logger.Error("failed to insert logs batch", zap.Error(insertErr))
		return nil, insertErr
	}

	if insertErr := r.insertLogsDocumentsToMeiliSearch(ctx, documents); insertErr != nil {
		logger.Error("failed to add documents", zap.Error(insertErr))
		return nil, insertErr
	}

	batch, deletedDuringProcessing, err := r.filterActiveWorkflowLogs(ctx, batch)
	if err != nil {
		return nil, err
	}
	if cleanupErr := r.cleanupRetainedLogs(ctx, deletedDuringProcessing); cleanupErr != nil {
		logger.Error("failed to cleanup logs for workflows deleted during processing", zap.Error(cleanupErr))
		return nil, cleanupErr
	}
	if len(batch) == 0 {
		logger.Info("skipped log batch because referenced workflows were deleted during processing")
		return nil, nil
	}

	if err := r.processLogsAnalytics(ctx, batch); err != nil {
		logger.Error("failed to process logs analytics", zap.Error(err))
		return nil, err
	}

	logger.Info(
		"successfully processed batch of logs",
		zap.Int("records", len(batch)),
	)

	return batch, nil
}

func (r *Repository) filterActiveWorkflowLogs(
	ctx context.Context,
	batch []*queueData,
) (active, skipped []*queueData, err error) {
	pairs := uniqueWorkflowLogPairs(batch)
	if len(pairs) == 0 {
		if len(batch) > 0 {
			loggerpkg.FromContext(ctx).Info("skipped job logs with invalid workflow or user ids", zap.Int("skipped", len(batch)))
		}
		return nil, nil, nil
	}

	existing, err := r.fetchActiveWorkflowLogPairs(ctx, pairs)
	if err != nil {
		return nil, nil, err
	}

	active = make([]*queueData, 0, len(batch))
	skipped = make([]*queueData, 0)
	for _, item := range batch {
		if item == nil || item.logEntry == nil {
			continue
		}

		workflowID, userID, ok := normalizeWorkflowLogIDs(item.logEntry.WorkflowID, item.logEntry.UserID)
		if !ok {
			continue
		}
		item.logEntry.WorkflowID = workflowID
		item.logEntry.UserID = userID
		key := workflowLogPairKey(workflowID, userID)

		if _, ok := existing[key]; !ok {
			skipped = append(skipped, item)
			continue
		}

		active = append(active, item)
	}

	if skippedCount := len(batch) - len(active); skippedCount > 0 {
		loggerpkg.FromContext(ctx).Info(
			"skipped job logs for deleted workflows",
			zap.Int("skipped", skippedCount),
			zap.Int("retained", len(active)),
		)
	}

	return active, skipped, nil
}

func (r *Repository) fetchActiveWorkflowLogPairs(ctx context.Context, pairs []workflowLogPair) (map[string]struct{}, error) {
	placeholders := make([]string, 0, len(pairs))
	args := make([]any, 0, len(pairs)*2)
	for i, pair := range pairs {
		workflowArg := i*2 + 1
		userArg := workflowArg + 1
		placeholders = append(placeholders, fmt.Sprintf("($%d::uuid, $%d::uuid)", workflowArg, userArg))
		args = append(args, pair.workflowID, pair.userID)
	}

	query := fmt.Sprintf(`
        SELECT id::text, user_id::text
        FROM %s
        WHERE (id, user_id) IN (%s);
    `, postgres.TableWorkflows, strings.Join(placeholders, ","))

	rows, err := r.pg.Query(ctx, query, args...)
	if err != nil {
		if r.pg.IsInvalidTextRepresentation(err) {
			return nil, status.Errorf(codes.InvalidArgument, "invalid workflow or user id in job log batch: %v", err)
		}

		return nil, status.Errorf(codes.Internal, "failed to fetch active workflows for job logs: %v", err)
	}
	defer rows.Close()

	existing := make(map[string]struct{}, len(pairs))
	for rows.Next() {
		var workflowID string
		var userID string
		if err := rows.Scan(&workflowID, &userID); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to scan active workflow for job logs: %v", err)
		}
		existing[workflowLogPairKey(workflowID, userID)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to read active workflows for job logs: %v", err)
	}

	return existing, nil
}

func uniqueWorkflowLogPairs(batch []*queueData) []workflowLogPair {
	seen := make(map[string]workflowLogPair, len(batch))
	for _, item := range batch {
		if item == nil || item.logEntry == nil {
			continue
		}

		if item.logEntry.WorkflowID == "" || item.logEntry.UserID == "" {
			continue
		}

		key, ok := workflowLogPairKeyFromLog(item.logEntry)
		if !ok {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		workflowID, userID, _ := normalizeWorkflowLogIDs(item.logEntry.WorkflowID, item.logEntry.UserID)
		seen[key] = workflowLogPair{
			workflowID: workflowID,
			userID:     userID,
		}
	}

	pairs := make([]workflowLogPair, 0, len(seen))
	for _, pair := range seen {
		pairs = append(pairs, pair)
	}
	sort.Slice(pairs, func(i, j int) bool {
		return workflowLogPairKey(pairs[i].workflowID, pairs[i].userID) < workflowLogPairKey(pairs[j].workflowID, pairs[j].userID)
	})

	return pairs
}

func workflowLogPairKey(workflowID, userID string) string {
	return strings.ToLower(workflowID) + "\x00" + strings.ToLower(userID)
}

func workflowLogPairKeyFromLog(log *jobsmodel.JobLogEvent) (string, bool) {
	if log == nil {
		return "", false
	}

	workflowID, userID, ok := normalizeWorkflowLogIDs(log.WorkflowID, log.UserID)
	if !ok {
		return "", false
	}

	return workflowLogPairKey(workflowID, userID), true
}

func normalizeWorkflowLogIDs(workflowID, userID string) (normalizedWorkflowID, normalizedUserID string, ok bool) {
	parsedWorkflowID, err := uuid.Parse(workflowID)
	if err != nil {
		return "", "", false
	}

	parsedUserID, err := uuid.Parse(userID)
	if err != nil {
		return "", "", false
	}

	return parsedWorkflowID.String(), parsedUserID.String(), true
}

func dedupeLogsInBatch(logs []*jobsmodel.JobLogEvent) []*jobsmodel.JobLogEvent {
	if len(logs) == 0 {
		return logs
	}

	seen := make(map[string]struct{}, len(logs))
	filtered := make([]*jobsmodel.JobLogEvent, 0, len(logs))
	for _, log := range logs {
		if log.EventKey == "" {
			filtered = append(filtered, log)
			continue
		}
		if _, ok := seen[log.EventKey]; ok {
			continue
		}
		seen[log.EventKey] = struct{}{}
		filtered = append(filtered, log)
	}

	return filtered
}

func retainedLogsFromBatch(batch []*queueData) []*jobsmodel.JobLogEvent {
	logs := make([]*jobsmodel.JobLogEvent, 0, len(batch))
	for _, item := range batch {
		if item == nil || item.logEntry == nil {
			continue
		}

		log := item.logEntry
		if !log.Retention {
			continue
		}
		if log.EventKey == "" {
			log.EventKey = idempotency.LogEventKey(log.JobID, log.Stream, log.SequenceNum)
		}

		logs = append(logs, log)
	}

	return dedupeLogsInBatch(logs)
}

func uniqueLogEventIDs(logs []*jobsmodel.JobLogEvent) []string {
	seen := make(map[string]struct{}, len(logs))
	eventIDs := make([]string, 0, len(logs))
	for _, log := range logs {
		if log == nil || log.EventKey == "" {
			continue
		}
		if _, ok := seen[log.EventKey]; ok {
			continue
		}
		seen[log.EventKey] = struct{}{}
		eventIDs = append(eventIDs, log.EventKey)
	}

	return eventIDs
}

func placeholders(count int) string {
	items := make([]string, 0, count)
	for range count {
		items = append(items, "?")
	}

	return strings.Join(items, ",")
}

func toAnySlice[T any](items []T) []any {
	args := make([]any, 0, len(items))
	for _, item := range items {
		args = append(args, item)
	}

	return args
}

func (r *Repository) cleanupRetainedLogs(ctx context.Context, batch []*queueData) error {
	logs := retainedLogsFromBatch(batch)
	eventIDs := uniqueLogEventIDs(logs)
	if len(eventIDs) == 0 {
		return nil
	}

	if err := r.deleteLogsFromClickHouseByEventID(ctx, eventIDs); err != nil {
		return err
	}

	return r.deleteLogsFromMeiliSearchByID(ctx, eventIDs)
}

func (r *Repository) deleteLogsFromClickHouseByEventID(parentCtx context.Context, eventIDs []string) error {
	if len(eventIDs) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(parentCtx, jobLogsExternalOperationTimeout)
	defer cancel()

	query := fmt.Sprintf(`
        ALTER TABLE %s DELETE
        WHERE event_id IN (%s)
        SETTINGS mutations_sync = 2
    `, clickhouse.TableJobLogs, placeholders(len(eventIDs)))

	if err := r.ch.Exec(ctx, query, toAnySlice(eventIDs)...); err != nil {
		return jobLogsStatusError("failed to delete skipped ClickHouse logs", err)
	}

	return nil
}

func (r *Repository) deleteLogsFromMeiliSearchByID(parentCtx context.Context, eventIDs []string) error {
	if len(eventIDs) == 0 {
		return nil
	}
	if r.ms == nil {
		return status.Error(codes.Internal, "failed to delete skipped Meilisearch logs: client is not configured")
	}

	ctx, cancel := context.WithTimeout(parentCtx, jobLogsExternalOperationTimeout)
	defer cancel()

	indexJobLogs := meilisearchpkg.Indexes[meilisearchpkg.IndexJobLogs]
	if indexJobLogs == nil {
		return status.Error(codes.Internal, "failed to delete skipped Meilisearch logs: job logs index is not configured")
	}
	taskInfo, err := r.ms.Index(indexJobLogs.Name).DeleteDocumentsWithContext(ctx, eventIDs)
	if err != nil {
		return jobLogsStatusError("failed to delete skipped Meilisearch logs", err)
	}
	if taskInfo == nil {
		return status.Error(codes.Internal, "failed to delete skipped Meilisearch logs: empty task response")
	}

	return r.waitForMeiliSearchTask(ctx, taskInfo.TaskUID, "delete-documents")
}

// insertLogsBatchToClickhouse inserts a batch of logs into the clickhouse database.
func (r *Repository) insertLogsBatchToClickhouse(ctx context.Context, logs []*jobsmodel.JobLogEvent) error {
	if len(logs) == 0 {
		return nil
	}

	// Prepare batch statement
	stmt := fmt.Sprintf(`
        INSERT INTO %s 
        (event_id, job_id, workflow_id, user_id, timestamp, message, sequence_num, stream)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?);
    `, clickhouse.TableJobLogs)

	if err := r.ch.BatchInsert(
		ctx,
		stmt,
		func(batch driver.Batch) error {
			for _, log := range logs {
				err := batch.Append(
					log.EventKey,
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
	if r.ms == nil {
		return status.Error(codes.Internal, "failed to add documents: Meilisearch client is not configured")
	}

	indexJobLogs := meilisearchpkg.Indexes[meilisearchpkg.IndexJobLogs]
	if indexJobLogs == nil {
		return status.Error(codes.Internal, "failed to add documents: job logs index is not configured")
	}

	taskInfo, err := r.ms.Index(indexJobLogs.Name).AddDocumentsWithContext(ctx, documents, &indexJobLogs.PrimaryKey)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to add documents: %v", err)
	}
	if taskInfo == nil {
		return status.Error(codes.Internal, "failed to add documents: empty task response")
	}

	return r.waitForMeiliSearchTask(ctx, taskInfo.TaskUID, "add-documents")
}

func (r *Repository) waitForMeiliSearchTask(parentCtx context.Context, taskUID int64, operation string) error {
	ctx, cancel := context.WithTimeout(parentCtx, jobLogsExternalOperationTimeout)
	defer cancel()

	task, err := r.ms.WaitForTaskWithContext(ctx, taskUID, jobLogsMeiliSearchPollPeriod)
	if err != nil {
		return jobLogsStatusError(fmt.Sprintf("failed waiting for Meilisearch %s task %d", operation, taskUID), err)
	}

	if task.Status != meilisearch.TaskStatusSucceeded {
		return status.Errorf(
			codes.Internal,
			"Meilisearch %s task %d finished with status %s: %v",
			operation,
			taskUID,
			task.Status,
			task.Error,
		)
	}

	return nil
}

func jobLogsStatusError(message string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, err.Error())
	}
	if errors.Is(err, context.Canceled) {
		return status.Error(codes.Canceled, err.Error())
	}

	return status.Errorf(codes.Internal, "%s: %v", message, err)
}
