//nolint:testpackage // Tests unexported log analytics helpers directly.
package joblogs

import (
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
)

func TestCountPartitionLogs(t *testing.T) {
	tests := []struct {
		name             string
		lastOffset       int64
		records          []*queueData
		wantMaxOffset    int64
		wantWorkflowLogs map[string]workflowLogCount
	}{
		{
			name:       "counts logs exactly per workflow",
			lastOffset: -1,
			records: []*queueData{
				logRecord(0, "workflow-1", "user-1"),
				logRecord(1, "workflow-1", "user-1"),
				logRecord(2, "workflow-2", "user-2"),
			},
			wantMaxOffset: 2,
			wantWorkflowLogs: map[string]workflowLogCount{
				"workflow-1": {userID: "user-1", count: 2},
				"workflow-2": {userID: "user-2", count: 1},
			},
		},
		{
			name:       "ignores records already covered by watermark",
			lastOffset: 5,
			records: []*queueData{
				logRecord(4, "workflow-1", "user-1"),
				logRecord(5, "workflow-1", "user-1"),
				logRecord(6, "workflow-1", "user-1"),
			},
			wantMaxOffset: 6,
			wantWorkflowLogs: map[string]workflowLogCount{
				"workflow-1": {userID: "user-1", count: 1},
			},
		},
		{
			name:       "ignores malformed records but still advances offset",
			lastOffset: 1,
			records: []*queueData{
				logRecord(2, "", "user-1"),
				logRecord(3, "workflow-1", ""),
				nil,
				logRecord(4, "", "user-2"),
				{logEntry: &jobsmodel.JobLogEvent{WorkflowID: "workflow-1", UserID: "user-1"}},
			},
			wantMaxOffset:    4,
			wantWorkflowLogs: map[string]workflowLogCount{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countPartitionLogs(tt.records, tt.lastOffset)

			if got.maxOffset != tt.wantMaxOffset {
				t.Fatalf("expected max offset %d, got %d", tt.wantMaxOffset, got.maxOffset)
			}
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

func logRecord(offset int64, workflowID, userID string) *queueData {
	return &queueData{
		record: &kgo.Record{Offset: offset},
		logEntry: &jobsmodel.JobLogEvent{
			WorkflowID: workflowID,
			UserID:     userID,
		},
	}
}
