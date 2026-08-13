package idempotency_test

import (
	"testing"

	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
)

func TestNotificationKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "job notification is job scoped",
			got:  idempotency.JobNotificationEventKey("job-1", "Job Execution Failed"),
			want: "notification:JOB:job-1:Job Execution Failed",
		},
		{
			name: "workflow notification without occurrence uses legacy workflow scope",
			got:  idempotency.WorkflowNotificationEventKey("workflow-1", "Workflow Terminated", ""),
			want: "notification:WORKFLOW:workflow-1:Workflow Terminated",
		},
		{
			name: "workflow notification with occurrence is occurrence scoped",
			got:  idempotency.WorkflowNotificationEventKey("workflow-1", "Workflow Terminated", "job-1"),
			want: "notification:WORKFLOW:workflow-1:Workflow Terminated:job-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.got != tt.want {
				t.Fatalf("key = %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestWorkflowNotificationOccurrenceKeysDiffer(t *testing.T) {
	t.Parallel()

	first := idempotency.WorkflowNotificationEventKey("workflow-1", "Workflow Terminated", "job-1")
	second := idempotency.WorkflowNotificationEventKey("workflow-1", "Workflow Terminated", "job-2")
	if first == second {
		t.Fatalf("keys should differ for different occurrences: %q", first)
	}

	replay := idempotency.WorkflowNotificationEventKey("workflow-1", "Workflow Terminated", "job-1")
	if first != replay {
		t.Fatalf("keys should match for same occurrence: first=%q replay=%q", first, replay)
	}
}

func TestJobDispatchEventKeyIncludesDispatchAttempt(t *testing.T) {
	t.Parallel()

	if got := idempotency.JobDispatchEventKey("job-1", 3); got != "job:job-1:dispatch:3" {
		t.Fatalf("JobDispatchEventKey() = %q", got)
	}
	if got := idempotency.JobDispatchEventKey("job-1"); got != "job:job-1:dispatch" {
		t.Fatalf("JobDispatchEventKey() = %q", got)
	}
}

func TestClaimCommandID(t *testing.T) {
	t.Parallel()

	first := idempotency.ClaimCommandID("process-1", "job-1", 2)
	if len(first) != 64 {
		t.Fatalf("ClaimCommandID() length = %d", len(first))
	}
	if replay := idempotency.ClaimCommandID("process-1", "job-1", 2); replay != first {
		t.Fatalf("ClaimCommandID() is not deterministic")
	}
	if restarted := idempotency.ClaimCommandID("process-2", "job-1", 2); restarted == first {
		t.Fatalf("ClaimCommandID() must bind process identity")
	}
}

func TestDeterministicIdentitiesCanonicalizeUUIDText(t *testing.T) {
	t.Parallel()

	const (
		upper     = "A0B1C2D3-E4F5-4678-9ABC-DEF012345678"
		canonical = "a0b1c2d3-e4f5-4678-9abc-def012345678"
	)
	if got, want := idempotency.ClaimCommandID(upper, upper, 2), idempotency.ClaimCommandID(canonical, canonical, 2); got != want {
		t.Fatalf("ClaimCommandID() differs by UUID spelling: %q != %q", got, want)
	}
	if got, want := idempotency.JobCancelCommandID(upper), idempotency.JobCancelCommandID(canonical); got != want {
		t.Fatalf("JobCancelCommandID() differs by UUID spelling: %q != %q", got, want)
	}
	if got := idempotency.WorkflowEventKey(upper, "BUILD", 1); got != "workflow:"+canonical+":BUILD:1" {
		t.Fatalf("WorkflowEventKey() = %q", got)
	}
	if got := idempotency.JobDispatchEventKey(upper, 2); got != "job:"+canonical+":dispatch:2" {
		t.Fatalf("JobDispatchEventKey() = %q", got)
	}
}

func TestJobWorkflowEventKeyIncludesAction(t *testing.T) {
	t.Parallel()

	if got := idempotency.JobWorkflowEventKey("job-1", "JOB_FAILED"); got != "workflow:job:job-1:JOB_FAILED" {
		t.Fatalf("JobWorkflowEventKey() = %q", got)
	}
}

func TestLogEventKeyIncludesRetryAttempt(t *testing.T) {
	t.Parallel()

	if got := idempotency.LogEventKey("job-1", "stdout", 2, 1); got != "log:job-1:stdout:2" {
		t.Fatalf("LogEventKey() attempt 1 = %q", got)
	}
	if got := idempotency.LogEventKey("job-1", "stdout", 2, 3); got != "log:job-1:attempt:3:stdout:2" {
		t.Fatalf("LogEventKey() attempt 3 = %q", got)
	}
}
