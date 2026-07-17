package jobs_test

import (
	"database/sql"
	"testing"
	"time"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
)

func TestGetJobResponseTerminalReason(t *testing.T) {
	now := time.Now()
	res := (&jobsmodel.GetJobResponse{
		ID: "job", WorkflowID: "workflow", JobStatus: jobsmodel.JobStatusFailed.ToString(),
		ScheduledAt: now, CreatedAt: now, UpdatedAt: now,
		TerminalReasonCode: sql.NullString{String: terminalreason.NonZeroExit.String(), Valid: true},
	}).ToProto()
	if res.GetStatusReasonCode() != terminalreason.NonZeroExit.String() || res.GetStatusReasonMessage() != "Process exited with non zero exit code" {
		t.Fatalf("terminal reason = %q, %q", res.GetStatusReasonCode(), res.GetStatusReasonMessage())
	}
}

func TestGetJobResponseHistoricalFallbacks(t *testing.T) {
	now := time.Now()
	failed := (&jobsmodel.GetJobResponse{
		ID: "failed", JobStatus: jobsmodel.JobStatusFailed.ToString(), ScheduledAt: now, CreatedAt: now, UpdatedAt: now,
		FailureKind:      sql.NullString{String: jobsmodel.FailureKindUser.ToString(), Valid: true},
		LastErrorMessage: sql.NullString{String: "ambiguous", Valid: true},
	}).ToProto()
	if failed.GetStatusReasonCode() != terminalreason.FailureReasonUnavailable.String() {
		t.Fatalf("failed fallback = %q", failed.GetStatusReasonCode())
	}

	canceled := (&jobsmodel.GetJobResponse{ID: "canceled", JobStatus: jobsmodel.JobStatusCanceled.ToString(), ScheduledAt: now, CreatedAt: now, UpdatedAt: now}).ToProto()
	if canceled.GetStatusReasonCode() != terminalreason.CancellationReasonUnavailable.String() {
		t.Fatalf("canceled fallback = %q", canceled.GetStatusReasonCode())
	}

	completed := (&jobsmodel.GetJobResponse{ID: "completed", JobStatus: jobsmodel.JobStatusCompleted.ToString(), ScheduledAt: now, CreatedAt: now, UpdatedAt: now}).ToProto()
	if completed.GetStatusReasonCode() != "" || completed.GetStatusReasonMessage() != "" {
		t.Fatalf("completed reason should be omitted: %+v", completed)
	}
}
