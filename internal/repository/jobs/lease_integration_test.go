//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package jobs

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

// seedRunningJob schedules, queues, and claims a job, returning its id and
// lease token.
func seedRunningJob(ctx context.Context, t *testing.T, pg *postgres.Postgres, repo *Repository, workflowID, userID, tag string) (jobID, leaseToken string) {
	t.Helper()

	scheduledAt := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	var err error
	jobID, err = repo.ScheduleJob(ctx, workflowID, userID, scheduledAt, "MANUAL", "idem-running-"+tag, 1)
	if err != nil {
		t.Fatalf("ScheduleJob: %v", err)
	}
	queueJob(ctx, t, pg, jobID)
	seedReadyRuntimeNode(ctx, t, pg, tag)

	claimed, ok, reason, err := repo.ClaimJob(ctx, jobID, workflowID, "test-worker", uuid.NewString(), "claim-"+tag, 30*time.Second, 1)
	if err != nil {
		t.Fatalf("ClaimJob: %v", err)
	}
	if !ok {
		t.Fatalf("ClaimJob not claimed: %s", reason)
	}
	return jobID, claimed.LeaseToken
}

func releaseForRetry(t *testing.T, repo *Repository, jobID, leaseToken, tag string) {
	t.Helper()

	nextAttemptAt := time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)
	if err := repo.ReleaseJobForRetry(context.Background(), jobID, leaseToken, nextAttemptAt, "Unavailable", "runtime down", "release-"+tag); err != nil {
		t.Fatalf("ReleaseJobForRetry: %v", err)
	}
}

func jobReleaseState(t *testing.T, pg *postgres.Postgres, jobID string) (status string, reason, lease sql.NullString, nextAttemptAt sql.NullTime) {
	t.Helper()

	if err := pg.QueryRow(context.Background(), `
		SELECT status, terminal_reason_code, lease_token, next_attempt_at
		FROM jobs WHERE id = $1
	`, jobID).Scan(&status, &reason, &lease, &nextAttemptAt); err != nil {
		t.Fatalf("fetch job state: %v", err)
	}
	return status, reason, lease, nextAttemptAt
}

func TestIntegrationReleaseForRetryReleasesActiveWorkflowJob(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	_, workflowID := seedUserWorkflow(ctx, t, pg)
	userID := mustWorkflowUser(ctx, t, pg, workflowID)
	jobID, leaseToken := seedRunningJob(ctx, t, pg, repo, workflowID, userID, t.Name())

	releaseForRetry(t, repo, jobID, leaseToken, t.Name())

	status, _, lease, nextAttemptAt := jobReleaseState(t, pg, jobID)
	if status != "PENDING" {
		t.Fatalf("job status = %q, want %q", status, "PENDING")
	}
	if lease.Valid {
		t.Fatalf("lease_token = %q, want NULL after release", lease.String)
	}
	if !nextAttemptAt.Valid {
		t.Fatal("next_attempt_at is NULL, want set for retry")
	}
}

func TestIntegrationReleaseForRetryCancelsTerminatedWorkflowJob(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	_, workflowID := seedUserWorkflow(ctx, t, pg)
	userID := mustWorkflowUser(ctx, t, pg, workflowID)
	jobID, leaseToken := seedRunningJob(ctx, t, pg, repo, workflowID, userID, t.Name())

	// Terminate first, then release: the job must converge to CANCELED
	// instead of orphan PENDING.
	if _, err := pg.Exec(ctx, `UPDATE workflows SET terminated_at = (now() AT TIME ZONE 'utc') WHERE id = $1`, workflowID); err != nil {
		t.Fatalf("terminate workflow: %v", err)
	}

	releaseForRetry(t, repo, jobID, leaseToken, t.Name())

	status, reason, lease, _ := jobReleaseState(t, pg, jobID)
	if status != "CANCELED" {
		t.Fatalf("job status = %q, want %q", status, "CANCELED")
	}
	if !reason.Valid || reason.String != "WORKFLOW_TERMINATED" {
		t.Fatalf("terminal_reason_code = %q, want %q", reason.String, "WORKFLOW_TERMINATED")
	}
	if lease.Valid {
		t.Fatalf("lease_token = %q, want NULL after cancel", lease.String)
	}
}

func TestIntegrationReleaseForRetrySerializesWithConcurrentTermination(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	_, workflowID := seedUserWorkflow(ctx, t, pg)
	userID := mustWorkflowUser(ctx, t, pg, workflowID)
	jobID, leaseToken := seedRunningJob(ctx, t, pg, repo, workflowID, userID, t.Name())

	// Hold the workflow row lock, then start the release: it must block on
	// the lock rather than reading a stale terminated_at.
	holder, err := pg.BeginTx(ctx)
	if err != nil {
		t.Fatalf("begin holder tx: %v", err)
	}
	//nolint:errcheck // Rollback is a no-op after commit.
	defer holder.Rollback(ctx)
	if _, err := holder.Exec(ctx, `SELECT id FROM workflows WHERE id = $1 FOR UPDATE`, workflowID); err != nil {
		t.Fatalf("lock workflow: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		nextAttemptAt := time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)
		done <- repo.ReleaseJobForRetry(context.Background(), jobID, leaseToken, nextAttemptAt, "Unavailable", "runtime down", "release-concurrent-"+t.Name())
	}()

	// Give the release time to reach the workflow check. Without the row
	// lock it would finish here; with the lock it stays blocked.
	time.Sleep(300 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("ReleaseJobForRetry finished while workflow lock held: %v", err)
	default:
	}

	// Terminate while the release waits, then commit: the release must
	// observe the termination and cancel instead of writing PENDING.
	if _, err := holder.Exec(ctx, `UPDATE workflows SET terminated_at = (now() AT TIME ZONE 'utc') WHERE id = $1`, workflowID); err != nil {
		t.Fatalf("terminate workflow: %v", err)
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatalf("commit holder tx: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ReleaseJobForRetry: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ReleaseJobForRetry did not finish after lock release")
	}

	status, reason, _, _ := jobReleaseState(t, pg, jobID)
	if status != "CANCELED" {
		t.Fatalf("job status = %q, want %q (orphan PENDING)", status, "CANCELED")
	}
	if !reason.Valid || reason.String != "WORKFLOW_TERMINATED" {
		t.Fatalf("terminal_reason_code = %q, want %q", reason.String, "WORKFLOW_TERMINATED")
	}
}

func mustWorkflowUser(ctx context.Context, t *testing.T, pg *postgres.Postgres, workflowID string) string {
	t.Helper()

	var userID string
	if err := pg.QueryRow(ctx, `SELECT user_id FROM workflows WHERE id = $1`, workflowID).Scan(&userID); err != nil {
		t.Fatalf("fetch workflow user: %v", err)
	}
	return userID
}
