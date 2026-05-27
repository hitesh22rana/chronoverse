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
