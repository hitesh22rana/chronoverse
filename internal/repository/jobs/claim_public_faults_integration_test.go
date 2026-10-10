//nolint:testpackage // Checks claim rollback with the shared database fixtures.
package jobs

import (
	"context"
	"database/sql"
	"reflect"
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

type publicClaimState struct {
	Status      string
	Attempts    int32
	LeaseToken  sql.NullString
	Process     sql.NullString
	LeasedBy    sql.NullString
	ExpiresAt   sql.NullTime
	StartedAt   sql.NullTime
	RuntimeNode sql.NullString
	RunningJobs int64
}

func readPublicClaimState(ctx context.Context, t *testing.T, pg *postgres.Postgres, jobID string) publicClaimState {
	t.Helper()
	var state publicClaimState
	err := pg.QueryRow(ctx, `SELECT status, attempts, lease_token, lease_process_instance_id,
 leased_by, lease_expires_at, started_at, runtime_node_id,
 (SELECT COALESCE(sum(running_jobs),0) FROM runtime_nodes)
 FROM jobs WHERE id=$1`, jobID).Scan(&state.Status, &state.Attempts, &state.LeaseToken, &state.Process, &state.LeasedBy, &state.ExpiresAt, &state.StartedAt, &state.RuntimeNode, &state.RunningJobs)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func assertPublicClaimLedgerCount(ctx context.Context, t *testing.T, pg *postgres.Postgres, process, command string, want int) {
	t.Helper()
	var count int
	if err := pg.QueryRow(ctx, `SELECT count(*) FROM command_idempotency_keys
 WHERE scope=$1 AND operation=$2 AND idempotency_key=$3`, commandidempotency.WorkerScope(process), commandidempotency.OperationJobClaim, command).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("claim ledger rows = %d, want %d", count, want)
	}
}

func seedPublicQueuedClaim(ctx context.Context, t *testing.T, pg *postgres.Postgres, repo *Repository) (jobID, workflowID string) {
	t.Helper()
	userID, workflowID := seedIsolatedWorkflow(ctx, t, pg)
	jobID, err := repo.ScheduleJob(ctx, workflowID, userID, time.Now().UTC().Format(time.RFC3339Nano), "MANUAL", "claim-schedule-"+fixtureTag(), 1)
	if err != nil {
		t.Fatal(err)
	}
	queueJob(ctx, t, pg, jobID)
	nodeID := seedReadyRuntimeNode(ctx, t, pg, "claim-public-"+fixtureTag())
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		if _, cleanupErr := pg.Exec(cleanupCtx, `DELETE FROM runtime_nodes WHERE id=$1`, nodeID); cleanupErr != nil {
			t.Errorf("delete claim fixture node: %v", cleanupErr)
		}
	})
	return jobID, workflowID
}

//nolint:gocyclo // Each fault shares rollback, retry, and replay assertions.
func TestIntegrationClaimJobFaultsRollBackAndAllowRetry(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	for _, tc := range []struct {
		name, table, event, message string
		deferred                    bool
	}{
		{name: "reservation", table: "command_idempotency_keys", event: "INSERT", message: "failed to reserve command"},
		{name: "claim query", table: "jobs", event: "UPDATE", message: "claim job"},
		{name: "completion", table: "command_idempotency_keys", event: "UPDATE", message: "failed to complete command"},
		{name: "commit", table: "jobs", event: "UPDATE", deferred: true, message: "commit claim transaction"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jobID, workflowID := seedPublicQueuedClaim(ctx, t, pg, repo)
			process, command := uuid.NewString(), "claim-fault-"+fixtureTag()
			before := readPublicClaimState(ctx, t, pg, jobID)
			remove := scheduleFaultTrigger(ctx, t, pg, tc.table, tc.event, tc.deferred)
			claimed, ok, reason, err := repo.ClaimJob(ctx, jobID, workflowID, "public-worker", process, command, time.Minute, 1)
			if claimed != nil || ok || reason != "" || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), tc.message) || !strings.Contains(err.Error(), "schedule fault") {
				t.Fatalf("faulted claim = %+v, %t, %q, %v", claimed, ok, reason, err)
			}
			if after := readPublicClaimState(ctx, t, pg, jobID); !reflect.DeepEqual(before, after) {
				t.Fatalf("claim leaked durable state: %+v -> %+v", before, after)
			}
			assertPublicClaimLedgerCount(ctx, t, pg, process, command, 0)
			remove()
			claimed, ok, reason, err = repo.ClaimJob(ctx, jobID, workflowID, "public-worker", process, command, time.Minute, 1)
			if err != nil || !ok || claimed == nil || reason != "" {
				t.Fatalf("retry = %+v, %t, %q, %v", claimed, ok, reason, err)
			}
			leaseToken := claimed.LeaseToken
			t.Cleanup(func() {
				cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
				defer cancel()
				if releaseErr := repo.ReleaseJobForRetry(cleanupCtx, jobID, leaseToken,
					time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano), "Unavailable", "fixture cleanup", "claim-cleanup-"+fixtureTag()); releaseErr != nil {
					t.Errorf("release claimed runtime slot: %v", releaseErr)
				}
			})
			after := readPublicClaimState(ctx, t, pg, jobID)
			if after.Status != "RUNNING" || after.Attempts != 1 || after.LeaseToken.String != claimed.LeaseToken || after.RunningJobs != before.RunningJobs+1 {
				t.Fatalf("successful retry state = %+v; baseline %+v", after, before)
			}
			assertPublicClaimLedgerCount(ctx, t, pg, process, command, 1)
			var ledgerStatus, resourceID string
			if ledgerErr := pg.QueryRow(ctx, `SELECT status, resource_id FROM command_idempotency_keys
            WHERE scope=$1 AND operation=$2 AND idempotency_key=$3`,
				commandidempotency.WorkerScope(process), commandidempotency.OperationJobClaim, command).Scan(&ledgerStatus, &resourceID); ledgerErr != nil {
				t.Fatal(ledgerErr)
			}
			if ledgerStatus != "COMPLETED" || resourceID != jobID {
				t.Fatalf("retry ledger = %q/%q, want completed job %q", ledgerStatus, resourceID, jobID)
			}
			replay, ok, reason, err := repo.ClaimJob(ctx, jobID, workflowID, "public-worker", process, command, time.Minute, 1)
			if err != nil || !ok || reason != "" || !reflect.DeepEqual(replay, claimed) {
				t.Fatalf("retry replay = %+v, %t, %q, %v", replay, ok, reason, err)
			}
			if replayed := readPublicClaimState(ctx, t, pg, jobID); !reflect.DeepEqual(after, replayed) {
				t.Fatalf("replay mutated claim: %+v -> %+v", after, replayed)
			}
		})
	}
}

func TestIntegrationClaimJobCanceledBeginHasNoEffects(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	jobID, workflowID := seedPublicQueuedClaim(ctx, t, pg, repo)
	before := readPublicClaimState(ctx, t, pg, jobID)
	for _, tc := range []struct {
		name    string
		expired bool
		code    codes.Code
	}{
		{name: "canceled", code: codes.Canceled}, {name: "expired", expired: true, code: codes.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller, cancel := context.WithCancel(ctx)
			if tc.expired {
				cancel()
				caller, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
			}
			cancel()
			process, command := uuid.NewString(), "claim-context-"+fixtureTag()
			claimed, ok, reason, err := repo.ClaimJob(caller, jobID, workflowID, "public-worker", process, command, time.Minute, 1)
			if claimed != nil || ok || reason != "" || status.Code(err) != tc.code || !strings.Contains(err.Error(), "context") {
				t.Fatalf("canceled claim = %+v, %t, %q, %v", claimed, ok, reason, err)
			}
			if after := readPublicClaimState(ctx, t, pg, jobID); !reflect.DeepEqual(before, after) {
				t.Fatalf("canceled claim mutated state: %+v -> %+v", before, after)
			}
			assertPublicClaimLedgerCount(ctx, t, pg, process, command, 0)
		})
	}
}

func TestIntegrationClaimJobRejectsEmptyCommandBeforeClaiming(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	jobID, workflowID := seedPublicQueuedClaim(ctx, t, pg, repo)
	before := readPublicClaimState(ctx, t, pg, jobID)
	process := uuid.NewString()
	claimed, ok, reason, err := repo.ClaimJob(ctx, jobID, workflowID, "public-worker", process, "", time.Minute, 1)
	if claimed != nil || ok || reason != "" || status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty command = %+v, %t, %q, %v", claimed, ok, reason, err)
	}
	if after := readPublicClaimState(ctx, t, pg, jobID); !reflect.DeepEqual(before, after) {
		t.Fatalf("empty command changed job: %+v -> %+v", before, after)
	}
	assertPublicClaimLedgerCount(ctx, t, pg, process, "", 0)
}
