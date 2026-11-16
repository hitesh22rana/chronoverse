//nolint:testpackage // An unregistered internal operation must fail closed before repository dispatch.
package jobs

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

func TestUnregisteredContainerIntentOperationCannotDispatch(t *testing.T) {
	ctx := auth.WithSubject(auth.WithRole(t.Context(), auth.RoleAdmin.String()), "internal/executor")
	if err := validateIntentCommand(ctx, "unregistered-operation", &jobspb.ContainerIntentCommand{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("unregistered lifecycle operation accepted: %v", err)
	}
}
