package jobs

// CleanupAttempt is durable execution authority for one cleanup generation.
type CleanupAttempt struct {
	State      string
	Generation int64
	Attempts   int32
	Token      string
}
