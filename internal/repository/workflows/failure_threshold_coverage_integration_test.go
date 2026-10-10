//nolint:testpackage // Integration tests share repository fixtures.
package workflows

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

// installFailureEffectFault scopes a database fault to one fixture's row.
func installFailureEffectFault(ctx context.Context, t *testing.T, pg *postgres.Postgres, table, event, condition string, deferred, skip bool) {
	t.Helper()
	name := "cv_failure_" + fixtureTag()
	body := "RAISE EXCEPTION 'injected failure effect fault';"
	if skip {
		body = "RETURN NULL;"
	}
	if _, err := pg.Exec(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$ BEGIN %s END; $body$`, name, body)); err != nil {
		t.Fatalf("create fault function: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fixtureCleanupTimeout)
		defer cancel()
		if _, err := pg.Exec(cleanupCtx, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON %s`, name, table)); err != nil {
			t.Errorf("drop fault trigger: %v", err)
		}
		if _, err := pg.Exec(cleanupCtx, fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, name)); err != nil {
			t.Errorf("drop fault function: %v", err)
		}
	})
	kind, timing, position := "TRIGGER", "", "BEFORE"
	if deferred {
		kind, timing, position = "CONSTRAINT TRIGGER", "DEFERRABLE INITIALLY DEFERRED", "AFTER"
	}
	query := fmt.Sprintf(`CREATE %s %s %s %s ON %s %s FOR EACH ROW WHEN (%s) EXECUTE FUNCTION %s()`, kind, name, position, event, table, timing, condition, name)
	if _, err := pg.Exec(ctx, query); err != nil {
		t.Fatalf("install fault trigger: %v", err)
	}
	var actualDeferred, initiallyDeferred bool
	if err := pg.QueryRow(ctx, `SELECT tgdeferrable, tginitdeferred FROM pg_trigger WHERE tgname=$1`, name).Scan(&actualDeferred, &initiallyDeferred); err != nil {
		t.Fatalf("read trigger timing: %v", err)
	}
	if actualDeferred != deferred || initiallyDeferred != deferred {
		t.Fatalf("trigger deferred=%v initially deferred=%v, want %v", actualDeferred, initiallyDeferred, deferred)
	}
}

func TestIntegrationFailureThresholdFaultsRollBackAllEffects(t *testing.T) {
	cases := []struct {
		name, table, event, message           string
		deferred, skip, threshold, terminated bool
	}{
		{name: "RecordEffect", table: postgres.TableWorkflowTerminalEffects, event: "INSERT", message: "failed to record workflow failure event"},
		{name: "IncrementCounter", table: postgres.TableWorkflows, event: "UPDATE", message: "failed to increment consecutive job failures count"},
		{name: "TerminateAtThreshold", table: postgres.TableWorkflows, event: "UPDATE", threshold: true, message: "failed to terminate threshold workflow"},
		{name: "PublishTermination", table: postgres.TableOutboxEvents, event: "INSERT", threshold: true, message: "failed to insert outbox event"},
		{name: "PersistThreshold", table: postgres.TableWorkflowTerminalEffects, event: "UPDATE", threshold: true, message: "failed to persist terminal threshold result"},
		{name: "MissingThresholdCompletion", table: postgres.TableWorkflowTerminalEffects, event: "UPDATE", threshold: true, skip: true, message: "terminal threshold completion invariant violated"},
		{name: "CommitCounter", table: postgres.TableWorkflowTerminalEffects, event: "INSERT", deferred: true, message: "failed to commit workflow failure count"},
		{name: "CommitTermination", table: postgres.TableWorkflowTerminalEffects, event: "INSERT", threshold: true, deferred: true, message: "failed to commit workflow failure count"},
		{name: "CommitLateFailure", table: postgres.TableWorkflowTerminalEffects, event: "INSERT", terminated: true, deferred: true, message: "failed to commit late failure effect"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			pg := testkit.Postgres(t)
			repo := newTestRepository(t)
			maximum := int32(3)
			if tc.threshold {
				maximum = 1
			}
			fixture := seedWorkflowFixture(ctx, t, pg, repo, maximum)
			if tc.terminated {
				if _, err := pg.Exec(ctx, `UPDATE workflows SET terminated_at=now() AT TIME ZONE 'utc' WHERE id=$1`, fixture.WorkflowID); err != nil {
					t.Fatalf("terminate fixture: %v", err)
				}
			}
			before := readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID)
			jobID := uuid.NewString()
			condition := fmt.Sprintf("NEW.workflow_id = '%s'::uuid", fixture.WorkflowID)
			if tc.table == postgres.TableWorkflows {
				condition = fmt.Sprintf("NEW.id = '%s'::uuid", fixture.WorkflowID)
				if tc.threshold {
					condition += " AND NEW.terminated_at IS NOT NULL AND OLD.terminated_at IS NULL"
				}
			}
			if tc.table == postgres.TableOutboxEvents {
				condition = fmt.Sprintf("NEW.payload->>'ID' = '%s' AND NEW.payload->>'Action' = 'TERMINATE'", fixture.WorkflowID)
			}
			installFailureEffectFault(ctx, t, pg, tc.table, tc.event, condition, tc.deferred, tc.skip)
			reached, err := repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, fixture.WorkflowID, fixture.UserID, jobID)
			if reached || status.Code(err) != codes.Internal || !strings.Contains(status.Convert(err).Message(), tc.message) {
				t.Fatalf("increment = (%v, %v), want Internal containing %q", reached, err, tc.message)
			}
			assertFailureState(t, "failed command", readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID), before)
			if count := countTerminalEffects(ctx, t, pg, jobID); count != 0 {
				t.Fatalf("terminal effects=%d, want 0 after rollback", count)
			}
			if count := countWorkflowActionEvents(ctx, t, pg, fixture.WorkflowID, "TERMINATE"); count != 0 {
				t.Fatalf("termination events=%d, want 0 after rollback", count)
			}
		})
	}
}

func TestIntegrationFailureThresholdInterruptedCounterRollsBack(t *testing.T) {
	for _, stop := range []string{"cancel", "deadline", "server"} {
		t.Run(stop, func(t *testing.T) {
			ctx := context.Background()
			pg := testkit.Postgres(t)
			repo := newTestRepository(t)
			fixture := seedWorkflowFixture(ctx, t, pg, repo, 1)
			before := readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID)
			jobID := uuid.NewString()
			lock := lockWorkflowWrite("%SET consecutive_job_failures_count = consecutive_job_failures_count + 1%")
			callerCtx, step := callerStopPlan(ctx, t, pg, stop, lock)
			err := runWhileBlocked(callerCtx, t, pg, lock, step, func(commandCtx context.Context) error {
				reached, incrementErr := repo.IncrementWorkflowConsecutiveJobFailuresCount(commandCtx, fixture.WorkflowID, fixture.UserID, jobID)
				if reached {
					return fmt.Errorf("interrupted increment incorrectly reached its threshold")
				}
				return incrementErr
			})
			want := codes.Internal
			if stop == "cancel" {
				want = codes.Canceled
			}
			if stop == "deadline" {
				want = codes.DeadlineExceeded
			}
			if status.Code(err) != want {
				t.Fatalf("interrupted increment error=%v, want %v", err, want)
			}
			assertFailureState(t, "interrupted command", readWorkflowFailureState(ctx, t, pg, fixture.WorkflowID), before)
			if count := countTerminalEffects(ctx, t, pg, jobID); count != 0 {
				t.Fatalf("terminal effects=%d, want 0", count)
			}
			if count := countWorkflowActionEvents(ctx, t, pg, fixture.WorkflowID, "TERMINATE"); count != 0 {
				t.Fatalf("termination events=%d, want 0", count)
			}
		})
	}
}

func TestIntegrationFailureThresholdCanceledBeforeTransaction(t *testing.T) {
	repo := newTestRepository(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reached, err := repo.IncrementWorkflowConsecutiveJobFailuresCount(ctx, uuid.NewString(), uuid.NewString(), uuid.NewString())
	if reached || status.Code(err) != codes.Internal || !strings.Contains(status.Convert(err).Message(), "failed to start transaction") {
		t.Fatalf("canceled increment = (%v,%v), want transaction failure", reached, err)
	}
}
