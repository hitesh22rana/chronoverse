//nolint:testpackage // Tests unexported log analytics helpers directly.
package joblogs

import (
	"testing"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
)

func TestCollectLogAnalyticsEvents(t *testing.T) {
	generatedKey := idempotency.LogEventKey("job-2", "stderr", 7)

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

func logRecord(eventKey, workflowID, userID string) *queueData {
	return &queueData{
		logEntry: &jobsmodel.JobLogEvent{
			EventKey:   eventKey,
			WorkflowID: workflowID,
			UserID:     userID,
		},
	}
}
