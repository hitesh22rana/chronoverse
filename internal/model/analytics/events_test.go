package analytics_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	analyticsmodel "github.com/hitesh22rana/chronoverse/internal/model/analytics"
)

func TestAnalyticEventWireFormat(t *testing.T) {
	for _, tc := range []struct {
		name      string
		eventType analyticsmodel.EventType
		data      any
		payload   string
	}{
		{"WORKFLOWS", analyticsmodel.EventTypeWorkflows, analyticsmodel.EventTypeWorkflowsData{Kind: "HEARTBEAT"}, `{"kind":"HEARTBEAT"}`},
		{"JOBS", analyticsmodel.EventTypeJobs, analyticsmodel.EventTypeJobsData{JobExecutionDuration: 9007199254740993}, `{"job_execution_duration":9007199254740993}`},
		{"LOGS", analyticsmodel.EventTypeLogs, analyticsmodel.EventTypeLogsData{Count: 7}, `{"count":7}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.name, tc.eventType.ToString())
			encoded, err := analyticsmodel.NewAnalyticEventBytesWithKey("event-key", "user-id", "workflow-id", tc.eventType, tc.data)
			require.NoError(t, err)
			var envelope map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(encoded, &envelope))
			require.Equal(t, map[string]json.RawMessage{
				"EventKey":   json.RawMessage(`"event-key"`),
				"UserID":     json.RawMessage(`"user-id"`),
				"WorkflowID": json.RawMessage(`"workflow-id"`),
				"EventType":  json.RawMessage(`"` + tc.name + `"`),
				"Data":       json.RawMessage(tc.payload),
			}, envelope)
		})
	}
}

func TestAnalyticEventRejectsUnserializablePayload(t *testing.T) {
	encoded, err := analyticsmodel.NewAnalyticEventBytesWithKey("event-key", "user-id", "workflow-id", analyticsmodel.EventTypeLogs, make(chan int))
	var unsupported *json.UnsupportedTypeError
	require.ErrorAs(t, err, &unsupported)
	require.Nil(t, encoded)
}
