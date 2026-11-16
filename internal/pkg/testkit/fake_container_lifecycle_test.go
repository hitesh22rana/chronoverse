package testkit_test

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	containerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestFakeContainerDoesNotPretendToImplementLifecycleAuthority(t *testing.T) {
	svc := testkit.NewFakeContainerSvc(time.Second)
	instance, err := svc.RuntimeInstance(t.Context())
	if instance != "" || status.Code(err) != codes.Unimplemented {
		t.Fatalf("fake supplied daemon identity without an implementation: %q %v", instance, err)
	}
	id, err := svc.Create(t.Context(), containerpkg.CreateRequest{Name: "named-intent", Image: "image", Attempt: 1})
	if id != "" || status.Code(err) != codes.Unimplemented {
		t.Fatalf("fake acknowledged unimplemented Create: %q %v", id, err)
	}
	logs, errs, err := svc.Start(t.Context(), time.Second, "container")
	if logs != nil || errs != nil || status.Code(err) != codes.Unimplemented {
		t.Fatalf("fake acknowledged unimplemented Start: logs=%v errs=%v error=%v", logs, errs, err)
	}
}
