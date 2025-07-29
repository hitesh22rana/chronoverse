package analytics

import "encoding/json"

// EventType represents the type of analytic event.
// It is used to categorize events for analytics processing.
type EventType string

// Types of analytic events.
const (
	EventTypeWorkflows EventType = "WORKFLOWS"
	EventTypeJobs      EventType = "JOBS"
	EventTypeLogs      EventType = "LOGS"
)

// ToString converts the EventType to its string representation.
func (t EventType) ToString() string {
	return string(t)
}

// EventTypeWorkflowsData represents the data structure for workflows analytics events.
type EventTypeWorkflowsData struct {
	Kind string `json:"kind"` // Kind of workflow (e.g., "HEARTBEAT", "CONTAINER", etc.)
}

// EventTypeJobsData represents the data structure for jobs analytics events.
type EventTypeJobsData struct {
	JobExecutionDurationMs uint64 `json:"job_execution_duration_ms"` // Job execution duration in milliseconds
}

// EventTypeLogsData represents the data structure for logs analytics events.
type EventTypeLogsData struct {
	Count uint64 `json:"count"`
}

// AnalyticEvent represents a generic analytic event, which is used to track various types of events in the system.
// The EventType field indicates the type of event, and Data holds the event-specific data.
type AnalyticEvent struct {
	UserID     string
	WorkflowID string
	EventType  EventType
	Data       json.RawMessage
}

// NewAnalyticEventBytes creates a new AnalyticEvent and marshals it to bytes.
func NewAnalyticEventBytes(userID, workflowID string, eventType EventType, data any) ([]byte, error) {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	return json.Marshal(&AnalyticEvent{
		UserID:     userID,
		WorkflowID: workflowID,
		EventType:  eventType,
		Data:       dataBytes,
	})
}
