//nolint:testpackage // Integration tests share repository fixtures.
package jobs

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestIntegrationCancelJobRefusesInvalidAndConflictingIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	if snapshot, err := repo.CancelJob(ctx, "invalid-id", "cancel-invalid", "OPERATOR_REQUEST"); snapshot != nil || status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid identity = %+v, %v", snapshot, err)
	}
	fixture := seedClaimedJob(ctx, t, pg, repo)
	commandID := "cancel-identity-" + fixtureTag()
	if _, err := repo.CancelJob(ctx, fixture.JobID, commandID, "OPERATOR_REQUEST"); err != nil {
		t.Fatalf("initial cancellation: %v", err)
	}
	before := readTerminalJobState(ctx, t, pg, fixture.JobID)
	ledger, exists := readJobCommand(ctx, t, pg, fixture.JobID, commandidempotency.OperationJobCancel, commandID)
	if !exists {
		t.Fatal("initial cancellation did not persist its identity")
	}
	if snapshot, err := repo.CancelJob(ctx, fixture.JobID, commandID, "WORKFLOW_TERMINATED"); snapshot != nil || status.Code(err) != codes.AlreadyExists {
		t.Fatalf("conflicting cancellation = %+v, %v", snapshot, err)
	}
	if after := readTerminalJobState(ctx, t, pg, fixture.JobID); *after != *before {
		t.Fatal("conflicting cancellation changed the canceled job")
	}
	after, exists := readJobCommand(ctx, t, pg, fixture.JobID, commandidempotency.OperationJobCancel, commandID)
	if !exists {
		t.Fatal("conflicting cancellation removed its original identity")
	}
	assertSameLedgerRow(t, after, ledger)
}
