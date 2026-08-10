package commandidempotency_test

import (
	"reflect"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
)

func TestNormalizeKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
		code codes.Code
	}{
		{name: "trim ASCII spaces", in: "  key  ", want: "key"},
		{name: "preserve non-breaking space", in: "\u00a0key\u00a0", want: "\u00a0key\u00a0"},
		{name: "reject empty", in: "   ", code: codes.InvalidArgument},
		{name: "reject controls before trim", in: "\nkey", code: codes.InvalidArgument},
		{name: "reject more than 255 bytes", in: strings.Repeat("é", 128), code: codes.InvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := commandidempotency.NormalizeKey(tt.in)
			if status.Code(err) != tt.code {
				t.Fatalf("NormalizeKey() code = %s, want %s: %v", status.Code(err), tt.code, err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPublishedOperationMapping(t *testing.T) {
	t.Parallel()
	want := []string{
		"user.register",
		"workflow.create",
		"workflow.update:workflow-id",
		"job.schedule.manual:workflow-id",
		"job.schedule.automatic",
		"notification.create",
		"job.cancel",
		"job.claim",
		"job.attach_container",
		"job.complete",
		"job.fail",
		"job.cancel_claimed",
		"job.release_for_retry",
		"job.recover_expired_leases",
	}
	got := []string{
		commandidempotency.OperationUserRegister,
		commandidempotency.OperationWorkflowCreate,
		commandidempotency.WorkflowUpdateOperation("workflow-id"),
		commandidempotency.ManualScheduleOperation("workflow-id"),
		commandidempotency.OperationJobScheduleAutomatic,
		commandidempotency.OperationNotificationCreate,
		commandidempotency.OperationJobCancel,
		commandidempotency.OperationJobClaim,
		commandidempotency.OperationJobAttachContainer,
		commandidempotency.OperationJobComplete,
		commandidempotency.OperationJobFail,
		commandidempotency.OperationJobCancelClaimed,
		commandidempotency.OperationJobReleaseForRetry,
		commandidempotency.OperationJobRecoverExpiredLeases,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("published operations = %v, want %v", got, want)
	}
}
