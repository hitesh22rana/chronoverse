package jobs

import (
	"errors"
	"strings"
)

// CleanupAttempt is durable execution authority for one cleanup generation.
type CleanupAttempt struct {
	State      string
	Generation int64
	Attempts   int32
	Token      string
}

// CleanupRedrive is authenticated audit metadata for an administrative retry.
// Begin and failure reports leave it empty.
type CleanupRedrive struct {
	Actor  string
	Reason string
}

// Validate rejects audit records that cannot identify a bounded actor and reason.
func (r CleanupRedrive) Validate() error {
	if strings.TrimSpace(r.Actor) == "" || len(r.Actor) > 256 {
		return errors.New("redrive actor is required (maximum 256 bytes)")
	}
	if strings.TrimSpace(r.Reason) == "" || len(r.Reason) > 2048 {
		return errors.New("redrive repair reason is required (maximum 2048 bytes)")
	}
	return nil
}

// Cleanup operation values are shared by service validation and durable transitions.
const (
	CleanupOperationBegin   = "BEGIN"
	CleanupOperationFail    = "FAIL"
	CleanupOperationRedrive = "REDRIVE"
)

// Cleanup states include non-mutating stale-generation responses.
const (
	CleanupStateReady       = "READY"
	CleanupStateProcessing  = "PROCESSING"
	CleanupStateRetry       = "RETRY"
	CleanupStateQuarantined = "QUARANTINED"
	CleanupStateCompleted   = "COMPLETED"
	CleanupStateStale       = "STALE"
)
