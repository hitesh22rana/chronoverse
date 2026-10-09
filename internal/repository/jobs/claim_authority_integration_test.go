//nolint:testpackage // Reuses repository fixtures to verify durable claim authority.
package jobs

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

//nolint:gocyclo // Each authority mutation shares the same replay assertions.
func TestIntegrationClaimReplayPreservesExactAuthority(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	userID, workflowID := seedUserWorkflow(ctx, t, pg)
	jobID, err := repo.ScheduleJob(ctx, workflowID, userID, time.Now().UTC().Format(time.RFC3339Nano), "MANUAL", "schedule-"+fixtureTag(), 1)
	if err != nil {
		t.Fatal(err)
	}
	queueJob(ctx, t, pg, jobID)
	seedReadyRuntimeNode(ctx, t, pg, t.Name())
	process, command := uuid.NewString(), "claim-"+fixtureTag()
	first, ok, reason, err := repo.ClaimJob(ctx, jobID, workflowID, "worker", process, command, time.Minute, 1)
	if err != nil || !ok {
		t.Fatalf("first claim = %+v, %t, %q, %v", first, ok, reason, err)
	}
	replay, ok, reason, err := repo.ClaimJob(ctx, jobID, workflowID, "worker", process, command, time.Minute, 1)
	if err != nil || !ok || !reflect.DeepEqual(first, replay) {
		t.Fatalf("replay = %+v, %t, %q, %v; first %+v", replay, ok, reason, err, first)
	}
	var attempts int32
	if err := pg.QueryRow(ctx, `SELECT attempts FROM jobs WHERE id=$1`, jobID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatalf("replay incremented execution attempts to %d", attempts)
	}

	changes := []struct {
		name, assignment string
		value            any
	}{
		{"lease token", "lease_token", first.LeaseToken + "-replaced"},
		{"worker", "leased_by", "different-worker"},
		{"process", "lease_process_instance_id", uuid.NewString()},
		{"generation", "dispatch_attempts", int32(2)},
		{"expired", "lease_expires_at", time.Now().UTC().Add(-time.Minute)},
		{"terminal", "status", "COMPLETED"},
	}
	for _, tc := range changes {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pg.Exec(ctx, `UPDATE jobs SET lease_token=$2, leased_by=$3,
 lease_process_instance_id=$4, dispatch_attempts=1, lease_expires_at=$5,
 status='RUNNING' WHERE id=$1`, jobID, first.LeaseToken, "worker", process, first.LeaseExpiresAt); err != nil {
				t.Fatal(err)
			}
			if _, err := pg.Exec(ctx, `UPDATE jobs SET `+tc.assignment+`=$2 WHERE id=$1`, jobID, tc.value); err != nil {
				t.Fatal(err)
			}
			got, ok, reason, err := repo.ClaimJob(ctx, jobID, workflowID, "worker", process, command, time.Minute, 1)
			if err != nil || ok || got != nil || reason != staleClaimReason {
				t.Fatalf("stale replay = %+v, %t, %q, %v", got, ok, reason, err)
			}
		})
	}
}

func TestIntegrationClaimRejectionReplayKeepsOriginalReason(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	userID, workflowID := seedUserWorkflow(ctx, t, pg)
	jobID, err := repo.ScheduleJob(ctx, workflowID, userID, time.Now().UTC().Format(time.RFC3339Nano), "MANUAL", "schedule-"+fixtureTag(), 1)
	if err != nil {
		t.Fatal(err)
	}
	queueJob(ctx, t, pg, jobID)
	seedReadyRuntimeNode(ctx, t, pg, t.Name())
	process, command := uuid.NewString(), "claim-"+fixtureTag()
	got, ok, reason, err := repo.ClaimJob(ctx, jobID, workflowID, "worker", process, command, time.Minute, 2)
	if err != nil || got != nil || ok || reason != "dispatch attempt mismatch: current 1" {
		t.Fatalf("rejection = %+v, %t, %q, %v", got, ok, reason, err)
	}
	if _, updateErr := pg.Exec(ctx, `UPDATE jobs SET dispatch_attempts=2 WHERE id=$1`, jobID); updateErr != nil {
		t.Fatal(updateErr)
	}
	got, ok, replayedReason, err := repo.ClaimJob(ctx, jobID, workflowID, "worker", process, command, time.Minute, 2)
	if err != nil || got != nil || ok || replayedReason != reason {
		t.Fatalf("negative replay = %+v, %t, %q, %v", got, ok, replayedReason, err)
	}
	var jobStatus string
	var attempts int32
	if err := pg.QueryRow(ctx, `SELECT status, attempts FROM jobs WHERE id=$1`, jobID).Scan(&jobStatus, &attempts); err != nil {
		t.Fatal(err)
	}
	if jobStatus != "QUEUED" || attempts != 0 {
		t.Fatalf("negative replay mutated job: %s, attempts %d", jobStatus, attempts)
	}
}
