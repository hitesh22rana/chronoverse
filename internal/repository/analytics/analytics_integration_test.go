//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package analytics

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestMain(m *testing.M) {
	testkit.Run(m, testkit.WithPostgres())
}

// seedAnalytics inserts a user and two workflows with analytics rows, returning
// the user id and the second workflow's id.
func seedAnalytics(ctx context.Context, t *testing.T, pg *postgres.Postgres) (userID, workflowID string) {
	t.Helper()

	if err := pg.QueryRow(ctx, `
		INSERT INTO users (email, password)
		VALUES ($1, $2)
		RETURNING id
	`, fmt.Sprintf("analytics-%s@chronoverse.test", t.Name()), "hash").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	var workflowA, workflowB string
	for i, wf := range []*string{&workflowA, &workflowB} {
		if err := pg.QueryRow(ctx, `
			INSERT INTO workflows (user_id, name, payload, kind, build_status, interval)
			VALUES ($1, $2, '{}', 'CONTAINER', 'COMPLETED', 1)
			RETURNING id
		`, userID, fmt.Sprintf("wf-%s-%d", t.Name(), i)).Scan(wf); err != nil {
			t.Fatalf("seed workflow %d: %v", i, err)
		}
	}

	// workflowA: 3 jobs, 10 logs, 60s duration.
	if _, err := pg.Exec(ctx, `
		INSERT INTO analytics (user_id, workflow_id, kind, total_job_execution_duration, jobs_count, logs_count)
		VALUES ($1, $2, 'CONTAINER', 60, 3, 10)
	`, userID, workflowA); err != nil {
		t.Fatalf("seed analytics A: %v", err)
	}
	// workflowB: 1 job, 4 logs, 30s duration.
	if _, err := pg.Exec(ctx, `
		INSERT INTO analytics (user_id, workflow_id, kind, total_job_execution_duration, jobs_count, logs_count)
		VALUES ($1, $2, 'HEARTBEAT', 30, 1, 4)
	`, userID, workflowB); err != nil {
		t.Fatalf("seed analytics B: %v", err)
	}

	return userID, workflowB
}

func TestIntegrationGetUserAnalytics(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := New(nil, pg)

	userID, _ := seedAnalytics(ctx, t, pg)

	res, err := repo.GetUserAnalytics(ctx, userID)
	if err != nil {
		t.Fatalf("GetUserAnalytics: %v", err)
	}
	if res.TotalWorkflows != 2 {
		t.Fatalf("total_workflows = %d, want 2", res.TotalWorkflows)
	}
	if res.TotalJobs != 4 {
		t.Fatalf("total_jobs = %d, want 4", res.TotalJobs)
	}
	if res.TotalJoblogs != 14 {
		t.Fatalf("total_joblogs = %d, want 14", res.TotalJoblogs)
	}
	if res.TotalJobExecutionDuration != 90 {
		t.Fatalf("total_job_execution_duration = %d, want 90", res.TotalJobExecutionDuration)
	}

	if len(res.WorkflowKinds) != 2 {
		t.Fatalf("workflow_kinds = %d entries, want 2", len(res.WorkflowKinds))
	}
	if len(res.TopWorkflows) != 2 {
		t.Fatalf("top_workflows = %d entries, want 2", len(res.TopWorkflows))
	}
	// Top workflows are ordered by jobs_count DESC; workflow A has more jobs.
	if res.TopWorkflows[0].TotalJobs != 3 {
		t.Fatalf("top workflow[0] total_jobs = %d, want 3", res.TopWorkflows[0].TotalJobs)
	}
	if res.TopWorkflows[0].WorkflowName == "" {
		t.Fatal("top workflow[0] name is empty, want the joined workflow name")
	}

	// A user without analytics gets zeroed aggregates (COUNT over no rows).
	empty, err := repo.GetUserAnalytics(ctx, "00000000-0000-0000-0000-000000000099")
	if err != nil {
		t.Fatalf("GetUserAnalytics(empty): %v", err)
	}
	if empty.TotalWorkflows != 0 || empty.TotalJobs != 0 {
		t.Fatalf("empty analytics = %+v, want all zeros", empty)
	}
}

func TestIntegrationGetWorkflowAnalytics(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := New(nil, pg)

	userID, workflowID := seedAnalytics(ctx, t, pg)

	res, err := repo.GetWorkflowAnalytics(ctx, userID, workflowID)
	if err != nil {
		t.Fatalf("GetWorkflowAnalytics: %v", err)
	}
	if res.TotalJobs != 1 {
		t.Fatalf("total_jobs = %d, want 1", res.TotalJobs)
	}
	if res.TotalJoblogs != 4 {
		t.Fatalf("total_joblogs = %d, want 4", res.TotalJoblogs)
	}
	if res.TotalJobExecutionDuration != 30 {
		t.Fatalf("total_job_execution_duration = %d, want 30", res.TotalJobExecutionDuration)
	}

	// Unknown workflow is not found.
	if _, err := repo.GetWorkflowAnalytics(ctx, userID, "00000000-0000-0000-0000-0000000000aa"); status.Code(err) != codes.NotFound {
		t.Fatalf("GetWorkflowAnalytics(unknown) code = %v, want %v (err: %v)", status.Code(err), codes.NotFound, err)
	}
}
