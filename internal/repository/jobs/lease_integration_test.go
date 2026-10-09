//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package jobs

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
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

func jobReleaseState(ctx context.Context, t *testing.T, pg *postgres.Postgres, jobID string) (status string, reason, lease sql.NullString, nextAttemptAt sql.NullTime) {
	t.Helper()

	if err := pg.QueryRow(ctx, `
		SELECT status, terminal_reason_code, lease_token, next_attempt_at
		FROM jobs WHERE id = $1
	`, jobID).Scan(&status, &reason, &lease, &nextAttemptAt); err != nil {
		t.Fatalf("fetch job state: %v", err)
	}
	return status, reason, lease, nextAttemptAt
}

func jobNextAttemptAt(ctx context.Context, t *testing.T, pg *postgres.Postgres, jobID string) sql.NullTime {
	t.Helper()

	var nextAttemptAt sql.NullTime
	if err := pg.QueryRow(ctx, `SELECT next_attempt_at FROM jobs WHERE id = $1`, jobID).Scan(&nextAttemptAt); err != nil {
		t.Fatalf("fetch job next_attempt_at: %v", err)
	}
	return nextAttemptAt
}

func TestIntegrationReleaseForRetryReleasesActiveWorkflowJob(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	_, workflowID := seedUserWorkflow(ctx, t, pg)
	userID := mustWorkflowUser(ctx, t, pg, workflowID)
	jobID, leaseToken := seedRunningJob(ctx, t, pg, repo, workflowID, userID, t.Name())

	releaseForRetry(t, repo, jobID, leaseToken, t.Name())

	status, _, lease, nextAttemptAt := jobReleaseState(ctx, t, pg, jobID)
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

// TestIntegrationReleaseForRetryStoresAnOffsetSpellingAsItsInstant pins the UTC contract of
// jobs.next_attempt_at, which is TIMESTAMP WITHOUT TIME ZONE. The retry backoff is computed on
// the worker with time.Now(), which carries that host's own offset, so a worker outside UTC
// spells an instant in its own zone on every single retry. The column keeps no offset, so the
// instant has to be normalized before storage: otherwise the scheduler, which compares
// next_attempt_at against now() AT TIME ZONE 'utc', sees the retry as due the offset's
// distance from when it was actually asked for.
func TestIntegrationReleaseForRetryStoresAnOffsetSpellingAsItsInstant(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	_, workflowID := seedUserWorkflow(ctx, t, pg)
	userID := mustWorkflowUser(ctx, t, pg, workflowID)
	jobID, leaseToken := seedRunningJob(ctx, t, pg, repo, workflowID, userID, t.Name())

	instant := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
	spelling := instant.In(time.FixedZone("plus2", 2*3600))
	if err := repo.ReleaseJobForRetry(
		ctx, jobID, leaseToken, spelling.Format(time.RFC3339Nano), "Unavailable", "runtime down", "release-offset-"+t.Name(),
	); err != nil {
		t.Fatalf("ReleaseJobForRetry: %v", err)
	}

	nextAttemptAt := jobNextAttemptAt(ctx, t, pg, jobID)
	if !nextAttemptAt.Valid {
		t.Fatal("next_attempt_at is NULL, want set for retry")
	}
	if !nextAttemptAt.Time.Equal(instant) {
		t.Fatalf("job next_attempt_at = %s, want the instant the spelling denotes %s",
			nextAttemptAt.Time.UTC(), instant.UTC())
	}
	if got := nextAttemptAt.Time.Sub(instant); got != 0 {
		t.Fatalf("job next_attempt_at sits %s from the requested instant, want 0", got)
	}
}

// TestIntegrationReleaseForRetryTreatsTwoSpellingsOfOneInstantAsOneRequest pins the request
// identity a release spends. next_attempt_at is stored UTC, so two spellings of one instant
// reserve the same retry. Hashing the supplied text instead would reject the second as a
// different request and fail a caller that merely spelled the same instant in its own zone.
func TestIntegrationReleaseForRetryTreatsTwoSpellingsOfOneInstantAsOneRequest(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	_, workflowID := seedUserWorkflow(ctx, t, pg)
	userID := mustWorkflowUser(ctx, t, pg, workflowID)
	jobID, leaseToken := seedRunningJob(ctx, t, pg, repo, workflowID, userID, t.Name())

	instant := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
	commandID := "release-spelling-" + t.Name()
	if err := repo.ReleaseJobForRetry(
		ctx, jobID, leaseToken, instant.Format(time.RFC3339Nano), "Unavailable", "runtime down", commandID,
	); err != nil {
		t.Fatalf("ReleaseJobForRetry (UTC spelling): %v", err)
	}

	// The lease is gone by now, so this only reaches the ledger if the request identity
	// matches; a mismatch reads as a reused commandID with a different request.
	spelling := instant.In(time.FixedZone("plus2", 2*3600))
	if err := repo.ReleaseJobForRetry(
		ctx, jobID, leaseToken, spelling.Format(time.RFC3339Nano), "Unavailable", "runtime down", commandID,
	); err != nil {
		t.Fatalf("ReleaseJobForRetry (offset spelling) read as a different request: %v", err)
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

	status, reason, lease, _ := jobReleaseState(ctx, t, pg, jobID)
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

	status, reason, _, _ := jobReleaseState(ctx, t, pg, jobID)
	if status != "CANCELED" {
		t.Fatalf("job status = %q, want %q (orphan PENDING)", status, "CANCELED")
	}
	if !reason.Valid || reason.String != "WORKFLOW_TERMINATED" {
		t.Fatalf("terminal_reason_code = %q, want %q", reason.String, "WORKFLOW_TERMINATED")
	}
}

// TestIntegrationReleaseForRetryRejectsBadInputBeforeTouchingTheJob pins the input guards on
// a release. Both reject before any statement runs, so the running job keeps its lease.
func TestIntegrationReleaseForRetryRejectsBadInputBeforeTouchingTheJob(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	_, workflowID := seedUserWorkflow(ctx, t, pg)
	userID := mustWorkflowUser(ctx, t, pg, workflowID)
	jobID, leaseToken := seedRunningJob(ctx, t, pg, repo, workflowID, userID, t.Name())
	valid := time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)

	for _, tc := range []struct{ name, jobID, nextAttemptAt, want string }{
		{name: "job ID is not a UUID", jobID: "not-a-uuid", nextAttemptAt: valid, want: "must be a valid UUID"},
		{name: "next attempt is not a timestamp", jobID: jobID, nextAttemptAt: "not-a-timestamp", want: "invalid next_attempt_at"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := repo.ReleaseJobForRetry(ctx, tc.jobID, leaseToken, tc.nextAttemptAt, "Unavailable", "runtime down", "release-bad-"+fixtureTag())
			if err == nil {
				t.Fatal("ReleaseJobForRetry accepted invalid input")
			}
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("ReleaseJobForRetry code = %s, want %s: %v", status.Code(err), codes.InvalidArgument, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ReleaseJobForRetry error = %v, want it to mention %q", err, tc.want)
			}
			jobStatus, _, lease, nextAttemptAt := jobReleaseState(ctx, t, pg, jobID)
			if jobStatus != "RUNNING" || !lease.Valid || nextAttemptAt.Valid {
				t.Fatalf("job after rejection = %q/lease %v/next %v, want RUNNING holding its lease",
					jobStatus, lease.Valid, nextAttemptAt.Valid)
			}
		})
	}
}

// TestIntegrationReleaseForRetryWithoutTheHeldLeaseIsRejected pins the precondition that a
// release only frees a lease this worker still holds.
func TestIntegrationReleaseForRetryWithoutTheHeldLeaseIsRejected(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	_, workflowID := seedUserWorkflow(ctx, t, pg)
	userID := mustWorkflowUser(ctx, t, pg, workflowID)
	jobID, _ := seedRunningJob(ctx, t, pg, repo, workflowID, userID, t.Name())

	err := repo.ReleaseJobForRetry(
		ctx, jobID, uuid.NewString(), time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano),
		"Unavailable", "runtime down", "release-stale-"+t.Name(),
	)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ReleaseJobForRetry with a stale token = %v, want %s", err, codes.FailedPrecondition)
	}
	if jobStatus, _, lease, nextAttemptAt := jobReleaseState(ctx, t, pg, jobID); jobStatus != "RUNNING" || !lease.Valid || nextAttemptAt.Valid {
		t.Fatalf("job after stale release = %q/lease %v/next %v, want RUNNING holding its lease",
			jobStatus, lease.Valid, nextAttemptAt.Valid)
	}
}

// TestIntegrationReleaseForRetryFaultsRollBackAndRecoverWithTheSameCommand drives the release
// against a database that fails partway. A release moves the job, its runtime slot and its
// ledger row together, so a failure anywhere must leave the job running on its original lease
// with no ledger trace, and the caller's commandID must still be usable afterwards.
func TestIntegrationReleaseForRetryFaultsRollBackAndRecoverWithTheSameCommand(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)

	for _, tc := range []struct {
		name, table, event string
		deferred           bool
	}{
		// The reservation insert, before the job is touched at all.
		{name: "reservation fails", table: "command_idempotency_keys", event: "INSERT"},
		// The release update, deferred so it fails at commit rather than mid-statement.
		{name: "commit fails", table: "jobs", event: "UPDATE", deferred: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			userID, workflowID := seedIsolatedWorkflow(ctx, t, pg)
			jobID, leaseToken := seedRunningJob(ctx, t, pg, repo, workflowID, userID, t.Name())

			commandID := "release-fault-" + fixtureTag()
			instant := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
			release := func() error {
				return repo.ReleaseJobForRetry(ctx, jobID, leaseToken, instant.Format(time.RFC3339Nano),
					"Unavailable", "runtime down", commandID)
			}

			remove := scheduleFaultTrigger(ctx, t, pg, tc.table, tc.event, tc.deferred)
			if err := release(); err == nil {
				remove()
				t.Fatal("ReleaseJobForRetry succeeded through an injected fault")
			}
			remove()

			jobStatus, _, lease, nextAttemptAt := jobReleaseState(ctx, t, pg, jobID)
			if jobStatus != "RUNNING" || !lease.Valid || nextAttemptAt.Valid {
				t.Fatalf("job after rollback = %q/lease %v/next %v, want RUNNING holding its lease",
					jobStatus, lease.Valid, nextAttemptAt.Valid)
			}
			if _, ok := readCommandByScope(ctx, t, pg, commandidempotency.JobScope(jobID), commandidempotency.OperationJobReleaseForRetry, commandID); ok {
				t.Fatal("a failed release left a completed ledger row behind")
			}

			// The same commandID must still work, and must then replay rather than reserve again.
			if err := release(); err != nil {
				t.Fatalf("retry after recovery: %v", err)
			}
			jobStatus, _, lease, _ = jobReleaseState(ctx, t, pg, jobID)
			if jobStatus != "PENDING" || lease.Valid {
				t.Fatalf("job after recovery = %q/lease %v, want PENDING released", jobStatus, lease.Valid)
			}
			if _, ok := readCommandByScope(ctx, t, pg, commandidempotency.JobScope(jobID), commandidempotency.OperationJobReleaseForRetry, commandID); !ok {
				t.Fatal("retry after recovery did not complete the ledger")
			}
			if err := release(); err != nil {
				t.Fatalf("replay after recovery: %v", err)
			}
			if got := jobNextAttemptAt(ctx, t, pg, jobID); !got.Valid {
				t.Fatal("replay dropped the stored next_attempt_at")
			}
		})
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
