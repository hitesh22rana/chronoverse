//nolint:testpackage // Reuses PostgreSQL fixtures and scoped repository constructors.
package workflows

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIntegrationWorkflowReadsRespectOwnership(t *testing.T) {
	repo := newTestRepository(t)
	owner := newListOwner(t)
	created, err := repo.CreateWorkflow(t.Context(), owner, "read-contract", heartbeatFixturePayload, "HEARTBEAT", 60, 3, true, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	read, err := repo.GetWorkflow(t.Context(), created.ID, owner)
	if err != nil || read == nil || read.ID != created.ID || read.Name != "read-contract" || read.Generation != created.Generation {
		t.Fatalf("owned read=%+v err=%v", read, err)
	}
	internal, err := repo.GetWorkflowByID(t.Context(), created.ID)
	if err != nil || internal == nil || internal.ID != created.ID || internal.UserID != owner {
		t.Fatalf("internal read=%+v err=%v", internal, err)
	}
	other := newListOwner(t)
	if got, readErr := repo.GetWorkflow(t.Context(), created.ID, other); got != nil || status.Code(readErr) != codes.NotFound {
		t.Fatalf("cross-owner read=%+v err=%v", got, readErr)
	}
}

func TestIntegrationWorkflowReadsReportCallerErrors(t *testing.T) {
	repo := newTestRepository(t)
	owner := newListOwner(t)
	created, err := repo.CreateWorkflow(t.Context(), owner, "read-errors", heartbeatFixturePayload, "HEARTBEAT", 60, 3, true, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, id         string
		cancel, deadline bool
		code             codes.Code
	}{
		{name: "missing", id: uuid.NewString(), code: codes.NotFound},
		{name: "malformed identity", id: "not-a-uuid", code: codes.InvalidArgument},
		{name: "canceled", id: created.ID, cancel: true, code: codes.Canceled},
		{name: "expired", id: created.ID, deadline: true, code: codes.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			cancel := func() {}
			if tc.cancel {
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if tc.deadline {
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
			}
			defer cancel()
			if got, readErr := repo.GetWorkflow(ctx, tc.id, owner); got != nil || status.Code(readErr) != tc.code {
				t.Fatalf("owned read=%+v err=%v want %s", got, readErr, tc.code)
			}
			if got, readErr := repo.GetWorkflowByID(ctx, tc.id); got != nil || status.Code(readErr) != tc.code {
				t.Fatalf("internal read=%+v err=%v want %s", got, readErr, tc.code)
			}
		})
	}
}
