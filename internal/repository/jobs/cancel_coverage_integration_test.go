//nolint:testpackage // Integration tests share repository fixtures.
package jobs

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

//nolint:gocyclo // One fault matrix asserts each transactional rollback contract.
func TestIntegrationCancelJobFaultsRollback(t *testing.T) {
	for _, tc := range []struct {
		name, table, condition, message string
		deferred                        bool
	}{
		{name: "job update", table: "jobs", condition: "NEW.status = 'CANCELED'", message: "failed to cancel job"},
		{name: "runtime release", table: "runtime_nodes", condition: "TRUE", message: "decrement runtime slot"},
		{name: "ledger completion", table: "command_idempotency_keys", condition: "NEW.status = 'COMPLETED'", message: "complete"},
		{name: "commit", table: "jobs", condition: "NEW.status = 'CANCELED'", message: "failed to commit cancel command", deferred: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			pg := testkit.Postgres(t)
			repo := newTestRepository(t)
			var jobID string
			if tc.table == "runtime_nodes" {
				jobID = seedClaimedJob(ctx, t, pg, repo).JobID
			} else {
				userID, workflowID := seedIsolatedWorkflow(ctx, t, pg)
				var err error
				jobID, err = repo.ScheduleJob(ctx, workflowID, userID, time.Now().UTC().Format(time.RFC3339Nano), "MANUAL", "cancel-fault-"+fixtureTag(), 1)
				if err != nil {
					t.Fatalf("schedule: %v", err)
				}
			}
			before := readTerminalJobState(ctx, t, pg, jobID)
			var runningBefore int
			if before.RuntimeNodeID.Valid {
				runningBefore = readRuntimeRunningJobs(ctx, t, pg, before.RuntimeNodeID.String)
			}
			remove := installCancelFault(ctx, t, pg, tc.table, tc.condition, tc.deferred)
			commandID := "cancel-" + fixtureTag()
			snapshot, err := repo.CancelJob(ctx, jobID, commandID, "OPERATOR_REQUEST")
			if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), tc.message) || !strings.Contains(err.Error(), "cancel coverage fault") || snapshot != nil {
				t.Fatalf("cancel = %+v, %v; want Internal %q with injected cause", snapshot, err, tc.message)
			}
			after := readTerminalJobState(ctx, t, pg, jobID)
			if *after != *before {
				t.Fatalf("failed cancellation changed job: %+v -> %+v", before, after)
			}
			if _, exists := readJobCommand(ctx, t, pg, jobID, commandidempotency.OperationJobCancel, commandID); exists {
				t.Fatal("failed cancellation persisted reservation")
			}
			if before.RuntimeNodeID.Valid && readRuntimeRunningJobs(ctx, t, pg, before.RuntimeNodeID.String) != runningBefore {
				t.Fatal("failed cancellation changed runtime capacity")
			}
			remove()
			if _, err := repo.CancelJob(ctx, jobID, commandID, "OPERATOR_REQUEST"); err != nil {
				t.Fatalf("retry after removing fault: %v", err)
			}
			if got := readTerminalJobState(ctx, t, pg, jobID); got.Status != "CANCELED" || !got.CompletedAt.Valid {
				t.Fatalf("retry state = %+v", got)
			}
		})
	}
}

//nolint:contextcheck // Cleanup needs a fresh context after command cancellation.
func installCancelFault(ctx context.Context, t *testing.T, pg *postgres.Postgres, table, condition string, deferred bool) func() {
	t.Helper()
	name := "cancel_fault_" + fixtureTag()
	statement := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'cancel coverage fault'; END $$`, name)
	if _, err := pg.Exec(ctx, statement); err != nil {
		t.Fatalf("create fault function: %v", err)
	}
	remove := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		if _, err := pg.Exec(cleanupCtx, fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s", name, table)); err != nil {
			t.Errorf("drop fault trigger: %v", err)
		}
		if _, err := pg.Exec(cleanupCtx, fmt.Sprintf("DROP FUNCTION IF EXISTS %s()", name)); err != nil {
			t.Errorf("drop fault function: %v", err)
		}
	}
	t.Cleanup(remove)
	kind, timing := "", ""
	if deferred {
		kind = "CONSTRAINT "
		timing = "DEFERRABLE INITIALLY DEFERRED"
	}
	statement = fmt.Sprintf("CREATE %sTRIGGER %s AFTER UPDATE ON %s %s FOR EACH ROW WHEN (%s) EXECUTE FUNCTION %s()", kind, name, table, timing, condition, name)
	if _, err := pg.Exec(ctx, statement); err != nil {
		t.Fatalf("create fault trigger: %v", err)
	}
	var deferrable, initiallyDeferred bool
	if err := pg.QueryRow(ctx, `SELECT tgdeferrable, tginitdeferred FROM pg_trigger WHERE tgname = $1`, name).Scan(&deferrable, &initiallyDeferred); err != nil {
		t.Fatalf("read trigger timing: %v", err)
	}
	if deferrable != deferred || initiallyDeferred != deferred {
		t.Fatalf("fault timing = %t/%t, want %t/%t", deferrable, initiallyDeferred, deferred, deferred)
	}
	return remove
}

func TestIntegrationCancelJobRejectsMalformedReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	fixture := seedClaimedJob(ctx, t, pg, repo)
	commandID := "cancel-corrupt-" + fixtureTag()
	if _, err := repo.CancelJob(ctx, fixture.JobID, commandID, "OPERATOR_REQUEST"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	before := readTerminalJobState(ctx, t, pg, fixture.JobID)
	if _, err := pg.Exec(ctx, `
		UPDATE command_idempotency_keys SET response = '"invalid snapshot"'::jsonb
		WHERE scope=$1 AND operation=$2 AND idempotency_key=$3
	`, commandidempotency.JobScope(fixture.JobID), commandidempotency.OperationJobCancel, commandID); err != nil {
		t.Fatalf("corrupt replay: %v", err)
	}
	snapshot, err := repo.CancelJob(ctx, fixture.JobID, commandID, "OPERATOR_REQUEST")
	if snapshot != nil || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to decode cancel replay") {
		t.Fatalf("replay = %+v, %v", snapshot, err)
	}
	if after := readTerminalJobState(ctx, t, pg, fixture.JobID); *after != *before {
		t.Fatalf("corrupt replay changed canceled job: %+v -> %+v", before, after)
	}
}

func TestIntegrationCancelJobCanceledCallerHasNoEffects(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	userID, workflowID := seedIsolatedWorkflow(ctx, t, pg)
	jobID, err := repo.ScheduleJob(ctx, workflowID, userID, time.Now().UTC().Format(time.RFC3339Nano), "MANUAL", "cancel-context-"+fixtureTag(), 1)
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	before := readTerminalJobState(ctx, t, pg, jobID)
	for _, tc := range []struct {
		name     string
		deadline bool
		code     codes.Code
	}{
		{name: "canceled", code: codes.Canceled},
		{name: "expired", deadline: true, code: codes.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var caller context.Context
			var cancel context.CancelFunc
			if tc.deadline {
				caller, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
			} else {
				caller, cancel = context.WithCancel(ctx)
			}
			cancel()
			commandID := "cancel-context-" + fixtureTag()
			snapshot, err := repo.CancelJob(caller, jobID, commandID, "OPERATOR_REQUEST")
			if snapshot != nil || status.Code(err) != tc.code {
				t.Fatalf("cancel = %+v, %v; want %v", snapshot, err, tc.code)
			}
			if after := readTerminalJobState(ctx, t, pg, jobID); *after != *before {
				t.Fatalf("canceled caller changed job: %+v -> %+v", before, after)
			}
			if _, exists := readJobCommand(ctx, t, pg, jobID, commandidempotency.OperationJobCancel, commandID); exists {
				t.Fatal("canceled caller persisted reservation")
			}
		})
	}
}
