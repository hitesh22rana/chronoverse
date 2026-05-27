//nolint:testpackage // Tests unexported batch-dedupe helper directly.
package joblogs

import (
	"testing"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
)

func TestDedupeLogsInBatch(t *testing.T) {
	logs := []*jobsmodel.JobLogEvent{
		{EventKey: "log-1", Message: "first"},
		{EventKey: "log-1", Message: "duplicate"},
		{EventKey: "log-2", Message: "second"},
		{Message: "without-key"},
	}

	deduped := dedupeLogsInBatch(logs)
	if len(deduped) != 3 {
		t.Fatalf("expected 3 logs after dedupe, got %d", len(deduped))
	}
	if deduped[0].Message != "first" {
		t.Fatalf("expected first copy to be retained, got %q", deduped[0].Message)
	}
	if deduped[1].EventKey != "log-2" {
		t.Fatalf("expected second unique event key, got %q", deduped[1].EventKey)
	}
	if deduped[2].Message != "without-key" {
		t.Fatalf("expected log without event key to be retained, got %q", deduped[2].Message)
	}
}

func TestUniqueWorkflowLogPairs(t *testing.T) {
	records := []*queueData{
		logData("00000000-0000-0000-0000-000000000001", "10000000-0000-0000-0000-000000000001"),
		logData("00000000-0000-0000-0000-000000000001", "10000000-0000-0000-0000-000000000001"),
		logData("00000000-0000-0000-0000-000000000002", "10000000-0000-0000-0000-000000000002"),
		logData("", "user-3"),
		logData("workflow-4", ""),
		nil,
	}

	pairs := uniqueWorkflowLogPairs(records)
	if len(pairs) != 2 {
		t.Fatalf("expected 2 unique workflow/user pairs, got %d", len(pairs))
	}
	if pairs[0].workflowID != "00000000-0000-0000-0000-000000000001" || pairs[0].userID != "10000000-0000-0000-0000-000000000001" {
		t.Fatalf("unexpected first pair: %+v", pairs[0])
	}
	if pairs[1].workflowID != "00000000-0000-0000-0000-000000000002" || pairs[1].userID != "10000000-0000-0000-0000-000000000002" {
		t.Fatalf("unexpected second pair: %+v", pairs[1])
	}
}

func TestNormalizeWorkflowLogIDs(t *testing.T) {
	workflowID, userID, ok := normalizeWorkflowLogIDs(
		"AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA",
		"BBBBBBBB-BBBB-BBBB-BBBB-BBBBBBBBBBBB",
	)
	if !ok {
		t.Fatal("expected uppercase UUIDs to normalize")
	}
	if workflowID != "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa" {
		t.Fatalf("unexpected normalized workflow id: %q", workflowID)
	}
	if userID != "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb" {
		t.Fatalf("unexpected normalized user id: %q", userID)
	}

	if _, _, ok := normalizeWorkflowLogIDs("workflow-id", "user-id"); ok {
		t.Fatal("expected invalid UUIDs to be rejected")
	}
}

func TestRetainedLogsFromBatch(t *testing.T) {
	records := []*queueData{
		{
			logEntry: &jobsmodel.JobLogEvent{
				JobID:       "job-1",
				Stream:      "stdout",
				SequenceNum: 1,
				Retention:   true,
			},
		},
		{
			logEntry: &jobsmodel.JobLogEvent{
				EventKey:  "log-2",
				Retention: true,
			},
		},
		{
			logEntry: &jobsmodel.JobLogEvent{
				EventKey:  "log-2",
				Retention: true,
			},
		},
		{
			logEntry: &jobsmodel.JobLogEvent{
				EventKey:  "log-3",
				Retention: false,
			},
		},
	}

	logs := retainedLogsFromBatch(records)
	if len(logs) != 2 {
		t.Fatalf("expected 2 retained deduped logs, got %d", len(logs))
	}

	expectedGeneratedKey := idempotency.LogEventKey("job-1", "stdout", 1)
	if logs[0].EventKey != expectedGeneratedKey {
		t.Fatalf("expected generated event key %q, got %q", expectedGeneratedKey, logs[0].EventKey)
	}
	if logs[1].EventKey != "log-2" {
		t.Fatalf("expected second retained key log-2, got %q", logs[1].EventKey)
	}
}

func logData(workflowID, userID string) *queueData {
	return &queueData{
		logEntry: &jobsmodel.JobLogEvent{
			WorkflowID: workflowID,
			UserID:     userID,
		},
	}
}
