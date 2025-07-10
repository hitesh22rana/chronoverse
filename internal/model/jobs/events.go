package jobs

import "time"

// JobLogEvent represents a log event for a job.
type JobLogEvent struct {
	JobID       string
	WorkflowID  string
	UserID      string
	Message     string
	TimeStamp   time.Time
	SequenceNum uint32
	Stream      string // "stdout" or "stderr"
}
