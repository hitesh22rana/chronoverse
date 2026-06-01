package jobs

import "time"

// JobLogEvent represents a log event for a job.
type JobLogEvent struct {
	EventKey      string
	JobID         string
	WorkflowID    string
	UserID        string
	Message       string
	TimeStamp     time.Time
	SequenceNum   uint32
	Stream        string // "stdout" or "stderr"
	Retention     bool   // whether the log is retained or ephemeral
	LivePublished bool   // whether live publish was already attempted before durable processing
}
