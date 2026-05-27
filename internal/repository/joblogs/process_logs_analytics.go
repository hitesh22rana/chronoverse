package joblogs

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

const logAnalyticsConsumer = "joblogs-processor:analytics"

type workflowLogCount struct {
	userID string
	count  uint64
}

type logAnalyticsEvent struct {
	eventKey   string
	workflowID string
	userID     string
}

type logAnalyticsCounts struct {
	workflowCounts map[string]workflowLogCount
}

// processLogsAnalytics updates durable log counters using log event keys as replay-safe identity.
func (r *Repository) processLogsAnalytics(ctx context.Context, batch []*queueData) error {
	ctx, span := r.tp.Start(ctx, "joblogs.Run.processLogsAnalytics")
	defer span.End()

	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to start log analytics transaction: %v", err)
	}
	//nolint:errcheck // Rollback is a no-op after commit.
	defer tx.Rollback(ctx)

	if processErr := r.processLogsAnalyticsTx(ctx, tx, batch); processErr != nil {
		return processErr
	}

	if commitErr := tx.Commit(ctx); commitErr != nil {
		return status.Errorf(codes.Internal, "failed to commit log analytics transaction: %v", commitErr)
	}

	return nil
}

func (r *Repository) processLogsAnalyticsTx(ctx context.Context, tx pgx.Tx, batch []*queueData) error {
	ctx, span := r.tp.Start(ctx, "joblogs.Run.processLogsAnalyticsTx")
	defer span.End()

	events := collectLogAnalyticsEvents(batch)
	if len(events) == 0 {
		return nil
	}

	insertedEvents, err := insertProcessedLogAnalyticsEvents(ctx, tx, events)
	if err != nil {
		return err
	}

	counts := countLogAnalyticsEvents(events, insertedEvents)
	return updatePartitionLogCounts(ctx, tx, counts.workflowCounts)
}

func insertProcessedLogAnalyticsEvents(
	ctx context.Context,
	tx pgx.Tx,
	events []logAnalyticsEvent,
) (map[string]struct{}, error) {
	placeholders := make([]string, 0, len(events))
	args := make([]any, 0, len(events)+1)
	args = append(args, logAnalyticsConsumer)
	for i, event := range events {
		placeholders = append(placeholders, fmt.Sprintf("($1, $%d)", i+2))
		args = append(args, event.eventKey)
	}

	rows, err := tx.Query(ctx, fmt.Sprintf(`
            INSERT INTO %s (consumer, event_key)
            VALUES %s
            ON CONFLICT DO NOTHING
            RETURNING event_key;
        `, postgres.TableProcessedEvents, strings.Join(placeholders, ",")), args...)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to insert processed log analytics events: %v", err)
	}
	defer rows.Close()

	inserted := make(map[string]struct{}, len(events))
	for rows.Next() {
		var eventKey string
		if err := rows.Scan(&eventKey); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to scan processed log analytics event: %v", err)
		}
		inserted[eventKey] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to read processed log analytics events: %v", err)
	}

	return inserted, nil
}

func updatePartitionLogCounts(
	ctx context.Context,
	tx pgx.Tx,
	workflowCounts map[string]workflowLogCount,
) error {
	for workflowID, entry := range workflowCounts {
		if entry.count == 0 {
			continue
		}

		if _, err := tx.Exec(ctx, fmt.Sprintf(`
            INSERT INTO %s
                (user_id, workflow_id, logs_count)
            VALUES ($1, $2, $3)
            ON CONFLICT (user_id, workflow_id)
            DO UPDATE SET
                logs_count = %s.logs_count + EXCLUDED.logs_count;
        `, postgres.TableAnalytics, postgres.TableAnalytics), entry.userID, workflowID, entry.count); err != nil {
			return status.Errorf(codes.Internal, "failed to update logs analytics: %v", err)
		}
	}

	return nil
}

func collectLogAnalyticsEvents(batch []*queueData) []logAnalyticsEvent {
	seen := make(map[string]struct{}, len(batch))
	events := make([]logAnalyticsEvent, 0, len(batch))

	for _, item := range batch {
		if item == nil || item.logEntry == nil {
			continue
		}

		log := item.logEntry
		eventKey := logAnalyticsEventKey(log)
		if eventKey == "" {
			continue
		}
		if _, ok := seen[eventKey]; ok {
			continue
		}
		if log.WorkflowID == "" || log.UserID == "" {
			continue
		}

		seen[eventKey] = struct{}{}
		events = append(events, logAnalyticsEvent{
			eventKey:   eventKey,
			workflowID: log.WorkflowID,
			userID:     log.UserID,
		})
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].eventKey < events[j].eventKey
	})

	return events
}

func logAnalyticsEventKey(log *jobsmodel.JobLogEvent) string {
	if log == nil {
		return ""
	}
	if log.EventKey != "" {
		return log.EventKey
	}
	if log.JobID == "" || log.Stream == "" {
		return ""
	}

	log.EventKey = idempotency.LogEventKey(log.JobID, log.Stream, log.SequenceNum)
	return log.EventKey
}

func countLogAnalyticsEvents(events []logAnalyticsEvent, insertedEvents map[string]struct{}) *logAnalyticsCounts {
	workflowCounts := make(map[string]workflowLogCount)
	for _, event := range events {
		if _, ok := insertedEvents[event.eventKey]; !ok {
			continue
		}

		entry := workflowCounts[event.workflowID]
		if entry.userID == "" {
			entry.userID = event.userID
		}
		entry.count++
		workflowCounts[event.workflowID] = entry
	}

	return &logAnalyticsCounts{workflowCounts: workflowCounts}
}
