//nolint:testpackage // Exercises package-internal scheduling fixtures.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

// scheduleFaultTrigger returns a remover so the same command can retry after recovery.
func scheduleFaultTrigger(ctx context.Context, t *testing.T, pg *postgres.Postgres, table, event string, deferred bool) func() {
	t.Helper()
	name := "cv_schedule_fault_" + fixtureTag()
	function := name + "_fn"
	if _, err := pg.Exec(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'schedule fault'; END; $$`, function)); err != nil {
		t.Fatalf("create fault function: %v", err)
	}
	kind, timing := "TRIGGER", ""
	if deferred {
		kind, timing = "CONSTRAINT TRIGGER", "DEFERRABLE INITIALLY DEFERRED"
	}
	removed := false
	remove := func() {
		t.Helper()
		if removed {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := pg.Exec(cleanupCtx, fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s", name, table)); err != nil {
			t.Errorf("drop fault trigger: %v", err)
			return
		}
		if _, err := pg.Exec(cleanupCtx, fmt.Sprintf("DROP FUNCTION IF EXISTS %s()", function)); err != nil {
			t.Errorf("drop fault function: %v", err)
			return
		}
		removed = true
	}
	t.Cleanup(remove)
	if _, err := pg.Exec(ctx, fmt.Sprintf(`CREATE %s %s AFTER %s ON %s %s FOR EACH ROW EXECUTE FUNCTION %s()`, kind, name, event, table, timing, function)); err != nil {
		t.Fatalf("create fault trigger: %v", err)
	}
	var deferrable, initiallyDeferred bool
	if err := pg.QueryRow(ctx, `SELECT tgdeferrable, tginitdeferred FROM pg_trigger WHERE tgname = $1`, name).Scan(&deferrable, &initiallyDeferred); err != nil {
		t.Fatalf("read trigger timing: %v", err)
	}
	if deferrable != deferred || initiallyDeferred != deferred {
		t.Fatalf("trigger timing = %v/%v, want %v/%v", deferrable, initiallyDeferred, deferred, deferred)
	}
	return remove
}

//nolint:gocyclo // The fault matrix keeps rollback, recovery and replay assertions together.
func TestIntegrationScheduleFaultRollsBackAndAllowsRetry(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	cases := []struct {
		name, table, event, message string
		deferred, legacy            bool
	}{
		{name: "insert failure", table: "jobs", event: "INSERT", message: "failed to insert job"},
		{name: "completion failure", table: "command_idempotency_keys", event: "UPDATE", message: "failed to complete command"},
		{name: "commit failure", table: "jobs", event: "INSERT", deferred: true, message: "failed to commit schedule command"},
		{name: "legacy completion failure", table: "command_idempotency_keys", event: "UPDATE", legacy: true, message: "failed to complete command"},
		{name: "legacy commit failure", table: "command_idempotency_keys", event: "UPDATE", deferred: true, legacy: true, message: "failed to commit legacy schedule replay"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := seedScheduleFixture(ctx, t, pg)
			instant := occurrenceInstant(time.Hour)
			key := "schedule-fault-" + fixtureTag()
			trigger, scope, operation, retention := jobsmodel.JobTriggerManual.ToString(), manualScope(fixture.UserID), fixture.ManualOperation, manualScheduleReplayWindow
			legacyID := ""
			if tc.legacy {
				trigger, scope, operation, retention = jobsmodel.JobTriggerAutomatic.ToString(), automaticScope(fixture.WorkflowID), fixture.AutomaticOperation, automaticScheduleReplayWindow
				generation := int64(1)
				legacyID = seedLegacyAutomaticJob(ctx, t, pg, fixture, instant, legacyAutomaticJob{idempotencyKey: &key, generation: &generation})
			}
			remove := scheduleFaultTrigger(ctx, t, pg, tc.table, tc.event, tc.deferred)
			id, err := repo.ScheduleJob(ctx, fixture.WorkflowID, fixture.UserID, instant.Format(time.RFC3339Nano), trigger, key, 1)
			if id != "" || status.Code(err) != codes.Internal || !strings.Contains(status.Convert(err).Message(), tc.message) || !strings.Contains(status.Convert(err).Message(), "schedule fault") {
				t.Fatalf("failed schedule = %q, %v; want Internal %q caused by schedule fault", id, err, tc.message)
			}
			wantJobs := 0
			if tc.legacy {
				wantJobs = 1
				assertScheduledJobOwnership(t, readScheduledJob(ctx, t, pg, legacyID), fixture, trigger, instant, key)
			}
			if got := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); got != wantJobs {
				t.Fatalf("jobs after rollback = %d, want %d", got, wantJobs)
			}
			if got := countScheduleCommands(ctx, t, pg, scope, operation); got != 0 {
				t.Fatalf("commands after rollback = %d, want 0", got)
			}
			remove()
			recovered, err := repo.ScheduleJob(ctx, fixture.WorkflowID, fixture.UserID, instant.Format(time.RFC3339Nano), trigger, key, 1)
			if err != nil || recovered == "" {
				t.Fatalf("retry after recovery = %q, %v", recovered, err)
			}
			if tc.legacy && recovered != legacyID {
				t.Fatalf("legacy retry = %q, want %q", recovered, legacyID)
			}
			assertScheduledJobOwnership(t, readScheduledJob(ctx, t, pg, recovered), fixture, trigger, instant, key)
			command, ok := readCommandByScope(ctx, t, pg, scope, operation, key)
			if !ok {
				t.Fatal("retry did not complete ledger")
			}
			assertCompletedScheduleCommand(t, command, recovered, retention)
			replayed, err := repo.ScheduleJob(ctx, fixture.WorkflowID, fixture.UserID, instant.Format(time.RFC3339Nano), trigger, key, 1)
			if err != nil || replayed != recovered {
				t.Fatalf("replay = %q, %v; want %q", replayed, err, recovered)
			}
			after, ok := readCommandByScope(ctx, t, pg, scope, operation, key)
			if !ok {
				t.Fatal("replay removed ledger")
			}
			assertSameLedgerRow(t, after, command)
			if got := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); got != 1 {
				t.Fatalf("jobs after recovery/replay = %d, want 1", got)
			}
		})
	}
}

//nolint:gocyclo // The lock probe and cleanup must share the command lifetime.
func TestIntegrationScheduleFaultLegacyReadCancellationLeavesNoReservation(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	fixture := seedScheduleFixture(ctx, t, pg)
	instant := occurrenceInstant(time.Hour)
	key := "legacy-read-fault-" + fixtureTag()
	generation := int64(1)
	legacyID := seedLegacyAutomaticJob(ctx, t, pg, fixture, instant, legacyAutomaticJob{idempotencyKey: &key, generation: &generation})
	lock, lockErr := pg.BeginTx(ctx)
	if lockErr != nil {
		t.Fatalf("begin lock transaction: %v", lockErr)
	}
	t.Cleanup(func() { rollbackScheduleLock(ctx, t, lock) })
	if _, err := lock.Exec(ctx, `LOCK TABLE jobs IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("lock legacy reads: %v", err)
	}
	commandCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	type result struct {
		id  string
		err error
	}
	done := make(chan result, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		id, err := repo.ScheduleJob(commandCtx, fixture.WorkflowID, fixture.UserID, instant.Format(time.RFC3339Nano), jobsmodel.JobTriggerAutomatic.ToString(), key, 1)
		done <- result{id: id, err: err}
	}()
	// Every exit releases the lock and bounds command cleanup.
	defer func() {
		cancel()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cleanupCancel()
		rollbackScheduleLock(cleanupCtx, t, lock)
		select {
		case <-finished:
		case <-cleanupCtx.Done():
			t.Error("schedule command did not stop during cleanup")
		}
	}()
	probeCtx, stopProbe := context.WithTimeout(ctx, 5*time.Second)
	defer stopProbe()
	var pid int
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for pid == 0 {
		err := pg.QueryRow(probeCtx, `SELECT pid FROM pg_stat_activity
			WHERE datname = current_database() AND pid <> pg_backend_pid()
			  AND state = 'active' AND wait_event_type = 'Lock'
			  AND query LIKE '%SELECT id, workflow_id, user_id, trigger, workflow_generation%FROM jobs%'
			LIMIT 1`).Scan(&pid)
		if err != nil && !pg.IsNoRows(err) {
			t.Fatalf("probe legacy read: %v", err)
		}
		if pid != 0 {
			break
		}
		select {
		case early := <-done:
			t.Fatalf("schedule returned before legacy read blocked: %q, %v", early.id, early.err)
		case <-probeCtx.Done():
			t.Fatal("legacy read did not block")
		case <-ticker.C:
		}
	}
	var canceled bool
	if err := pg.QueryRow(probeCtx, `SELECT pg_cancel_backend($1)`, pid).Scan(&canceled); err != nil || !canceled {
		t.Fatalf("cancel blocked legacy read = %v, %v", canceled, err)
	}
	var failed result
	select {
	case failed = <-done:
	case <-commandCtx.Done():
		t.Fatal("schedule did not return after backend cancellation")
	}
	message := status.Convert(failed.err).Message()
	if failed.id != "" || status.Code(failed.err) != codes.Internal ||
		!strings.Contains(message, "failed to read legacy schedule command") || !strings.Contains(message, "57014") {
		t.Fatalf("legacy read failure = %q, %v; want Internal server cancellation", failed.id, failed.err)
	}
	if err := lock.Rollback(ctx); err != nil {
		t.Fatalf("release legacy read lock: %v", err)
	}
	if got := countScheduleCommands(ctx, t, pg, automaticScope(fixture.WorkflowID), fixture.AutomaticOperation); got != 0 {
		t.Fatalf("commands after read failure = %d, want 0", got)
	}
	assertScheduledJobOwnership(t, readScheduledJob(ctx, t, pg, legacyID), fixture, jobsmodel.JobTriggerAutomatic.ToString(), instant, key)
	recovered, err := repo.ScheduleJob(ctx, fixture.WorkflowID, fixture.UserID, instant.Format(time.RFC3339Nano), jobsmodel.JobTriggerAutomatic.ToString(), key, 1)
	if err != nil || recovered != legacyID {
		t.Fatalf("legacy read retry = %q, %v; want %q", recovered, err, legacyID)
	}
	command, ok := readCommandByScope(ctx, t, pg, automaticScope(fixture.WorkflowID), fixture.AutomaticOperation, key)
	if !ok {
		t.Fatal("recovered legacy command has no ledger row")
	}
	assertCompletedScheduleCommand(t, command, legacyID, automaticScheduleReplayWindow)
	if got := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); got != 1 {
		t.Fatalf("jobs after read recovery = %d, want 1", got)
	}
}

func rollbackScheduleLock(ctx context.Context, t *testing.T, tx pgx.Tx) {
	t.Helper()
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := tx.Rollback(cleanupCtx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		t.Errorf("release scheduling lock: %v", err)
	}
}
