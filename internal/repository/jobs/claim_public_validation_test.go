//nolint:testpackage // Verifies validation before any database access.
package jobs

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestClaimJobRejectsInvalidIdentityBeforeDatabaseAccess(t *testing.T) {
	repo := &Repository{tp: noop.NewTracerProvider().Tracer("claim-validation")}
	validID := uuid.NewString()
	for _, tc := range []struct{ name, job, workflow, process, message string }{
		{name: "job identity", job: "invalid", workflow: validID, process: validID, message: "job ID"},
		{name: "workflow", job: validID, workflow: "invalid", process: validID, message: "workflow ID"},
		{name: "process", job: validID, workflow: validID, process: "invalid", message: "process instance ID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claimed, ok, reason, err := repo.ClaimJob(context.Background(), tc.job, tc.workflow, "worker", tc.process, "command", time.Minute, 1)
			if claimed != nil || ok || reason != "" || status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("invalid claim = %+v, %t, %q, %v", claimed, ok, reason, err)
			}
		})
	}
}
