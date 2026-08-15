//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package workflows

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestMain(m *testing.M) {
	testkit.Run(m, testkit.WithPostgres())
}

// newTestRepository builds a workflows repository against the shared PostgreSQL
// container.
func newTestRepository(t *testing.T) *Repository {
	t.Helper()
	return New(&Config{FetchLimit: 20}, testkit.Postgres(t))
}

//nolint:gocyclo // The lifecycle test exercises the full workflow state machine in one flow.
func TestIntegrationWorkflowLifecycle(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	userID := seedUser(ctx, t, pg)
	idempotencyKey := "workflow-lifecycle-" + t.Name()

	// Create a workflow.
	created, err := repo.CreateWorkflow(
		ctx, userID, "nightly-backup", `{"image":"alpine:3.22.2"}`, "CONTAINER",
		3600, 3, true, idempotencyKey,
	)
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected a workflow id")
	}
	if created.WorkflowBuildStatus != string(workflowsmodel.WorkflowBuildStatusQueued) {
		t.Fatalf("build_status = %q, want %q", created.WorkflowBuildStatus, workflowsmodel.WorkflowBuildStatusQueued)
	}
	if !created.LogRetention {
		t.Fatal("log_retention = false, want true")
	}

	// Replaying the same idempotency key returns the same workflow.
	replayed, err := repo.CreateWorkflow(
		ctx, userID, "nightly-backup", `{"image":"alpine:3.22.2"}`, "CONTAINER",
		3600, 3, true, idempotencyKey,
	)
	if err != nil {
		t.Fatalf("CreateWorkflow (idempotent replay): %v", err)
	}
	if replayed.ID != created.ID {
		t.Fatalf("idempotent replay id = %q, want %q", replayed.ID, created.ID)
	}

	// GetWorkflow returns the created workflow.
	got, err := repo.GetWorkflow(ctx, created.ID, userID)
	if err != nil {
		t.Fatalf("GetWorkflow: %v", err)
	}
	if got.Name != "nightly-backup" {
		t.Fatalf("name = %q, want %q", got.Name, "nightly-backup")
	}

	// UpdateWorkflow changes the payload and interval.
	if updateErr := repo.UpdateWorkflow(ctx, created.ID, userID, "nightly-backup", `{"image":"alpine:3.22.2","tag":"v2"}`, 7200, 5, idempotencyKey); updateErr != nil {
		t.Fatalf("UpdateWorkflow: %v", updateErr)
	}
	updated, err := repo.GetWorkflow(ctx, created.ID, userID)
	if err != nil {
		t.Fatalf("GetWorkflow after update: %v", err)
	}
	if updated.Interval != 7200 {
		t.Fatalf("interval = %d, want %d", updated.Interval, 7200)
	}

	// UpdateWorkflowBuildStatus transitions the build pipeline. The payload
	// change above bumped the generation, so use the updated generation.
	if buildErr := repo.UpdateWorkflowBuildStatus(ctx, created.ID, userID, "STARTED", updated.Generation, "", ""); buildErr != nil {
		t.Fatalf("UpdateWorkflowBuildStatus(STARTED): %v", buildErr)
	}
	if buildErr := repo.UpdateWorkflowBuildStatus(ctx, created.ID, userID, "COMPLETED", updated.Generation, "alpine:3.22.2", "sha256:abc123"); buildErr != nil {
		t.Fatalf("UpdateWorkflowBuildStatus(COMPLETED): %v", buildErr)
	}
	built, err := repo.GetWorkflow(ctx, created.ID, userID)
	if err != nil {
		t.Fatalf("GetWorkflow after build: %v", err)
	}
	if built.WorkflowBuildStatus != "COMPLETED" {
		t.Fatalf("build_status = %q, want %q", built.WorkflowBuildStatus, "COMPLETED")
	}
	if built.ResolvedImageRef.String != "alpine:3.22.2" {
		t.Fatalf("resolved_image_ref = %q, want %q", built.ResolvedImageRef.String, "alpine:3.22.2")
	}

	// Build status updates are gated on the generation.
	if staleErr := repo.UpdateWorkflowBuildStatus(ctx, created.ID, userID, "FAILED", updated.Generation+1, "", ""); status.Code(staleErr) != codes.FailedPrecondition {
		t.Fatalf("UpdateWorkflowBuildStatus(stale generation) code = %v, want %v (err: %v)", status.Code(staleErr), codes.FailedPrecondition, staleErr)
	}

	// IncrementWorkflowConsecutiveJobFailuresCount reaches the failure threshold.
	thresholdReached := false
	for i := range 5 {
		reached, incErr := repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, created.ID, userID, fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1))
		if incErr != nil {
			t.Fatalf("IncrementWorkflowConsecutiveJobFailuresCount: %v", incErr)
		}
		thresholdReached = reached
	}
	if !thresholdReached {
		t.Fatal("expected failure threshold to be reached after 5 failures (max 5)")
	}

	// ResetWorkflowConsecutiveJobFailuresCount clears the counter.
	if resetErr := repo.ResetWorkflowConsecutiveJobFailuresCount(ctx, created.ID, userID); resetErr != nil {
		t.Fatalf("ResetWorkflowConsecutiveJobFailuresCount: %v", resetErr)
	}
	reset, err := repo.GetWorkflow(ctx, created.ID, userID)
	if err != nil {
		t.Fatalf("GetWorkflow after reset: %v", err)
	}
	if reset.ConsecutiveJobFailuresCount != 0 {
		t.Fatalf("consecutive_job_failures_count = %d, want 0", reset.ConsecutiveJobFailuresCount)
	}

	// ListWorkflows returns the workflow with filters.
	list, err := repo.ListWorkflows(ctx, userID, "", &workflowsmodel.ListWorkflowsFilters{Kind: "CONTAINER", Query: "nightly"})
	if err != nil {
		t.Fatalf("ListWorkflows: %v", err)
	}
	if len(list.Workflows) != 1 || list.Workflows[0].ID != created.ID {
		t.Fatalf("ListWorkflows = %+v, want single workflow %q", list.Workflows, created.ID)
	}

	// TerminateWorkflow marks the workflow as terminated.
	if termErr := repo.TerminateWorkflow(ctx, created.ID, userID); termErr != nil {
		t.Fatalf("TerminateWorkflow: %v", termErr)
	}
	terminated, err := repo.GetWorkflow(ctx, created.ID, userID)
	if err != nil {
		t.Fatalf("GetWorkflow after terminate: %v", err)
	}
	if !terminated.TerminatedAt.Valid {
		t.Fatal("terminated_at is null, want set")
	}

	// DeleteWorkflow removes the workflow.
	if deleteErr := repo.DeleteWorkflow(ctx, created.ID, userID); deleteErr != nil {
		t.Fatalf("DeleteWorkflow: %v", deleteErr)
	}
	if _, getErr := repo.GetWorkflow(ctx, created.ID, userID); status.Code(getErr) != codes.NotFound {
		t.Fatalf("GetWorkflow after delete code = %v, want %v (err: %v)", status.Code(getErr), codes.NotFound, getErr)
	}
}

// seedUser inserts a fresh user and returns its id. Workflows reference users
// through FKs (idempotency keys, workflows.user_id), so the user must exist.
func seedUser(ctx context.Context, t *testing.T, pg *postgres.Postgres) string {
	t.Helper()

	return testkit.SeedUser(ctx, t, pg, fmt.Sprintf("workflows-%s@chronoverse.test", t.Name()))
}
