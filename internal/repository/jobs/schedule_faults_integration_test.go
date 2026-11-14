//nolint:testpackage // Exercises package-internal scheduling fixtures.
package jobs

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

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

func TestIntegrationScheduleFaultRollsBackAndAllowsRetry(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	cases := []struct {
		name, table, event, message string
		deferred                    bool
	}{
		{name: "insert failure", table: "jobs", event: "INSERT", message: "failed to insert job"},
		{name: "completion failure", table: "command_idempotency_keys", event: "UPDATE", message: "failed to complete command"},
		{name: "commit failure", table: "jobs", event: "INSERT", deferred: true, message: "failed to commit schedule command"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := seedScheduleFixture(ctx, t, pg)
			instant := occurrenceInstant(time.Hour)
			key := "schedule-fault-" + fixtureTag()
			trigger, scope, operation, retention := jobsmodel.JobTriggerManual.ToString(), manualScope(fixture.UserID), fixture.ManualOperation, manualScheduleReplayWindow
			remove := scheduleFaultTrigger(ctx, t, pg, tc.table, tc.event, tc.deferred)
			id, err := repo.ScheduleJob(ctx, fixture.WorkflowID, fixture.UserID, instant.Format(time.RFC3339Nano), trigger, key, 1)
			if id != "" || status.Code(err) != codes.Internal || !strings.Contains(status.Convert(err).Message(), tc.message) || !strings.Contains(status.Convert(err).Message(), "schedule fault") {
				t.Fatalf("failed schedule = %q, %v; want Internal %q caused by schedule fault", id, err, tc.message)
			}
			if got := countWorkflowJobs(ctx, t, pg, fixture.WorkflowID); got != 0 {
				t.Fatalf("jobs after rollback = %d, want %d", got, 0)
			}
			if got := countScheduleCommands(ctx, t, pg, scope, operation); got != 0 {
				t.Fatalf("commands after rollback = %d, want 0", got)
			}
			remove()
			recovered, err := repo.ScheduleJob(ctx, fixture.WorkflowID, fixture.UserID, instant.Format(time.RFC3339Nano), trigger, key, 1)
			if err != nil || recovered == "" {
				t.Fatalf("retry after recovery = %q, %v", recovered, err)
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
