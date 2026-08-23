package commandidempotency_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
)

func TestCommandRetentionContract(t *testing.T) {
	t.Parallel()

	if commandidempotency.ClientCommandRetention != 24*time.Hour {
		t.Fatalf("client retention = %s", commandidempotency.ClientCommandRetention)
	}
	if commandidempotency.DefaultEventCommandRetention != 336*time.Hour {
		t.Fatalf("event retention = %s", commandidempotency.DefaultEventCommandRetention)
	}
	err := commandidempotency.Complete(context.Background(), nil, "scope", "operation", "key", strings.Repeat("a", 64), "", struct{}{}, 0)
	if status.Code(err) != codes.Internal {
		t.Fatalf("zero retention code = %s, want Internal", status.Code(err))
	}
}

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

func TestUUIDDerivedIdentitiesUseCanonicalText(t *testing.T) {
	t.Parallel()

	const (
		upper     = "A0B1C2D3-E4F5-4678-9ABC-DEF012345678"
		canonical = "a0b1c2d3-e4f5-4678-9abc-def012345678"
	)
	got := []string{
		commandidempotency.WorkflowUpdateOperation(upper),
		commandidempotency.ManualScheduleOperation(upper),
		commandidempotency.UserScope(upper),
		commandidempotency.WorkflowScope(upper),
		commandidempotency.JobScope(upper),
		commandidempotency.WorkerScope(upper),
	}
	want := []string{
		"workflow.update:" + canonical,
		"job.schedule.manual:" + canonical,
		"user:" + canonical,
		"workflow:" + canonical,
		"job:" + canonical,
		"worker:" + canonical,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("UUID-derived identities = %v, want %v", got, want)
	}

	canonicalID, err := commandidempotency.CanonicalUUID("{"+upper+"}", "workflow ID")
	if err != nil {
		t.Fatalf("CanonicalUUID() error = %v", err)
	}
	if canonicalID != canonical {
		t.Fatalf("CanonicalUUID() = %q, want %q", canonicalID, canonical)
	}
	_, err = commandidempotency.CanonicalUUID("not-a-uuid", "workflow ID")
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Fatalf("CanonicalUUID() invalid code = %s, want %s", code, codes.InvalidArgument)
	}
}

func TestLegacyWorkflowOperationsPreserveRawIdentity(t *testing.T) {
	t.Parallel()

	const upper = "A0B1C2D3-E4F5-4678-9ABC-DEF012345678"
	if commandidempotency.LegacyOperationWorkflowCreate != "create_workflow" {
		t.Fatalf("legacy create operation = %q", commandidempotency.LegacyOperationWorkflowCreate)
	}
	if got := commandidempotency.LegacyWorkflowUpdateOperation(upper); got != "update_workflow:"+upper {
		t.Fatalf("legacy update operation = %q", got)
	}
}
