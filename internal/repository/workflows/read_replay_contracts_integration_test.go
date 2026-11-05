//nolint:testpackage // Reuses PostgreSQL fixtures and scoped repository constructors.
package workflows

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
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

func TestIntegrationLegacyCreateReplayReloadsOwnedWorkflowWithoutNewEffects(t *testing.T) {
	for _, field := range []string{"id", "ID"} {
		t.Run(field, func(t *testing.T) {
			repo := newTestRepository(t)
			owner := newListOwner(t)
			key := uuid.NewString()
			created, err := repo.CreateWorkflow(t.Context(), owner, "legacy-replay", heartbeatFixturePayload, "HEARTBEAT", 60, 3, true, key)
			if err != nil {
				t.Fatal(err)
			}
			response, err := json.Marshal(map[string]string{field: created.ID})
			if err != nil {
				t.Fatal(err)
			}
			// Completed ID-only responses were persisted by the older create command.
			tag, err := testkit.Postgres(t).Exec(t.Context(), "UPDATE "+postgres.TableCommandIdempotencyKeys+" SET response=$1::jsonb WHERE scope=$2 AND operation=$3 AND idempotency_key=$4",
				string(response), commandidempotency.UserScope(owner), commandidempotency.OperationWorkflowCreate, key)
			if err != nil || tag.RowsAffected() != 1 {
				t.Fatalf("seed legacy response affected=%d err=%v", tag.RowsAffected(), err)
			}
			replay, err := repo.CreateWorkflow(t.Context(), owner, "legacy-replay", heartbeatFixturePayload, "HEARTBEAT", 60, 3, true, key)
			if err != nil || replay == nil || !replay.IdempotencyReplay || replay.ID != created.ID || replay.Name != created.Name || !replay.CreatedAt.Equal(created.CreatedAt) {
				t.Fatalf("legacy replay=%+v err=%v created=%+v", replay, err, created)
			}
			assertCreateEffects(t.Context(), t, repo, owner, 1, 1, 1, 1)
			assertBuildEvent(t.Context(), t, repo, created.ID, created.Generation)
		})
	}
}

// TestIntegrationLegacyCreateReplayReportsMissingWorkflow proves an old ID-only
// snapshot still has to name a workflow the user owns when it is replayed.
func TestIntegrationLegacyCreateReplayReportsMissingWorkflow(t *testing.T) {
	ctx := t.Context()
	repo := newTestRepository(t)
	owner := newListOwner(t)
	key := uuid.NewString()
	created, err := repo.CreateWorkflow(ctx, owner, "legacy-missing", heartbeatFixturePayload, "HEARTBEAT", 60, 3, true, key)
	if err != nil {
		t.Fatal(err)
	}
	response, err := json.Marshal(map[string]string{"id": created.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, execErr := repo.pg.Exec(ctx, "UPDATE "+postgres.TableCommandIdempotencyKeys+" SET response=$1::jsonb WHERE scope=$2 AND operation=$3 AND idempotency_key=$4",
		string(response), commandidempotency.UserScope(owner), commandidempotency.OperationWorkflowCreate, key); execErr != nil {
		t.Fatal(execErr)
	}
	if _, execErr := repo.pg.Exec(ctx, "DELETE FROM "+postgres.TableWorkflows+" WHERE id=$1", created.ID); execErr != nil {
		t.Fatal(execErr)
	}

	replay, err := repo.CreateWorkflow(ctx, owner, "legacy-missing", heartbeatFixturePayload, "HEARTBEAT", 60, 3, true, key)
	if replay != nil || status.Code(err) != codes.NotFound {
		t.Fatalf("legacy replay=%+v err=%v, want NotFound", replay, err)
	}
}
