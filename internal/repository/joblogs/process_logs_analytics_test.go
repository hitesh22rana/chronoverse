//nolint:testpackage // Tests unexported log analytics helpers directly.
package joblogs

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
)

func TestCollectLogAnalyticsEvents(t *testing.T) {
	generatedKey := "log:job-2:stderr:7"

	events := collectLogAnalyticsEvents([]*queueData{
		logRecord("log-2", "workflow-2", "user-2"),
		logRecord("log-1", "workflow-1", "user-1"),
		logRecord("log-1", "workflow-1", "user-1"),
		{
			logEntry: &jobsmodel.JobLogEvent{
				JobID:       "job-2",
				WorkflowID:  "workflow-2",
				UserID:      "user-2",
				Stream:      "stderr",
				SequenceNum: 7,
			},
		},
		logRecord("", "workflow-3", "user-3"),
		logRecord("log-4", "", "user-4"),
		logRecord("log-5", "workflow-5", ""),
		nil,
	})

	if len(events) != 3 {
		t.Fatalf("expected 3 analytics events, got %d", len(events))
	}
	if events[0].eventKey != "log-1" {
		t.Fatalf("expected log-1 as first event, got %q", events[0].eventKey)
	}
	if events[1].eventKey != "log-2" {
		t.Fatalf("expected log-2 as second event, got %q", events[1].eventKey)
	}
	if events[2].eventKey != generatedKey {
		t.Fatalf("expected generated key %q third after sort, got %q", generatedKey, events[2].eventKey)
	}
}

func TestCountLogAnalyticsEvents(t *testing.T) {
	tests := []struct {
		name             string
		events           []logAnalyticsEvent
		insertedEvents   map[string]struct{}
		wantWorkflowLogs map[string]workflowLogCount
	}{
		{
			name: "counts newly inserted events exactly per workflow",
			events: []logAnalyticsEvent{
				{eventKey: "log-1", workflowID: "workflow-1", userID: "user-1"},
				{eventKey: "log-2", workflowID: "workflow-1", userID: "user-1"},
				{eventKey: "log-3", workflowID: "workflow-2", userID: "user-2"},
			},
			insertedEvents: map[string]struct{}{
				"log-1": {},
				"log-2": {},
				"log-3": {},
			},
			wantWorkflowLogs: map[string]workflowLogCount{
				"workflow-1": {userID: "user-1", count: 2},
				"workflow-2": {userID: "user-2", count: 1},
			},
		},
		{
			name: "ignores events that were already processed",
			events: []logAnalyticsEvent{
				{eventKey: "log-1", workflowID: "workflow-1", userID: "user-1"},
				{eventKey: "log-2", workflowID: "workflow-1", userID: "user-1"},
				{eventKey: "log-3", workflowID: "workflow-1", userID: "user-1"},
			},
			insertedEvents: map[string]struct{}{
				"log-2": {},
			},
			wantWorkflowLogs: map[string]workflowLogCount{
				"workflow-1": {userID: "user-1", count: 1},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countLogAnalyticsEvents(tt.events, tt.insertedEvents)

			if len(got.workflowCounts) != len(tt.wantWorkflowLogs) {
				t.Fatalf("expected %d workflow counts, got %d", len(tt.wantWorkflowLogs), len(got.workflowCounts))
			}
			for workflowID, want := range tt.wantWorkflowLogs {
				gotCount, ok := got.workflowCounts[workflowID]
				if !ok {
					t.Fatalf("missing workflow count for %s", workflowID)
				}
				if gotCount != want {
					t.Fatalf("expected count %+v for %s, got %+v", want, workflowID, gotCount)
				}
			}
		})
	}
}

func TestLogAnalyticsEventKey(t *testing.T) {
	if got := logAnalyticsEventKey(nil); got != "" {
		t.Fatalf("nil log key = %q, want empty", got)
	}
	if got := logAnalyticsEventKey(&jobsmodel.JobLogEvent{JobID: "job-1"}); got != "" {
		t.Fatalf("missing stream key = %q, want empty", got)
	}
	event := &jobsmodel.JobLogEvent{JobID: "job-1", Stream: "stdout", SequenceNum: 3}
	if got := logAnalyticsEventKey(event); got != "log:job-1:stdout:3" || event.EventKey != got {
		t.Fatalf("generated key = %q (stored %q)", got, event.EventKey)
	}
}

// batchAnalyticsTx scripts the transaction calls made by the analytics helpers.
type batchAnalyticsTx struct {
	pgx.Tx
	rows     pgx.Rows
	queryErr error
	execErr  error
}

func (tx *batchAnalyticsTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	if tx.queryErr != nil {
		return nil, tx.queryErr
	}
	return tx.rows, nil
}

func (tx *batchAnalyticsTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, tx.execErr
}

// batchAnalyticsRows scripts inserted event keys or a scan/read failure.
type batchAnalyticsRows struct {
	pgx.Rows
	keys    []string
	next    int
	scanErr error
	err     error
}

func (rows *batchAnalyticsRows) Next() bool {
	if rows.next >= len(rows.keys) {
		return false
	}
	rows.next++
	return true
}

func (rows *batchAnalyticsRows) Scan(dest ...any) error {
	if rows.scanErr != nil {
		return rows.scanErr
	}
	value, ok := dest[0].(*string)
	if !ok {
		return errors.New("scan destination is not *string")
	}
	*value = rows.keys[rows.next-1]
	return nil
}

func (rows *batchAnalyticsRows) Err() error { return rows.err }

func (rows *batchAnalyticsRows) Close() {}

func TestProcessLogsAnalyticsTxFailures(t *testing.T) {
	batch := []*queueData{{logEntry: &jobsmodel.JobLogEvent{
		EventKey: "log-1", WorkflowID: "workflow-1", UserID: "user-1",
	}}}
	tests := []struct {
		name  string
		tx    *batchAnalyticsTx
		batch []*queueData
		want  string
	}{
		{name: "empty batch", tx: nil, batch: nil},
		{
			name:  "insert query failure",
			tx:    &batchAnalyticsTx{queryErr: errors.New("query failed")},
			batch: batch,
			want:  "failed to insert processed log analytics events",
		},
		{
			name: "scan failure",
			tx: &batchAnalyticsTx{rows: &batchAnalyticsRows{
				keys: []string{"log-1"}, scanErr: errors.New("scan failed"),
			}},
			batch: batch,
			want:  "failed to scan processed log analytics event",
		},
		{
			name:  "rows failure",
			tx:    &batchAnalyticsTx{rows: &batchAnalyticsRows{err: errors.New("rows failed")}},
			batch: batch,
			want:  "failed to read processed log analytics events",
		},
		{
			name: "update failure",
			tx: &batchAnalyticsTx{
				rows: &batchAnalyticsRows{keys: []string{"log-1"}}, execErr: errors.New("update failed"),
			},
			batch: batch,
			want:  "failed to update logs analytics",
		},
		{name: "success", tx: &batchAnalyticsTx{rows: &batchAnalyticsRows{keys: []string{"log-1"}}}, batch: batch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &Repository{tp: otel.Tracer("joblogs-test")}
			err := repo.processLogsAnalyticsTx(t.Context(), tt.tx, tt.batch)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("process analytics = %v, want nil", err)
				}
				return
			}
			if status.Code(err) != codes.Internal || !strings.Contains(status.Convert(err).Message(), tt.want) {
				t.Fatalf("process analytics = %v, want Internal containing %q", err, tt.want)
			}
		})
	}
}

func TestUpdatePartitionLogCountsSkipsZeroCounts(t *testing.T) {
	err := updatePartitionLogCounts(t.Context(), nil, map[string]workflowLogCount{
		"workflow-1": {userID: "user-1", count: 0},
	})
	if err != nil {
		t.Fatalf("zero counts = %v, want nil", err)
	}
}

func logRecord(eventKey, workflowID, userID string) *queueData {
	return &queueData{
		logEntry: &jobsmodel.JobLogEvent{
			EventKey:   eventKey,
			WorkflowID: workflowID,
			UserID:     userID,
		},
	}
}
